// Package model holds the pure domain types for wlaunch: the things shown in the
// TUI (repos, PRs, branches, worktrees) and the enums for views and launch
// targets. It has NO bubbletea/UI imports, so the data layer and its tests can
// use it headless.
package model

// View is one of the main-panel views, cycled with the ←/→ arrows in the order
// declared here.
type View int

const (
	ViewPRs View = iota
	ViewBranches
	ViewWorktrees
	ViewActionable
	viewCount
)

// Label is the tab-strip caption for a view.
func (v View) Label() string {
	switch v {
	case ViewPRs:
		return "PRs"
	case ViewBranches:
		return "Branches"
	case ViewWorktrees:
		return "Worktrees"
	case ViewActionable:
		return "Actionable"
	default:
		return "?"
	}
}

// Next / Prev cycle the active view for ←/→, wrapping around.
func (v View) Next() View { return (v + 1) % viewCount }
func (v View) Prev() View { return (v + viewCount - 1) % viewCount }

// Target is the launch action chosen for a selected item.
type Target int

const (
	TargetDefault Target = iota
	TargetClaude
	TargetCodex
	TargetLazygit
	TargetSerie
	TargetShell
)

// Tool resolves a Target to the concrete tool string used in the stdout contract.
// The default open action maps to claude (the dominant use).
func (t Target) Tool() string {
	switch t {
	case TargetCodex:
		return "codex"
	case TargetLazygit:
		return "lazygit"
	case TargetSerie:
		return "serie"
	case TargetShell:
		return "shell"
	default: // TargetDefault, TargetClaude
		return "claude"
	}
}

// Repo is a git repository the launcher can scope to, or a plain non-repo
// quick-launch location (Plain=true; e.g. $HOME) that can only be launched
// on directly, not browsed for PRs/branches/worktrees.
type Repo struct {
	Path  string // absolute path to the main checkout root (or plain dir)
	Name  string // basename(Path) for repos, "~" for the home entry
	Plain bool   // true for a non-git quick location; zero value = real repo
}

// PR mirrors the fields wlaunch reads from `gh pr list --json`.
type PR struct {
	Number      int
	Title       string
	HeadRefName string
	Author      string
}

// Branch is a local branch with its upstream tracking state.
type Branch struct {
	Name           string
	Upstream       string // e.g. origin/foo; empty if none
	Ahead          int
	Behind         int
	Gone           bool // upstream is [gone]
	LastCommitUnix int64
	Subject        string
	IsCurrent      bool // HEAD of the main checkout
}

// Worktree is one record from `git worktree list --porcelain`.
type Worktree struct {
	Path     string
	Branch   string // empty if detached
	HEAD     string // short sha
	Detached bool
	Locked   bool
	Prunable bool
	Bare     bool
	IsMain   bool // first record = main checkout
}
