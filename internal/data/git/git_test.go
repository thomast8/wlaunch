package git

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/thomast8/wlaunch/internal/model"
)

func TestParseBranches(t *testing.T) {
	// fields: name \t upstream \t track \t unix \t HEAD \t subject
	in := []byte(
		"main\torigin/main\t\t1700000000\t*\tInitial commit\n" +
			"feature/x\torigin/feature/x\t[ahead 2, behind 1]\t1700000100\t \tWork in progress\n" +
			"orphan\t\t\t1700000050\t \tNo upstream here\n" +
			"stale\torigin/stale\t[gone]\t1699990000\t \tUpstream deleted\n")
	br := parseBranches(in)
	if len(br) != 4 {
		t.Fatalf("len = %d, want 4", len(br))
	}
	if !br[0].IsCurrent || br[0].Name != "main" {
		t.Errorf("br0 = %+v, want current main", br[0])
	}
	if br[0].LastCommitUnix != 1700000000 {
		t.Errorf("br0 unix = %d", br[0].LastCommitUnix)
	}
	if br[1].Ahead != 2 || br[1].Behind != 1 {
		t.Errorf("br1 ahead/behind = %d/%d, want 2/1", br[1].Ahead, br[1].Behind)
	}
	if br[1].IsCurrent {
		t.Errorf("br1 should not be current")
	}
	if br[2].Upstream != "" {
		t.Errorf("br2 upstream = %q, want empty", br[2].Upstream)
	}
	if !br[3].Gone {
		t.Errorf("br3 should be gone")
	}
}

func TestParseBranchEmptySubject(t *testing.T) {
	// trailing tab + empty subject must still parse as 6 fields
	in := []byte("topic\t\t\t1700000000\t \t\n")
	br := parseBranches(in)
	if len(br) != 1 {
		t.Fatalf("len = %d, want 1", len(br))
	}
	if br[0].Name != "topic" || br[0].Subject != "" {
		t.Errorf("br0 = %+v", br[0])
	}
}

func TestParseWorktrees(t *testing.T) {
	in := []byte(
		"worktree /repo/main\nHEAD aaaaaaaaaaaaaaaaaaaa\nbranch refs/heads/main\n\n" +
			"worktree /wt/pr289\nHEAD bbbbbbbbbbbbbbbbbbbb\nbranch refs/heads/fix/re\nlocked\n\n" +
			"worktree /wt/detached\nHEAD cccccccccccccccccccc\ndetached\n")
	wts := parseWorktrees(in)
	if len(wts) != 3 {
		t.Fatalf("len = %d, want 3", len(wts))
	}
	if !wts[0].IsMain || wts[0].Branch != "main" {
		t.Errorf("wt0 = %+v, want main checkout on main", wts[0])
	}
	if wts[0].HEAD != "aaaaaaaaaaaa" { // truncated to 12
		t.Errorf("wt0 HEAD = %q (len %d), want 12-char", wts[0].HEAD, len(wts[0].HEAD))
	}
	if !wts[1].Locked || wts[1].Branch != "fix/re" {
		t.Errorf("wt1 = %+v, want locked on fix/re", wts[1])
	}
	if wts[1].IsMain {
		t.Errorf("wt1 should not be main")
	}
	if !wts[2].Detached || wts[2].Branch != "" {
		t.Errorf("wt2 = %+v, want detached/no-branch", wts[2])
	}
}

func TestDefaultBranchFromOriginHead(t *testing.T) {
	cases := map[string]string{
		"origin/main\n":       "main",
		"origin/master":       "master",
		"origin/release/v2\n": "release/v2", // slashes inside the name survive
		"main":                "main",       // no remote prefix at all
		"":                    "",
		"  \n":                "",
	}
	for in, want := range cases {
		if got := defaultBranchFromOriginHead([]byte(in)); got != want {
			t.Errorf("defaultBranchFromOriginHead(%q) = %q, want %q", in, got, want)
		}
	}
}

// The default branch usually lives in a dedicated worktree, not the primary
// checkout — which is routinely parked on a feature branch.
func TestLiveWorktreeForBranch(t *testing.T) {
	wts := []model.Worktree{
		{Path: "/r", Branch: "feat/x", IsMain: true},
		{Path: "/wt/r/main", Branch: "main"},
		{Path: "/wt/r/detached", Detached: true},
	}
	if got := liveWorktreeForBranch(wts, "main"); got != "/wt/r/main" {
		t.Errorf("liveWorktreeForBranch(main) = %q, want /wt/r/main", got)
	}
	if got := liveWorktreeForBranch(wts, "feat/x"); got != "/r" {
		t.Errorf("liveWorktreeForBranch(feat/x) = %q, want /r (the primary checkout)", got)
	}
	if got := liveWorktreeForBranch(wts, "nope"); got != "" {
		t.Errorf("liveWorktreeForBranch(nope) = %q, want empty", got)
	}
	// An unresolvable default branch must not match the detached worktree's empty Branch.
	if got := liveWorktreeForBranch(wts, ""); got != "" {
		t.Errorf("liveWorktreeForBranch(\"\") = %q, want empty", got)
	}
	// A worktree git still lists but whose directory is gone must not be handed back:
	// the caller would cd into a path that doesn't exist.
	stale := []model.Worktree{
		{Path: "/r", Branch: "feat/x", IsMain: true},
		{Path: "/wt/r/main", Branch: "main", Prunable: true},
	}
	if got := liveWorktreeForBranch(stale, "main"); got != "" {
		t.Errorf("liveWorktreeForBranch on a prunable worktree = %q, want empty", got)
	}
}

