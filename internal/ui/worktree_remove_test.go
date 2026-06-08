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
	m = step(t, m, key("d"))
	if m.confirm != confirmNone {
		t.Errorf("the main checkout must not enter a remove confirm")
	}
	if m.status == "" {
		t.Errorf("expected a status explaining the main checkout can't be removed")
	}
}

func TestRemoveOneConfirmThenCancel(t *testing.T) {
	m := worktreeModel(t)
	m = step(t, m, key("j")) // move to the non-main worktree (/wt/pr289)
	m = step(t, m, key("d"))
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
	m = step(t, m, key("j"))
	m = step(t, m, key("d"))
	nm, cmd := m.Update(key("y"))
	m = nm.(Model)
	if m.confirm != confirmNone {
		t.Errorf("y should clear the confirm")
	}
	if cmd == nil {
		t.Errorf("y should return the removal+reload command")
	}
	if m.state[model.ViewWorktrees] != stateLoading {
		t.Errorf("worktrees should be loading after removal kicks off")
	}
}

func TestRemoveAllExcludesMain(t *testing.T) {
	m := worktreeModel(t)
	m = step(t, m, key("D"))
	if m.confirm != confirmRemoveAll {
		t.Fatalf("confirm = %v, want confirmRemoveAll", m.confirm)
	}
	if len(m.confirmPaths) != 1 || m.confirmPaths[0] != "/wt/pr289" {
		t.Errorf("remove-all should exclude the main checkout; got %v", m.confirmPaths)
	}
}

func TestRemoveKeysIgnoredOutsideWorktrees(t *testing.T) {
	m := loadedModel(t) // PRs view
	m = step(t, m, key("d"))
	m = step(t, m, key("D"))
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
