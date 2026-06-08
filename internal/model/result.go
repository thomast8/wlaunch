package model

import "strings"

// Kind identifies what a Selection points at, and which dir-resolution path the
// wl wrapper takes.
type Kind string

const (
	KindPR       Kind = "pr"
	KindBranch   Kind = "branch"
	KindWorktree Kind = "worktree"
	KindRepo     Kind = "repo"
)

// Selection is what the TUI emits on a successful pick. Encode() is the single
// source of truth for the stdout contract the wl wrapper parses.
type Selection struct {
	Kind     Kind
	RepoRoot string // absolute main-checkout root; always set
	Ref      string // PR number | branch name | worktree path | "" for repo
	Tool     string // claude | lazygit | serie | shell
}

// schemaVersion is the leading field of the contract. Bump only on a breaking
// change, and append fields rather than reordering.
const schemaVersion = "v1"

// Encode renders the one-line, tab-separated result with a trailing newline,
// mirroring the `printf '%s\n'` convention of the bin scripts.
func (s Selection) Encode() string {
	return strings.Join([]string{
		schemaVersion,
		string(s.Kind),
		s.RepoRoot,
		s.Ref,
		s.Tool,
	}, "\t") + "\n"
}
