package ui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/thomast8/wlaunch/internal/data/cache"
	"github.com/thomast8/wlaunch/internal/model"
)

// step applies one message to the model and returns the next model.
func step(t *testing.T, m Model, msg tea.Msg) Model {
	t.Helper()
	nm, _ := m.Update(msg)
	out, ok := nm.(Model)
	if !ok {
		t.Fatalf("Update returned %T, not Model", nm)
	}
	return out
}

func key(s string) tea.KeyMsg { return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(s)} }

// keymap test helpers for the type-to-filter model: bare letters now filter, so
// tools/actions go through the ';' leader, navigation uses the arrow keys, and the
// only quit is Ctrl+C.
var (
	down  = tea.KeyMsg{Type: tea.KeyDown}
	up    = tea.KeyMsg{Type: tea.KeyUp}
	ctrlC = tea.KeyMsg{Type: tea.KeyCtrlC}
)

// leader sends ';' then the action key (e.g. leader(m,"c") = launch claude).
func leader(t *testing.T, m Model, s string) Model {
	t.Helper()
	m = step(t, m, key(";"))
	return step(t, m, key(s))
}

// typeStr feeds each rune as a key press (drives the live filter or a modal input).
func typeStr(t *testing.T, m Model, s string) Model {
	t.Helper()
	for _, r := range s {
		m = step(t, m, key(string(r)))
	}
	return m
}

// loadedModel returns a model already focused in the panel (PRs view, scoped to
// /r) since most callers exercise panel row actions. Startup itself now defaults
// to the sidebar (see New()) — tests of that specific behavior should press ←
// from this model's PRs view (the leftmost) to step back out to the sidebar.
func loadedModel(t *testing.T) Model {
	t.Helper()
	m := stubDefault(New(), map[string]string{"/r": "main"})
	m.cache = nil
	m = step(t, m, tea.WindowSizeMsg{Width: 120, Height: 40})
	m = step(t, m, reposLoadedMsg{repos: []model.Repo{{Path: "/r", Name: "r"}}})
	m = step(t, m, prsLoadedMsg{gen: m.gen, prs: []model.PR{
		{Number: 289, Title: "fix things", HeadRefName: "fix/x", Author: "tho"},
		{Number: 232, Title: "feat stuff", HeadRefName: "feat/y", Author: "moh"},
	}})
	m = step(t, m, branchesLoadedMsg{gen: m.gen, branches: []model.Branch{
		{Name: "main", Upstream: "origin/main", IsCurrent: true},
		{Name: "feat/y", Ahead: 2},
	}})
	m = step(t, m, worktreesLoadedMsg{gen: m.gen, worktrees: []model.Worktree{
		{Path: "/r", Branch: "main", HEAD: "abc123", IsMain: true},
		{Path: "/wt/pr289", Branch: "fix/x", HEAD: "def456"},
	}})
	m = step(t, m, tea.KeyMsg{Type: tea.KeyRight}) // sidebar (the startup default) -> panel
	return m
}

// twoRepoModel is loadedModel with a second repo in the sidebar, for exercising the
// scope-on-panel-focus path (sideCur can then differ from scopedIdx).
func twoRepoModel(t *testing.T) Model {
	t.Helper()
	m := stubDefault(New(), nil)
	m.cache = nil
	m = step(t, m, tea.WindowSizeMsg{Width: 120, Height: 40})
	m = step(t, m, reposLoadedMsg{repos: []model.Repo{
		{Path: "/r", Name: "r"},
		{Path: "/r2", Name: "r2"},
	}})
	return m
}

func TestPickPREmitsContract(t *testing.T) {
	m := loadedModel(t)
	if m.state[model.ViewPRs] != stateReady {
		t.Fatalf("state = %v, want ready", m.state[model.ViewPRs])
	}
	m = leader(t, m, "c") // claude on the first PR
	if m.Selection() == nil {
		t.Fatal("expected a selection after ; c")
	}
	if got := m.Selection().Encode(); got != "v1\tpr\t/r\t289\tclaude\t\n" {
		t.Errorf("Encode() = %q", got)
	}
}

