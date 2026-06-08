package ui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"github.com/thomast8/wlaunch/internal/model"
)

// realisticModel mimics the screenshot: long titles and long, hyphenated author
// names that previously wrapped onto a second line.
func realisticModel(t *testing.T, w, h int) Model {
	t.Helper()
	m := New()
	m = step(t, m, tea.WindowSizeMsg{Width: w, Height: h})
	m = step(t, m, reposLoadedMsg{repos: []model.Repo{
		{Path: "/r/PolicyAsCode", Name: "PolicyAsCode"},
		{Path: "/r/warp-claude-workflow", Name: "warp-claude-workflow"},
	}})
	m = step(t, m, prsLoadedMsg{gen: m.gen, prs: []model.PR{
		{Number: 312, Title: "fix: match state embed text by name so each state queries its own text", HeadRefName: "fix/state-chunk-matcher-name-lookup", Author: "annelies-jaspers_kyndryl"},
		{Number: 310, Title: "fix: plug state regeneration retry into extraction pipeline and raise base token budget", HeadRefName: "fix/state-machine-extraction-budget-retry", Author: "mohammad-beit-sadi_kyndryl"},
		{Number: 305, Title: "[OPA] drop directory", HeadRefName: "feature/opa-drop-directory", Author: "thomas-tiotto_kyndryl"},
	}})
	return m
}

// TestNoLineWraps is the headless guard for the screenshot bug: every rendered
// line must fit within the terminal width (no overflow), and the panel must show
// exactly one line per visible PR (no wrap-induced extra rows).
func TestNoLineWraps(t *testing.T) {
	const w, h = 100, 24
	m := realisticModel(t, w, h)
	out := m.View()

	for i, line := range strings.Split(out, "\n") {
		if dw := lipgloss.Width(line); dw > w {
			t.Errorf("line %d width %d > %d: %q", i, dw, w, ansi.Strip(line))
		}
	}

	// Each PR number must appear on exactly one line — a wrapped row would push a
	// continuation line with no number, or drop a number off the visible panel.
	plain := ansi.Strip(out)
	for _, num := range []string{"#312", "#310", "#305"} {
		if got := strings.Count(plain, num); got != 1 {
			t.Errorf("expected %q exactly once, got %d", num, got)
		}
	}
}

// TestSnapshot logs the ANSI-stripped layout so the column alignment can be
// eyeballed from test output (colors aside).
func TestSnapshot(t *testing.T) {
	m := realisticModel(t, 100, 16)
	t.Logf("\n%s", ansi.Strip(m.View()))
}

func TestSnapshotAllViews(t *testing.T) {
	const w, h = 100, 12
	m := realisticModel(t, w, h)
	m = step(t, m, branchesLoadedMsg{gen: m.gen, branches: []model.Branch{
		{Name: "docs/diataxis-redesign", Upstream: "origin/docs/diataxis-redesign", LastCommitUnix: 1700000000, Subject: "docs: point sample commands at bundled PDF", IsCurrent: true},
		{Name: "fix/state-machine-extraction-budget-retry", Ahead: 3, Behind: 1, LastCommitUnix: 1699900000, Subject: "raise base token budget"},
		{Name: "stale/old", Gone: true, LastCommitUnix: 1690000000, Subject: "abandoned work"},
	}})
	m = step(t, m, worktreesLoadedMsg{gen: m.gen, worktrees: []model.Worktree{
		{Path: "/Users/x/GitRepos/PolicyAsCode", Branch: "main", HEAD: "abc123def456", IsMain: true},
		{Path: "/Users/x/worktrees/PolicyAsCode/pr289", Branch: "fix/state-chunk-matcher", HEAD: "999aaa888bbb", Locked: true},
		{Path: "/Users/x/worktrees/PolicyAsCode/detached", HEAD: "777ccc666ddd", Detached: true},
	}})

	for _, v := range []struct {
		name string
		key  tea.KeyMsg
	}{
		{"Branches", tea.KeyMsg{Type: tea.KeyRight}},
		{"Worktrees", tea.KeyMsg{Type: tea.KeyRight}},
	} {
		m = step(t, m, v.key)
		out := m.View()
		for i, line := range strings.Split(out, "\n") {
			if dw := lipgloss.Width(line); dw > w {
				t.Errorf("[%s] line %d width %d > %d", v.name, i, dw, w)
			}
		}
		t.Logf("=== %s view ===\n%s", v.name, ansi.Strip(out))
	}

	// sidebar focused: snapshot to eyeball the focus affordance + scoped marker
	m = step(t, m, tea.KeyMsg{Type: tea.KeyTab})
	t.Logf("=== sidebar focused ===\n%s", ansi.Strip(m.View()))
}
