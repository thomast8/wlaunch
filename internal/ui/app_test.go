package ui

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/thomast8/wlaunch/internal/model"
)

// step applies one message to the model and returns the next model.
func step(t *testing.T, m Model, msg tea.Msg) Model {
	t.Helper()
	nm, _ := m.Update(msg)
	out, ok := nm.(Model)
	if !ok {
		t.Fatalf("Update returned %T, not Model", nm)
	}
	return out
}

func key(s string) tea.KeyMsg { return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(s)} }

func loadedModel(t *testing.T) Model {
	t.Helper()
	m := New()
	m = step(t, m, tea.WindowSizeMsg{Width: 120, Height: 40})
	m = step(t, m, reposLoadedMsg{repos: []model.Repo{{Path: "/r", Name: "r"}}})
	m = step(t, m, prsLoadedMsg{gen: m.gen, prs: []model.PR{
		{Number: 289, Title: "fix things", HeadRefName: "fix/x", Author: "tho"},
		{Number: 232, Title: "feat stuff", HeadRefName: "feat/y", Author: "moh"},
	}})
	m = step(t, m, branchesLoadedMsg{gen: m.gen, branches: []model.Branch{
		{Name: "main", Upstream: "origin/main", IsCurrent: true},
		{Name: "feat/y", Ahead: 2},
	}})
	m = step(t, m, worktreesLoadedMsg{gen: m.gen, worktrees: []model.Worktree{
		{Path: "/r", Branch: "main", HEAD: "abc123", IsMain: true},
		{Path: "/wt/pr289", Branch: "fix/x", HEAD: "def456"},
	}})
	return m
}

func TestPickPREmitsContract(t *testing.T) {
	m := loadedModel(t)
	if m.state[model.ViewPRs] != stateReady {
		t.Fatalf("state = %v, want ready", m.state[model.ViewPRs])
	}
	m = step(t, m, key("c")) // claude on the first PR
	if m.Selection() == nil {
		t.Fatal("expected a selection after pressing c")
	}
	if got := m.Selection().Encode(); got != "v1\tpr\t/r\t289\tclaude\n" {
		t.Errorf("Encode() = %q", got)
	}
}

func TestPickPRSecondRowLazygit(t *testing.T) {
	m := loadedModel(t)
	m = step(t, m, key("j")) // move to PR #232
	m = step(t, m, key("l")) // lazygit
	if got := m.Selection().Encode(); got != "v1\tpr\t/r\t232\tlazygit\n" {
		t.Errorf("Encode() = %q", got)
	}
}

func TestCancelEmitsNothing(t *testing.T) {
	m := loadedModel(t)
	m = step(t, m, key("q"))
	if !m.quit {
		t.Error("expected quit flag")
	}
	if m.Selection() != nil {
		t.Errorf("expected nil selection on cancel, got %+v", m.Selection())
	}
}

func TestStalePRLoadDropped(t *testing.T) {
	m := loadedModel(t)
	// A late load from a superseded generation must not overwrite the panel.
	m = step(t, m, prsLoadedMsg{gen: m.gen - 1, prs: []model.PR{{Number: 999}}})
	if len(m.prs) != 2 || m.prs[0].Number != 289 {
		t.Errorf("stale load clobbered current PRs: %+v", m.prs)
	}
}

func TestViewRendersWithoutPanic(t *testing.T) {
	m := loadedModel(t)
	if out := m.View(); out == "" {
		t.Error("View() returned empty for a ready model")
	}
	// also exercise loading/empty/error panels
	m.state[model.ViewPRs] = stateLoading
	_ = m.View()
	m.state[model.ViewPRs] = stateEmpty
	_ = m.View()
	m.state[model.ViewPRs] = stateError
	m.errMsg[model.ViewPRs] = "boom"
	_ = m.View()
}

func TestSwitchToBranchesAndPick(t *testing.T) {
	m := loadedModel(t)
	m = step(t, m, tea.KeyMsg{Type: tea.KeyRight}) // PRs -> Branches
	if m.view != model.ViewBranches {
		t.Fatalf("view = %v, want Branches", m.view)
	}
	m = step(t, m, key("c"))
	if m.Selection() == nil || m.Selection().Encode() != "v1\tbranch\t/r\tmain\tclaude\n" {
		t.Errorf("branch pick = %v", m.Selection())
	}
}

func TestSwitchToReposAndPickShell(t *testing.T) {
	m := loadedModel(t)
	m = step(t, m, tea.KeyMsg{Type: tea.KeyLeft}) // PRs -> Repos (wrap left)
	if m.view != model.ViewRepos {
		t.Fatalf("view = %v, want Repos", m.view)
	}
	m = step(t, m, key("o")) // default (claude) on the scoped/first repo
	sel := m.Selection()
	if sel == nil || sel.Kind != model.KindRepo || sel.Ref != "" || sel.RepoRoot != "/r" {
		t.Errorf("repo pick = %+v", sel)
	}
}

func TestNewBranchAction(t *testing.T) {
	m := loadedModel(t)
	m = step(t, m, tea.KeyMsg{Type: tea.KeyRight}) // -> Branches
	m = step(t, m, key("n"))                       // new-branch prompt
	if m.inMode != inputNewBranch {
		t.Fatalf("inMode = %v, want newBranch", m.inMode)
	}
	for _, r := range "feat/zzz" {
		m = step(t, m, key(string(r)))
	}
	m = step(t, m, tea.KeyMsg{Type: tea.KeyEnter})
	if m.Selection() == nil || m.Selection().Encode() != "v1\tbranch\t/r\tfeat/zzz\tclaude\n" {
		t.Errorf("new-branch pick = %v", m.Selection())
	}
}

func TestFilterReducesRows(t *testing.T) {
	m := loadedModel(t)
	all := len(m.visibleRows())
	m = step(t, m, key("/"))
	for _, r := range "289" {
		m = step(t, m, key(string(r)))
	}
	got := len(m.visibleRows())
	if got >= all || got != 1 {
		t.Errorf("filtered rows = %d (was %d), want 1", got, all)
	}
}