// ';o' = "open" must be a plain shell, distinct from ';c' (claude).
func TestLeaderOpenIsShellNotClaude(t *testing.T) {
	m := loadedModel(t)
	m = leader(t, m, "o")
	if m.Selection() == nil {
		t.Fatal("expected a selection after ; o")
	}
	if got := m.Selection().Encode(); got != "v1\tpr\t/r\t289\tshell\t\n" {
		t.Errorf("Encode() = %q, want a shell launch", got)
	}
}

func TestPickPRSecondRowLazygit(t *testing.T) {
	m := loadedModel(t)
	m = step(t, m, down)  // move to PR #232
	m = leader(t, m, "l") // lazygit
	if got := m.Selection().Encode(); got != "v1\tpr\t/r\t232\tlazygit\t\n" {
		t.Errorf("Encode() = %q", got)
	}
}

func TestCancelEmitsNothing(t *testing.T) {
	m := loadedModel(t)
	m = step(t, m, ctrlC)
	if !m.quit {
		t.Error("expected quit flag")
	}
	if m.Selection() != nil {
		t.Errorf("expected nil selection on cancel, got %+v", m.Selection())
	}
}

func TestStalePRLoadDropped(t *testing.T) {
	m := loadedModel(t)
	// A late load from a superseded generation must not overwrite the panel.
	m = step(t, m, prsLoadedMsg{gen: m.gen - 1, prs: []model.PR{{Number: 999}}})
	if len(m.prs) != 2 || m.prs[0].Number != 289 {
		t.Errorf("stale load clobbered current PRs: %+v", m.prs)
	}
}

func TestHydrateCacheSeedsFirstRender(t *testing.T) {
	t.Setenv("WLAUNCH_CACHE_DIR", t.TempDir())
	store := cache.Default()
	cache.Write(store, cache.KeyRepos(), []model.Repo{{Path: "/r", Name: "r"}})
	cache.Write(store, cache.KeyPRs("/r"), []model.PR{{Number: 42, Title: "cached", HeadRefName: "feat/cached", Author: "me"}})

	m := New().HydrateCache()
	if len(m.repos) != 1 || m.repos[0].Path != "/r" {
		t.Fatalf("cached repos not hydrated: %+v", m.repos)
	}
	if m.state[model.ViewPRs] != stateReady || len(m.prs) != 1 || m.prs[0].Number != 42 {
		t.Fatalf("cached PRs not hydrated: state=%v prs=%+v", m.state[model.ViewPRs], m.prs)
	}
	if m.loaded[model.ViewPRs] {
		t.Fatal("hydrating cache must not mark the live PR refresh as already loaded")
	}
}

func TestScopeReloadKeepsCachedRowsWhileRefreshing(t *testing.T) {
	t.Setenv("WLAUNCH_CACHE_DIR", t.TempDir())
	store := cache.Default()
	cache.Write(store, cache.KeyPRs("/r"), []model.PR{{Number: 42, Title: "cached", HeadRefName: "feat/cached", Author: "me"}})

	m := New()
	m = step(t, m, tea.WindowSizeMsg{Width: 120, Height: 40})
	m = step(t, m, reposLoadedMsg{repos: []model.Repo{{Path: "/r", Name: "r"}}})
	if m.state[model.ViewPRs] != stateReady || len(m.prs) != 1 {
		t.Fatalf("cached PRs should stay visible while refresh runs: state=%v prs=%+v", m.state[model.ViewPRs], m.prs)
	}
	if !m.loaded[model.ViewPRs] {
		t.Fatal("scope reload should kick the live refresh after showing cache")
	}
	if m.status != "refreshing PRs..." {
		t.Fatalf("status = %q, want refresh marker", m.status)
	}
}

func TestScopeReloadPrefetchesSiblingViews(t *testing.T) {
	m := New()
	m.cache = nil
	m = step(t, m, tea.WindowSizeMsg{Width: 120, Height: 40})
	m = step(t, m, reposLoadedMsg{repos: []model.Repo{{Path: "/r", Name: "r"}}})

	for _, v := range []model.View{model.ViewPRs, model.ViewBranches, model.ViewWorktrees} {
		if !m.loaded[v] {
			t.Fatalf("%s should be prefetched", v.Label())
		}
		if m.state[v] != stateLoading {
			t.Fatalf("%s state = %v, want loading", v.Label(), m.state[v])
		}
	}

	m = step(t, m, prsLoadedMsg{gen: m.gen, prs: []model.PR{{Number: 42}}})
	if !m.anyLoading() {
		t.Fatal("sibling prefetches should keep loading active until they complete")
	}
	m = step(t, m, branchesLoadedMsg{gen: m.gen, branches: []model.Branch{{Name: "main"}}})
	m = step(t, m, worktreesLoadedMsg{gen: m.gen, worktrees: []model.Worktree{{Path: "/r", Branch: "main"}}})
	if m.anyLoading() {
		t.Fatal("loading should stop after all prefetched views complete")
	}
}

