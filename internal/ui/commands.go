package ui

import (
	"context"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/thomast8/wlaunch/internal/data/cache"
	"github.com/thomast8/wlaunch/internal/data/gh"
	"github.com/thomast8/wlaunch/internal/data/git"
	"github.com/thomast8/wlaunch/internal/data/repos"
	"github.com/thomast8/wlaunch/internal/model"
)

// --- async result messages ---

type reposLoadedMsg struct{ repos []model.Repo }

type prsLoadedMsg struct {
	gen uint64
	prs []model.PR
}

type branchesLoadedMsg struct {
	gen      uint64
	branches []model.Branch
}

type worktreesLoadedMsg struct {
	gen       uint64
	worktrees []model.Worktree
}

type loadErrMsg struct {
	gen  uint64
	view model.View
	err  error
}

// actionablesLoadedMsg carries the classified actionable set. It rides actionGen
// (the Actionable view's own generation), not the repo gen, so a repo switch
// never drops an in-flight all-repos fan-out.
type actionablesLoadedMsg struct {
	gen   uint64
	items []model.ActionItem
}

type prefetchDoneMsg struct{}

// --- tea.Cmd factories (each runs in its own goroutine; the gen guard in Update
// drops results from a superseded repo scope) ---

func loadReposCmd() tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
		defer cancel()
		rs, _ := repos.List(ctx)
		return reposLoadedMsg{repos: rs}
	}
}

func loadPRsCmd(repo string, gen uint64) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
		defer cancel()
		prs, err := gh.ListPRs(ctx, repo)
		if err != nil {
			return loadErrMsg{gen: gen, view: model.ViewPRs, err: err}
		}
		return prsLoadedMsg{gen: gen, prs: prs}
	}
}

// loadActionableThisRepoCmd builds the rich actionable set for one repo (pr list +
// CI + batched review-thread counts).
func loadActionableThisRepoCmd(repo string, gen uint64) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
		defer cancel()
		items, err := gh.ListActionableForRepo(ctx, repo)
		if err != nil {
			return loadErrMsg{gen: gen, view: model.ViewActionable, err: err}
		}
		return actionablesLoadedMsg{gen: gen, items: items}
	}
}

// loadActionableAllReposCmd aggregates actionable PRs across every configured gh
// account and maps them back to local clones. Heavier (multi-account search +
// slug→path scan), hence the longer timeout.
func loadActionableAllReposCmd(store *cache.Store, gen uint64) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		accounts := gh.AccountsToAggregate(ctx)
		slugMap, saved, ok := cache.Read[map[string]string](store, cache.KeySlugToPath())
		if !ok || !cache.Fresh(saved, 5*time.Minute) {
			if live, err := repos.SlugToPath(ctx); err == nil {
				slugMap = live
				cache.Write(store, cache.KeySlugToPath(), slugMap)
			}
		}
		items, err := gh.ListActionableAllRepos(ctx, accounts, slugMap)
		if err != nil {
			return loadErrMsg{gen: gen, view: model.ViewActionable, err: err}
		}
		return actionablesLoadedMsg{gen: gen, items: items}
	}
}

func loadBranchesCmd(repo string, gen uint64) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
		defer cancel()
		br, err := git.ListBranches(ctx, repo)
		if err != nil {
			return loadErrMsg{gen: gen, view: model.ViewBranches, err: err}
		}
		return branchesLoadedMsg{gen: gen, branches: br}
	}
}

func loadWorktreesCmd(repo string, gen uint64) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
		defer cancel()
		wts, err := git.ListWorktrees(ctx, repo)
		if err != nil {
			return loadErrMsg{gen: gen, view: model.ViewWorktrees, err: err}
		}
		return worktreesLoadedMsg{gen: gen, worktrees: wts}
	}
}

// gitDefaultBranch resolves a repo's default branch.
// Unlike every other data call in this file it is synchronous rather than a tea.Cmd:
// it runs on the launch keypress, one frame before the program quits, so there is no
// later frame for a message to land in. The git probes are local ref reads against an
// already-open repo (~50ms), and the context bounds the pathological case.
func gitDefaultBranch(repo string) string {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	return git.DefaultBranch(ctx, repo)
}

