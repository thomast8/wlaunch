package ui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/thomast8/wlaunch/internal/model"
)

// worktreeModel returns a loaded model parked on the Worktrees view (cursor on the
// main checkout at index 0; index 1 is a removable worktree, from loadedModel).
func worktreeModel(t *testing.T) Model {
	t.Helper()
	m := loadedModel(t)
	m = step(t, m, tea.KeyMsg{Type: tea.KeyRight}) // -> Branches
	m = step(t, m, tea.KeyMsg{Type: tea.KeyRight}) // -> Worktrees
	if m.view != model.ViewWorktrees {
		t.Fatalf("expected Worktrees view, got %v", m.view)
	}
	return m
}

func TestRemoveMainBlocked(t *testing.T) {
	m := worktreeModel(t) // cursor 0 = the main checkout (IsMain)
	m = leader(t, m, "d")
	if m.confirm != confirmNone {
		t.Errorf("the main checkout must not enter a remove confirm")
	}
	if m.status == "" {
		t.Errorf("expected a status explaining the main checkout can't be removed")
	}
}

func TestRemoveOneConfirmThenCancel(t *testing.T) {
	m := worktreeModel(t)
	m = step(t, m, down) // move to the non-main worktree (/wt/pr289)
	m = leader(t, m, "d")
	if m.confirm != confirmRemoveOne {
		t.Fatalf("confirm = %v, want confirmRemoveOne", m.confirm)
	}
	if len(m.confirmPaths) != 1 || m.confirmPaths[0] != "/wt/pr289" {
		t.Fatalf("confirmPaths = %v, want [/wt/pr289]", m.confirmPaths)
	}
	m = step(t, m, key("n")) // cancel
	if m.confirm != confirmNone || m.confirmPaths != nil {
		t.Errorf("n should clear the confirm, got %v / %v", m.confirm, m.confirmPaths)
	}
}

func TestRemoveOneConfirmYesKicksRemoval(t *testing.T) {
	m := worktreeModel(t)
	m = step(t, m, down)
	m = leader(t, m, "d")
	nm, cmd := m.Update(key("y"))
	m = nm.(Model)
	if m.confirm != confirmNone {
		t.Errorf("y should clear the confirm")
	}
	if cmd == nil {
		t.Errorf("y should return the removal command")
	}
	if m.status != "removing…" {
		t.Errorf("expected a 'removing…' status, got %q", m.status)
	}
}

// TestRemovalSplicesInMemory is the guard for the "don't re-read all of them" fix:
// the removed-result message must splice the path out of the in-memory list and
// issue NO reload command.
func TestRemovalSplicesInMemory(t *testing.T) {
	m := worktreeModel(t)
	before := len(m.worktrees)
	nm, cmd := m.Update(worktreesRemovedMsg{gen: m.gen, removed: []string{"/wt/pr289"}, failed: 0})
	m = nm.(Model)
	if cmd != nil {
		t.Errorf("in-memory splice should issue no reload command")
	}
	if len(m.worktrees) != before-1 {
		t.Errorf("worktrees not spliced: before=%d after=%d", before, len(m.worktrees))
	}
	if m.worktreeByPath("/wt/pr289") != nil {
		t.Errorf("/wt/pr289 should be gone from the in-memory list")
	}
	if m.state[model.ViewWorktrees] != stateReady {
		t.Errorf("state should stay ready (no reload spinner), got %v", m.state[model.ViewWorktrees])
	}
}

func TestRemoveAllExcludesMain(t *testing.T) {
	m := worktreeModel(t)
	m = leader(t, m, "D")
	if m.confirm != confirmRemoveAll {
		t.Fatalf("confirm = %v, want confirmRemoveAll", m.confirm)
	}
	if len(m.confirmPaths) != 1 || m.confirmPaths[0] != "/wt/pr289" {
		t.Errorf("remove-all should exclude the main checkout; got %v", m.confirmPaths)
	}
}

func TestRemoveKeysIgnoredOutsideWorktrees(t *testing.T) {
	m := loadedModel(t) // PRs view
	m = leader(t, m, "d")
	m = leader(t, m, "D")
	if m.confirm != confirmNone {
		t.Errorf("d/D should do nothing outside the Worktrees view")
	}
}

func TestRemovalStatusFormatting(t *testing.T) {
	if got := removalStatus(3, 0); got != "✓ removed 3 worktree(s)" {
		t.Errorf("removalStatus(3,0) = %q", got)
	}
	if got := removalStatus(2, 1); !strings.Contains(got, "removed 2") || !strings.Contains(got, "1 skipped") {
		t.Errorf("removalStatus(2,1) = %q", got)
	}
	if got := removalStatus(0, 2); !strings.Contains(got, "removed none") {
		t.Errorf("removalStatus(0,2) = %q", got)
	}
}