func TestSidebarMoveWarmsFocusedRepoOnce(t *testing.T) {
	m := twoRepoModel(t)
	m.focus = focusSidebar
	m = step(t, m, down)

	if !m.warmed["/r2"] {
		t.Fatal("highlighted repo should be queued for cache warming")
	}
	if m.scopedIdx != 0 {
		t.Fatalf("sidebar prefetch should not change scoped repo, got %d", m.scopedIdx)
	}
}

func TestViewRendersWithoutPanic(t *testing.T) {
	m := loadedModel(t)
	if out := m.View(); out == "" {
		t.Error("View() returned empty for a ready model")
	}
	// also exercise loading/empty/error panels
	m.state[model.ViewPRs] = stateIdle
	_ = m.View()
	m.state[model.ViewPRs] = stateLoading
	_ = m.View()
	m.state[model.ViewPRs] = stateEmpty
	_ = m.View()
	m.state[model.ViewPRs] = stateError
	m.errMsg[model.ViewPRs] = "boom"
	_ = m.View()
}

func TestSwitchToBranchesAndPick(t *testing.T) {
	m := loadedModel(t)
	m = step(t, m, tea.KeyMsg{Type: tea.KeyRight}) // PRs -> Branches
	if m.view != model.ViewBranches {
		t.Fatalf("view = %v, want Branches", m.view)
	}
	m = leader(t, m, "c")
	if m.Selection() == nil || m.Selection().Encode() != "v1\tbranch\t/r\tmain\tclaude\t\n" {
		t.Errorf("branch pick = %v", m.Selection())
	}
}

// The sidebar is the startup default (New()), so this needs no navigation at all.
func TestSidebarLaunchesRepoRoot(t *testing.T) {
	m := stubDefault(New(), nil)
	m.cache = nil
	m = step(t, m, tea.WindowSizeMsg{Width: 120, Height: 40})
	m = step(t, m, reposLoadedMsg{repos: []model.Repo{{Path: "/r", Name: "r"}}})
	if m.focus != focusSidebar {
		t.Fatalf("focus = %v, want sidebar (the startup default)", m.focus)
	}
	m = leader(t, m, "c") // launch claude on the highlighted repo's root
	sel := m.Selection()
	if sel == nil || sel.Kind != model.KindRepo || sel.Ref != "" || sel.RepoRoot != "/r" || sel.Tool != "claude" {
		t.Errorf("sidebar repo launch = %+v", sel)
	}
}

// Sidebar Enter is the fast path: launch claude on the highlighted repo's main
// checkout (kind=repo, empty ref), the same target ';c' produces from the sidebar.
// The sidebar is the startup default, so plain Enter fires with zero preamble.
func TestSidebarEnterLaunchesClaudeOnMain(t *testing.T) {
	m := stubDefault(New(), nil)
	m.cache = nil
	m = step(t, m, tea.WindowSizeMsg{Width: 120, Height: 40})
	m = step(t, m, reposLoadedMsg{repos: []model.Repo{{Path: "/r", Name: "r"}}})
	m = step(t, m, tea.KeyMsg{Type: tea.KeyEnter}) // enter launches claude on the repo root
	sel := m.Selection()
	if sel == nil {
		t.Fatal("sidebar enter should launch claude on the repo root")
	}
	if sel.Kind != model.KindRepo || sel.Ref != "" || sel.RepoRoot != "/r" || sel.Tool != "claude" {
		t.Errorf("sidebar enter launch = %+v, want repo/claude on /r", sel)
	}
}

