package gh

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/thomast8/wlaunch/internal/data/repos"
)

// TestListActionableForRepoReal exercises the rich this-repo actionable query
// against a real repo. Opt-in (WLAUNCH_IT=1) since it needs gh auth + network.
func TestListActionableForRepoReal(t *testing.T) {
	if os.Getenv("WLAUNCH_IT") == "" {
		t.Skip("set WLAUNCH_IT=1 to run gh integration test")
	}
	repo := os.Getenv("WLAUNCH_IT_REPO")
	if repo == "" {
		t.Skip("set WLAUNCH_IT_REPO to a real git repo path")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	items, err := ListActionableForRepo(ctx, repo)
	if err != nil {
		t.Fatalf("ListActionableForRepo(%s): %v", repo, err)
	}
	t.Logf("got %d actionable items", len(items))
	for _, it := range items {
		t.Logf("  %s #%-4d %-14s %s", it.Marker, it.Number, it.Summary, it.Title)
	}
}

// TestListActionableAllReposReal exercises the cross-repo aggregation across all
// configured accounts. Opt-in.
func TestListActionableAllReposReal(t *testing.T) {
	if os.Getenv("WLAUNCH_IT") == "" {
		t.Skip("set WLAUNCH_IT=1 to run gh integration test")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Second)
	defer cancel()
	accounts := AccountsToAggregate(ctx)
	slugMap, _ := repos.SlugToPath(ctx)
	items, err := ListActionableAllRepos(ctx, accounts, slugMap)
	if err != nil {
		t.Fatalf("ListActionableAllRepos: %v", err)
	}
	t.Logf("got %d actionable items across %d account(s), %d local repos mapped",
		len(items), len(accounts), len(slugMap))
	for i, it := range items {
		if i >= 15 {
			t.Logf("  … (%d more)", len(items)-15)
			break
		}
		local := "local"
		if !it.Launchable() {
			local = "remote"
		}
		t.Logf("  %s #%-4d %-12s %-22s %s [%s]", it.Marker, it.Number, it.Summary, it.RepoName, it.Title, local)
	}
}
