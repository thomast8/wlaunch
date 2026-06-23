// Command wlaunch is the unified launcher TUI. It renders to stderr and, on a
// pick, prints one tab-separated contract line to stdout for the `wl` wrapper to
// resolve and launch. Cancel prints nothing and exits 130.
package main

import (
	"flag"
	"fmt"
	"io"
	"os"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/thomast8/wlaunch/internal/model"
	"github.com/thomast8/wlaunch/internal/ui"
)

func main() {
	// Headless bypass for tests/CI: emit a contract line without the UI, so the
	// stdout format and the wl wrapper's parsing are assertable without a tty. The
	// base is optional (only branch picks carry one).
	//   wlaunch --print-selection <kind> <repo_root> <ref> <tool> [base]
	if len(os.Args) > 1 && os.Args[1] == "--print-selection" {
		if len(os.Args) != 6 && len(os.Args) != 7 {
			fmt.Fprintln(os.Stderr, "usage: wlaunch --print-selection <kind> <repo_root> <ref> <tool> [base]")
			os.Exit(2)
		}
		sel := model.Selection{
			Kind:     model.Kind(os.Args[2]),
			RepoRoot: os.Args[3],
			Ref:      os.Args[4],
			Tool:     os.Args[5],
		}
		if len(os.Args) == 7 {
			sel.Base = os.Args[6]
		}
		fmt.Fprint(os.Stdout, sel.Encode())
		return
	}

	// Optional startup flags (the wl wrapper forwards "$@"): --view opens directly
	// on a view, --scope sets the Actionable view's repo scope. Unknown flags are
	// ignored so env-driven knobs and future args don't error here.
	var viewFlag, scopeFlag string
	fs := flag.NewFlagSet("wlaunch", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	fs.StringVar(&viewFlag, "view", "", "initial view: actionable")
	fs.StringVar(&scopeFlag, "scope", "", "actionable scope: all | repo")
	_ = fs.Parse(os.Args[1:])

	m := ui.New()
	if viewFlag == "actionable" {
		m = m.WithInitialView(model.ViewActionable)
	}
	if scopeFlag == "all" {
		m = m.WithAllReposScope()
	}

	// Drive the UI from the controlling terminal so stdout stays a clean data
	// channel: render → stderr, input ← /dev/tty, result → stdout.
	tty, err := os.OpenFile("/dev/tty", os.O_RDWR, 0)
	if err != nil {
		fmt.Fprintln(os.Stderr, "wlaunch: cannot open /dev/tty:", err)
		os.Exit(1)
	}
	defer tty.Close()

	// Alt-screen by default (clean full-height dashboard). WLAUNCH_NO_ALTSCREEN=1
	// renders inline instead — an A/B knob for diagnosing the Warp CLI-agent handoff
	// after the TUI tears down (caw's fzf picker is inline and hands off cleanly).
	opts := []tea.ProgramOption{tea.WithOutput(os.Stderr), tea.WithInput(tty)}
	if os.Getenv("WLAUNCH_NO_ALTSCREEN") == "" {
		opts = append(opts, tea.WithAltScreen())
	}
	p := tea.NewProgram(m, opts...)

	final, err := p.Run()
	if err != nil {
		fmt.Fprintln(os.Stderr, "wlaunch:", err)
		os.Exit(1)
	}

	m, ok := final.(ui.Model)
	if !ok || m.Selection() == nil {
		os.Exit(130) // cancelled — no selection
	}
	fmt.Fprint(os.Stdout, m.Selection().Encode())
}
