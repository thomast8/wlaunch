package ui

import (
	"errors"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/thomast8/wlaunch/internal/model"
)

// cleanupModel parks a loaded model on the Branches view with a branch list that
// exercises every cleanup classification: current, live-tracked, gone, no-upstream,
// and a branch checked out in a worktree (fix/x lives in /wt/pr289 from loadedModel).
func cleanupModel(t *testing.T) Model {
	t.Helper()
	m := loadedModel(t)
	m = step(t, m, tea.KeyMsg{Type: tea.KeyRight}) // -> Branches
	m.branches = []model.Branch{
		{Name: "main", Upstream: "origin/main", IsCurrent: true}, // current -> excluded
		{Name: "live", Upstream: "origin/live"},                  // live tracked -> excluded
		{Name: "old-pr", Upstream: "origin/old-pr", Gone: true},  // gone -> force delete
		{Name: "wip"},   // no upstream -> safe delete
		{Name: "fix/x"}, // checked out in /wt/pr289 -> excluded
	}
	m.state[model.ViewBranches] = stateReady
	m.cursor[model.ViewBranches] = 0
	return m
}

func TestDeleteCurrentBranchBlocked(t *testing.T) {
	m := cleanupModel(t) // cursor 0 = main (IsCurrent)
	m = leader(t, m, "d")
	if m.confirm != confirmNone {
		t.Errorf("the current branch must not enter a delete confirm")
	}
	if m.status == "" {
		t.Errorf("expected a status explaining the current branch can't be deleted")
	}
}

// Deleting a branch that's checked out in a worktree must not dead-end: it offers to
// remove the worktree and then delete the branch.
func TestDeleteCheckedOutBranchOffersWorktreeRemoval(t *testing.T) {
	m := cleanupModel(t)
	for i := 0; i < 4; i++ { // move to fix/x (index 4), checked out in /wt/pr289
		m = step(t, m, down)
	}
	if b := m.selectedBranch(); b == nil || b.Name != "fix/x" {
		t.Fatalf("cursor not on fix/x: %+v", b)
	}
	m = leader(t, m, "d")
	if m.confirm != confirmRemoveWtAndBranch {
		t.Fatalf("confirm = %v, want confirmRemoveWtAndBranch", m.confirm)
	}
	if m.delBranch != "fix/x" || len(m.confirmPaths) != 1 || m.confirmPaths[0] != "/wt/pr289" {
		t.Errorf("expected fix/x + /wt/pr289 queued, got %q / %v", m.delBranch, m.confirmPaths)
	}
}

func TestRemoveWtAndBranchYesRemovesWorktreeFirst(t *testing.T) {
	m := cleanupModel(t)
	for i := 0; i < 4; i++ {
		m = step(t, m, down)
	}
	m = leader(t, m, "d") // confirmRemoveWtAndBranch
	nm, cmd := m.Update(key("y"))
	m = nm.(Model)
	if cmd == nil {
		t.Error("y should kick the worktree removal")
	}
	if m.autoDeleteBranch != "fix/x" {
		t.Errorf("autoDeleteBranch = %q, want fix/x (carried to after the removal)", m.autoDeleteBranch)
	}
	if m.status != "removing worktree…" {
		t.Errorf("status = %q", m.status)
	}
}

// Once the worktree is gone, the carried intent auto-deletes the branch (no re-ask).
func TestAutoDeleteBranchAfterWorktreeRemoval(t *testing.T) {
	m := cleanupModel(t)
	m.autoDeleteBranch = "fix/x"
	nm, cmd := m.Update(worktreesRemovedMsg{gen: m.gen, removed: []string{"/wt/pr289"}, failed: 0})
	m = nm.(Model)
	if cmd == nil {
		t.Error("expected a follow-up delete command for the freed branch")
	}
	if m.autoDeleteBranch != "" {
		t.Errorf("autoDeleteBranch should be consumed, got %q", m.autoDeleteBranch)
	}
	if m.status != "deleting fix/x…" {
		t.Errorf("status = %q, want 'deleting fix/x…'", m.status)
	}
}

