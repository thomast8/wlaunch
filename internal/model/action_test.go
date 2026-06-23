package model

import (
	"testing"
	"time"
)

func mine(n int, rd, merge string, ci bool, threads int, draft, hasRev bool, age time.Duration, now time.Time) RawActionPR {
	return RawActionPR{
		Number: n, Title: "t", Author: "me", RepoName: "r", RepoRoot: "/r",
		ReviewDecision: rd, Mergeable: merge, CIFailing: ci, UnresolvedThreads: threads,
		IsDraft: draft, HasReviewers: hasRev, UpdatedAt: now.Add(-age),
	}
}

func TestClassifyMineBuckets(t *testing.T) {
	now := time.Date(2026, 6, 23, 12, 0, 0, 0, time.UTC)
	day := 24 * time.Hour
	cases := []struct {
		name        string
		pr          RawActionPR
		wantKind    ActionKind
		wantPrio    int
		wantMarker  string
		wantSummary string
	}{
		{"conflict+ci", mine(1, "", "CONFLICTING", true, 7, false, true, day, now),
			ActionMineNeedsAction, prioBroken, "✗", "conflict/CI"},
		{"conflict only", mine(2, "REVIEW_REQUIRED", "CONFLICTING", false, 0, false, true, day, now),
			ActionMineNeedsAction, prioBroken, "✗", "conflict"},
		{"changes+threads", mine(3, "CHANGES_REQUESTED", "MERGEABLE", false, 34, false, true, day, now),
			ActionMineNeedsAction, prioChanges, "⚠", "changes+34"},
		{"threads only", mine(4, "REVIEW_REQUIRED", "MERGEABLE", false, 9, false, true, day, now),
			ActionMineNeedsAction, prioThreads, "◆", "9 threads"},
		{"ready", mine(5, "APPROVED", "MERGEABLE", false, 0, false, true, day, now),
			ActionMineNeedsAction, prioReady, "✓", "ready"},
		{"no reviewer", mine(6, "REVIEW_REQUIRED", "MERGEABLE", false, 0, false, false, day, now),
			ActionStaleNoReviewer, prioStale, "·", "no reviewer"},
		{"stale", mine(7, "REVIEW_REQUIRED", "MERGEABLE", false, 0, false, true, 30*day, now),
			ActionStaleNoReviewer, prioStale, "·", "stale 30d"},
		{"waiting", mine(8, "REVIEW_REQUIRED", "MERGEABLE", false, 0, false, true, day, now),
			ActionWaiting, prioWaiting, "·", "waiting"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := ClassifyActionItems([]RawActionPR{c.pr}, nil, now, 14)
			if len(got) != 1 {
				t.Fatalf("got %d items, want 1", len(got))
			}
			it := got[0]
			if it.Kind != c.wantKind {
				t.Errorf("Kind = %v, want %v", it.Kind, c.wantKind)
			}
			if it.Priority != c.wantPrio {
				t.Errorf("Priority = %d, want %d", it.Priority, c.wantPrio)
			}
			if it.Marker != c.wantMarker {
				t.Errorf("Marker = %q, want %q", it.Marker, c.wantMarker)
			}
			if it.Summary != c.wantSummary {
				t.Errorf("Summary = %q, want %q", it.Summary, c.wantSummary)
			}
		})
	}
}

