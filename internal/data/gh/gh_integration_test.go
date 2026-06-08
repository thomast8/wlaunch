package gh

import (
	"context"
	"os"
	"testing"
	"time"
)

// TestListPRsReal exercises the real `gh pr list` against a repo with open PRs.
// Opt-in (WLAUNCH_IT=1) since it needs gh auth + network.
func TestListPRsReal(t *testing.T) {
	if os.Getenv("WLAUNCH_IT") == "" {
		t.Skip("set WLAUNCH_IT=1 to run gh integration test")
	}
	repo := os.Getenv("WLAUNCH_IT_REPO")
	if repo == "" {
		t.Skip("set WLAUNCH_IT_REPO to a real git repo path")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	prs, err := ListPRs(ctx, repo)
	if err != nil {
		t.Fatalf("ListPRs(%s): %v", repo, err)
	}
	t.Logf("got %d open PRs", len(prs))
	for i, p := range prs {
		if i >= 3 {
			break
		}
		t.Logf("  #%d %q [%s] @%s", p.Number, p.Title, p.HeadRefName, p.Author)
	}
}