// A plain worktree removal (no carried intent) OFFERS to delete the freed branch.
func TestWorktreeRemovalOffersBranchDelete(t *testing.T) {
	m := cleanupModel(t) // fix/x is a branch AND checked out in /wt/pr289
	nm, cmd := m.Update(worktreesRemovedMsg{gen: m.gen, removed: []string{"/wt/pr289"}, failed: 0})
	m = nm.(Model)
	if cmd != nil {
		t.Error("offering a delete should not issue a command yet")
	}
	if m.confirm != confirmDeleteBranch || m.delBranch != "fix/x" {
		t.Errorf("expected an offer to delete fix/x, got confirm=%v delBranch=%q", m.confirm, m.delBranch)
	}
}

func TestDeleteBranchConfirmThenCancel(t *testing.T) {
	m := cleanupModel(t)
	m = step(t, m, down) // live
	m = step(t, m, down) // old-pr
	m = leader(t, m, "d")
	if m.confirm != confirmDeleteBranch || m.delBranch != "old-pr" {
		t.Fatalf("confirm = %v, delBranch = %q", m.confirm, m.delBranch)
	}
	m = step(t, m, key("n")) // cancel
	if m.confirm != confirmNone || m.delBranch != "" {
		t.Errorf("n should clear the confirm, got %v / %q", m.confirm, m.delBranch)
	}
}

func TestDeleteBranchYesKicksSafeDelete(t *testing.T) {
	m := cleanupModel(t)
	m = step(t, m, down)
	m = step(t, m, down) // old-pr
	m = leader(t, m, "d")
	nm, cmd := m.Update(key("y"))
	m = nm.(Model)
	if m.confirm != confirmNone {
		t.Errorf("y should clear the confirm")
	}
	if cmd == nil {
		t.Errorf("y should return the delete command")
	}
	if m.status != "deleting old-pr…" {
		t.Errorf("status = %q, want 'deleting old-pr…'", m.status)
	}
}

// A safe delete refused as unmerged must escalate to a force confirm, not just fail.
func TestSafeDeleteEscalatesToForce(t *testing.T) {
	m := cleanupModel(t)
	m.delBranch = "old-pr"
	nm, _ := m.Update(branchDeletedMsg{
		gen:    m.gen,
		name:   "old-pr",
		forced: false,
		err:    errors.New("error: the branch 'old-pr' is not fully merged."),
	})
	m = nm.(Model)
	if m.confirm != confirmForceDeleteBranch {
		t.Fatalf("confirm = %v, want confirmForceDeleteBranch", m.confirm)
	}
	if m.delBranch != "old-pr" {
		t.Errorf("delBranch should stay set for the escalation, got %q", m.delBranch)
	}
}

func TestForceDeleteYesKicksForce(t *testing.T) {
	m := cleanupModel(t)
	m.confirm = confirmForceDeleteBranch
	m.delBranch = "old-pr"
	nm, cmd := m.Update(key("y"))
	m = nm.(Model)
	if cmd == nil {
		t.Errorf("y should return the force-delete command")
	}
	if m.status != "force-deleting old-pr…" {
		t.Errorf("status = %q, want 'force-deleting old-pr…'", m.status)
	}
}

// A non-unmerged failure (e.g. checked out) must NOT escalate to force.
func TestSafeDeleteOtherErrorDoesNotEscalate(t *testing.T) {
	m := cleanupModel(t)
	m.delBranch = "old-pr"
	nm, _ := m.Update(branchDeletedMsg{
		gen: m.gen, name: "old-pr", forced: false,
		err: errors.New("error: cannot delete branch 'old-pr' checked out at ..."),
	})
	m = nm.(Model)
	if m.confirm != confirmNone {
		t.Errorf("a non-unmerged failure should not offer a force confirm, got %v", m.confirm)
	}
}

func TestBranchDeleteSplicesInMemory(t *testing.T) {
	m := cleanupModel(t)
	before := len(m.branches)
	nm, cmd := m.Update(branchDeletedMsg{gen: m.gen, name: "old-pr", err: nil})
	m = nm.(Model)
	if cmd != nil {
		t.Errorf("in-memory splice should issue no reload command")
	}
	if len(m.branches) != before-1 || m.branchByName("old-pr") != nil {
		t.Errorf("old-pr not spliced: %+v", m.branches)
	}
	if m.status != "✓ deleted old-pr" {
		t.Errorf("status = %q", m.status)
	}
}

