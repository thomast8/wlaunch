// Package data provides the headless data layer for wlaunch: a small subprocess
// runner plus (in subpackages) the git/gh/repo queries that back each view. No
// bubbletea imports here, so everything is unit-testable without a terminal.
package data

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"strings"
)

// Run executes name+args with the given working directory (empty = inherit the
// process cwd) and returns stdout. On failure the error wraps the trimmed stderr
// so callers can surface a friendly message. The caller's context governs the
// timeout, so a hung gh/git can't wedge the UI.
func Run(ctx context.Context, cwd, name string, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	if cwd != "" {
		cmd.Dir = cwd
	}
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		if msg := strings.TrimSpace(stderr.String()); msg != "" {
			return stdout.Bytes(), fmt.Errorf("%s: %w: %s", name, err, msg)
		}
		return stdout.Bytes(), fmt.Errorf("%s: %w", name, err)
	}
	return stdout.Bytes(), nil
}