func TestClassifyAwaiting(t *testing.T) {
	now := time.Date(2026, 6, 23, 12, 0, 0, 0, time.UTC)
	myReview := now.Add(-48 * time.Hour)
	myComment := now.Add(-24 * time.Hour)
	authorReply := now.Add(-2 * time.Hour)
	authorCommit := now.Add(-1 * time.Hour)
	cases := []struct {
		name        string
		pr          RawActionPR
		wantSummary string
		wantMarker  string
		wantPrio    int
	}{
		{
			name:        "never touched",
			pr:          RawActionPR{Number: 99, Author: "someone", RepoName: "r", RepoRoot: "/r", UnresolvedThreads: -1},
			wantSummary: "review",
			wantMarker:  "◆",
			wantPrio:    prioReview,
		},
		{
			name: "author replied after me",
			pr: RawActionPR{
				Number: 99, Author: "someone", RepoName: "r", RepoRoot: "/r", UnresolvedThreads: -1,
				LastMyTurnAt: myComment, LastAuthorTurnAt: authorReply,
			},
			wantSummary: "reply",
			wantMarker:  "◆",
			wantPrio:    prioReview,
		},
		{
			name: "author pushed after changes requested",
			pr: RawActionPR{
				Number: 99, Author: "someone", RepoName: "r", RepoRoot: "/r", UnresolvedThreads: -1,
				LastMyReviewState: "CHANGES_REQUESTED", LastMyReviewAt: myReview, LastMyTurnAt: myReview, LastAuthorCommitAt: authorCommit,
			},
			wantSummary: "re-review",
			wantMarker:  "◆",
			wantPrio:    prioReview,
		},
		{
			name: "my turn is latest",
			pr: RawActionPR{
				Number: 99, Author: "someone", RepoName: "r", RepoRoot: "/r", UnresolvedThreads: -1,
				LastMyReviewState: "COMMENTED", LastMyReviewAt: myReview, LastMyTurnAt: myComment, LastAuthorTurnAt: myReview.Add(-time.Hour),
			},
			wantSummary: "waiting",
			wantMarker:  "·",
			wantPrio:    prioWaiting,
		},
		{
			name: "my comment after author push",
			pr: RawActionPR{
				Number: 99, Author: "someone", RepoName: "r", RepoRoot: "/r", UnresolvedThreads: -1,
				LastMyReviewState: "CHANGES_REQUESTED", LastMyReviewAt: myReview, LastMyTurnAt: myComment, LastAuthorCommitAt: myReview.Add(time.Hour),
			},
			wantSummary: "waiting",
			wantMarker:  "·",
			wantPrio:    prioWaiting,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := ClassifyActionItems(nil, []RawActionPR{c.pr}, now, 14)
			if len(got) != 1 || got[0].Kind != ActionAwaitingMyReview {
				t.Fatalf("awaiting classification wrong: %+v", got)
			}
			if got[0].Summary != c.wantSummary {
				t.Errorf("Summary = %q, want %q", got[0].Summary, c.wantSummary)
			}
			if got[0].Marker != c.wantMarker {
				t.Errorf("Marker = %q, want %q", got[0].Marker, c.wantMarker)
			}
			if got[0].Priority != c.wantPrio {
				t.Errorf("Priority = %d, want %d", got[0].Priority, c.wantPrio)
			}
		})
	}
}

// TestClassifyOrder verifies the dashboard-style quick-win sort: non-drafts
// first, then highest score, with drafts demoted even if their raw score is
// good. This keeps ready-to-merge and easy-review items above noisy broken work.
func TestClassifyOrder(t *testing.T) {
	now := time.Date(2026, 6, 23, 12, 0, 0, 0, time.UTC)
	day := 24 * time.Hour
	mineList := []RawActionPR{
		mine(10, "REVIEW_REQUIRED", "MERGEABLE", false, 0, false, true, day, now),     // waiting
		mine(11, "", "CONFLICTING", false, 0, false, true, day, now),                  // broken
		mine(12, "APPROVED", "MERGEABLE", false, 0, false, true, day, now),            // ready
		mine(13, "CHANGES_REQUESTED", "MERGEABLE", false, 0, false, true, day, now),   // changes
		mine(14, "CHANGES_REQUESTED", "MERGEABLE", false, 0, false, true, 5*day, now), // changes, older draft? no, older
	}
	mineList[4].IsDraft = true // draft changes-requested: same bucket as #13, but demoted
	awaiting := []RawActionPR{{Number: 20, Author: "x", RepoName: "r", RepoRoot: "/r"}}
	got := ClassifyActionItems(mineList, awaiting, now, 14)
	order := make([]int, len(got))
	for i, it := range got {
		order[i] = it.Number
	}
	// ready(12) scores highest, then a review request(20), then addressable
	// feedback(13), then broken work(11), then idle waiting(10), with draft(14)
	// last despite having a better raw score than broken/waiting.
	want := []int{12, 20, 13, 11, 10, 14}
	for i := range want {
		if order[i] != want[i] {
			t.Fatalf("order = %v, want %v", order, want)
		}
	}
}

func TestClassifyScoresFavorQuickWins(t *testing.T) {
	now := time.Date(2026, 6, 23, 12, 0, 0, 0, time.UTC)
	day := 24 * time.Hour
	ready := mine(1, "APPROVED", "MERGEABLE", false, 0, false, true, day, now)
	ready.CIStatus = "success"
	ready.LinesChanged = 40
	broken := mine(2, "REVIEW_REQUIRED", "CONFLICTING", true, 0, false, true, day, now)
	broken.CIStatus = "failure"
	broken.LinesChanged = 40

	got := ClassifyActionItems([]RawActionPR{broken, ready}, nil, now, 14)
	if got[0].Number != 1 {
		t.Fatalf("ready PR should sort before broken PR, got order %+v", got)
	}
	if got[0].Score <= got[1].Score {
		t.Fatalf("ready score should exceed broken score, got %d <= %d", got[0].Score, got[1].Score)
	}
}
