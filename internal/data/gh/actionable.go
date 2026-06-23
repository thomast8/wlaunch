package gh

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/thomast8/wlaunch/internal/data"
	"github.com/thomast8/wlaunch/internal/data/repos"
	"github.com/thomast8/wlaunch/internal/model"
)

const defaultStaleDays = 14

// --- JSON shapes ---

type ghReviewer struct {
	Login string `json:"login"` // user reviewers
	Name  string `json:"name"`  // team reviewers
	Slug  string `json:"slug"`
}

type ghCheck struct {
	Conclusion string `json:"conclusion"` // CheckRun: SUCCESS | FAILURE | NEUTRAL | SKIPPED | ...
	State      string `json:"state"`      // StatusContext: SUCCESS | FAILURE | ERROR | PENDING
}

// ghPRFull is the extended `gh pr list --json` row used by the this-repo
// actionable view (rich signals: review decision, mergeable, CI, reviewers).
type ghPRFull struct {
	Number         int          `json:"number"`
	Title          string       `json:"title"`
	HeadRefName    string       `json:"headRefName"`
	Author         ghAuthor     `json:"author"`
	Additions      int          `json:"additions"`
	Deletions      int          `json:"deletions"`
	IsDraft        bool         `json:"isDraft"`
	ReviewDecision string       `json:"reviewDecision"`
	Mergeable      string       `json:"mergeable"`
	CreatedAt      time.Time    `json:"createdAt"`
	UpdatedAt      time.Time    `json:"updatedAt"`
	ReviewRequests []ghReviewer `json:"reviewRequests"`
	Checks         []ghCheck    `json:"statusCheckRollup"`
}

type prTurnInfo struct {
	LastMyReviewState  string
	LastMyReviewAt     time.Time
	LastMyTurnAt       time.Time
	LastAuthorTurnAt   time.Time
	LastAuthorCommitAt time.Time
}

// ghSearchPR is the cheaper `gh search prs --json` row used by the cross-repo
// view. gh search exposes far fewer fields than `gh pr list` — notably no
// reviewDecision / mergeable / CI / threads — so the cross-repo tier classifies
// on author bucket, draft, and age only; richer signals need per-PR enrichment
// (a follow-up).
type ghSearchPR struct {
	Number     int          `json:"number"`
	Title      string       `json:"title"`
	Repository ghSearchRepo `json:"repository"`
	Author     ghAuthor     `json:"author"`
	IsDraft    bool         `json:"isDraft"`
	CreatedAt  time.Time    `json:"createdAt"`
	UpdatedAt  time.Time    `json:"updatedAt"`
}

type ghSearchRepo struct {
	Name          string `json:"name"`
	NameWithOwner string `json:"nameWithOwner"`
}

// --- pure parsers (unit-tested against fixture JSON) ---

func parsePRsFull(b []byte) ([]ghPRFull, error) {
	var out []ghPRFull
	if err := json.Unmarshal(b, &out); err != nil {
		return nil, err
	}
	return out, nil
}

func parseSearchPRs(b []byte) ([]ghSearchPR, error) {
	var out []ghSearchPR
	if err := json.Unmarshal(b, &out); err != nil {
		return nil, err
	}
	return out, nil
}

// parseThreads turns the reviewThreads GraphQL payload into a number→unresolved
// count map. A PR present with all threads resolved maps to 0 (known zero), which
// the classifier distinguishes from -1 (unknown).
func parseThreads(b []byte) (map[int]int, error) {
	var resp struct {
		Data struct {
			Repository struct {
				PullRequests struct {
					Nodes []struct {
						Number        int `json:"number"`
						ReviewThreads struct {
							Nodes []struct {
								IsResolved bool `json:"isResolved"`
							} `json:"nodes"`
						} `json:"reviewThreads"`
					} `json:"nodes"`
				} `json:"pullRequests"`
			} `json:"repository"`
		} `json:"data"`
	}
	if err := json.Unmarshal(b, &resp); err != nil {
		return nil, err
	}
	out := map[int]int{}
	for _, n := range resp.Data.Repository.PullRequests.Nodes {
		count := 0
		for _, th := range n.ReviewThreads.Nodes {
			if !th.IsResolved {
				count++
			}
		}
		out[n.Number] = count
	}
	return out, nil
}