// Tab is a plain alias of Enter, so it launches the same repo-root pick as Enter —
// it no longer focuses the sidebar (already the startup default) or moves into it.
func TestSidebarTabLaunchesRepoRootLikeEnter(t *testing.T) {
	m := stubDefault(New(), nil)
	m.cache = nil
	m = step(t, m, tea.WindowSizeMsg{Width: 120, Height: 40})
	m = step(t, m, reposLoadedMsg{repos: []model.Repo{{Path: "/r", Name: "r"}}})
	m = step(t, m, tea.KeyMsg{Type: tea.KeyTab})
	sel := m.Selection()
	if sel == nil || sel.Kind != model.KindRepo || sel.Ref != "" || sel.RepoRoot != "/r" || sel.Tool != "claude" {
		t.Errorf("sidebar tab launch = %+v, want repo/claude on /r", sel)
	}
}

// Alt+Enter opens Codex Desktop instead of Claude, from either focus.
func TestAltEnterLaunchesCodexDesktop(t *testing.T) {
	m := loadedModel(t) // panel-focused, PR #289 under the cursor
	m = step(t, m, tea.KeyMsg{Type: tea.KeyEnter, Alt: true})
	sel := m.Selection()
	if sel == nil || sel.Kind != model.KindPR || sel.Ref != "289" || sel.Tool != "codex-desktop" {
		t.Errorf("alt+enter panel launch = %+v, want PR #289/codex-desktop", sel)
	}
}

func TestAltEnterFromSidebarLaunchesCodexDesktopOnRepoRoot(t *testing.T) {
	m := stubDefault(New(), nil)
	m.cache = nil
	m = step(t, m, tea.WindowSizeMsg{Width: 120, Height: 40})
	m = step(t, m, reposLoadedMsg{repos: []model.Repo{{Path: "/r", Name: "r"}}})
	m = step(t, m, tea.KeyMsg{Type: tea.KeyEnter, Alt: true})
	sel := m.Selection()
	if sel == nil || sel.Kind != model.KindRepo || sel.Ref != "" || sel.RepoRoot != "/r" || sel.Tool != "codex-desktop" {
		t.Errorf("alt+enter sidebar launch = %+v, want repo/codex-desktop on /r", sel)
	}
}

// → is now the only way to move focus from the sidebar into the panel, scoping it
// to the highlighted repo so the PRs/branches/worktrees shown follow the sidebar
// selection. (Tab no longer does this — it launches instead, see TestSidebarTab*.)
func TestEnterPanelScopesToHighlightedRepo(t *testing.T) {
	m := twoRepoModel(t)                           // sidebar-focused by default, repo 0 scoped
	m = step(t, m, down)                           // highlight repo 1
	m = step(t, m, tea.KeyMsg{Type: tea.KeyRight}) // enter the panel
	if m.focus != focusMain {
		t.Errorf("focus = %v, want panel", m.focus)
	}
	if m.scopedIdx != 1 {
		t.Errorf("scopedIdx = %d, want 1 (scoped to the highlighted repo)", m.scopedIdx)
	}
	if m.Selection() != nil {
		t.Errorf("entering the panel must not launch; got %+v", m.Selection())
	}
}

// Shift+Tab is the way back to the sidebar from the panel — Left/Right stay
// pure view-cycling (they wrap all the way around, including through
// Actionable), so returning to the sidebar needed its own key rather than
// overloading Left at the first view (which Actionable's wraparound already
// depends on: Left from PRs cycles to Actionable, the last view).
func TestShiftTabReturnsToSidebar(t *testing.T) {
	m := loadedModel(t) // panel-focused, PRs view
	m = step(t, m, tea.KeyMsg{Type: tea.KeyShiftTab})
	if m.focus != focusSidebar {
		t.Errorf("focus = %v, want sidebar", m.focus)
	}
}

// The two-stage new-worktree flow: a typed name then a typed base emits a branch
// pick carrying both.
func TestNewWorktreeTypedNameAndBase(t *testing.T) {
	m := loadedModel(t)
	m = step(t, m, tea.KeyMsg{Type: tea.KeyRight}) // -> Branches
	m = leader(t, m, "n")                          // stage 1: name prompt
	if m.inMode != inputNewWtName {
		t.Fatalf("inMode = %v, want inputNewWtName", m.inMode)
	}
	m = typeStr(t, m, "feat/zzz")
	m = step(t, m, tea.KeyMsg{Type: tea.KeyEnter}) // -> stage 2: base prompt
	if m.inMode != inputNewWtBase {
		t.Fatalf("inMode = %v, want inputNewWtBase", m.inMode)
	}
	m = typeStr(t, m, "origin/dev")
	m = step(t, m, tea.KeyMsg{Type: tea.KeyEnter})
	if m.Selection() == nil || m.Selection().Encode() != "v1\tbranch\t/r\tfeat/zzz\tclaude\torigin/dev\n" {
		t.Errorf("new-worktree pick = %v", m.Selection())
	}
}

