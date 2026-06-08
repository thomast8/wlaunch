// Package repos resolves the repo sidebar: the current/most-recent repo via
// repo-default.sh, plus the recent-repos history file, filtered to existing dirs
// (mirrors _recent_repos in _lib.sh).
package repos

import (
	"bufio"
	"context"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/thomast8/wlaunch/internal/data"
	"github.com/thomast8/wlaunch/internal/model"
)

// recentsFile resolves the recent-repos path, honoring the same env overrides as
// _lib.sh: RECENTS_FILE, else WARP_STATE_DIR/recent-repos, else ~/.warp/state/...
func recentsFile() string {
	if f := os.Getenv("RECENTS_FILE"); f != "" {
		return f
	}
	sd := os.Getenv("WARP_STATE_DIR")
	if sd == "" {
		home, _ := os.UserHomeDir()
		sd = filepath.Join(home, ".warp", "state")
	}
	return filepath.Join(sd, "recent-repos")
}

func repoDefaultScript() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".warp", "bin", "repo-default.sh")
}

func readRecents(path string) []string {
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer f.Close()
	var out []string
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		if line := strings.TrimSpace(sc.Text()); line != "" {
			out = append(out, line)
		}
	}
	return out
}

// reposDir is the root scanned for "all repos" (GIT_REPOS_DIR, else ~/GitRepos).
func reposDir() string {
	if d := os.Getenv("GIT_REPOS_DIR"); d != "" {
		return d
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, "GitRepos")
}

// scanRepos returns the git repos directly under dir, sorted by name, so the
// sidebar can reach a repo that isn't in the recents history yet.
func scanRepos(dir string) []string {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var out []string
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		p := filepath.Join(dir, e.Name())
		if _, err := os.Stat(filepath.Join(p, ".git")); err == nil {
			out = append(out, p)
		}
	}
	sort.Strings(out)
	return out
}

// List returns the scoped-repo candidates, default/most-recent first, deduped and
// filtered to directories that still exist. repo-default.sh runs with the process
// cwd so "current repo if in one" resolves correctly.
func List(ctx context.Context) ([]model.Repo, error) {
	var repos []model.Repo
	seen := map[string]bool{}
	add := func(p string) {
		p = strings.TrimSpace(p)
		if p == "" || seen[p] {
			return
		}
		if fi, err := os.Stat(p); err != nil || !fi.IsDir() {
			return
		}
		seen[p] = true
		repos = append(repos, model.Repo{Path: p, Name: filepath.Base(p)})
	}

	if b, err := data.Run(ctx, "", repoDefaultScript()); err == nil {
		add(strings.TrimSpace(string(b)))
	}
	for _, p := range readRecents(recentsFile()) {
		add(p)
	}
	// Then every repo under ~/GitRepos, so a repo you haven't visited recently is
	// still selectable (recents stay on top; the rest fill in alphabetically).
	for _, p := range scanRepos(reposDir()) {
		add(p)
	}
	return repos, nil
}