func parseTurnInfo(b []byte, me string) (map[int]prTurnInfo, error) {
	var resp struct {
		Data struct {
			Repository struct {
				PullRequests struct {
					Nodes []struct {
						Number  int      `json:"number"`
						Author  ghAuthor `json:"author"`
						Reviews struct {
							Nodes []struct {
								Author      ghAuthor  `json:"author"`
								State       string    `json:"state"`
								SubmittedAt time.Time `json:"submittedAt"`
							} `json:"nodes"`
						} `json:"reviews"`
						Comments struct {
							Nodes []struct {
								Author    ghAuthor  `json:"author"`
								CreatedAt time.Time `json:"createdAt"`
							} `json:"nodes"`
						} `json:"comments"`
						Commits struct {
							Nodes []struct {
								Commit struct {
									AuthoredDate  time.Time `json:"authoredDate"`
									CommittedDate time.Time `json:"committedDate"`
								} `json:"commit"`
							} `json:"nodes"`
						} `json:"commits"`
					} `json:"nodes"`
				} `json:"pullRequests"`
			} `json:"repository"`
		} `json:"data"`
	}
	if err := json.Unmarshal(b, &resp); err != nil {
		return nil, err
	}
	out := map[int]prTurnInfo{}
	for _, n := range resp.Data.Repository.PullRequests.Nodes {
		var info prTurnInfo
		for _, r := range n.Reviews.Nodes {
			if strings.EqualFold(r.Author.Login, me) && r.SubmittedAt.After(info.LastMyReviewAt) {
				info.LastMyReviewState = r.State
				info.LastMyReviewAt = r.SubmittedAt
			}
		}
		info.LastMyTurnAt = info.LastMyReviewAt
		for _, c := range n.Comments.Nodes {
			switch {
			case strings.EqualFold(c.Author.Login, me):
				if c.CreatedAt.After(info.LastMyTurnAt) {
					info.LastMyTurnAt = c.CreatedAt
				}
			case strings.EqualFold(c.Author.Login, n.Author.Login):
				if c.CreatedAt.After(info.LastAuthorTurnAt) {
					info.LastAuthorTurnAt = c.CreatedAt
				}
			}
		}
		for _, c := range n.Commits.Nodes {
			t := c.Commit.CommittedDate
			if t.IsZero() {
				t = c.Commit.AuthoredDate
			}
			if t.After(info.LastAuthorCommitAt) {
				info.LastAuthorCommitAt = t
			}
		}
		out[n.Number] = info
	}
	return out, nil
}

// ciFailing reports whether any rollup check is in a hard-failure state (ignoring
// NEUTRAL/SKIPPED/PENDING/SUCCESS, which are not actionable failures).
func (p ghPRFull) ciFailing() bool {
	return p.ciStatus() == "failure"
}

func (p ghPRFull) ciStatus() string {
	seen := false
	pending := false
	for _, c := range p.Checks {
		conclusion := strings.ToUpper(c.Conclusion)
		state := strings.ToUpper(c.State)
		switch {
		case conclusion == "FAILURE" || conclusion == "TIMED_OUT" || conclusion == "CANCELLED" ||
			conclusion == "ACTION_REQUIRED" || conclusion == "STARTUP_FAILURE" ||
			state == "FAILURE" || state == "ERROR":
			return "failure"
		case state == "PENDING" || state == "QUEUED" || state == "IN_PROGRESS":
			pending = true
			seen = true
		case state == "SUCCESS" || conclusion == "SUCCESS" || conclusion == "NEUTRAL" || conclusion == "SKIPPED":
			seen = true
		case conclusion == "" && state == "":
			continue
		default:
			pending = true
			seen = true
		}
	}
	if pending {
		return "pending"
	}
	if seen {
		return "success"
	}
	return "unknown"
}

func (p ghPRFull) requestsReviewer(me string) bool {
	if me == "" {
		return false
	}
	for _, r := range p.ReviewRequests {
		if strings.EqualFold(r.Login, me) {
			return true
		}
	}
	return false
}

