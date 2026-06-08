package ui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/thomast8/wlaunch/internal/model"
)

func branchesView(t *testing.T) Model {
	t.Helper()
	m := loadedModel(t)
	m = step(t, m, tea.KeyMsg{Type: tea.KeyRight}) // PRs -> Branches
	if m.view != model.ViewBranches {
		t.Fatalf("expected Branches view, got %v", m.view)
	}
	return m
}

func TestFetchKeyKicksRefresh(t *testing.T) {
	m := branchesView(t) // cursor 0 = "main" (has upstream, from loadedModel)
	nm, cmd := m.Update(key("f"))
	m = nm.(Model)
	if cmd == nil {
		t.Error("f should return a fetch command")
	}
	if !strings.HasPrefix(m.status, "fetching ") {
		t.Errorf("status = %q, want 'fetching <branch>…'", m.status)
	}
}

func TestPullKeyKicksRefresh(t *testing.T) {
	m := branchesView(t) // cursor 0 = "main" (has upstream, from loadedModel)
	nm, cmd := m.Update(key("p"))
	m = nm.(Model)
	if cmd == nil {
		t.Error("p should return a pull command")
	}
	if !strings.HasPrefix(m.status, "pulling ") {
		t.Errorf("status = %q, want 'pulling …'", m.status)
	}
}

func TestFetchPullIgnoredOutsideBranches(t *testing.T) {
	m := loadedModel(t) // PRs view
	_, cmdF := m.Update(key("f"))
	_, cmdP := m.Update(key("p"))
	if cmdF != nil || cmdP != nil {
		t.Error("f/p should do nothing outside the Branches view")
	}
}

func TestBranchesRefreshedSwapsListAndStatus(t *testing.T) {
	m := branchesView(t)
	nm, cmd := m.Update(branchesRefreshedMsg{
		gen:      m.gen,
		branches: []model.Branch{{Name: "only", Upstream: "origin/only"}},
		status:   "✓ fetched",
	})
	m = nm.(Model)
	if cmd != nil {
		t.Error("refresh carries its own data; no follow-up reload expected")
	}
	if m.status != "✓ fetched" {
		t.Errorf("status = %q", m.status)
	}
	if len(m.branches) != 1 || m.branches[0].Name != "only" {
		t.Errorf("branches not swapped: %+v", m.branches)
	}
}

func TestBranchesRefreshedNilKeepsOldList(t *testing.T) {
	m := branchesView(t)
	before := len(m.branches)
	nm, _ := m.Update(branchesRefreshedMsg{gen: m.gen, branches: nil, status: "fetch failed: boom"})
	m = nm.(Model)
	if len(m.branches) != before {
		t.Errorf("nil branches should keep the old list (%d), got %d", before, len(m.branches))
	}
	if m.status != "fetch failed: boom" {
		t.Errorf("status = %q", m.status)
	}
}