// Two bare Enters accept both placeholders: the name becomes the random suggestion
// and the base stays empty (so worktree-setup.sh auto-detects origin's default).
func TestNewWorktreeTwoEntersAcceptDefaults(t *testing.T) {
	m := loadedModel(t)
	m = step(t, m, tea.KeyMsg{Type: tea.KeyRight}) // -> Branches
	m = leader(t, m, "n")
	wantName := m.nameInput.Placeholder // the generated random default
	if wantName == "" {
		t.Fatal("expected a random name placeholder")
	}
	m = step(t, m, tea.KeyMsg{Type: tea.KeyEnter}) // accept name default
	m = step(t, m, tea.KeyMsg{Type: tea.KeyEnter}) // accept base default
	sel := m.Selection()
	if sel == nil {
		t.Fatal("expected a selection after two Enters")
	}
	if sel.Kind != model.KindBranch || sel.RepoRoot != "/r" || sel.Ref != wantName || sel.Base != "" || sel.Tool != "claude" {
		t.Errorf("defaults pick = %+v (want Ref=%q, empty Base)", sel, wantName)
	}
}

// The default base placeholder reflects the repo's main checkout branch.
func TestNewWorktreeBaseDefaultPlaceholder(t *testing.T) {
	m := loadedModel(t)
	m = leader(t, m, "n")
	m = step(t, m, tea.KeyMsg{Type: tea.KeyEnter}) // advance to base stage
	if m.baseInput.Placeholder != "main" {
		t.Errorf("base placeholder = %q, want main", m.baseInput.Placeholder)
	}
}

// ';n' is no longer confined to the Branches view — it works from any view.
func TestNewWorktreeWorksFromPRsView(t *testing.T) {
	m := loadedModel(t)
	if m.view != model.ViewPRs {
		t.Fatalf("view = %v, want PRs", m.view)
	}
	m = leader(t, m, "n")
	if m.inMode != inputNewWtName {
		t.Fatalf("inMode = %v, want inputNewWtName from the PRs view", m.inMode)
	}
}

// esc at either stage cancels the whole flow without emitting.
func TestNewWorktreeEscCancels(t *testing.T) {
	m := loadedModel(t)
	m = leader(t, m, "n")
	m = step(t, m, tea.KeyMsg{Type: tea.KeyEnter}) // into base stage
	m = step(t, m, tea.KeyMsg{Type: tea.KeyEsc})
	if m.inMode != inputNone {
		t.Errorf("inMode = %v after esc, want inputNone", m.inMode)
	}
	if m.Selection() != nil {
		t.Errorf("esc must not emit a selection, got %+v", m.Selection())
	}
}

func TestFilterReducesRows(t *testing.T) {
	m := loadedModel(t)
	all := len(m.visibleRows())
	m = typeStr(t, m, "289") // type-to-filter: no '/' needed
	got := len(m.visibleRows())
	if got >= all || got != 1 {
		t.Errorf("filtered rows = %d (was %d), want 1", got, all)
	}
	// esc clears the filter (and must NOT quit)
	m = step(t, m, tea.KeyMsg{Type: tea.KeyEsc})
	if m.quit {
		t.Error("esc must not quit the TUI")
	}
	if len(m.visibleRows()) != all {
		t.Errorf("esc should clear the filter back to %d rows, got %d", all, len(m.visibleRows()))
	}
}

// plainModel loads a real repo plus a synthetic Plain entry (mirroring the "~"
// home entry repos.List() appends), with the plain entry highlighted.
func plainModel(t *testing.T) Model {
	t.Helper()
	m := New()
	m.cache = nil
	m = step(t, m, tea.WindowSizeMsg{Width: 120, Height: 40})
	m = step(t, m, reposLoadedMsg{repos: []model.Repo{
		{Path: "/r", Name: "r"},
		{Path: "/home/u", Name: "~", Plain: true},
	}})
	return step(t, m, down) // highlight the plain entry (sideCur=1)
}