func (p ghPRFull) toRaw(repoName, repoRoot string, threads map[int]int, turn prTurnInfo) model.RawActionPR {
	t := -1
	if threads != nil {
		if n, ok := threads[p.Number]; ok {
			t = n
		}
	}
	return model.RawActionPR{
		Number:             p.Number,
		Title:              p.Title,
		HeadRefName:        p.HeadRefName,
		Author:             p.Author.Login,
		RepoName:           repoName,
		RepoRoot:           repoRoot,
		ReviewDecision:     p.ReviewDecision,
		Mergeable:          p.Mergeable,
		CIStatus:           p.ciStatus(),
		CIFailing:          p.ciFailing(),
		UnresolvedThreads:  t,
		LinesChanged:       p.Additions + p.Deletions,
		IsDraft:            p.IsDraft,
		HasReviewers:       len(p.ReviewRequests) > 0,
		LastMyReviewState:  turn.LastMyReviewState,
		LastMyReviewAt:     turn.LastMyReviewAt,
		LastMyTurnAt:       turn.LastMyTurnAt,
		LastAuthorTurnAt:   turn.LastAuthorTurnAt,
		LastAuthorCommitAt: turn.LastAuthorCommitAt,
		CreatedAt:          p.CreatedAt,
		UpdatedAt:          p.UpdatedAt,
	}
}

// --- queries ---

// ListActionableForRepo builds the rich actionable set for a single repo: my open
// PRs (bucketed by conflict/CI/changes/threads/ready/stale/waiting) plus PRs where
// I'm a requested reviewer. Uses the repo's pinned gh account throughout.
func ListActionableForRepo(ctx context.Context, repo string) ([]model.ActionItem, error) {
	env, err := ghEnv(ctx, repo)
	if err != nil {
		return nil, err
	}
	me := accountFor(ctx, repo)
	if me == "" {
		me, err = loginFor(ctx, env)
		if err != nil {
			return nil, err
		}
	}
	type fullResult struct {
		prs []ghPRFull
		err error
	}
	type metaResult struct {
		threads map[int]int
		turns   map[int]prTurnInfo
	}
	fullCh := make(chan fullResult, 1)
	metaCh := make(chan metaResult, 1)
	go func() {
		b, err := data.RunEnv(ctx, repo, env, "gh", "pr", "list",
			"--limit", "50",
			"--json", "number,title,headRefName,author,additions,deletions,isDraft,reviewDecision,mergeable,reviewRequests,createdAt,updatedAt,statusCheckRollup")
		if err != nil {
			fullCh <- fullResult{err: err}
			return
		}
		prs, err := parsePRsFull(b)
		fullCh <- fullResult{prs: prs, err: err}
	}()
	go func() {
		// Thread/turn metadata is best-effort: a GraphQL hiccup must not blank the view.
		if slug, e := repos.OriginSlug(ctx, repo); e == nil && slug != "" {
			threads, turns, _ := fetchRepoPRMeta(ctx, env, slug, me)
			metaCh <- metaResult{threads: threads, turns: turns}
			return
		}
		metaCh <- metaResult{}
	}()
	fullRes := <-fullCh
	if fullRes.err != nil {
		return nil, fullRes.err
	}
	metaRes := <-metaCh
	full := fullRes.prs
	threads := metaRes.threads
	turns := metaRes.turns
	repoName := filepath.Base(repo)
	var mine, awaiting []model.RawActionPR
	for _, p := range full {
		raw := p.toRaw(repoName, repo, threads, turns[p.Number])
		switch {
		case strings.EqualFold(p.Author.Login, me):
			mine = append(mine, raw)
		case p.requestsReviewer(me):
			awaiting = append(awaiting, raw)
		}
	}
	return model.ClassifyActionItems(mine, awaiting, time.Now(), staleDays(ctx)), nil
}

// Account is a gh identity to aggregate the cross-repo view over. Token is the
// GH_TOKEN to use; empty means inherit the active account's environment.
type Account struct {
	Login string
	Token string
}

