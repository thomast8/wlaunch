package ui

import (
	"context"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

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

type worktreesRemovedMsg struct {
	gen     uint64
	removed int
	failed  int
}

// removeWorktreesCmd removes each path (one for single-remove, many for remove-all)
// and reports counts; git.RemoveWorktree refuses dirty ones, so those land in failed.
func removeWorktreesCmd(repo string, paths []string, gen uint64) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		var removed, failed int
		for _, p := range paths {
			if err := git.RemoveWorktree(ctx, repo, p); err != nil {
				failed++
			} else {
				removed++
			}
		}
		return worktreesRemovedMsg{gen: gen, removed: removed, failed: failed}
	}
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
	case strings.Contains(s, "no default remote"), strings.Contains(s, "none of the git remotes"):
		return "no GitHub remote configured."
	case strings.Contains(s, "not a git repository"):
		return "not a git repository."
	default:
		if len(s) > 90 {
			s = s[:90] + "…"
		}
		return s
	}
}
