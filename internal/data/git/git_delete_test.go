package git

import (
	"context"
	"strings"
	"testing"

	"github.com/thomast8/wlaunch/internal/data"
)

// TestDeleteBranchReal exercises the real `git branch -d/-D` semantics the cleanup
// relies on: a safe delete refuses an unmerged branch (and the error says "not fully
// merged", which the UI keys its force-escalation on), force deletes it, and a safe
// delete succeeds on a branch already merged into HEAD.
func TestDeleteBranchReal(t *testing.T) {
	ctx := context.Background()
	cfg := []string{"-c", "user.email=t@e", "-c", "user.name=t"}
	repo := t.TempDir()
	run := func(args ...string) {
		t.Helper()
		full := append([]string{"-C", repo}, append(cfg, args...)...)
		if _, err := data.Run(ctx, repo, "git", full...); err != nil {
			t.Fatalf("git %v: %v", args, err)
		}
	}
	run("init", "-q", "-b", "main")
	run("commit", "-q", "--allow-empty", "-m", "c1")

	// unmerged: a branch with a commit not on main
	run("checkout", "-q", "-b", "unmerged")
	run("commit", "-q", "--allow-empty", "-m", "only-on-branch")
	run("checkout", "-q", "main")

	// merged: a branch pointing at main's tip (fully merged)
	run("branch", "merged")

	// safe delete of the unmerged branch must REFUSE with "not fully merged"
	err := DeleteBranch(ctx, repo, "unmerged", false)
	if err == nil {
		t.Fatal("safe delete of an unmerged branch should fail")
	}
	if !strings.Contains(err.Error(), "not fully merged") {
		t.Errorf("error should mention 'not fully merged' (UI escalates on it): %v", err)
	}

	// force delete of the unmerged branch succeeds
	if err := DeleteBranch(ctx, repo, "unmerged", true); err != nil {
		t.Fatalf("force delete should succeed: %v", err)
	}

	// safe delete of the merged branch succeeds
	if err := DeleteBranch(ctx, repo, "merged", false); err != nil {
		t.Fatalf("safe delete of a merged branch should succeed: %v", err)
	}

	// both branches are gone; only main remains
	left := mustBranches(t, ctx, repo)
	if len(left) != 1 || left[0].Name != "main" {
		t.Errorf("after deletes, remaining branches = %+v, want [main]", left)
	}
}