// The cleanup batch must force gone branches, safe-delete no-upstream ones, and skip
// the current branch, live-tracked branches, and worktree-checked-out branches.
func TestCleanTargetsClassification(t *testing.T) {
	m := cleanupModel(t)
	m = leader(t, m, "D")
	if m.confirm != confirmCleanBranches {
		t.Fatalf("confirm = %v, want confirmCleanBranches", m.confirm)
	}
	got := map[string]bool{}
	for _, tgt := range m.cleanTargets {
		got[tgt.name] = tgt.force
	}
	if len(got) != 2 {
		t.Fatalf("cleanTargets = %+v, want exactly old-pr + wip", m.cleanTargets)
	}
	if force, ok := got["old-pr"]; !ok || !force {
		t.Errorf("old-pr should be a forced target, got %v/%v", ok, force)
	}
	if force, ok := got["wip"]; !ok || force {
		t.Errorf("wip should be a safe (non-forced) target, got %v/%v", ok, force)
	}
}

// With a filter active, D cleans only the visible subset (matches worktree D).
func TestCleanRespectsFilter(t *testing.T) {
	m := cleanupModel(t)
	m = typeStr(t, m, "old") // live type-to-filter, no '/' or enter needed
	m = leader(t, m, "D")
	if m.confirm != confirmCleanBranches {
		t.Fatalf("confirm = %v, want confirmCleanBranches", m.confirm)
	}
	if len(m.cleanTargets) != 1 || m.cleanTargets[0].name != "old-pr" {
		t.Errorf("filtered clean should target only old-pr, got %+v", m.cleanTargets)
	}
}

func TestCleanBranchesYesKicksCleanup(t *testing.T) {
	m := cleanupModel(t)
	m = leader(t, m, "D")
	nm, cmd := m.Update(key("y"))
	m = nm.(Model)
	if cmd == nil {
		t.Errorf("y should return the cleanup command")
	}
	if m.status != "cleaning…" {
		t.Errorf("status = %q, want 'cleaning…'", m.status)
	}
}

func TestCleanBranchesSplicesRemoved(t *testing.T) {
	m := cleanupModel(t)
	before := len(m.branches)
	nm, cmd := m.Update(branchesCleanedMsg{gen: m.gen, removed: []string{"old-pr", "wip"}, skipped: 0})
	m = nm.(Model)
	if cmd != nil {
		t.Errorf("in-memory splice should issue no reload command")
	}
	if len(m.branches) != before-2 {
		t.Errorf("cleaned branches not spliced: before=%d after=%d", before, len(m.branches))
	}
	if m.branchByName("old-pr") != nil || m.branchByName("wip") != nil {
		t.Errorf("old-pr/wip should be gone from the list")
	}
}

// A dirty worktree skipped during removal must offer a force-remove escalation
// rather than silently dead-ending.
func TestDirtyWorktreeOffersForceRemove(t *testing.T) {
	m := cleanupModel(t)
	nm, cmd := m.Update(worktreesRemovedMsg{
		gen: m.gen, removed: nil, dirty: []string{"/wt/pr289"}, dirtyFiles: 7, failed: 0,
	})
	m = nm.(Model)
	if cmd != nil {
		t.Error("offering force-remove should not issue a command yet")
	}
	if m.confirm != confirmForceRemoveWt {
		t.Fatalf("confirm = %v, want confirmForceRemoveWt", m.confirm)
	}
	if len(m.confirmPaths) != 1 || m.confirmPaths[0] != "/wt/pr289" || m.dirtyFiles != 7 {
		t.Errorf("force-remove not queued correctly: %v / %d", m.confirmPaths, m.dirtyFiles)
	}
}

func TestForceRemoveYesDiscards(t *testing.T) {
	m := cleanupModel(t)
	m.confirm = confirmForceRemoveWt
	m.confirmPaths = []string{"/wt/pr289"}
	m.dirtyFiles = 7
	nm, cmd := m.Update(key("y"))
	m = nm.(Model)
	if cmd == nil {
		t.Error("y should kick the force removal")
	}
	if m.status != "force-removing…" {
		t.Errorf("status = %q, want 'force-removing…'", m.status)
	}
}