func prefetchRepoTabsCmd(store *cache.Store, repo string) tea.Cmd {
	if repo == "" {
		return nil
	}
	return tea.Batch(
		prefetchPRsCacheCmd(store, repo),
		prefetchBranchesCacheCmd(store, repo),
		prefetchWorktreesCacheCmd(store, repo),
	)
}

func prefetchPRsCacheCmd(store *cache.Store, repo string) tea.Cmd {
	return func() tea.Msg {
		if _, saved, ok := cache.Read[[]model.PR](store, cache.KeyPRs(repo)); ok && cache.Fresh(saved, cache.CloudTTL) {
			return prefetchDoneMsg{}
		}
		ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
		defer cancel()
		if prs, err := gh.ListPRs(ctx, repo); err == nil {
			cache.Write(store, cache.KeyPRs(repo), prs)
		}
		return prefetchDoneMsg{}
	}
}

func prefetchBranchesCacheCmd(store *cache.Store, repo string) tea.Cmd {
	return func() tea.Msg {
		if _, saved, ok := cache.Read[[]model.Branch](store, cache.KeyBranches(repo)); ok && cache.Fresh(saved, cache.CloudTTL) {
			return prefetchDoneMsg{}
		}
		ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
		defer cancel()
		if branches, err := git.ListBranches(ctx, repo); err == nil {
			cache.Write(store, cache.KeyBranches(repo), branches)
		}
		return prefetchDoneMsg{}
	}
}

func prefetchWorktreesCacheCmd(store *cache.Store, repo string) tea.Cmd {
	return func() tea.Msg {
		if _, saved, ok := cache.Read[[]model.Worktree](store, cache.KeyWorktrees(repo)); ok && cache.Fresh(saved, cache.CloudTTL) {
			return prefetchDoneMsg{}
		}
		ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
		defer cancel()
		if worktrees, err := git.ListWorktrees(ctx, repo); err == nil {
			cache.Write(store, cache.KeyWorktrees(repo), worktrees)
		}
		return prefetchDoneMsg{}
	}
}

type worktreesRemovedMsg struct {
	gen        uint64
	removed    []string // paths git actually removed (so the model can splice them out)
	dirty      []string // skipped because dirty — force-removable (model offers force)
	dirtyFiles int      // total uncommitted files across `dirty` (for the discard warning)
	failed     int      // hard failures (not recoverable with --force)
}

// removeWorktreesCmd removes each path (one for single-remove, many for remove-all).
// With force=false, git.RemoveWorktree refuses a dirty worktree; those land in `dirty`
// (force-removable) so the model can offer a force escalation, while hard failures
// (incl. locked worktrees) land in `failed`. force=true discards uncommitted files.
// The caller splices `removed` out.
func removeWorktreesCmd(repo string, paths []string, force bool, gen uint64) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		var removed, dirty []string
		var dirtyFiles, failed int
		for _, p := range paths {
			err := git.RemoveWorktree(ctx, repo, p, force)
			switch {
			case err == nil:
				removed = append(removed, p)
			case !force && git.IsForceRemovable(err):
				dirty = append(dirty, p)
				dirtyFiles += git.DirtyFileCount(ctx, p)
			default:
				failed++
			}
		}
		return worktreesRemovedMsg{gen: gen, removed: removed, dirty: dirty, dirtyFiles: dirtyFiles, failed: failed}
	}
}

// branchesRefreshedMsg carries the post-fetch/pull branch list (nil = couldn't
// re-list, keep the old one) plus a status line. No loading state, so the list
// stays visible during the op and swaps when done — no spinner flicker.
type branchesRefreshedMsg struct {
	gen      uint64
	branches []model.Branch
	status   string
}