// Scoping a Plain entry must skip the gh/git loads entirely (there is nothing
// to browse) and land all three views in a clean empty state, not stateError.
func TestPlainRepoScopeSkipsLoads(t *testing.T) {
	m := plainModel(t)
	nm, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRight}) // scope + enter panel
	m = nm.(Model)
	if cmd != nil {
		t.Error("scoping a plain entry must not kick any load commands")
	}
	for _, v := range []model.View{model.ViewPRs, model.ViewBranches, model.ViewWorktrees} {
		if m.state[v] != stateEmpty {
			t.Errorf("state[%v] = %v, want stateEmpty for a plain scope", v, m.state[v])
		}
	}
}

// The panel's empty state names the reason for a plain scope instead of the
// generic per-view "no PRs/branches/worktrees" copy.
func TestPlainRepoPanelShowsFriendlyEmptyMessage(t *testing.T) {
	m := plainModel(t)
	m = step(t, m, tea.KeyMsg{Type: tea.KeyRight}) // scope + enter panel
	if out := m.View(); !strings.Contains(out, "Not a git repo") {
		t.Errorf("expected a friendly empty message for a plain scope, got:\n%s", out)
	}
}

// A Plain sidebar entry launches exactly like any other repo — claude, Codex Desktop,
// and shell all just emit Kind=repo at its Path, unchanged from emitRepo.
func TestPlainRepoLaunchesClaudeCodexShell(t *testing.T) {
	m := plainModel(t)

	claude := step(t, m, tea.KeyMsg{Type: tea.KeyEnter})
	if sel := claude.Selection(); sel == nil || sel.Kind != model.KindRepo || sel.RepoRoot != "/home/u" || sel.Ref != "" || sel.Tool != "claude" {
		t.Errorf("enter on plain entry = %+v", sel)
	}

	codex := step(t, m, tea.KeyMsg{Type: tea.KeyEnter, Alt: true})
	if sel := codex.Selection(); sel == nil || sel.RepoRoot != "/home/u" || sel.Tool != "codex-desktop" {
		t.Errorf("alt+enter on plain entry = %+v", sel)
	}

	shiftEnter := step(t, m, tea.KeyMsg{Type: tea.KeyCtrlJ}) // Shift+Enter arrives as ctrl+j
	if sel := shiftEnter.Selection(); sel == nil || sel.RepoRoot != "/home/u" || sel.Tool != "shell" {
		t.Errorf("shift+enter (ctrl+j) on plain entry = %+v", sel)
	}

	ctrlO := step(t, m, tea.KeyMsg{Type: tea.KeyCtrlO})
	if sel := ctrlO.Selection(); sel == nil || sel.RepoRoot != "/home/u" || sel.Tool != "shell" {
		t.Errorf("ctrl+o on plain entry = %+v", sel)
	}
}

// ';n' (new worktree) is nonsensical outside a git repo and must refuse with a
// status message rather than opening the name-input prompt.
func TestNewWorktreeRefusedOnPlainRepo(t *testing.T) {
	m := plainModel(t)
	m = leader(t, m, "n")
	if m.inMode != inputNone {
		t.Errorf("inMode = %v, want inputNone (refused for a plain entry)", m.inMode)
	}
	if m.status == "" {
		t.Error("expected a refusal status message for ';n' on a plain entry")
	}
}

// Once scoped, the panel's empty state for a plain entry offers ⏎/⌥⏎/⇧⏎ (see
// TestPlainRepoPanelShowsFriendlyEmptyMessage) — those keys must actually launch
// from THAT focus too, not silently no-op because emit() finds no ready row.
func TestPlainRepoLaunchesFromPanelFocusToo(t *testing.T) {
	m := plainModel(t)
	m = step(t, m, tea.KeyMsg{Type: tea.KeyRight}) // scope + enter panel on "~"
	if m.focus != focusMain {
		t.Fatalf("focus = %v, want panel", m.focus)
	}

	claude := step(t, m, tea.KeyMsg{Type: tea.KeyEnter})
	if sel := claude.Selection(); sel == nil || sel.Kind != model.KindRepo || sel.RepoRoot != "/home/u" || sel.Tool != "claude" {
		t.Errorf("enter from panel focus on a plain scope = %+v", sel)
	}

	codex := step(t, m, tea.KeyMsg{Type: tea.KeyEnter, Alt: true})
	if sel := codex.Selection(); sel == nil || sel.RepoRoot != "/home/u" || sel.Tool != "codex-desktop" {
		t.Errorf("alt+enter from panel focus on a plain scope = %+v", sel)
	}

	shell := step(t, m, tea.KeyMsg{Type: tea.KeyCtrlJ})
	if sel := shell.Selection(); sel == nil || sel.RepoRoot != "/home/u" || sel.Tool != "shell" {
		t.Errorf("shift+enter from panel focus on a plain scope = %+v", sel)
	}
}

