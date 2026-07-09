package ui

import (
	"strings"
	"testing"

	"github.com/muesli/termenv"
)

// TestStylesEmitANSIWhenColorAvailable guards the captured-stdout color bug: the
// styles must render ANSI when the renderer's profile supports color. The renderer
// is bound to os.Stderr (the TUI's real output), so in actual use color is detected
// from the terminal even when stdout is a pipe under `wl`.
func TestStylesEmitANSIWhenColorAvailable(t *testing.T) {
	old := renderer.ColorProfile()
	renderer.SetColorProfile(termenv.TrueColor)
	t.Cleanup(func() { renderer.SetColorProfile(old) })

	cases := map[string]string{
		"styText":    styText.Render("x"),
		"styNum":     styNum.Render("x"),
		"styMeta":    styMeta.Render("x"),
		"tabActive":  styTabActive.Render("x"),
		"rowFocused": rowStyle().Render("x"),
	}
	for name, out := range cases {
		if !strings.Contains(out, "\x1b[") {
			t.Errorf("%s produced no ANSI escape: %q", name, out)
		}
	}
}
