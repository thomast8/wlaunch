package gh

import (
	"context"
	"os"
	"os/exec"
	"testing"
)

// isolateGitConfig points git at empty global/system config files so a developer's
// real ~/.gitconfig (which may set wlaunch.ghaccount as the personal default) can't
// leak into the per-repo config lookups these tests assert on.
func isolateGitConfig(t *testing.T) {
	t.Helper()
	t.Setenv("GIT_CONFIG_GLOBAL", os.DevNull)
	t.Setenv("GIT_CONFIG_SYSTEM", os.DevNull)
}

func TestParsePRs(t *testing.T) {
	in := []byte(`[
	  {"number":289,"title":"fix: reasoning effort","headRefName":"fix/re","author":{"login":"tho"}},
	  {"number":232,"title":"feat: per-stage","headRefName":"feat/p","author":{"login":"moh"}}
	]`)
	prs, err := parsePRs(in)
	if err != nil {
		t.Fatalf("parsePRs: %v", err)
	}
	if len(prs) != 2 {
		t.Fatalf("len = %d, want 2", len(prs))
	}
	if prs[0].Number != 289 || prs[0].HeadRefName != "fix/re" || prs[0].Author != "tho" {
		t.Errorf("pr0 = %+v", prs[0])
	}
	if prs[1].Author != "moh" {
		t.Errorf("pr1.Author = %q, want moh", prs[1].Author)
	}
}

func TestParsePRsEmpty(t *testing.T) {
	prs, err := parsePRs([]byte(`[]`))
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if len(prs) != 0 {
		t.Fatalf("want empty, got %d", len(prs))
	}
}

func TestParsePRsMalformed(t *testing.T) {
	if _, err := parsePRs([]byte(`not json`)); err == nil {
		t.Fatal("expected error on malformed JSON")
	}
}

func TestAccountForUnset(t *testing.T) {
	isolateGitConfig(t)
	dir := t.TempDir()
	mustGit(t, dir, "init")
	if got := accountFor(context.Background(), dir); got != "" {
		t.Errorf("accountFor = %q, want empty for unset key", got)
	}
}

func TestAccountForSet(t *testing.T) {
	isolateGitConfig(t)
	dir := t.TempDir()
	mustGit(t, dir, "init")
	mustGit(t, dir, "config", "wlaunch.ghaccount", "some-account")
	if got := accountFor(context.Background(), dir); got != "some-account" {
		t.Errorf("accountFor = %q, want some-account", got)
	}
}

func mustGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}