// Up from the top of the sidebar wraps to the last entry (and Down from the
// last wraps to the top) — the fast way to reach the pinned-last "~" entry.
func TestSidebarUpWrapsToLastEntry(t *testing.T) {
	m := twoRepoModel(t) // sidebar-focused, sideCur=0
	m = step(t, m, up)
	if m.sideCur != len(m.repos)-1 {
		t.Errorf("sideCur = %d after wrapping up, want %d (the last entry)", m.sideCur, len(m.repos)-1)
	}
	m = step(t, m, down)
	if m.sideCur != 0 {
		t.Errorf("sideCur = %d after wrapping down from the last entry, want 0", m.sideCur)
	}
}

// ←/→ form one ring across the sidebar and all four panel tabs: → always
// crosses the sidebar boundary onto the first tab (PRs), ← always onto the
// last (Actionable), so repeatedly pressing one direction visits every tab
// exactly once before repeating.
func TestArrowsRingWrapsThroughSidebarBothDirections(t *testing.T) {
	m := loadedModel(t) // panel-focused, PRs (already entered once via →)

	order := []model.View{model.ViewBranches, model.ViewWorktrees, model.ViewActionable}
	for _, want := range order {
		m = step(t, m, tea.KeyMsg{Type: tea.KeyRight})
		if m.view != want || m.focus != focusMain {
			t.Fatalf("after →: view=%v focus=%v, want %v/panel", m.view, m.focus, want)
		}
	}
	m = step(t, m, tea.KeyMsg{Type: tea.KeyRight}) // Actionable (last) -> wraps out to the sidebar
	if m.focus != focusSidebar {
		t.Fatalf("focus = %v after → from the last tab, want sidebar", m.focus)
	}
	m = step(t, m, tea.KeyMsg{Type: tea.KeyRight}) // sidebar -> wraps back in on the first tab
	if m.view != model.ViewPRs || m.focus != focusMain {
		t.Fatalf("after → from sidebar: view=%v focus=%v, want PRs/panel", m.view, m.focus)
	}

	m = step(t, m, tea.KeyMsg{Type: tea.KeyLeft}) // PRs (first) -> wraps out to the sidebar
	if m.focus != focusSidebar {
		t.Fatalf("focus = %v after ← from the first tab, want sidebar", m.focus)
	}
	m = step(t, m, tea.KeyMsg{Type: tea.KeyLeft}) // sidebar -> wraps back in on the last tab
	if m.view != model.ViewActionable || m.focus != focusMain {
		t.Fatalf("after ← from sidebar: view=%v focus=%v, want Actionable/panel", m.view, m.focus)
	}
}

// stubDefault replaces the git default-branch lookup with a repo->branch table, so no
// UI test shells out. A missing entry means the default branch does not resolve.
func stubDefault(m Model, refs map[string]string) Model {
	m.defaultBranch = func(repo string) string { return refs[repo] }
	return m
}

// Sidebar launches always hand the primary checkout to the shell dispatcher. That
// shared layer reconciles the default branch for both Claude and Codex.
func TestSidebarEnterAlwaysEmitsThePrimaryCheckout(t *testing.T) {
	m := stubDefault(New(), map[string]string{"/r": "main"})
	m.cache = nil
	m = step(t, m, tea.WindowSizeMsg{Width: 120, Height: 40})
	m = step(t, m, reposLoadedMsg{repos: []model.Repo{{Path: "/r", Name: "r"}}})
	m = step(t, m, tea.KeyMsg{Type: tea.KeyEnter})
	sel := m.Selection()
	if sel == nil {
		t.Fatal("sidebar enter should launch")
	}
	if got, want := sel.Encode(), "v1\trepo\t/r\t\tclaude\t\n"; got != want {
		t.Errorf("Encode() = %q, want %q", got, want)
	}
}

