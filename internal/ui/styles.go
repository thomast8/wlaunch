package ui

import (
	"strings"

	"github.com/charmbracelet/lipgloss"
)

// Palette. 256-color codes chosen for legibility on a dark, semi-transparent
// background (Warp runs with window opacity), so primary text is near-white and
// metadata stays a readable mid-grey rather than a faint one.
var (
	colAccent = lipgloss.Color("111") // focus / numbers
	colText   = lipgloss.Color("252") // primary text (titles, names)
	colMeta   = lipgloss.Color("245") // branch / author metadata
	colHint   = lipgloss.Color("244") // footer + inactive tabs
	colErr    = lipgloss.Color("203") // error states
	colSelBg  = lipgloss.Color("238") // selected row, unfocused pane
	colSelFg  = lipgloss.Color("231") // near-white on selection
)

var (
	styText    = lipgloss.NewStyle().Foreground(colText)
	styMeta    = lipgloss.NewStyle().Foreground(colMeta)
	styHint    = lipgloss.NewStyle().Foreground(colHint)
	styErr     = lipgloss.NewStyle().Foreground(colErr)
	styNum     = lipgloss.NewStyle().Foreground(colAccent).Bold(true)
	styHeading = lipgloss.NewStyle().Foreground(colAccent).Bold(true)

	styTabActive   = lipgloss.NewStyle().Foreground(lipgloss.Color("16")).Background(colAccent).Bold(true)
	styTabInactive = lipgloss.NewStyle().Foreground(colHint)
)

// rowStyle is the highlight bar for the selected row: a strong accent bar in the
// focused pane, a subtle grey bar in the unfocused one.
func rowStyle(focused bool) lipgloss.Style {
	if focused {
		return lipgloss.NewStyle().Foreground(lipgloss.Color("16")).Background(colAccent).Bold(true)
	}
	return lipgloss.NewStyle().Foreground(colSelFg).Background(colSelBg)
}

// truncate shortens s to at most n display runes, adding an ellipsis when cut.
func truncate(s string, n int) string {
	if n <= 0 {
		return ""
	}
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	if n == 1 {
		return "…"
	}
	return string(r[:n-1]) + "…"
}

// padTrunc fits s to exactly width columns: truncate (with …) if too long, else
// right-pad with spaces. Rune-count is used as a display-width proxy, which holds
// for the ASCII + simple-symbol content we render; the panel's fixed-width frame
// absorbs the rare wide-rune (emoji) case.
func padTrunc(s string, width int) string {
	if width <= 0 {
		return ""
	}
	r := []rune(s)
	if len(r) > width {
		return truncate(s, width)
	}
	return s + strings.Repeat(" ", width-len(r))
}

func clamp(v, lo, hi int) int {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

// windowStart centers cursor in a height-row window over n items, clamped to
// valid bounds. Stateless, so render can derive the scroll position each frame.
func windowStart(cursor, height, n int) int {
	if n <= height || height <= 0 {
		return 0
	}
	start := cursor - height/2
	if start < 0 {
		start = 0
	}
	if start > n-height {
		start = n - height
	}
	return start
}
