package git

import "testing"

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
