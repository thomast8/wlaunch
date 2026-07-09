package ui

import (
	"os"
	"strings"

	"github.com/charmbracelet/lipgloss"
)

// renderer detects color from stderr — where the TUI actually renders
// (tea.WithOutput(os.Stderr)). The default lipgloss renderer probes stdout, but
// stdout is a captured pipe when wlaunch runs under `wl` (`line="$(wlaunch)"`), so
// the default would wrongly see no color and strip all styling. Binding detection
// to stderr keeps colors whether wlaunch is run directly or captured.
var renderer = lipgloss.NewRenderer(os.Stderr)

func init() {
	// Resolve light-vs-dark up front, before Bubble Tea acquires the terminal.
	// Bubble Tea ships its own version of this trick (see its tea_init.go) to stop
	// lipgloss's background-color probe (an OSC 11 query/response over the tty)
	// from racing Bubble Tea's own tty reader — but that one only warms the
	// *default* renderer (os.Stdout/os.Stdin). wlaunch uses a separate renderer
	// bound to os.Stderr (see above) with input read from a manually opened
	// /dev/tty (cmd/wlaunch), so it needs its own warm-up here, before main opens
	// that tty and starts reading raw input from it. Without this, the probe and
	// Bubble Tea's reader can both be waiting on the same terminal response and the
	// probe stalls for the full termenv.OSCTimeout (5s).
	//
	// WLAUNCH_THEME=dark|light skips the probe entirely for terminals that don't
	// answer OSC 11 (e.g. some multiplexers), where the probe would otherwise still
	// pay that timeout.
	switch strings.ToLower(os.Getenv("WLAUNCH_THEME")) {
	case "dark":
		renderer.SetHasDarkBackground(true)
	case "light":
		renderer.SetHasDarkBackground(false)
	default:
		renderer.HasDarkBackground()
	}
}

// Palette. Adaptive pairs pick a dark-background value (near-white text, chosen
// for a dark, semi-transparent background — Warp runs with window opacity) and a
// separate light-background value (dark text, since near-white would vanish on a
// light background — this was the original bug). colAccentBg pairs unconditionally
// with black foreground text, so it's terminal-background-agnostic and stays fixed.
var (
	colAccent      = lipgloss.AdaptiveColor{Dark: "111", Light: "25"}  // focus / numbers (text only)
	colAccentBg    = lipgloss.Color("111")                             // active tab / focused row chip, paired with black text
	colText        = lipgloss.AdaptiveColor{Dark: "252", Light: "236"} // primary text (titles, names)
	colMeta        = lipgloss.AdaptiveColor{Dark: "245", Light: "240"} // branch / author metadata
	colHint        = lipgloss.AdaptiveColor{Dark: "244", Light: "243"} // footer hints
	colTabInactive = lipgloss.AdaptiveColor{Dark: "250", Light: "242"} // inactive tabs
	colErr         = lipgloss.AdaptiveColor{Dark: "203", Light: "160"} // error states
)

var (
	styText    = renderer.NewStyle().Foreground(colText)
	styMeta    = renderer.NewStyle().Foreground(colMeta)
	styHint    = renderer.NewStyle().Foreground(colHint)
	styErr     = renderer.NewStyle().Foreground(colErr)
	styNum     = renderer.NewStyle().Foreground(colAccent).Bold(true)
	styHeading = renderer.NewStyle().Foreground(colAccent).Bold(true)

	// Inactive tabs use a brighter grey than footer hints so the tab strip stays
	// readable on the translucent background; the active tab is a solid accent chip.
	styTabActive   = renderer.NewStyle().Foreground(lipgloss.Color("16")).Background(colAccentBg).Bold(true)
	styTabInactive = renderer.NewStyle().Foreground(colTabInactive)
)

// rowStyle is the highlight bar for the cursor row: an accent bar, drawn only by
// the pane that has focus. An unfocused pane draws no cursor row at all, so exactly
// one selection is visible at a time — the one Enter acts on.
func rowStyle() lipgloss.Style {
	return renderer.NewStyle().Foreground(lipgloss.Color("16")).Background(colAccentBg).Bold(true)
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