// AccountsToAggregate reads the logins listed in the global wlaunch.accounts git
// config (space/comma separated) and resolves each to a token, so the cross-repo
// view can span e.g. a personal and a work account. Falls back to the single
// active account when unset.
func AccountsToAggregate(ctx context.Context) []Account {
	b, err := data.Run(ctx, "", "git", "config", "--global", "--get", "wlaunch.accounts")
	if err != nil || strings.TrimSpace(string(b)) == "" {
		return []Account{{}}
	}
	fields := strings.FieldsFunc(string(b), func(r rune) bool {
		return r == ' ' || r == '\t' || r == '\n' || r == ','
	})
	var accts []Account
	seen := map[string]bool{}
	for _, login := range fields {
		if login == "" || seen[login] {
			continue
		}
		seen[login] = true
		accts = append(accts, Account{Login: login})
	}
	type tokenResult struct {
		idx   int
		token string
	}
	ch := make(chan tokenResult, len(accts))
	for i, acct := range accts {
		go func() {
			tok, err := data.Run(ctx, "", "gh", "auth", "token", "-u", acct.Login)
			if err != nil {
				ch <- tokenResult{idx: i}
				return
			}
			ch <- tokenResult{idx: i, token: strings.TrimSpace(string(tok))}
		}()
	}
	resolved := make([]Account, 0, len(accts))
	tokens := make([]string, len(accts))
	for range accts {
		res := <-ch
		tokens[res.idx] = res.token
	}
	for i, acct := range accts {
		if tokens[i] != "" {
			acct.Token = tokens[i]
			resolved = append(resolved, acct)
		}
	}
	if len(resolved) == 0 {
		return []Account{{}}
	}
	return resolved
}

// ListActionableAllRepos aggregates the cross-repo actionable set: for each
// account, my open PRs and PRs awaiting my review, mapped to local clones via
// slugToPath. Cheap tier — mergeable/CI/threads are left unknown (enrichment is a
// follow-up). A single failing account/search degrades to a partial result rather
// than failing the whole view.
func ListActionableAllRepos(ctx context.Context, accounts []Account, slugToPath map[string]string) ([]model.ActionItem, error) {
	var mine, awaiting []model.RawActionPR
	seen := map[string]bool{}
	add := func(dst *[]model.RawActionPR, sp ghSearchPR) {
		slug := sp.Repository.NameWithOwner
		key := strings.ToLower(slug) + "#" + strconv.Itoa(sp.Number)
		if seen[key] {
			return
		}
		seen[key] = true
		name := sp.Repository.Name
		if name == "" {
			name = lastSlugSeg(slug)
		}
		*dst = append(*dst, model.RawActionPR{
			Number:            sp.Number,
			Title:             sp.Title,
			Author:            sp.Author.Login,
			RepoName:          name,
			RepoRoot:          slugToPath[strings.ToLower(slug)],
			Mergeable:         "UNKNOWN",
			UnresolvedThreads: -1,
			LinesChanged:      -1,
			IsDraft:           sp.IsDraft,
			HasReviewers:      true, // unknown at this tier; assume present so stale keys off age only
			CreatedAt:         sp.CreatedAt,
			UpdatedAt:         sp.UpdatedAt,
		})
	}
	var firstErr error
	type querySpec struct {
		flag, val string
		mine      bool
	}
	type queryResult struct {
		mine bool
		prs  []ghSearchPR
		err  error
	}
	queries := []querySpec{
		{flag: "--author", val: "@me", mine: true},
		{flag: "--review-requested", val: "@me"},
	}
	ch := make(chan queryResult, len(accounts)*len(queries))
	for _, acct := range accounts {
		var env []string
		if acct.Token != "" {
			env = []string{"GH_TOKEN=" + acct.Token}
		}
		for _, q := range queries {
			env := env
			q := q
			go func() {
				b, err := data.RunEnv(ctx, "", env, "gh", "search", "prs",
					"--state", "open", q.flag, q.val, "--limit", "50",
					"--json", "number,title,repository,author,isDraft,createdAt,updatedAt")
				if err != nil {
					ch <- queryResult{mine: q.mine, err: err}
					return
				}
				prs, err := parseSearchPRs(b)
				ch <- queryResult{mine: q.mine, prs: prs, err: err}
			}()
		}
	}
	for i := 0; i < len(accounts)*len(queries); i++ {
		res := <-ch
		if res.err != nil {
			if firstErr == nil {
				firstErr = res.err
			}
			continue
		}
		for _, sp := range res.prs {
			if res.mine {
				add(&mine, sp)
			} else {
				add(&awaiting, sp)
			}
		}
	}
	items := model.ClassifyActionItems(mine, awaiting, time.Now(), staleDays(ctx))
	if len(items) == 0 && firstErr != nil {
		return nil, firstErr
	}
	return items, nil
}

