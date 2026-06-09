package git

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/thomast8/wlaunch/internal/data"
)

// TestRemoveWorktreeReal creates a real temp repo + worktree and removes it through
// RemoveWorktree, asserting it disappears from the worktree list. Self-contained
// (its own temp repo), so it runs in the normal suite — git is a hard dependency anyway.
func TestRemoveWorktreeReal(t *testing.T) {
	repo := t.TempDir()
	ctx := context.Background()
	git := func(args ...string) {
		t.Helper()
		full := append([]string{"-C", repo}, args...)
		if _, err := data.Run(ctx, repo, "git", full...); err != nil {
			t.Fatalf("git %v: %v", args, err)
		}
	}
	git("init", "-q")
	git("-c", "user.email=t@example.com", "-c", "user.name=t", "commit", "-q", "--allow-empty", "-m", "init")

	wt := filepath.Join(t.TempDir(), "wt")
	git("worktree", "add", "-q", "--detach", wt)

	before, err := ListWorktrees(ctx, repo)
	if err != nil {
		t.Fatalf("ListWorktrees: %v", err)
	}
	if len(before) != 2 {
		t.Fatalf("expected 2 worktrees (main + added), got %d", len(before))
	}

	if err := RemoveWorktree(ctx, repo, wt, false); err != nil {
		t.Fatalf("RemoveWorktree: %v", err)
	}

	after, err := ListWorktrees(ctx, repo)
	if err != nil {
		t.Fatalf("ListWorktrees after: %v", err)
	}
	if len(after) != 1 {
		t.Errorf("expected 1 worktree after removal, got %d", len(after))
	}
	if !after[0].IsMain {
		t.Errorf("remaining worktree should be the main checkout")
	}
}

// TestRemoveDirtyWorktreeReal covers the force escalation: a worktree with an
// uncommitted file is refused by a safe remove (IsForceRemovable + a nonzero dirty
// count drive the UI's offer), and a force remove discards it and succeeds.
func TestRemoveDirtyWorktreeReal(t *testing.T) {
	repo := t.TempDir()
	ctx := context.Background()
	git := func(args ...string) {
		t.Helper()
		full := append([]string{"-C", repo}, args...)
		if _, err := data.Run(ctx, repo, "git", full...); err != nil {
			t.Fatalf("git %v: %v", args, err)
		}
	}
	git("init", "-q")
	git("-c", "user.email=t@example.com", "-c", "user.name=t", "commit", "-q", "--allow-empty", "-m", "init")

	wt := filepath.Join(t.TempDir(), "wt")
	git("worktree", "add", "-q", "--detach", wt)
	if err := os.WriteFile(filepath.Join(wt, "scratch.txt"), []byte("wip\n"), 0o644); err != nil {
		t.Fatalf("write dirty file: %v", err)
	}

	// safe remove is refused, and the refusal is force-removable with a dirty count
	err := RemoveWorktree(ctx, repo, wt, false)
	if err == nil {
		t.Fatal("safe remove of a dirty worktree should fail")
	}
	if !IsForceRemovable(err) {
		t.Errorf("dirty-worktree error should be force-removable: %v", err)
	}
	if n := DirtyFileCount(ctx, wt); n != 1 {
		t.Errorf("DirtyFileCount = %d, want 1 (the untracked scratch.txt)", n)
	}

	// force remove discards the change and succeeds
	if err := RemoveWorktree(ctx, repo, wt, true); err != nil {
		t.Fatalf("force RemoveWorktree: %v", err)
	}
	after, err := ListWorktrees(ctx, repo)
	if err != nil {
		t.Fatalf("ListWorktrees after: %v", err)
	}
	if len(after) != 1 || !after[0].IsMain {
		t.Errorf("expected only the main checkout after force removal, got %+v", after)
	}
}
