package ui

import (
	"errors"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

// A bare letter must filter, not fire an action (the whole point of type-to-filter).
func TestBareLetterFilters(t *testing.T) {
	m := loadedModel(t)
	m = typeStr(t, m, "fix")
	if m.filterStr != "fix" {
		t.Errorf("filterStr = %q, want 'fix' (letters should filter, not act)", m.filterStr)
	}
	if m.Selection() != nil {
		t.Error("typing must not launch anything")
	}
}

// ';' arms the leader; the next key resolves it and disarms it.
func TestLeaderArmsAndDisarms(t *testing.T) {
	m := loadedModel(t)
	m = step(t, m, key(";"))
	if !m.awaiting {
		t.Fatal("';' should arm the leader")
	}
	m = step(t, m, key("z")) // z isn't an action
	if m.awaiting {
		t.Error("leader should disarm after the next key")
	}
	if m.filterStr != "" {
		t.Errorf("a consumed leader key must not leak into the filter, got %q", m.filterStr)
	}
}

func TestLeaderMenuShowsCodex(t *testing.T) {
	m := loadedModel(t)
	m = step(t, m, key(";"))
	if out := m.View(); !strings.Contains(out, "x codex") {
		t.Errorf("leader menu should advertise codex, got:\n%s", out)
	}
}

// esc clears the filter and, crucially, never quits the TUI (the user's complaint).
func TestEscClearsFilterNeverQuits(t *testing.T) {
	m := loadedModel(t)
	m = typeStr(t, m, "abc")
	m = step(t, m, tea.KeyMsg{Type: tea.KeyEsc})
	if m.quit {
		t.Error("esc must never quit")
	}
	if m.filterStr != "" {
		t.Errorf("esc should clear the filter, got %q", m.filterStr)
	}
	// esc on an already-empty filter is a harmless no-op, still no quit
	m = step(t, m, tea.KeyMsg{Type: tea.KeyEsc})
	if m.quit {
		t.Error("esc on an empty filter must still not quit")
	}
}

// Only Ctrl+C quits now (bare 'q' filters).
func TestOnlyCtrlCQuits(t *testing.T) {
	m := loadedModel(t)
	m = step(t, m, key("q"))
	if m.quit {
		t.Error("bare 'q' should filter, not quit")
	}
	if m.filterStr != "q" {
		t.Errorf("'q' should land in the filter, got %q", m.filterStr)
	}
	m = step(t, m, ctrlC)
	if !m.quit {
		t.Error("Ctrl+C should quit")
	}
}

// Ctrl+O opens the selection in a shell, while plain Enter stays claude
// (Enter-modifiers are indistinguishable from Enter, so a Ctrl-chord is required).
func TestCtrlOOpensShell(t *testing.T) {
	m := loadedModel(t) // PRs view, first row = PR #289
	m = step(t, m, tea.KeyMsg{Type: tea.KeyCtrlO})
	if m.Selection() == nil {
		t.Fatal("Ctrl+O should launch the selection")
	}
	if got := m.Selection().Encode(); got != "v1\tpr\t/r\t289\tshell\t\n" {
		t.Errorf("Ctrl+O Encode() = %q, want a shell launch", got)
	}
}

func TestLeaderXOpensCodex(t *testing.T) {
	m := loadedModel(t) // PRs view, first row = PR #289
	m = step(t, m, key(";"))
	m = step(t, m, key("x"))
	if m.Selection() == nil {
		t.Fatal(";x should launch the selection")
	}
	if got := m.Selection().Encode(); got != "v1\tpr\t/r\t289\tcodex\t\n" {
		t.Errorf(";x Encode() = %q, want a codex launch", got)
	}
}

// Backspace edits the live filter.
func TestBackspaceEditsFilter(t *testing.T) {
	m := loadedModel(t)
	m = typeStr(t, m, "feat")
	m = step(t, m, tea.KeyMsg{Type: tea.KeyBackspace})
	if m.filterStr != "fea" {
		t.Errorf("filterStr = %q, want 'fea' after backspace", m.filterStr)
	}
}

func TestFriendlyNoRemotes(t *testing.T) {
	// gh's phrasing when a repo has no remotes at all (`gh pr list` in a
	// local-only repo) must map to the same friendly line as the other
	// no-remote variants.
	got := friendly(errors.New("gh: exit status 1: no git remotes found"))
	if got != "no GitHub remote configured." {
		t.Errorf("friendly = %q", got)
	}
}
