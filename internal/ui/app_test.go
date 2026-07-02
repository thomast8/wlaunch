package ui

import (
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

func loadedModel(t *testing.T) Model {
	t.Helper()
	m := New()
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
	return m
}

// twoRepoModel is loadedModel with a second repo in the sidebar, for exercising the
// scope-on-panel-focus path (sideCur can then differ from scopedIdx).
func twoRepoModel(t *testing.T) Model {
	t.Helper()
	m := New()
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

func TestSidebarLaunchesRepoRoot(t *testing.T) {
	m := loadedModel(t)
	m = step(t, m, tea.KeyMsg{Type: tea.KeyTab}) // focus the repo sidebar
	if m.focus != focusSidebar {
		t.Fatalf("focus = %v, want sidebar", m.focus)
	}
	m = leader(t, m, "c") // launch claude on the highlighted repo's root
	sel := m.Selection()
	if sel == nil || sel.Kind != model.KindRepo || sel.Ref != "" || sel.RepoRoot != "/r" || sel.Tool != "claude" {
		t.Errorf("sidebar repo launch = %+v", sel)
	}
}

// Sidebar Enter is the fast path: launch claude on the highlighted repo's main
// checkout (kind=repo, empty ref), the same target ';c' produces from the sidebar.
func TestSidebarEnterLaunchesClaudeOnMain(t *testing.T) {
	m := loadedModel(t)
	m = step(t, m, tea.KeyMsg{Type: tea.KeyTab})   // focus sidebar
	m = step(t, m, tea.KeyMsg{Type: tea.KeyEnter}) // enter launches claude on the repo root
	sel := m.Selection()
	if sel == nil {
		t.Fatal("sidebar enter should launch claude on the repo root")
	}
	if sel.Kind != model.KindRepo || sel.Ref != "" || sel.RepoRoot != "/r" || sel.Tool != "claude" {
		t.Errorf("sidebar enter launch = %+v, want repo/claude on /r", sel)
	}
}

// Moving focus into the panel (Tab or →) scopes it to the highlighted repo, so the
// PRs/branches/worktrees the panel shows follow the sidebar selection.
func TestEnterPanelScopesToHighlightedRepo(t *testing.T) {
	for _, tc := range []struct {
		name string
		key  tea.KeyType
	}{
		{"tab", tea.KeyTab},
		{"right", tea.KeyRight},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := twoRepoModel(t)
			m = step(t, m, tea.KeyMsg{Type: tea.KeyTab}) // focus sidebar (repo 0 scoped)
			m = step(t, m, down)                         // highlight repo 1
			m = step(t, m, tea.KeyMsg{Type: tc.key})     // enter the panel
			if m.focus != focusMain {
				t.Errorf("focus = %v, want panel", m.focus)
			}
			if m.scopedIdx != 1 {
				t.Errorf("scopedIdx = %d, want 1 (scoped to the highlighted repo)", m.scopedIdx)
			}
			if m.Selection() != nil {
				t.Errorf("entering the panel must not launch; got %+v", m.Selection())
			}
		})
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
