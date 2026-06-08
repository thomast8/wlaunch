package git

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/thomast8/wlaunch/internal/data"
	"github.com/thomast8/wlaunch/internal/model"
)

func findBranch(bs []model.Branch, name string) *model.Branch {
	for i := range bs {
		if bs[i].Name == name {
			return &bs[i]
		}
	}
	return nil
}

// TestFetchAndPullReal builds a real bare remote + two clones, advances a branch on
// the remote, and exercises Fetch + PullBranch (the non-current fast-forward path,
// which is the novel one). Self-contained, so it runs in the normal suite.
func TestFetchAndPullReal(t *testing.T) {
	ctx := context.Background()
	cfg := []string{"-c", "user.email=t@e", "-c", "user.name=t"}
	run := func(dir string, args ...string) {
		t.Helper()
		if _, err := data.Run(ctx, dir, "git", append([]string{"-C", dir}, args...)...); err != nil {
			t.Fatalf("git -C %s %v: %v", dir, args, err)
		}
	}
	remote := t.TempDir()
	run(remote, "init", "-q", "--bare", "-b", "main")

	// worker: seed main + feat, push both
	worker := t.TempDir()
	if _, err := data.Run(ctx, "", "git", "clone", "-q", remote, worker); err != nil {
		t.Fatalf("clone worker: %v", err)
	}
	run(worker, append(cfg, "commit", "-q", "--allow-empty", "-m", "c1")...)
	run(worker, "push", "-q", "-u", "origin", "main")
	run(worker, "branch", "feat")
	run(worker, "push", "-q", "-u", "origin", "feat")

	// local: clone, then track feat locally (not checked out — main is current)
	local := t.TempDir()
	if _, err := data.Run(ctx, "", "git", "clone", "-q", remote, local); err != nil {
		t.Fatalf("clone local: %v", err)
	}
	run(local, "branch", "--track", "feat", "origin/feat")

	// worker advances feat on the remote
	run(worker, "checkout", "-q", "feat")
	run(worker, append(cfg, "commit", "-q", "--allow-empty", "-m", "c2")...)
	run(worker, "push", "-q", "origin", "feat")

	// local Fetch -> feat is behind 1, and not current
	if err := Fetch(ctx, local); err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	feat := findBranch(mustBranches(t, ctx, local), "feat")
	if feat == nil {
		t.Fatal("no feat branch")
	}
	if feat.IsCurrent {
		t.Fatal("feat should not be current (main is)")
	}
	if feat.Behind != 1 {
		t.Errorf("feat behind = %d after fetch, want 1", feat.Behind)
	}

	// PullBranch on a branch checked out nowhere -> fast-forward via fetch refspec
	if err := PullBranch(ctx, local, *feat, ""); err != nil {
		t.Fatalf("PullBranch(feat): %v", err)
	}
	if f := findBranch(mustBranches(t, ctx, local), "feat"); f == nil || f.Behind != 0 {
		t.Errorf("feat not fast-forwarded: %+v", f)
	}
}

// TestPullCheckedOutWorktreeReal covers the bug the user hit: a branch checked out
// in a worktree can't be ff'd via a fetch refspec, so PullBranch must pull in the
// worktree itself (checkoutPath != "").
func TestPullCheckedOutWorktreeReal(t *testing.T) {
	ctx := context.Background()
	cfg := []string{"-c", "user.email=t@e", "-c", "user.name=t"}
	run := func(dir string, args ...string) {
		t.Helper()
		if _, err := data.Run(ctx, dir, "git", append([]string{"-C", dir}, args...)...); err != nil {
			t.Fatalf("git -C %s %v: %v", dir, args, err)
		}
	}
	remote := t.TempDir()
	run(remote, "init", "-q", "--bare", "-b", "main")

	worker := t.TempDir()
	if _, err := data.Run(ctx, "", "git", "clone", "-q", remote, worker); err != nil {
		t.Fatalf("clone worker: %v", err)
	}
	run(worker, append(cfg, "commit", "-q", "--allow-empty", "-m", "c1")...)
	run(worker, "push", "-q", "-u", "origin", "main")
	run(worker, "branch", "feat")
	run(worker, "push", "-q", "-u", "origin", "feat")

	local := t.TempDir()
	if _, err := data.Run(ctx, "", "git", "clone", "-q", remote, local); err != nil {
		t.Fatalf("clone local: %v", err)
	}
	// check feat out in a worktree, tracking origin/feat
	wtpath := filepath.Join(t.TempDir(), "featwt")
	run(local, "worktree", "add", "-q", "--track", "-b", "feat", wtpath, "origin/feat")

	// worker advances feat
	run(worker, "checkout", "-q", "feat")
	run(worker, append(cfg, "commit", "-q", "--allow-empty", "-m", "c2")...)
	run(worker, "push", "-q", "origin", "feat")

	if err := Fetch(ctx, local); err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	feat := findBranch(mustBranches(t, ctx, local), "feat")
	if feat == nil || feat.Behind != 1 {
		t.Fatalf("feat = %+v, want behind 1", feat)
	}
	// the fetch-refspec path WOULD fail (checked out elsewhere); checkoutPath fixes it
	if err := PullBranch(ctx, local, *feat, wtpath); err != nil {
		t.Fatalf("PullBranch via worktree: %v", err)
	}
	if f := findBranch(mustBranches(t, ctx, local), "feat"); f == nil || f.Behind != 0 {
		t.Errorf("feat not fast-forwarded in its worktree: %+v", f)
	}
}

func mustBranches(t *testing.T, ctx context.Context, repo string) []model.Branch {
	t.Helper()
	br, err := ListBranches(ctx, repo)
	if err != nil {
		t.Fatalf("ListBranches: %v", err)
	}
	return br
}
