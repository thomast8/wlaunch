package model

import (
	"fmt"
	"sort"
	"strings"
	"time"
)

// ActionKind buckets a PR by why it needs the user's attention.
type ActionKind int

const (
	ActionMineNeedsAction  ActionKind = iota // my PR: conflict / CI / changes-requested / open threads / ready
	ActionAwaitingMyReview                   // someone else's PR where I'm a requested reviewer
	ActionStaleNoReviewer                    // my PR: no reviewer assigned, or untouched past the stale cutoff
	ActionWaiting                            // my PR: reviewers have the ball, nothing for me to do
)

// String is a short keyword for filtering and tests.
func (k ActionKind) String() string {
	switch k {
	case ActionMineNeedsAction:
		return "mine"
	case ActionAwaitingMyReview:
		return "review"
	case ActionStaleNoReviewer:
		return "stale"
	case ActionWaiting:
		return "waiting"
	}
	return "?"
}

// Priority buckets break score ties only. Score decides the main order.
const (
	prioReady   = 0 // mine, approved + mergeable + green
	prioReview  = 1 // someone is blocked on my review
	prioChanges = 2 // mine, changes requested
	prioThreads = 3 // mine, unresolved review threads
	prioStale   = 4 // mine, no reviewer / stale
	prioWaiting = 5 // mine, reviewers have the ball
	prioBroken  = 6 // mine, conflict or failing CI
)

// RawActionPR is the gh-sourced input to the classifier: one open PR plus the
// signals used to bucket it. Pure data, so ClassifyActionItems is unit-tested
// headless. Fields the cheap (cross-repo search) tier can't fill are left at
// their zero value or, for thread counts, -1 (unknown).
type RawActionPR struct {
	Number             int
	Title              string
	HeadRefName        string
	Author             string
	RepoName           string
	RepoRoot           string // resolved local main-checkout path; "" if not cloned locally
	ReviewDecision     string // CHANGES_REQUESTED | APPROVED | REVIEW_REQUIRED | ""
	Mergeable          string // MERGEABLE | CONFLICTING | UNKNOWN
	CIStatus           string // success | pending | failure | unknown
	CIFailing          bool
	UnresolvedThreads  int // -1 = unknown (not yet fetched)
	LinesChanged       int // additions + deletions; -1 = unknown
	IsDraft            bool
	HasReviewers       bool // any reviewer requested (drives no-reviewer detection)
	LastMyReviewState  string
	LastMyReviewAt     time.Time
	LastMyTurnAt       time.Time
	LastAuthorTurnAt   time.Time
	LastAuthorCommitAt time.Time
	CreatedAt          time.Time
	UpdatedAt          time.Time
}

// ActionItem is a classified, display-ready actionable PR.
type ActionItem struct {
	RepoRoot           string
	RepoName           string
	Number             int
	Title              string
	HeadRefName        string
	Author             string
	Kind               ActionKind
	ReviewDecision     string
	Mergeable          string
	CIStatus           string
	CIFailing          bool
	UnresolvedThreads  int
	LinesChanged       int
	IsDraft            bool
	LastMyReviewState  string
	LastMyReviewAt     time.Time
	LastMyTurnAt       time.Time
	LastAuthorTurnAt   time.Time
	LastAuthorCommitAt time.Time
	CreatedAt          time.Time
	UpdatedAt          time.Time
	Score              int    // quick-win score, higher = more useful to do next
	Priority           int    // score tiebreak, lower = more useful to do next
	Marker             string // status glyph: ✗ ⚠ ◆ ✓ ·
	Summary            string // short reason, e.g. "conflict/CI", "changes+34", "9 threads", "review", "stale 21d"
}

// Launchable reports whether the item resolves to a local clone the wl wrapper
// can cd into; cross-repo items from repos not cloned locally are display-only.
func (it ActionItem) Launchable() bool { return it.RepoRoot != "" }

// FilterText is the lowercased haystack typed filtering matches against: number,
// title, repo, the kind keyword, and the human summary (so typing "conflict" or
// "review" narrows by reason).
func (it ActionItem) FilterText() string {
	return strings.ToLower(fmt.Sprintf("%d %s %s %s %s",
		it.Number, it.Title, it.RepoName, it.Kind, it.Summary))
}

