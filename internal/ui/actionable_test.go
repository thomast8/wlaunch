package ui

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/thomast8/wlaunch/internal/model"
)

// actionableModel enters the Actionable view (one KeyLeft from PRs, since
// Actionable is the last view and the cycle wraps) and feeds it items as if the
// async load completed. The load cmd returned on entry is discarded by step, so
// no real gh call happens.
func actionableModel(t *testing.T, items []model.ActionItem) Model {
	t.Helper()
	m := loadedModel(t)
	m = step(t, m, tea.KeyMsg{Type: tea.KeyLeft}) // PRs -> Actionable (wraps)
	if m.view != model.ViewActionable {
		t.Fatalf("view = %v, want Actionable", m.view)
	}
	if m.state[model.ViewActionable] != stateLoading {
		t.Fatalf("entering Actionable should kick a load; state = %v", m.state[model.ViewActionable])
	}
	return step(t, m, actionablesLoadedMsg{gen: m.actionGen, items: items})
}

func TestActionableViewPickEmits(t *testing.T) {
	items := []model.ActionItem{
		{RepoRoot: "/r", RepoName: "r", Number: 221, Title: "docs", Marker: "⚠", Summary: "changes+34"},
		{RepoRoot: "/r", RepoName: "r", Number: 300, Title: "pub", Marker: "✗", Summary: "conflict"},
	}
	m := actionableModel(t, items)
	if m.state[model.ViewActionable] != stateReady {
		t.Fatalf("state = %v, want ready", m.state[model.ViewActionable])
	}
	m = leader(t, m, "c") // claude on the first (most-urgent) item
	if m.Selection() == nil {
		t.Fatal("expected a selection")
	}
	if got := m.Selection().Encode(); got != "v1\tpr\t/r\t221\tclaude\t\n" {
		t.Errorf("Encode() = %q", got)
	}
}

// A cross-repo item whose repo isn't cloned locally (empty RepoRoot) is
// display-only: launching it sets a status instead of emitting a selection.
func TestActionableNonLocalNotLaunchable(t *testing.T) {
	items := []model.ActionItem{{RepoRoot: "", RepoName: "remote", Number: 7, Title: "x", Marker: "·", Summary: "review"}}
	m := actionableModel(t, items)
	m = leader(t, m, "c")
	if m.Selection() != nil {
		t.Errorf("expected no selection for a non-cloned item, got %+v", m.Selection())
	}
	if m.status == "" {
		t.Error("expected a status explaining the item isn't local")
	}
}

func TestActionableScopeToggle(t *testing.T) {
	m := actionableModel(t, nil)
	if m.actScope != scopeThisRepo {
		t.Fatalf("default scope = %v, want this-repo", m.actScope)
	}
	m = leader(t, m, "a") // ;a toggles scope
	if m.actScope != scopeAllRepos {
		t.Errorf("after ;a scope = %v, want all-repos", m.actScope)
	}
	if m.state[model.ViewActionable] != stateLoading {
		t.Error("toggling scope should kick a reload")
	}
}

// Typing a reason keyword filters the list, because the summary is part of each
// row's filter text.
func TestActionableFilterByReason(t *testing.T) {
	items := []model.ActionItem{
		{RepoRoot: "/r", RepoName: "r", Number: 300, Title: "pub", Marker: "✗", Summary: "conflict"},
		{RepoRoot: "/r", RepoName: "r", Number: 221, Title: "docs", Marker: "⚠", Summary: "changes+34"},
	}
	m := actionableModel(t, items)
	all := len(m.visibleRows())
	m = typeStr(t, m, "conflict")
	if got := len(m.visibleRows()); got != 1 {
		t.Errorf("filter 'conflict' -> %d rows (was %d), want 1", got, all)
	}
}

func TestActionableViewRenders(t *testing.T) {
	items := []model.ActionItem{{RepoRoot: "/r", RepoName: "r", Number: 1, Title: "t", Marker: "✗", Summary: "conflict"}}
	m := actionableModel(t, items)
	if out := m.View(); out == "" {
		t.Error("empty Actionable view")
	}
	m = leader(t, m, "a") // switch to all-repos (renders the repo column)
	m = step(t, m, actionablesLoadedMsg{gen: m.actionGen, items: items})
	if out := m.View(); out == "" {
		t.Error("empty all-repos Actionable view")
	}
}