// The force-remove escalation must preserve a carried branch-delete intent, so the
// combined "remove worktree and delete branch" flow completes after a dirty skip.
func TestForceRemovePreservesAutoDeleteIntent(t *testing.T) {
	m := cleanupModel(t)
	m.autoDeleteBranch = "fix/x"
	// a dirty skip arrives mid-combined-flow -> offer force-remove
	nm, _ := m.Update(worktreesRemovedMsg{gen: m.gen, dirty: []string{"/wt/pr289"}, dirtyFiles: 3})
	m = nm.(Model)
	if m.confirm != confirmForceRemoveWt || m.autoDeleteBranch != "fix/x" {
		t.Fatalf("intent not preserved: confirm=%v auto=%q", m.confirm, m.autoDeleteBranch)
	}
	// confirming force-remove must keep the intent alive for the follow-up
	nm, cmd := m.Update(key("y"))
	m = nm.(Model)
	if cmd == nil || m.autoDeleteBranch != "fix/x" {
		t.Errorf("force-remove should keep autoDeleteBranch and issue a command, got auto=%q cmd=%v", m.autoDeleteBranch, cmd != nil)
	}
}

// Cancelling a force-remove confirm must drop the carried branch-delete intent, so a
// later unrelated worktree removal can't silently consume a stale autoDeleteBranch.
func TestCancelForceRemoveDropsAutoDeleteIntent(t *testing.T) {
	m := cleanupModel(t)
	m.autoDeleteBranch = "fix/x"
	m.confirm = confirmForceRemoveWt
	m.confirmPaths = []string{"/wt/pr289"}
	m.dirtyFiles = 3
	m = step(t, m, key("n")) // keep the files
	if m.confirm != confirmNone {
		t.Errorf("n should clear the confirm, got %v", m.confirm)
	}
	if m.autoDeleteBranch != "" {
		t.Errorf("cancel must drop the carried intent, got %q", m.autoDeleteBranch)
	}
}

// An async message that raises a confirm while the leader is armed must not strand
// `awaiting` — otherwise the keystroke after the confirm resolves gets eaten.
func TestLeaderDisarmedByAsyncConfirm(t *testing.T) {
	m := cleanupModel(t)
	m = step(t, m, key(";")) // arm the leader
	if !m.awaiting {
		t.Fatal("';' should arm the leader")
	}
	// an in-flight delete result raises a force-delete confirm underneath it
	m = step(t, m, branchDeletedMsg{
		gen: m.gen, name: "old-pr", forced: false,
		err: errors.New("error: the branch 'old-pr' is not fully merged."),
	})
	if m.confirm != confirmForceDeleteBranch {
		t.Fatalf("expected the force-delete confirm, got %v", m.confirm)
	}
	m = step(t, m, key("n")) // cancel the confirm
	if m.awaiting {
		t.Error("cancelling the confirm must disarm the stranded leader")
	}
	m = step(t, m, key("z")) // next key must filter, not be swallowed
	if m.filterStr != "z" {
		t.Errorf("next key should filter, got filterStr=%q awaiting=%v", m.filterStr, m.awaiting)
	}
}

func TestBranchDeleteKeysIgnoredOutsidePanel(t *testing.T) {
	m := loadedModel(t) // PRs view
	m = leader(t, m, "d")
	m = leader(t, m, "D")
	if m.confirm != confirmNone {
		t.Errorf("d/D in the PRs view should not start a branch delete")
	}
}

// branchTrack drives the status column; these are the user-facing meanings.
func TestBranchTrackLabels(t *testing.T) {
	cases := []struct {
		b    model.Branch
		want string
	}{
		{model.Branch{Upstream: "origin/x"}, "✓"}, // tracked, in sync
		{model.Branch{Upstream: "origin/x", Ahead: 2}, "↑2"},
		{model.Branch{Upstream: "origin/x", Behind: 3}, "↓3"},
		{model.Branch{Upstream: "origin/x", Ahead: 1, Behind: 4}, "↑1↓4"},
		{model.Branch{Upstream: "origin/x", Gone: true}, "gone"}, // remote deleted
		{model.Branch{}, "local"},                                // no upstream at all
	}
	for _, c := range cases {
		if got := branchTrack(c.b); got != c.want {
			t.Errorf("branchTrack(%+v) = %q, want %q", c.b, got, c.want)
		}
	}
}

func TestCleanStatusFormatting(t *testing.T) {
	if got := cleanStatus(2, 0); got != "✓ deleted 2 branch(es)" {
		t.Errorf("cleanStatus(2,0) = %q", got)
	}
	if got := cleanStatus(3, 1); got != "✓ deleted 3 · 1 skipped (unmerged)" {
		t.Errorf("cleanStatus(3,1) = %q", got)
	}
	if got := cleanStatus(0, 2); got != "⚠ deleted none · 2 skipped (unmerged)" {
		t.Errorf("cleanStatus(0,2) = %q", got)
	}
}
