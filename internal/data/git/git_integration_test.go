package git

import (
	"context"
	"os"
	"testing"
	"time"
)

// itRepo returns the real repo to exercise, or skips. Opt-in via WLAUNCH_IT=1 so
// normal `go test` (and CI without these repos) stays hermetic.
func itRepo(t *testing.T) string {
	t.Helper()
	if os.Getenv("WLAUNCH_IT") == "" {
		t.Skip("set WLAUNCH_IT=1 to run integration tests against a real repo")
	}
	repo := os.Getenv("WLAUNCH_IT_REPO")
	if repo == "" {
		t.Skip("set WLAUNCH_IT_REPO to a real git repo path")
	}
	return repo
}

func TestListBranchesReal(t *testing.T) {
	repo := itRepo(t)
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
	defer cancel()
	br, err := ListBranches(ctx, repo)
	if err != nil {
		t.Fatalf("ListBranches(%s): %v", repo, err)
	}
	if len(br) == 0 {
		t.Fatalf("expected at least one branch in %s", repo)
	}
	t.Logf("got %d branches; first: %+v", len(br), br[0])
}

func TestListWorktreesReal(t *testing.T) {
	repo := itRepo(t)
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
	defer cancel()
	wts, err := ListWorktrees(ctx, repo)
	if err != nil {
		t.Fatalf("ListWorktrees(%s): %v", repo, err)
	}
	if len(wts) == 0 || !wts[0].IsMain {
		t.Fatalf("expected a main worktree first, got %+v", wts)
	}
	t.Logf("got %d worktrees; main: %s (%s)", len(wts), wts[0].Path, wts[0].Branch)
}
