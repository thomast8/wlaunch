package repos

import (
	"context"
	"os"
	"testing"
	"time"
)

// TestListReal exercises the real repo-default.sh + recent-repos file. Opt-in
// (WLAUNCH_IT=1) since it depends on the live ~/.warp state.
func TestListReal(t *testing.T) {
	if os.Getenv("WLAUNCH_IT") == "" {
		t.Skip("set WLAUNCH_IT=1 to run repos integration test")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
	defer cancel()
	rs, err := List(ctx)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	t.Logf("got %d repos", len(rs))
	for i, r := range rs {
		if i >= 5 {
			break
		}
		t.Logf("  %s -> %s", r.Name, r.Path)
	}
	// Every returned path must be an existing directory (the filter's contract).
	for _, r := range rs {
		if fi, err := os.Stat(r.Path); err != nil || !fi.IsDir() {
			t.Errorf("returned non-dir repo: %s", r.Path)
		}
	}
}
