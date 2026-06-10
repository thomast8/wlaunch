package model

import "testing"

func TestSelectionEncode(t *testing.T) {
	cases := []struct {
		name string
		sel  Selection
		want string
	}{
		{"pr", Selection{Kind: KindPR, RepoRoot: "/r", Ref: "289", Tool: "claude"}, "v1\tpr\t/r\t289\tclaude\t\n"},
		{"branch", Selection{Kind: KindBranch, RepoRoot: "/r", Ref: "feat/x", Tool: "lazygit"}, "v1\tbranch\t/r\tfeat/x\tlazygit\t\n"},
		{"branch-with-base", Selection{Kind: KindBranch, RepoRoot: "/r", Ref: "feat/x", Tool: "claude", Base: "origin/dev"}, "v1\tbranch\t/r\tfeat/x\tclaude\torigin/dev\n"},
		{"worktree", Selection{Kind: KindWorktree, RepoRoot: "/r", Ref: "/wt/pr289", Tool: "serie"}, "v1\tworktree\t/r\t/wt/pr289\tserie\t\n"},
		{"repo", Selection{Kind: KindRepo, RepoRoot: "/r", Ref: "", Tool: "shell"}, "v1\trepo\t/r\t\tshell\t\n"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := c.sel.Encode(); got != c.want {
				t.Fatalf("Encode() = %q, want %q", got, c.want)
			}
		})
	}
}

func TestTargetTool(t *testing.T) {
	cases := map[Target]string{
		TargetDefault: "claude",
		TargetClaude:  "claude",
		TargetLazygit: "lazygit",
		TargetSerie:   "serie",
		TargetShell:   "shell",
	}
	for tgt, want := range cases {
		if got := tgt.Tool(); got != want {
			t.Errorf("Target(%d).Tool() = %q, want %q", tgt, got, want)
		}
	}
}

func TestViewCycleWraps(t *testing.T) {
	if got := ViewPRs.Prev(); got != ViewWorktrees {
		t.Errorf("ViewPRs.Prev() = %v, want ViewWorktrees", got)
	}
	if got := ViewWorktrees.Next(); got != ViewPRs {
		t.Errorf("ViewWorktrees.Next() = %v, want ViewPRs", got)
	}
	if got := ViewPRs.Next(); got != ViewBranches {
		t.Errorf("ViewPRs.Next() = %v, want ViewBranches", got)
	}
}
