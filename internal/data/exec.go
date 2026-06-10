// Package data provides the headless data layer for wlaunch: a small subprocess
// runner plus (in subpackages) the git/gh/repo queries that back each view. No
// bubbletea imports here, so everything is unit-testable without a terminal.
package data

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
)

// Run executes name+args with the given working directory (empty = inherit the
// process cwd) and returns stdout. On failure the error wraps the trimmed stderr
// so callers can surface a friendly message. The caller's context governs the
// timeout, so a hung gh/git can't wedge the UI.
func Run(ctx context.Context, cwd, name string, args ...string) ([]byte, error) {
	return RunEnv(ctx, cwd, nil, name, args...)
}

// RunEnv is Run with extra environment variables layered on top of the parent
// environment (nil = plain inherit). Used to pin GH_TOKEN per repo so gh calls
// don't depend on whichever account happens to be gh's active one.
func RunEnv(ctx context.Context, cwd string, env []string, name string, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	if cwd != "" {
		cmd.Dir = cwd
	}
	if len(env) > 0 {
		cmd.Env = append(os.Environ(), env...)
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