func fetchBranchCmd(repo string, b model.Branch, gen uint64) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		status := "✓ fetched " + b.Name
		if err := git.FetchBranch(ctx, repo, b); err != nil {
			status = b.Name + ": " + friendly(err)
		}
		br, err := git.ListBranches(ctx, repo)
		if err != nil {
			br = nil
		}
		return branchesRefreshedMsg{gen: gen, branches: br, status: status}
	}
}

func pullBranchCmd(repo string, b model.Branch, checkoutPath string, gen uint64) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		status := "✓ " + b.Name + " up to date"
		if err := git.PullBranch(ctx, repo, b, checkoutPath); err != nil {
			status = b.Name + ": " + friendly(err)
		}
		br, err := git.ListBranches(ctx, repo)
		if err != nil {
			br = nil
		}
		return branchesRefreshedMsg{gen: gen, branches: br, status: status}
	}
}

// branchDeletedMsg is the result of a single-branch delete. On success the model
// splices the branch out in-memory; on an "unmerged" refusal of a SAFE delete the
// model escalates to a force confirm (the squash-merge case).
type branchDeletedMsg struct {
	gen    uint64
	name   string
	forced bool
	err    error // nil = deleted
}

func deleteBranchCmd(repo, name string, force bool, gen uint64) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		err := git.DeleteBranch(ctx, repo, name, force)
		return branchDeletedMsg{gen: gen, name: name, forced: force, err: err}
	}
}

// branchCleanTarget pairs a branch with the delete mode cleanup chose for it: force
// for a gone branch (remote-deleted = done, squash-merge-proof), safe for a
// no-upstream one (git's merge check then skips any still holding unique commits).
type branchCleanTarget struct {
	name  string
	force bool
}

type branchesCleanedMsg struct {
	gen     uint64
	removed []string // branches git actually deleted (the model splices these out)
	skipped int      // safe-delete refused (unmerged) or otherwise failed
}

func cleanBranchesCmd(repo string, targets []branchCleanTarget, gen uint64) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		defer cancel()
		var removed []string
		var skipped int
		for _, t := range targets {
			if err := git.DeleteBranch(ctx, repo, t.name, t.force); err != nil {
				skipped++
			} else {
				removed = append(removed, t.name)
			}
		}
		return branchesCleanedMsg{gen: gen, removed: removed, skipped: skipped}
	}
}

// isUnmerged reports whether a safe (`-d`) delete was refused because the branch
// still holds commits not merged into HEAD — the only case that warrants offering
// a force escalation. Any other failure (checked out, etc.) is surfaced as-is.
func isUnmerged(err error) bool {
	return err != nil && strings.Contains(err.Error(), "not fully merged")
}

// friendly maps a raw subprocess error to a short, human line for an error state.
func friendly(err error) string {
	s := err.Error()
	switch {
	case strings.Contains(s, "executable file not found"):
		return "command not found on PATH."
	case strings.Contains(s, "context deadline exceeded"):
		return "timed out."
	case strings.Contains(s, "auth"):
		return "gh isn't authenticated for this repo (gh auth login)."
	case strings.Contains(s, "no default remote"), strings.Contains(s, "none of the git remotes"), strings.Contains(s, "no git remotes found"):
		return "no GitHub remote configured."
	case strings.Contains(s, "not a git repository"):
		return "not a git repository."
	case strings.Contains(s, "no upstream"):
		return "no upstream"
	case strings.Contains(s, "is already checked out"), strings.Contains(s, "checked out at"):
		return "checked out elsewhere"
	case strings.Contains(s, "non-fast-forward"), strings.Contains(s, "rejected"), strings.Contains(s, "Not possible to fast-forward"), strings.Contains(s, "diverging"):
		return "diverged — can't fast-forward"
	case strings.Contains(s, "couldn't find remote ref"):
		return "upstream gone from remote"
	case strings.Contains(s, "cannot lock ref"), strings.Contains(s, "could not delete references"):
		return "a remote-tracking ref is broken (case-colliding ref?)"
	default:
		if len(s) > 90 {
			s = s[:90] + "…"
		}
		return s
	}
}
