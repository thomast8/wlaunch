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
	"sync"

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

// NormalizeSlug reduces a git remote URL to its owner/name slug (no host, no
// .git), so gh search results (which carry owner/name) can be matched to local
// clones. Handles scp-like (git@host:owner/name.git), https/ssh URLs, and a
// user@ prefix; returns "" when it can't extract a slug.
func NormalizeSlug(raw string) string {
	s := strings.TrimSpace(raw)
	s = strings.TrimSuffix(s, ".git")
	if s == "" {
		return ""
	}
	if i := strings.Index(s, "://"); i >= 0 { // scheme://[user@]host/owner/name
		rest := s[i+3:]
		if at := strings.Index(rest, "@"); at >= 0 {
			rest = rest[at+1:]
		}
		if k := strings.Index(rest, "/"); k >= 0 {
			return strings.TrimPrefix(rest[k:], "/")
		}
		return ""
	}
	if c := strings.LastIndex(s, ":"); c >= 0 { // scp-like git@host:owner/name
		return s[c+1:]
	}
	return s
}

// OriginSlug resolves a repo's origin remote to its owner/name slug, or "" when
// the repo has no origin.
func OriginSlug(ctx context.Context, path string) (string, error) {
	b, err := data.Run(ctx, path, "git", "config", "--get", "remote.origin.url")
	if err != nil {
		return "", err
	}
	return NormalizeSlug(string(b)), nil
}

// SlugToPath maps each candidate repo's lowercased origin slug to its local main
// checkout, so the cross-repo actionable view can turn a gh search hit
// (owner/name) into a path the wl wrapper can cd into.
func SlugToPath(ctx context.Context) (map[string]string, error) {
	rs, err := List(ctx)
	if err != nil {
		return nil, err
	}
	m := make(map[string]string, len(rs))
	var mu sync.Mutex
	var wg sync.WaitGroup
	sem := make(chan struct{}, 8)
	for _, r := range rs {
		r := r
		wg.Add(1)
		go func() {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			if slug, e := OriginSlug(ctx, r.Path); e == nil && slug != "" {
				mu.Lock()
				m[strings.ToLower(slug)] = r.Path
				mu.Unlock()
			}
		}()
	}
	wg.Wait()
	return m, nil
}

// List returns the scoped-repo candidates, default/most-recent first, deduped and
// filtered to directories that still exist. repo-default.sh runs with the process
// cwd so "current repo if in one" resolves correctly. A synthetic, non-git "~"
// entry for $HOME is appended last, so the sidebar always offers a quick-launch
// location outside any repo (see model.Repo.Plain).
func List(ctx context.Context) ([]model.Repo, error) {
	home, _ := os.UserHomeDir()
	var repos []model.Repo
	seen := map[string]bool{}
	add := func(p string) {
		p = strings.TrimSpace(p)
		if p == "" || seen[p] || (home != "" && p == home) {
			return // home is appended once, below, as the dedicated plain entry
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
	if home != "" {
		if fi, err := os.Stat(home); err == nil && fi.IsDir() {
			repos = append(repos, model.Repo{Path: home, Name: "~", Plain: true})
		}
	}
	return repos, nil
}
