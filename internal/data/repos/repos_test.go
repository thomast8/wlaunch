package repos

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

// List() appends a synthetic, non-git "home" entry last, so the sidebar always
// offers a quick-launch location outside any repo.
func TestListAppendsHomeEntryLast(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home) // repoDefaultScript() resolves under here and won't exist -> skipped
	t.Setenv("RECENTS_FILE", filepath.Join(t.TempDir(), "recent-repos"))

	reposDir := t.TempDir()
	t.Setenv("GIT_REPOS_DIR", reposDir)
	repoPath := filepath.Join(reposDir, "myrepo")
	if err := os.MkdirAll(filepath.Join(repoPath, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}

	rs, err := List(context.Background())
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(rs) == 0 {
		t.Fatal("expected at least the home entry")
	}
	last := rs[len(rs)-1]
	if last.Path != home || last.Name != "~" || !last.Plain {
		t.Errorf("last entry = %+v, want the plain home entry {%s, ~, true}", last, home)
	}
	for _, r := range rs[:len(rs)-1] {
		if r.Plain {
			t.Errorf("non-last entry marked Plain: %+v", r)
		}
	}
}

// $HOME can already arrive via recent-repos today; it must be absorbed into the
// single plain home entry, not added a second time as a normal (non-plain) one.
func TestListSkipsHomeFromRecents(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	recents := filepath.Join(t.TempDir(), "recent-repos")
	if err := os.WriteFile(recents, []byte(home+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("RECENTS_FILE", recents)
	t.Setenv("GIT_REPOS_DIR", t.TempDir())

	rs, err := List(context.Background())
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	homeCount := 0
	for _, r := range rs {
		if r.Path == home {
			homeCount++
			if !r.Plain {
				t.Errorf("home entry from recents must be Plain, got %+v", r)
			}
		}
	}
	if homeCount != 1 {
		t.Errorf("home entry appears %d times, want exactly 1", homeCount)
	}
}
