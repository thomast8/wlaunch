package ui

import (
	"regexp"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/muesli/termenv"

	"github.com/thomast8/wlaunch/internal/model"
)

// realisticModel mimics the screenshot: long titles and long, hyphenated author
// names that previously wrapped onto a second line.
func realisticModel(t *testing.T, w, h int) Model {
	t.Helper()
	m := stubMain(New(), nil)
	m.cache = nil
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

	m = step(t, m, tea.KeyMsg{Type: tea.KeyRight}) // sidebar (the startup default) -> panel, still PRs

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
	m = step(t, m, tea.KeyMsg{Type: tea.KeyShiftTab})
	t.Logf("=== sidebar focused ===\n%s", ansi.Strip(m.View()))
}

// The reported bug: with the sidebar focused, the panel still painted a highlight
// bar (and a ▸) on its cursor row, so two rows looked selected at once and the panel
// row looked like what Enter would open — it isn't; Enter launches the sidebar repo.
// Exactly one ▸ cursor marker may be visible, in the focused pane.
func TestOnlyTheFocusedPaneMarksItsCursorRow(t *testing.T) {
	// Force color on so the highlight background is actually emitted (the test
	// renderer is bound to stderr and defaults to no-color under `go test`).
	oldProfile := renderer.ColorProfile()
	renderer.SetColorProfile(termenv.TrueColor)
	t.Cleanup(func() { renderer.SetColorProfile(oldProfile) })

	// The accent-background SGR a highlighted row is painted with. Asserting on the
	// actual paint, not just the ▸ glyph, catches a differently-shaped regression (a
	// bar drawn without the glyph). The panel is rendered in ISOLATION here — View()
	// joins each sidebar row and panel row onto one physical line, so a whole-frame
	// line scan couldn't attribute a bar to the right pane.
	barSeq := backgroundSGR(rowStyle().Render("x"))
	if barSeq == "" {
		t.Fatal("rowStyle emits no background SGR under this color profile; test can't run")
	}
	countBars := func(panel string) int { return strings.Count(panel, barSeq) }

	m := realisticModel(t, 100, 16) // startup focus = sidebar

	// Sidebar has focus: the panel it renders must carry no highlight bar at all.
	if got := countBars(m.renderPanel(60, 15)); got != 0 {
		t.Errorf("sidebar focused: panel has %d highlight bars, want 0", got)
	}
	plain := ansi.Strip(m.View())
	if got := strings.Count(plain, "▸"); got != 1 {
		t.Errorf("sidebar focused: %d ▸ markers, want 1 (the REPOS heading)\n%s", got, plain)
	}
	for _, line := range strings.Split(plain, "\n") {
		if strings.Contains(line, "▸") && !strings.Contains(line, "REPOS") {
			t.Errorf("sidebar focused: ▸ outside the sidebar heading: %q", line)
		}
	}

	m = step(t, m, tea.KeyMsg{Type: tea.KeyRight}) // move into the panel
	// Panel now has focus: exactly one of its rows carries the bar — the cursor row.
	if got := countBars(m.renderPanel(60, 15)); got != 1 {
		t.Errorf("panel focused: panel has %d highlight bars, want 1 (the cursor row)", got)
	}
	plain = ansi.Strip(m.View())
	if got := strings.Count(plain, "▸"); got != 2 {
		t.Errorf("panel focused: %d ▸ markers, want 2 (the PRs heading + the cursor row)\n%s", got, plain)
	}
	if !strings.Contains(plain, "▸ #312") {
		t.Errorf("panel focused: cursor row #312 is not marked\n%s", plain)
	}
}

// backgroundSGR extracts the first `48;...m` set-background escape from a rendered
// string, the marker a highlighted row is painted with. "" if the profile emits none.
func backgroundSGR(s string) string {
	for _, m := range regexp.MustCompile(`\x1b\[[0-9;]*m`).FindAllString(s, -1) {
		if strings.Contains(m, "48;") {
			return m
		}
	}
	return ""
}

// The cursor survives leaving the panel: it is un-drawn, not reset, so arrowing back
// in lands where you left off.
func TestPanelCursorSurvivesLeavingTheFocus(t *testing.T) {
	m := realisticModel(t, 100, 16)
	m = step(t, m, tea.KeyMsg{Type: tea.KeyRight}) // into the panel
	m = step(t, m, tea.KeyMsg{Type: tea.KeyDown})  // cursor -> PR #310
	m = step(t, m, tea.KeyMsg{Type: tea.KeyShiftTab})
	if got := m.cursor[model.ViewPRs]; got != 1 {
		t.Fatalf("cursor = %d after leaving the panel, want 1 (preserved)", got)
	}
	m = step(t, m, tea.KeyMsg{Type: tea.KeyRight})
	if plain := ansi.Strip(m.View()); !strings.Contains(plain, "▸ #310") {
		t.Errorf("returning to the panel should re-mark #310\n%s", plain)
	}
}