// ClassifyActionItems buckets and sorts the actionable set. `mine` are PRs I
// authored; `awaiting` are PRs (any author) where I'm a requested reviewer. now
// and staleDays drive the stale cutoff for PRs with nothing else pending.
func ClassifyActionItems(mine, awaiting []RawActionPR, now time.Time, staleDays int) []ActionItem {
	items := make([]ActionItem, 0, len(mine)+len(awaiting))
	for _, p := range mine {
		items = append(items, classifyMine(p, now, staleDays))
	}
	for _, p := range awaiting {
		items = append(items, classifyAwaiting(p, now))
	}
	sort.SliceStable(items, func(i, j int) bool {
		a, b := items[i], items[j]
		if a.IsDraft != b.IsDraft {
			return !a.IsDraft // drafts are work-in-progress, so keep them below actionable work
		}
		if a.Score != b.Score {
			return a.Score > b.Score
		}
		if a.Priority != b.Priority {
			return a.Priority < b.Priority
		}
		if !a.UpdatedAt.Equal(b.UpdatedAt) {
			return a.UpdatedAt.Before(b.UpdatedAt) // older = more neglected, first
		}
		return a.Number < b.Number
	})
	return items
}

func classifyMine(p RawActionPR, now time.Time, staleDays int) ActionItem {
	it := baseItem(p)
	it.Kind = ActionMineNeedsAction
	it.Score = ownerQuickWinScore(p, now)
	switch {
	case p.Mergeable == "CONFLICTING" || p.CIFailing:
		it.Priority = prioBroken
		it.Marker = "✗"
		it.Summary = brokenSummary(p)
	case p.ReviewDecision == "CHANGES_REQUESTED":
		it.Priority = prioChanges
		it.Marker = "⚠"
		it.Summary = "changes" + threadSuffix(p)
	case p.UnresolvedThreads > 0:
		it.Priority = prioThreads
		it.Marker = "◆"
		it.Summary = fmt.Sprintf("%d threads", p.UnresolvedThreads)
	case p.ReviewDecision == "APPROVED" && p.Mergeable == "MERGEABLE" && !p.CIFailing:
		it.Priority = prioReady
		it.Marker = "✓"
		it.Summary = "ready"
	case !p.HasReviewers:
		it.Kind = ActionStaleNoReviewer
		it.Priority = prioStale
		it.Marker = "·"
		it.Summary = "no reviewer"
	case ageDays(p.UpdatedAt, now) >= staleDays:
		it.Kind = ActionStaleNoReviewer
		it.Priority = prioStale
		it.Marker = "·"
		it.Summary = fmt.Sprintf("stale %dd", ageDays(p.UpdatedAt, now))
	default:
		it.Kind = ActionWaiting
		it.Priority = prioWaiting
		it.Score = 0
		it.Marker = "·"
		it.Summary = "waiting"
	}
	return it
}

func classifyAwaiting(p RawActionPR, now time.Time) ActionItem {
	it := baseItem(p)
	it.Kind = ActionAwaitingMyReview
	it.Priority = prioReview
	it.Score = reviewQuickWinScore(p, now)
	it.Marker = "◆"
	it.Summary = awaitingReviewSummary(p)
	if it.Summary == "waiting" {
		it.Priority = prioWaiting
		it.Score = 0
		it.Marker = "·"
	}
	return it
}

func baseItem(p RawActionPR) ActionItem {
	return ActionItem{
		RepoRoot:           p.RepoRoot,
		RepoName:           p.RepoName,
		Number:             p.Number,
		Title:              p.Title,
		HeadRefName:        p.HeadRefName,
		Author:             p.Author,
		ReviewDecision:     p.ReviewDecision,
		Mergeable:          p.Mergeable,
		CIStatus:           p.CIStatus,
		CIFailing:          p.CIFailing,
		UnresolvedThreads:  p.UnresolvedThreads,
		LinesChanged:       p.LinesChanged,
		IsDraft:            p.IsDraft,
		LastMyReviewState:  p.LastMyReviewState,
		LastMyReviewAt:     p.LastMyReviewAt,
		LastMyTurnAt:       p.LastMyTurnAt,
		LastAuthorTurnAt:   p.LastAuthorTurnAt,
		LastAuthorCommitAt: p.LastAuthorCommitAt,
		CreatedAt:          p.CreatedAt,
		UpdatedAt:          p.UpdatedAt,
	}
}

func awaitingReviewSummary(p RawActionPR) string {
	if p.LastMyTurnAt.IsZero() {
		return "review"
	}
	if p.LastAuthorTurnAt.After(p.LastMyTurnAt) {
		return "reply"
	}
	if p.LastMyReviewState == "CHANGES_REQUESTED" && p.LastAuthorCommitAt.After(p.LastMyTurnAt) {
		return "re-review"
	}
	return "waiting"
}