func TestSidebarEnterOnDefaultBranchRootEmitsRepoKind(t *testing.T) {
	m := stubDefault(New(), map[string]string{"/r": "main"})
	m.cache = nil
	m = step(t, m, tea.WindowSizeMsg{Width: 120, Height: 40})
	m = step(t, m, reposLoadedMsg{repos: []model.Repo{{Path: "/r", Name: "r"}}})
	m = step(t, m, tea.KeyMsg{Type: tea.KeyEnter})
	sel := m.Selection()
	if sel == nil || sel.Kind != model.KindRepo || sel.Ref != "" || sel.RepoRoot != "/r" {
		t.Errorf("sidebar enter = %+v, want repo//r with an empty ref", sel)
	}
}

// A repo launch does not resolve branches inside the TUI; it launches on the primary
// path and leaves canonical reconciliation to the shell dispatcher.
func TestSidebarEnterOnPlainEntrySkipsGitAndEmitsRepoKind(t *testing.T) {
	probed := false
	m := New()
	m.defaultBranch = func(string) string { probed = true; return "main" }
	m.cache = nil
	m = step(t, m, tea.WindowSizeMsg{Width: 120, Height: 40})
	m = step(t, m, reposLoadedMsg{repos: []model.Repo{{Path: "/home/t", Name: "~", Plain: true}}})
	m = step(t, m, tea.KeyMsg{Type: tea.KeyEnter})
	if probed {
		t.Error("a plain entry must not be probed for a default branch")
	}
	sel := m.Selection()
	if sel == nil || sel.Kind != model.KindRepo || sel.RepoRoot != "/home/t" || sel.Ref != "" {
		t.Errorf("plain sidebar enter = %+v, want repo//home/t", sel)
	}
}

func TestSidebarLeaderClaudeAlsoTargetsThePrimaryCheckout(t *testing.T) {
	m := stubDefault(New(), map[string]string{"/r": "main"})
	m.cache = nil
	m = step(t, m, tea.WindowSizeMsg{Width: 120, Height: 40})
	m = step(t, m, reposLoadedMsg{repos: []model.Repo{{Path: "/r", Name: "r"}}})
	m = leader(t, m, "c")
	sel := m.Selection()
	if sel == nil || sel.Kind != model.KindRepo || sel.RepoRoot != "/r" || sel.Ref != "" {
		t.Errorf("sidebar ;c = %+v, want repo /r with an empty ref", sel)
	}
}

// The stage-2 "branch from" placeholder must name the repo's DEFAULT branch. It used to
// report the primary checkout's current branch, which in a worktree workflow is a
// feature branch — the one thing the new worktree is certainly not based on (an empty
// field makes worktree-setup.sh branch from origin's default).
func TestNewWorktreeBasePlaceholderIsTheDefaultBranchNotTheParkedOne(t *testing.T) {
	m := stubDefault(New(), map[string]string{"/r": "main"})
	m.cache = nil
	m = step(t, m, tea.WindowSizeMsg{Width: 120, Height: 40})
	m = step(t, m, reposLoadedMsg{repos: []model.Repo{{Path: "/r", Name: "r"}}})
	// The primary checkout is parked on a feature branch, as it usually is.
	m = step(t, m, worktreesLoadedMsg{gen: m.gen, worktrees: []model.Worktree{
		{Path: "/r", Branch: "feat/parked", IsMain: true},
		{Path: "/wt/r/main", Branch: "main"},
	}})
	m = leader(t, m, "n")
	if got := m.baseInput.Placeholder; got != "main" {
		t.Errorf("base placeholder = %q, want main (not the parked feature branch)", got)
	}
}

// A repo whose default branch can't be resolved still offers a usable placeholder.
func TestNewWorktreeBasePlaceholderFallsBackToMain(t *testing.T) {
	m := stubDefault(New(), nil)
	m.cache = nil
	m = step(t, m, tea.WindowSizeMsg{Width: 120, Height: 40})
	m = step(t, m, reposLoadedMsg{repos: []model.Repo{{Path: "/r", Name: "r"}}})
	m = leader(t, m, "n")
	if got := m.baseInput.Placeholder; got != "main" {
		t.Errorf("base placeholder = %q, want the main fallback", got)
	}
}