// --- MainCheckout against real git, in throwaway repos ---

func gitT(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(),
		"GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_SYSTEM=/dev/null",
		"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t.t",
		"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t.t",
	)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

// newRepo builds a repo whose primary checkout sits on `parked`, with `main` present.
func newRepo(t *testing.T, parked string) string {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not on PATH")
	}
	root := filepath.Join(t.TempDir(), "root")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	gitT(t, root, "init", "-q", "-b", "main")
	// git reports canonical (symlink-resolved) paths; TempDir on macOS is under
	// /var -> /private/var, so canonicalize here to compare against git's output.
	if resolved, err := filepath.EvalSymlinks(root); err == nil {
		root = resolved
	}
	gitT(t, root, "commit", "-q", "--allow-empty", "-m", "init")
	if parked != "main" {
		gitT(t, root, "checkout", "-q", "-b", parked)
	}
	return root
}

// The headline case: the primary checkout is parked on a feature branch and a separate
// worktree holds the default branch. MainCheckout must point at that worktree.
func TestMainCheckoutPrefersTheDefaultBranchWorktree(t *testing.T) {
	root := newRepo(t, "feat/parked")
	wt := filepath.Join(filepath.Dir(root), "mainwt")
	gitT(t, root, "worktree", "add", "-q", wt, "main")

	branch, dir := MainCheckout(context.Background(), root)
	if branch != "main" {
		t.Errorf("branch = %q, want main", branch)
	}
	if dir != wt {
		t.Errorf("dir = %q, want %q", dir, wt)
	}
}

// No second worktree: the primary checkout already holds the default branch, so dir is
// the root and the caller emits its plain repo-root contract.
func TestMainCheckoutReturnsTheRootWhenItHoldsTheDefaultBranch(t *testing.T) {
	root := newRepo(t, "main")
	branch, dir := MainCheckout(context.Background(), root)
	if branch != "main" || dir != root {
		t.Errorf("MainCheckout = (%q, %q), want (main, %q)", branch, dir, root)
	}
}

// Regression guard: `rm -rf` on a worktree leaves git listing it, still recorded on
// `main`, flagged prunable. Handing that path back makes the wl wrapper abort with
// "could not resolve directory" — strictly worse than the repo-root fallback it replaced.
func TestMainCheckoutSkipsAWorktreeWhoseDirectoryIsGone(t *testing.T) {
	root := newRepo(t, "feat/parked")
	wt := filepath.Join(filepath.Dir(root), "mainwt")
	gitT(t, root, "worktree", "add", "-q", wt, "main")
	if err := os.RemoveAll(wt); err != nil { // deleted WITHOUT `git worktree remove`
		t.Fatal(err)
	}

	branch, dir := MainCheckout(context.Background(), root)
	if branch != "main" {
		t.Errorf("branch = %q, want main (it still resolves; only the checkout is gone)", branch)
	}
	if dir != "" {
		t.Errorf("dir = %q, want empty so the caller falls back to the repo root", dir)
	}
}

// A repo with no origin/HEAD, no main and no master has no default branch to resolve.
func TestMainCheckoutOnARepoWithNoDefaultBranch(t *testing.T) {
	root := newRepo(t, "main")
	gitT(t, root, "checkout", "-q", "-b", "develop")
	gitT(t, root, "branch", "-q", "-D", "main")

	branch, dir := MainCheckout(context.Background(), root)
	if branch != "" || dir != "" {
		t.Errorf("MainCheckout = (%q, %q), want both empty", branch, dir)
	}
}

// origin/HEAD wins over the main/master probes, and a slashed default branch survives.
func TestMainCheckoutHonorsOriginHEADIncludingSlashedNames(t *testing.T) {
	root := newRepo(t, "main")
	gitT(t, root, "checkout", "-q", "-b", "release/v2")
	gitT(t, root, "update-ref", "refs/remotes/origin/release/v2", "HEAD")
	gitT(t, root, "symbolic-ref", "refs/remotes/origin/HEAD", "refs/remotes/origin/release/v2")

	branch, dir := MainCheckout(context.Background(), root)
	if branch != "release/v2" {
		t.Errorf("branch = %q, want release/v2 (origin/HEAD beats the main fallback)", branch)
	}
	if dir != root {
		t.Errorf("dir = %q, want %q", dir, root)
	}
}