// ownerQuickWinScore mirrors the PR dashboard's owner/default priority model:
// "what is closest to done, and therefore most worth doing next?" Approved,
// green, clean, small PRs float up; conflicted or red PRs stop dominating just
// because they are noisy.
func ownerQuickWinScore(p RawActionPR, now time.Time) int {
	return ownerReviewScore(p) +
		ownerCIScore(p) +
		ownerMergeScore(p) +
		ownerSizeScore(p.LinesChanged) +
		ageScore(scoreAgeBase(p), now, 10, 7) +
		ownerFeedbackScore(p)
}

// reviewQuickWinScore mirrors the dashboard review-queue shape: PRs where I'm
// the blocker rank high, then passing CI, small diffs, age, and clean merge make
// the "easy useful review" rise.
func reviewQuickWinScore(p RawActionPR, now time.Time) int {
	return 35 +
		reviewCIScore(p) +
		reviewSizeScore(p.LinesChanged) +
		ageScore(scoreAgeBase(p), now, 15, 7) +
		reviewMergeScore(p)
}

func ownerReviewScore(p RawActionPR) int {
	switch p.ReviewDecision {
	case "APPROVED":
		return 35
	case "CHANGES_REQUESTED":
		return 0
	}
	if p.UnresolvedThreads > 0 {
		return 20
	}
	return 10
}

func ownerCIScore(p RawActionPR) int {
	switch effectiveCIStatus(p) {
	case "success":
		return 25
	case "pending":
		return 10
	case "failure":
		return 0
	default:
		return 5
	}
}

func reviewCIScore(p RawActionPR) int {
	switch effectiveCIStatus(p) {
	case "success":
		return 20
	case "pending":
		return 8
	case "failure":
		return 0
	default:
		return 4
	}
}

func ownerMergeScore(p RawActionPR) int {
	switch p.Mergeable {
	case "MERGEABLE":
		return 15
	case "CONFLICTING":
		return 0
	case "UNKNOWN":
		return 5
	default:
		return 5
	}
}

func reviewMergeScore(p RawActionPR) int {
	switch p.Mergeable {
	case "MERGEABLE":
		return 10
	case "CONFLICTING":
		return 0
	default:
		return 0
	}
}

func ownerSizeScore(lines int) int {
	switch {
	case lines < 0:
		return 5
	case lines <= 50:
		return 10
	case lines <= 200:
		return 8
	case lines <= 500:
		return 5
	case lines <= 1000:
		return 2
	default:
		return 0
	}
}

func reviewSizeScore(lines int) int {
	switch {
	case lines < 0:
		return 7
	case lines <= 50:
		return 15
	case lines <= 200:
		return 12
	case lines <= 500:
		return 7
	case lines <= 1000:
		return 3
	default:
		return 0
	}
}

func ownerFeedbackScore(p RawActionPR) int {
	if p.UnresolvedThreads > 0 {
		return 5
	}
	return 0
}

func effectiveCIStatus(p RawActionPR) string {
	if p.CIFailing {
		return "failure"
	}
	if p.CIStatus != "" {
		return p.CIStatus
	}
	return "unknown"
}

func scoreAgeBase(p RawActionPR) time.Time {
	if !p.CreatedAt.IsZero() {
		return p.CreatedAt
	}
	return p.UpdatedAt
}

func ageScore(t, now time.Time, maxPts, days int) int {
	if t.IsZero() || days <= 0 {
		return 0
	}
	ageDays := now.Sub(t).Hours() / 24
	score := int(ageDays * float64(maxPts) / float64(days))
	if score < 0 {
		return 0
	}
	if score > maxPts {
		return maxPts
	}
	return score
}

// brokenSummary describes the blocking failure(s): "conflict", "CI", or both.
func brokenSummary(p RawActionPR) string {
	var parts []string
	if p.Mergeable == "CONFLICTING" {
		parts = append(parts, "conflict")
	}
	if p.CIFailing {
		parts = append(parts, "CI")
	}
	if len(parts) == 0 {
		return "blocked"
	}
	return strings.Join(parts, "/")
}

// threadSuffix appends "+N" when there are also unresolved threads to address.
func threadSuffix(p RawActionPR) string {
	if p.UnresolvedThreads > 0 {
		return fmt.Sprintf("+%d", p.UnresolvedThreads)
	}
	return ""
}

func ageDays(t, now time.Time) int {
	if t.IsZero() {
		return 0
	}
	return int(now.Sub(t).Hours() / 24)
}
