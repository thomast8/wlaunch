// Command wlaunch is the unified launcher TUI. It renders to stderr and, on a
// pick, prints one tab-separated contract line to stdout for the `wl` wrapper to
// resolve and launch. Cancel prints nothing and exits 130.
package main

import (
	"fmt"
	"os"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/thomast8/wlaunch/internal/model"
	"github.com/thomast8/wlaunch/internal/ui"
)

func main() {
	// Headless bypass for tests/CI: emit a contract line without the UI, so the
	// stdout format and the wl wrapper's parsing are assertable without a tty.
	//   wlaunch --print-selection <kind> <repo_root> <ref> <tool>
	if len(os.Args) > 1 && os.Args[1] == "--print-selection" {
		if len(os.Args) != 6 {
			fmt.Fprintln(os.Stderr, "usage: wlaunch --print-selection <kind> <repo_root> <ref> <tool>")
			os.Exit(2)
		}
		sel := model.Selection{
			Kind:     model.Kind(os.Args[2]),
			RepoRoot: os.Args[3],
			Ref:      os.Args[4],
			Tool:     os.Args[5],
		}
		fmt.Fprint(os.Stdout, sel.Encode())
		return
	}

	// Drive the UI from the controlling terminal so stdout stays a clean data
	// channel: render → stderr, input ← /dev/tty, result → stdout.
	tty, err := os.OpenFile("/dev/tty", os.O_RDWR, 0)
	if err != nil {
		fmt.Fprintln(os.Stderr, "wlaunch: cannot open /dev/tty:", err)
		os.Exit(1)
	}
	defer tty.Close()

	p := tea.NewProgram(
		ui.New(),
		tea.WithOutput(os.Stderr),
		tea.WithInput(tty),
		tea.WithAltScreen(),
	)

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