// --- helpers ---

func loginFor(ctx context.Context, env []string) (string, error) {
	b, err := data.RunEnv(ctx, "", env, "gh", "api", "user", "-q", ".login")
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(b)), nil
}

func fetchUnresolvedThreads(ctx context.Context, env []string, slug string) (map[int]int, error) {
	owner, name, ok := splitSlug(slug)
	if !ok {
		return nil, fmt.Errorf("unparseable repo slug %q", slug)
	}
	const q = `query($owner:String!,$name:String!){repository(owner:$owner,name:$name){pullRequests(states:OPEN,first:50){nodes{number reviewThreads(first:100){nodes{isResolved}}}}}}`
	b, err := data.RunEnv(ctx, "", env, "gh", "api", "graphql",
		"-f", "query="+q, "-F", "owner="+owner, "-F", "name="+name)
	if err != nil {
		return nil, err
	}
	return parseThreads(b)
}

func fetchRepoPRMeta(ctx context.Context, env []string, slug, me string) (map[int]int, map[int]prTurnInfo, error) {
	owner, name, ok := splitSlug(slug)
	if !ok {
		return nil, nil, fmt.Errorf("unparseable repo slug %q", slug)
	}
	const q = `query($owner:String!,$name:String!){repository(owner:$owner,name:$name){pullRequests(states:OPEN,first:50){nodes{number author{login} reviewThreads(first:100){nodes{isResolved}} reviews(last:20){nodes{author{login} state submittedAt}} comments(last:20){nodes{author{login} createdAt}} commits(last:1){nodes{commit{authoredDate committedDate}}}}}}}`
	b, err := data.RunEnv(ctx, "", env, "gh", "api", "graphql",
		"-f", "query="+q, "-F", "owner="+owner, "-F", "name="+name)
	if err != nil {
		return nil, nil, err
	}
	threads, err := parseThreads(b)
	if err != nil {
		return nil, nil, err
	}
	turns, err := parseTurnInfo(b, me)
	if err != nil {
		return nil, nil, err
	}
	return threads, turns, nil
}

func fetchTurnInfo(ctx context.Context, env []string, slug, me string) (map[int]prTurnInfo, error) {
	owner, name, ok := splitSlug(slug)
	if !ok {
		return nil, fmt.Errorf("unparseable repo slug %q", slug)
	}
	const q = `query($owner:String!,$name:String!){repository(owner:$owner,name:$name){pullRequests(states:OPEN,first:50){nodes{number author{login} reviews(last:20){nodes{author{login} state submittedAt}} comments(last:20){nodes{author{login} createdAt}} commits(last:1){nodes{commit{authoredDate committedDate}}}}}}}`
	b, err := data.RunEnv(ctx, "", env, "gh", "api", "graphql",
		"-f", "query="+q, "-F", "owner="+owner, "-F", "name="+name)
	if err != nil {
		return nil, err
	}
	return parseTurnInfo(b, me)
}

func staleDays(ctx context.Context) int {
	b, err := data.Run(ctx, "", "git", "config", "--global", "--get", "wlaunch.staleDays")
	if err == nil {
		if n, e := strconv.Atoi(strings.TrimSpace(string(b))); e == nil && n > 0 {
			return n
		}
	}
	return defaultStaleDays
}

func splitSlug(slug string) (owner, name string, ok bool) {
	i := strings.Index(slug, "/")
	if i <= 0 || i >= len(slug)-1 {
		return "", "", false
	}
	return slug[:i], slug[i+1:], true
}

func lastSlugSeg(slug string) string {
	if i := strings.LastIndex(slug, "/"); i >= 0 {
		return slug[i+1:]
	}
	return slug
}
