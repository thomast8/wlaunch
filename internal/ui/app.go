// Package ui is the Bubble Tea front end for wlaunch: a persistent repo sidebar
// plus a main panel of four views (PRs, branches, worktrees, actionable) cycled
// with ←/→. It renders to stderr and emits a model.Selection on a launch pick;
// main reads Selection() and prints its Encode() to stdout.
package ui

import (
	"fmt"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/thomast8/wlaunch/internal/data/cache"
	"github.com/thomast8/wlaunch/internal/model"
)

type focus int

const (
	focusSidebar focus = iota
	focusMain
)

type loadState int

const (
	stateIdle loadState = iota
	stateLoading
	stateReady
	stateError
	stateEmpty
)

type inputMode int

const (
	inputNone      inputMode = iota
	inputNewWtName           // stage 1 of the new-worktree prompt: the branch/worktree name
	inputNewWtBase           // stage 2: the ref to branch from
)

// confirmMode gates the destructive worktree-removal actions behind a y/n prompt.
type confirmMode int

const (
	confirmNone confirmMode = iota
	confirmRemoveOne
	confirmRemoveAll
	confirmDeleteBranch      // single safe delete (escalates to force on unmerged)
	confirmForceDeleteBranch // force a single delete after -d refused it
	confirmCleanBranches     // batch: force gone + safe no-upstream
	confirmRemoveWtAndBranch // branch is checked out: remove its worktree, then delete it
	confirmForceRemoveWt     // dirty worktree skipped: force-remove (discards changes)?
)

const viewN = 4 // PRs, Branches, Worktrees, Actionable (repos live in the sidebar, not a view)

// actScope toggles the Actionable view between the scoped repo and all repos. The
// zero value is this-repo, so the ←/→ launcher cycle defaults to the scoped repo;
// the dedicated tab opts into all-repos via WithAllReposScope.
type actScope int

const (
	scopeThisRepo actScope = iota
	scopeAllRepos
)

// rowData is the per-view uniform row: how to render it, how to filter it, and
// what Selection it produces when launched.
type rowData struct {
	kind     model.Kind
	repoRoot string
	ref      string
	filter   string
	render   func(w int, selected bool) string
}

// Model is the top-level Bubble Tea model.
type Model struct {
	width, height int
	ready         bool

	repos     []model.Repo
	scopedIdx int
	sideCur   int

	focus focus
	view  model.View

	prs       []model.PR
	branches  []model.Branch
	worktrees []model.Worktree

	state  [viewN]loadState
	errMsg [viewN]string
	cursor [viewN]int
	loaded [viewN]bool

	gen     uint64 // bumped on repo switch; stamps async loads to drop stale ones
	spinner spinner.Model
	cache   *cache.Store
	warmed  map[string]bool

	// Actionable view. Its load is repo-independent (all-repos) or scoped, so it
	// runs on its own generation and lazily — entering the view (or --view
	// actionable) loads it; repo switches and ;a/;r invalidate it.
	actionItems       []model.ActionItem
	actScope          actScope
	actionGen         uint64
	actionLoaded      bool
	actionBypassCache bool

	inMode    inputMode
	filterStr string          // live type-to-filter query (always on; no mode to enter)
	awaiting  bool            // true after ';' — next key is a tool/action from the leader menu
	nameInput textinput.Model // stage-1 name field of the new-worktree prompt
	baseInput textinput.Model // stage-2 base-ref field of the new-worktree prompt

	wtRepoPath string // target repo captured when the new-worktree flow starts
	wtName     string // name chosen in stage 1, carried into stage 2

	confirm          confirmMode         // pending y/n for a destructive action
	confirmPaths     []string            // worktree paths queued for removal
	delBranch        string              // branch queued for single delete / force escalation
	cleanTargets     []branchCleanTarget // branches queued for batch cleanup
	autoDeleteBranch string              // after a worktree removal, delete this branch w/o re-asking
	dirtyFiles       int                 // uncommitted-file count for a pending force-remove prompt
	status           string              // transient result line (e.g. "removed 3 · 1 skipped")

	selection *model.Selection
	quit      bool

	// defaultBranch resolves a repo's default branch. A field, not a direct call, so
	// tests can drive the new-worktree base placeholder without shelling out to git.
	defaultBranch func(repo string) string
}

// New builds the initial model.
func New() Model {
	sp := spinner.New()
	sp.Spinner = spinner.Dot

	ni := textinput.New()
	ni.Prompt = ""
	bi := textinput.New()
	bi.Prompt = ""

	return Model{
		focus:         focusSidebar,
		view:          model.ViewPRs,
		spinner:       sp,
		cache:         cache.Default(),
		warmed:        map[string]bool{},
		nameInput:     ni,
		baseInput:     bi,
		defaultBranch: gitDefaultBranch,
	}
}

// WithInitialView opens the launcher on a specific view (e.g. the Actionable
// tab). Used by the --view flag. Also focuses the panel: the whole point of
// asking for a specific view up front is to land ready to interact with it,
// not stranded in the sidebar (the default startup focus) behind an extra →.
func (m Model) WithInitialView(v model.View) Model {
	m.view = v
	m.focus = focusMain
	return m
}

// WithAllReposScope makes the Actionable view default to all-repos rather than
// the scoped repo. Used by the --scope flag / the dedicated tab.
func (m Model) WithAllReposScope() Model { m.actScope = scopeAllRepos; return m }

// Selection is what main reads after Run; nil means the user cancelled.
func (m Model) Selection() *model.Selection { return m.selection }

// HydrateCache seeds the first render with the latest local snapshot. Live loads
// still run after startup; this only avoids an empty first frame.
func (m Model) HydrateCache() Model {
	m.hydrateReposCache()
	if len(m.repos) > 0 {
		m.hydrateScopedCache(m.scopedIdx)
	}
	if m.view == model.ViewActionable {
		m.hydrateActionableCache()
	}
	return m
}

func (m *Model) hydrateReposCache() bool {
	rs, _, ok := cache.Read[[]model.Repo](m.cache, cache.KeyRepos())
	if !ok || len(rs) == 0 {
		return false
	}
	m.repos = rs
	if m.scopedIdx < 0 || m.scopedIdx >= len(m.repos) {
		m.scopedIdx = 0
	}
	if m.sideCur < 0 || m.sideCur >= len(m.repos) {
		m.sideCur = m.scopedIdx
	}
	return true
}

func (m *Model) hydrateScopedCache(idx int) {
	if idx < 0 || idx >= len(m.repos) {
		return
	}
	repo := m.repos[idx].Path
	if prs, _, ok := cache.Read[[]model.PR](m.cache, cache.KeyPRs(repo)); ok {
		m.prs = prs
		m.state[model.ViewPRs] = readyOrEmpty(len(prs))
	}
	if branches, _, ok := cache.Read[[]model.Branch](m.cache, cache.KeyBranches(repo)); ok {
		m.branches = branches
		m.state[model.ViewBranches] = readyOrEmpty(len(branches))
	}
	if worktrees, _, ok := cache.Read[[]model.Worktree](m.cache, cache.KeyWorktrees(repo)); ok {
		m.worktrees = worktrees
		m.state[model.ViewWorktrees] = readyOrEmpty(len(worktrees))
	}
}

func (m *Model) hydrateActionableCache() bool {
	if m.actionBypassCache {
		return false
	}
	key := cache.KeyActionableAllRepos()
	if m.actScope == scopeThisRepo {
		repo := m.scopedPath()
		if repo == "" {
			return false
		}
		key = cache.KeyActionableRepo(repo)
	}
	items, _, ok := cache.Read[[]model.ActionItem](m.cache, key)
	if !ok {
		return false
	}
	m.actionItems = items
	m.state[model.ViewActionable] = readyOrEmpty(len(items))
	return true
}

func (m Model) hasRenderable(v model.View) bool {
	return m.state[v] == stateReady || m.state[v] == stateEmpty
}

func (m *Model) setRefreshingStatus(v model.View) {
	if m.view == v && m.status == "" && m.hasRenderable(v) {
		m.status = "refreshing " + v.Label() + "..."
	}
}

func (m *Model) clearRefreshingStatus(v model.View) {
	if m.status == "refreshing "+v.Label()+"..." {
		m.status = ""
	}
}

// maybeLoadActionable kicks the Actionable load when the user is on that view and
// its data isn't loaded for the current (scope, repo). It's a no-op otherwise, so
// callers can fire it after any view/scope/repo change. Runs on actionGen (not the
// repo gen) so repo switches don't drop an in-flight all-repos fan-out.
func (m *Model) maybeLoadActionable() tea.Cmd {
	if m.view != model.ViewActionable || m.actionLoaded {
		return nil
	}
	m.hydrateActionableCache()
	hadCached := m.hasRenderable(model.ViewActionable)
	m.actionLoaded = true
	m.actionBypassCache = false
	m.actionGen++
	m.cursor[model.ViewActionable] = 0
	m.errMsg[model.ViewActionable] = ""
	if hadCached {
		m.setRefreshingStatus(model.ViewActionable)
	} else {
		m.state[model.ViewActionable] = stateLoading
	}
	if m.actScope == scopeAllRepos {
		return tea.Batch(m.spinner.Tick, loadActionableAllReposCmd(m.cache, m.actionGen))
	}
	repo := m.scopedPath()
	if repo == "" {
		m.state[model.ViewActionable] = stateError
		m.errMsg[model.ViewActionable] = "no repo to scope to"
		return nil
	}
	if m.scopedIdx >= 0 && m.scopedIdx < len(m.repos) && m.repos[m.scopedIdx].Plain {
		m.state[model.ViewActionable] = stateEmpty
		return nil
	}
	return tea.Batch(m.spinner.Tick, loadActionableThisRepoCmd(repo, m.actionGen))
}

func (m *Model) maybeLoadScopedView(v model.View) tea.Cmd {
	if v == model.ViewActionable || int(v) < 0 || int(v) >= viewN || m.loaded[v] {
		return nil
	}
	repo := m.scopedPath()
	if repo == "" {
		return nil
	}
	m.loaded[v] = true
	if m.hasRenderable(v) {
		m.setRefreshingStatus(v)
	} else {
		m.state[v] = stateLoading
	}
	switch v {
	case model.ViewPRs:
		return tea.Batch(m.spinner.Tick, loadPRsCmd(repo, m.gen))
	case model.ViewBranches:
		return tea.Batch(m.spinner.Tick, loadBranchesCmd(repo, m.gen))
	case model.ViewWorktrees:
		return tea.Batch(m.spinner.Tick, loadWorktreesCmd(repo, m.gen))
	}
	return nil
}

func (m *Model) maybeLoadCurrentView() tea.Cmd {
	// A plain (non-git) scope has nothing to load for any view — cycling views
	// with ←/→ must not spawn gh/git against it.
	if m.scopedIdx >= 0 && m.scopedIdx < len(m.repos) && m.repos[m.scopedIdx].Plain {
		return nil
	}
	if m.view == model.ViewActionable {
		return m.maybeLoadActionable()
	}
	return m.maybeLoadScopedView(m.view)
}

func (m *Model) prefetchScopedViews() tea.Cmd {
	return tea.Batch(
		m.maybeLoadScopedView(model.ViewPRs),
		m.maybeLoadScopedView(model.ViewBranches),
		m.maybeLoadScopedView(model.ViewWorktrees),
	)
}

func (m *Model) prefetchFocusedRepo() tea.Cmd {
	if m.focus != focusSidebar || m.sideCur < 0 || m.sideCur >= len(m.repos) {
		return nil
	}
	if m.repos[m.sideCur].Plain {
		return nil // nothing to prefetch for a non-git quick-launch entry
	}
	repo := m.repos[m.sideCur].Path
	if repo == "" || repo == m.scopedPath() {
		return nil
	}
	if m.warmed == nil {
		m.warmed = map[string]bool{}
	}
	if m.warmed[repo] {
		return nil
	}
	m.warmed[repo] = true
	return prefetchRepoTabsCmd(m.cache, repo)
}

func (m Model) Init() tea.Cmd {
	return tea.Batch(m.spinner.Tick, loadReposCmd())
}

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		m.ready = true
		return m, nil

	case reposLoadedMsg:
		m.repos = msg.repos
		cache.Write(m.cache, cache.KeyRepos(), m.repos)
		if len(m.repos) == 0 {
			for v := range m.state {
				m.state[v] = stateEmpty
			}
			return m, nil
		}
		m.scopedIdx, m.sideCur = 0, 0
		return m, m.scopeReload(0)

	case prsLoadedMsg:
		if msg.gen != m.gen {
			return m, nil
		}
		m.prs = msg.prs
		cache.Write(m.cache, cache.KeyPRs(m.scopedPath()), m.prs)
		m.clearRefreshingStatus(model.ViewPRs)
		m.state[model.ViewPRs] = readyOrEmpty(len(m.prs))
		return m, nil

	case branchesLoadedMsg:
		if msg.gen != m.gen {
			return m, nil
		}
		m.branches = msg.branches
		cache.Write(m.cache, cache.KeyBranches(m.scopedPath()), m.branches)
		m.clearRefreshingStatus(model.ViewBranches)
		m.state[model.ViewBranches] = readyOrEmpty(len(m.branches))
		return m, nil

	case branchesRefreshedMsg:
		if msg.gen != m.gen {
			return m, nil
		}
		if msg.branches != nil { // nil = re-list failed; keep the old list
			m.branches = msg.branches
			cache.Write(m.cache, cache.KeyBranches(m.scopedPath()), m.branches)
			m.state[model.ViewBranches] = readyOrEmpty(len(m.branches))
			if m.view == model.ViewBranches {
				m.clampCursor()
			}
		}
		m.status = msg.status
		return m, nil

	case worktreesLoadedMsg:
		if msg.gen != m.gen {
			return m, nil
		}
		m.worktrees = msg.worktrees
		cache.Write(m.cache, cache.KeyWorktrees(m.scopedPath()), m.worktrees)
		m.clearRefreshingStatus(model.ViewWorktrees)
		m.state[model.ViewWorktrees] = readyOrEmpty(len(m.worktrees))
		if m.view == model.ViewWorktrees {
			m.clampCursor()
		}
		return m, nil

	case worktreesRemovedMsg:
		if msg.gen != m.gen {
			return m, nil
		}
		// Capture the branches the removed worktrees held BEFORE splicing them out, so
		// we can offer to delete them now that they're no longer checked out.
		var freedNames []string
		if len(msg.removed) > 0 {
			gone := make(map[string]bool, len(msg.removed))
			for _, p := range msg.removed {
				gone[p] = true
				if wt := m.worktreeByPath(p); wt != nil && wt.Branch != "" && !wt.IsMain {
					freedNames = append(freedNames, wt.Branch)
				}
			}
			kept := make([]model.Worktree, 0, len(m.worktrees))
			for _, wt := range m.worktrees {
				if !gone[wt.Path] {
					kept = append(kept, wt)
				}
			}
			m.worktrees = kept
			cache.Write(m.cache, cache.KeyWorktrees(m.scopedPath()), m.worktrees)
			m.state[model.ViewWorktrees] = readyOrEmpty(len(m.worktrees))
			if m.view == model.ViewWorktrees {
				m.clampCursor()
			}
		}
		m.status = removalStatus(len(msg.removed), msg.failed)
		// Some worktrees were refused as dirty/locked: offer to force-remove them
		// (keeps any carried autoDeleteBranch so the branch delete still follows).
		if len(msg.dirty) > 0 {
			m.confirmPaths = msg.dirty
			m.dirtyFiles = msg.dirtyFiles
			m.confirm = confirmForceRemoveWt
			return m, nil
		}
		return m.afterWorktreeRemoval(freedNames)

	case branchDeletedMsg:
		if msg.gen != m.gen {
			return m, nil
		}
		if msg.err == nil {
			m.removeBranches(map[string]bool{msg.name: true})
			cache.Write(m.cache, cache.KeyBranches(m.scopedPath()), m.branches)
			m.status = "✓ deleted " + msg.name
			return m, nil
		}
		// A safe delete refused because the branch isn't merged into HEAD: offer to
		// force it (keeps delBranch set for the escalation confirm).
		if !msg.forced && isUnmerged(msg.err) {
			m.delBranch = msg.name
			m.confirm = confirmForceDeleteBranch
			m.status = msg.name + " has unmerged commits"
			return m, nil
		}
		m.status = msg.name + ": " + friendly(msg.err)
		return m, nil

	case branchesCleanedMsg:
		if msg.gen != m.gen {
			return m, nil
		}
		if len(msg.removed) > 0 {
			gone := make(map[string]bool, len(msg.removed))
			for _, n := range msg.removed {
				gone[n] = true
			}
			m.removeBranches(gone)
			cache.Write(m.cache, cache.KeyBranches(m.scopedPath()), m.branches)
		}
		m.status = cleanStatus(len(msg.removed), msg.skipped)
		return m, nil

	case actionablesLoadedMsg:
		if msg.gen != m.actionGen {
			return m, nil
		}
		m.actionItems = msg.items
		if m.actScope == scopeAllRepos {
			cache.Write(m.cache, cache.KeyActionableAllRepos(), m.actionItems)
		} else {
			cache.Write(m.cache, cache.KeyActionableRepo(m.scopedPath()), m.actionItems)
		}
		m.clearRefreshingStatus(model.ViewActionable)
		m.state[model.ViewActionable] = readyOrEmpty(len(m.actionItems))
		if m.view == model.ViewActionable {
			m.clampCursor()
		}
		return m, nil

	case loadErrMsg:
		// The Actionable view loads on actionGen; everything else on the repo gen.
		wantGen := m.gen
		if msg.view == model.ViewActionable {
			wantGen = m.actionGen
		}
		if msg.gen != wantGen {
			return m, nil
		}
		if m.hasRenderable(msg.view) {
			m.status = friendly(msg.err)
			return m, nil
		}
		m.state[msg.view] = stateError
		m.errMsg[msg.view] = friendly(msg.err)
		return m, nil

	case spinner.TickMsg:
		if m.anyLoading() {
			var cmd tea.Cmd
			m.spinner, cmd = m.spinner.Update(msg)
			return m, cmd
		}
		return m, nil

	case tea.KeyMsg:
		return m.handleKey(msg)
	}
	return m, nil
}

func readyOrEmpty(n int) loadState {
	if n == 0 {
		return stateEmpty
	}
	return stateReady
}

func (m Model) anyLoading() bool {
	return m.state[model.ViewPRs] == stateLoading ||
		m.state[model.ViewBranches] == stateLoading ||
		m.state[model.ViewWorktrees] == stateLoading ||
		m.state[model.ViewActionable] == stateLoading
}

// scopeReload points the views at repos[idx], invalidating in-flight loads via a
// new generation, and kicks all three async loads (the cheap ones preload so the
// view is warm when the user arrows to it). A Plain entry (e.g. the "~" home
// quick-launch) has no PRs/branches/worktrees to load — gh/git would just error
// against it — so it skips straight to a clean empty state instead.
func (m *Model) scopeReload(idx int) tea.Cmd {
	m.scopedIdx = idx
	m.gen++
	m.prs, m.branches, m.worktrees = nil, nil, nil
	m.cursor = [viewN]int{}
	m.confirm, m.confirmPaths, m.status = confirmNone, nil, "" // drop any pending remove
	m.delBranch, m.cleanTargets = "", nil                      // drop any pending branch delete
	m.autoDeleteBranch, m.dirtyFiles = "", 0                   // drop any carried delete intent
	m.awaiting = false                                         // drop a dangling leader
	if m.actScope == scopeThisRepo {
		m.actionLoaded = false // this-repo actionable data is now stale; all-repos is repo-independent
	}
	if idx >= 0 && idx < len(m.repos) && m.repos[idx].Plain {
		m.state[model.ViewPRs] = stateEmpty
		m.state[model.ViewBranches] = stateEmpty
		m.state[model.ViewWorktrees] = stateEmpty
		m.loaded = [viewN]bool{}
		return nil
	}
	m.state[model.ViewPRs] = stateIdle
	m.state[model.ViewBranches] = stateIdle
	m.state[model.ViewWorktrees] = stateIdle
	m.loaded = [viewN]bool{}
	m.hydrateScopedCache(idx)
	return tea.Batch(m.prefetchScopedViews(), m.maybeLoadCurrentView())
}

// enterPanel moves focus into the panel, scoping it to the highlighted sidebar
// repo first when that differs from what's loaded. Scoping is the async load
// scopeReload kicks; when the repo is already scoped this is a cheap focus flip
// with no reload (so repeated → presses don't respawn gh/git).
func (m *Model) enterPanel() tea.Cmd {
	var cmd tea.Cmd
	if m.sideCur != m.scopedIdx {
		cmd = m.scopeReload(m.sideCur)
	}
	m.focus = focusMain
	return tea.Batch(cmd, m.maybeLoadActionable()) // reload Actionable if we landed back on it
}

func (m Model) handleKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if msg.String() == "ctrl+c" { // the one true quit; esc never kills the TUI
		m.quit = true
		return m, tea.Quit
	}
	if m.confirm != confirmNone {
		return m.handleConfirmKey(msg)
	}
	if m.inMode != inputNone {
		return m.handleInputKey(msg)
	}
	if m.awaiting { // previous key was ';' — resolve the leader menu
		m.awaiting = false
		return m.handleLeader(msg)
	}
	return m.handleMainKey(msg)
}

// handleMainKey is the default state: printable keys filter live (no '/' needed),
// arrows navigate, Enter/Tab launch, ';' opens the tool+action leader, and esc
// clears the filter rather than quitting (Ctrl+C is the only quit).
func (m Model) handleMainKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.Type {
	case tea.KeyTab, tea.KeyEnter:
		// Tab is a plain alias of Enter (least-resistance launch from wherever focus
		// already is), not a focus toggle — ←/→ (below) move focus instead. Alt+Enter
		// (and Alt+Tab, for symmetry) launches codex instead of claude; see the
		// Ctrl+O comment below for why a modifier key is the only reliable way to
		// distinguish this from plain Enter.
		return m.launchFromKey(msg)
	case tea.KeyShiftTab:
		// An instant, direction-independent way back to the sidebar from anywhere in
		// the panel, alongside the full ←/→ ring below (which can take a few presses
		// to get there from the middle of the tab strip).
		m.focus = focusSidebar
		return m, nil
	case tea.KeyLeft:
		// ←/→ form one ring: sidebar - PRs - Branches - Worktrees - Actionable - back
		// to the sidebar. Crossing the sidebar boundary is deterministic (→ always
		// lands on the first tab, ← on the last) rather than resuming whatever view
		// was last shown, so repeatedly pressing one direction visits every tab
		// exactly once before repeating — the same guarantee the 4 panel tabs
		// already gave each other, extended to include the sidebar.
		if m.focus == focusSidebar {
			m.view = model.ViewActionable
			return m, m.enterPanel()
		}
		if m.view == model.ViewPRs { // leftmost tab: ← wraps back out to the sidebar
			m.focus = focusSidebar
			return m, nil
		}
		m.view = m.view.Prev()
		m.clampCursor()
		cmd := m.maybeLoadCurrentView()
		return m, cmd
	case tea.KeyRight:
		if m.focus == focusSidebar {
			m.view = model.ViewPRs
			return m, m.enterPanel()
		}
		if m.view == model.ViewActionable { // rightmost tab: → wraps back out to the sidebar
			m.focus = focusSidebar
			return m, nil
		}
		m.view = m.view.Next()
		m.clampCursor()
		cmd := m.maybeLoadCurrentView()
		return m, cmd
	case tea.KeyUp:
		m.move(-1)
		return m, m.prefetchFocusedRepo()
	case tea.KeyDown:
		m.move(1)
		return m, m.prefetchFocusedRepo()
	case tea.KeyCtrlO, tea.KeyCtrlJ:
		// Shift+Enter arrives at the terminal layer as ctrl+j (probe-confirmed against
		// real Warp), so KeyCtrlJ is the third Enter-modifier: ⏎ claude · ⌥⏎ codex ·
		// ⇧⏎ shell. Ctrl+O stays bound to the same launch as an always-works alias —
		// 'o' keeps the mnemonic from `;o`, and it doesn't depend on Shift+Enter being
		// delivered as ctrl+j (a terminal-encoding detail another terminal could differ
		// on). Ctrl+Enter is still indistinguishable from plain Enter and stays unusable.
		m.status = ""
		return m.launch(model.TargetShell)
	case tea.KeyEsc:
		if m.filterStr != "" {
			m.filterStr = ""
			m.clampCursor()
		}
		return m, nil
	case tea.KeyBackspace:
		if r := []rune(m.filterStr); len(r) > 0 {
			m.filterStr = string(r[:len(r)-1])
			m.clampCursor()
		}
		return m, nil
	case tea.KeySpace:
		m.filterStr += " "
		m.clampCursor()
		return m, nil
	case tea.KeyRunes:
		if string(msg.Runes) == ";" { // leader: next key is a tool/action
			m.awaiting = true
			return m, nil
		}
		m.status = ""
		m.filterStr += string(msg.Runes)
		m.clampCursor()
		return m, nil
	}
	return m, nil
}

// handleLeader resolves the key pressed after ';' — the tool + action menu that the
// bare letters used to be (freed up so typing can filter).
func (m Model) handleLeader(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	m.status = ""
	switch msg.String() {
	case "c":
		return m.launch(model.TargetClaude)
	case "x":
		return m.launch(model.TargetCodex)
	case "l":
		return m.launch(model.TargetLazygit)
	case "s":
		return m.launch(model.TargetSerie)
	case "o":
		return m.launch(model.TargetShell) // "open" = a plain terminal in the worktree
	case "n":
		return m.startNewWorktree()
	case "a":
		if m.focus == focusMain && m.view == model.ViewActionable {
			if m.actScope == scopeAllRepos {
				m.actScope = scopeThisRepo
			} else {
				m.actScope = scopeAllRepos
			}
			m.actionLoaded = false
			m.actionItems = nil
			m.state[model.ViewActionable] = stateLoading
			m.filterStr = "" // reason keywords differ across scopes; start clean
			cmd := m.maybeLoadActionable()
			return m, cmd
		}
	case "r":
		if m.focus == focusMain && m.view == model.ViewActionable {
			m.actionLoaded = false
			m.actionBypassCache = true
			m.actionItems = nil
			m.state[model.ViewActionable] = stateLoading
			cmd := m.maybeLoadActionable()
			return m, cmd
		}
	case "f":
		if m.focus == focusMain && m.view == model.ViewBranches {
			if b := m.selectedBranch(); b != nil {
				m.status = "fetching " + b.Name + "…"
				return m, fetchBranchCmd(m.scopedPath(), *b, m.gen)
			}
		}
	case "p":
		if m.focus == focusMain && m.view == model.ViewBranches {
			if b := m.selectedBranch(); b != nil {
				m.status = "pulling " + b.Name + "…"
				return m, pullBranchCmd(m.scopedPath(), *b, m.branchCheckoutPath(*b), m.gen)
			}
		}
	case "d":
		if m.focus == focusMain {
			switch m.view {
			case model.ViewWorktrees:
				return m.askRemoveOne()
			case model.ViewBranches:
				return m.askDeleteBranch()
			}
		}
	case "D":
		if m.focus == focusMain {
			switch m.view {
			case model.ViewWorktrees:
				return m.askRemoveAll()
			case model.ViewBranches:
				return m.askCleanBranches()
			}
		}
	}
	return m, nil // any other key after ';' is a no-op (leader already dismissed)
}

// handleConfirmKey resolves a pending destructive-action y/n prompt. Any non-yes
// key cancels and clears all pending state.
func (m Model) handleConfirmKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if s := msg.String(); s != "y" && s != "Y" {
		m.autoDeleteBranch = "" // a cancel also drops any carried branch-delete intent
		m.clearConfirm()
		return m, nil
	}
	mode := m.confirm
	switch mode {
	case confirmRemoveOne, confirmRemoveAll:
		paths := m.confirmPaths
		m.clearConfirm()
		if len(paths) == 0 {
			return m, nil
		}
		m.status = "removing…"
		return m, removeWorktreesCmd(m.scopedPath(), paths, false, m.gen)
	case confirmDeleteBranch, confirmForceDeleteBranch:
		name := m.delBranch
		force := mode == confirmForceDeleteBranch
		m.clearConfirm()
		if name == "" {
			return m, nil
		}
		if force {
			m.status = "force-deleting " + name + "…"
		} else {
			m.status = "deleting " + name + "…"
		}
		return m, deleteBranchCmd(m.scopedPath(), name, force, m.gen)
	case confirmCleanBranches:
		targets := m.cleanTargets
		m.clearConfirm()
		if len(targets) == 0 {
			return m, nil
		}
		m.status = "cleaning…"
		return m, cleanBranchesCmd(m.scopedPath(), targets, m.gen)
	case confirmRemoveWtAndBranch:
		paths := m.confirmPaths
		branch := m.delBranch
		m.clearConfirm()
		if len(paths) == 0 {
			return m, nil
		}
		// Remove the worktree; afterWorktreeRemoval then auto-deletes the branch.
		m.autoDeleteBranch = branch
		m.status = "removing worktree…"
		return m, removeWorktreesCmd(m.scopedPath(), paths, false, m.gen)
	case confirmForceRemoveWt:
		paths := m.confirmPaths
		m.clearConfirm() // keeps autoDeleteBranch so a carried branch delete still follows
		if len(paths) == 0 {
			return m, nil
		}
		m.status = "force-removing…"
		return m, removeWorktreesCmd(m.scopedPath(), paths, true, m.gen)
	}
	m.clearConfirm()
	return m, nil
}

// clearConfirm drops the pending destructive action and its queued targets. It also
// disarms a leader that an async message may have stranded behind the confirm (the
// confirm is dispatched before `awaiting`, so a leftover `awaiting` would otherwise
// eat the next keystroke after the confirm resolves). It does NOT touch
// autoDeleteBranch — that intent must survive the clearConfirm() that precedes
// kicking a worktree removal.
func (m *Model) clearConfirm() {
	m.confirm = confirmNone
	m.confirmPaths = nil
	m.delBranch = ""
	m.cleanTargets = nil
	m.dirtyFiles = 0
	m.awaiting = false
}

// askDeleteBranch queues the selected branch for a safe delete (the current branch
// and any branch checked out in a worktree are refused — git won't delete those).
func (m Model) askDeleteBranch() (tea.Model, tea.Cmd) {
	b := m.selectedBranch()
	if b == nil {
		return m, nil
	}
	if b.IsCurrent {
		m.status = "can't delete the current branch"
		return m, nil
	}
	if p := m.branchCheckoutPath(*b); p != "" {
		// Don't dead-end: offer to remove the worktree and then delete the branch.
		m.delBranch = b.Name
		m.confirmPaths = []string{p}
		m.confirm = confirmRemoveWtAndBranch
		return m, nil
	}
	m.delBranch = b.Name
	m.confirm = confirmDeleteBranch
	return m, nil
}

// askCleanBranches queues the cleanup batch: gone branches force-deleted, no-upstream
// branches safe-deleted (git skips any with unmerged commits). Current + worktree-
// checked-out branches are excluded.
func (m Model) askCleanBranches() (tea.Model, tea.Cmd) {
	var targets []branchCleanTarget
	for _, rd := range m.visibleRows() { // visible = respects the filter, like worktree D
		b := m.branchByName(rd.ref)
		if b == nil || b.IsCurrent || m.branchCheckoutPath(*b) != "" {
			continue
		}
		switch {
		case b.Gone:
			targets = append(targets, branchCleanTarget{name: b.Name, force: true})
		case b.Upstream == "":
			targets = append(targets, branchCleanTarget{name: b.Name, force: false})
		}
	}
	if len(targets) == 0 {
		m.status = "no gone or unpushed branches to clean"
		return m, nil
	}
	m.cleanTargets = targets
	m.confirm = confirmCleanBranches
	return m, nil
}

// removeBranches splices a set of branch names out of the in-memory list (no re-read,
// same pattern as worktree removal).
func (m *Model) removeBranches(names map[string]bool) {
	kept := make([]model.Branch, 0, len(m.branches))
	for _, b := range m.branches {
		if !names[b.Name] {
			kept = append(kept, b)
		}
	}
	m.branches = kept
	m.state[model.ViewBranches] = readyOrEmpty(len(m.branches))
	if m.view == model.ViewBranches {
		m.clampCursor()
	}
}

// cleanStatus summarizes a batch branch cleanup.
func cleanStatus(removed, skipped int) string {
	switch {
	case removed == 0 && skipped == 0:
		return "nothing to clean"
	case skipped == 0:
		return fmt.Sprintf("✓ deleted %d branch(es)", removed)
	case removed == 0:
		return fmt.Sprintf("⚠ deleted none · %d skipped (unmerged)", skipped)
	default:
		return fmt.Sprintf("✓ deleted %d · %d skipped (unmerged)", removed, skipped)
	}
}

// afterWorktreeRemoval runs once worktrees are gone. If the user came from the branch
// view ("remove worktree and delete branch"), it auto-deletes that branch (no second
// prompt — the confirm already promised it). Otherwise it OFFERS to delete the freed
// branches: a single one via the branch-delete confirm, several via the clean batch.
func (m Model) afterWorktreeRemoval(freedNames []string) (tea.Model, tea.Cmd) {
	freed := m.deletableBranches(freedNames)
	if want := m.autoDeleteBranch; want != "" {
		m.autoDeleteBranch = ""
		if containsStr(freed, want) {
			m.status = "deleting " + want + "…"
			return m, deleteBranchCmd(m.scopedPath(), want, false, m.gen)
		}
		return m, nil // worktree gone, but the branch can't be deleted; keep the status
	}
	switch len(freed) {
	case 0:
		return m, nil
	case 1:
		m.delBranch = freed[0]
		m.confirm = confirmDeleteBranch
		return m, nil
	default:
		var targets []branchCleanTarget
		for _, n := range freed {
			b := m.branchByName(n)
			targets = append(targets, branchCleanTarget{name: n, force: b != nil && b.Gone})
		}
		m.cleanTargets = targets
		m.confirm = confirmCleanBranches
		return m, nil
	}
}

// deletableBranches keeps only the names that can actually be deleted right now: the
// branch still exists locally, isn't the current branch, and isn't checked out in
// some OTHER worktree (deduped, order preserved).
func (m Model) deletableBranches(names []string) []string {
	var out []string
	seen := map[string]bool{}
	for _, n := range names {
		if seen[n] {
			continue
		}
		seen[n] = true
		b := m.branchByName(n)
		if b == nil || b.IsCurrent || m.branchCheckoutPath(*b) != "" {
			continue
		}
		out = append(out, n)
	}
	return out
}

func containsStr(ss []string, s string) bool {
	for _, x := range ss {
		if x == s {
			return true
		}
	}
	return false
}

func (m Model) askRemoveOne() (tea.Model, tea.Cmd) {
	wt := m.selectedWorktree()
	if wt == nil {
		return m, nil
	}
	if wt.IsMain {
		m.status = "can't remove the main checkout"
		return m, nil
	}
	m.confirm = confirmRemoveOne
	m.confirmPaths = []string{wt.Path}
	return m, nil
}

func (m Model) askRemoveAll() (tea.Model, tea.Cmd) {
	var paths []string
	for _, rd := range m.visibleRows() { // visible = respects the filter, minus main
		if wt := m.worktreeByPath(rd.ref); wt != nil && !wt.IsMain {
			paths = append(paths, wt.Path)
		}
	}
	if len(paths) == 0 {
		m.status = "no removable worktrees here"
		return m, nil
	}
	m.confirm = confirmRemoveAll
	m.confirmPaths = paths
	return m, nil
}

func (m Model) selectedWorktree() *model.Worktree {
	if m.view != model.ViewWorktrees {
		return nil
	}
	vis := m.visibleRows()
	c := m.cursor[m.view]
	if c < 0 || c >= len(vis) {
		return nil
	}
	return m.worktreeByPath(vis[c].ref)
}

func (m Model) worktreeByPath(path string) *model.Worktree {
	for i := range m.worktrees {
		if m.worktrees[i].Path == path {
			return &m.worktrees[i]
		}
	}
	return nil
}

func (m Model) selectedBranch() *model.Branch {
	if m.view != model.ViewBranches {
		return nil
	}
	vis := m.visibleRows()
	c := m.cursor[m.view]
	if c < 0 || c >= len(vis) {
		return nil
	}
	return m.branchByName(vis[c].ref)
}

func (m Model) branchByName(name string) *model.Branch {
	for i := range m.branches {
		if m.branches[i].Name == name {
			return &m.branches[i]
		}
	}
	return nil
}

// branchCheckoutPath returns the path where a branch is checked out (the main repo
// or a worktree), or "" if it isn't checked out anywhere. A checked-out branch must
// be pulled in place; only an unchecked-out one can be ff'd via a fetch refspec.
func (m Model) branchCheckoutPath(b model.Branch) string {
	for _, wt := range m.worktrees {
		if wt.Branch == b.Name {
			return wt.Path
		}
	}
	return ""
}

func removalStatus(removed, failed int) string {
	switch {
	case removed == 0 && failed == 0:
		return "nothing removed"
	case failed == 0:
		return fmt.Sprintf("✓ removed %d worktree(s)", removed)
	case removed == 0:
		return fmt.Sprintf("⚠ removed none · %d skipped (dirty/locked)", failed)
	default:
		return fmt.Sprintf("✓ removed %d · %d skipped (dirty/locked)", removed, failed)
	}
}

// launch routes a target key to the sidebar (open the repo root) or the panel
// (open the selected PR/branch/worktree), depending on focus.
func (m Model) launch(t model.Target) (tea.Model, tea.Cmd) {
	if m.focus == focusSidebar {
		return m.emitRepo(t)
	}
	// A plain scope (e.g. the "~" home entry) has no PRs/branches/worktrees to pick
	// a row from — its panel is just the empty-state message — so a panel-focused
	// launch targets the scoped repo directly instead of falling through to emit()
	// (which would silently no-op: there is no ready row to read a Selection from).
	if m.scopedIdx >= 0 && m.scopedIdx < len(m.repos) && m.repos[m.scopedIdx].Plain {
		return m.emitRepoAt(m.scopedIdx, t)
	}
	return m.emit(t)
}

// launchFromKey resolves an Enter/Tab keypress to a launch: claude by default,
// codex when the Alt modifier is set (Option+Enter on a Mac keyboard — the only
// Enter-modifier this terminal stack can distinguish from plain Enter; see the
// Ctrl+O comment above for why Shift/Ctrl+Enter can't be used the same way).
func (m Model) launchFromKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	m.status = ""
	t := model.TargetDefault
	if msg.Alt {
		t = model.TargetCodex
	}
	return m.launch(t)
}

// emitRepo launches a tool on the highlighted sidebar repo (the claude-here case).
func (m Model) emitRepo(t model.Target) (tea.Model, tea.Cmd) {
	return m.emitRepoAt(m.sideCur, t)
}

// emitRepoAt is emitRepo generalized to an explicit repos[] index, so a
// panel-focused launch on a plain (non-git) scope can target the scoped repo
// instead of the sidebar's highlighted one.
//
// Repo launches always emit the primary checkout. The shell dispatcher owns canonical
// default-branch reconciliation so Claude and Codex use one policy, while branch and
// PR selections continue to emit their shared worktree targets.
func (m Model) emitRepoAt(idx int, t model.Target) (tea.Model, tea.Cmd) {
	if idx < 0 || idx >= len(m.repos) {
		return m, nil
	}
	m.selection = &model.Selection{
		Kind:     model.KindRepo,
		RepoRoot: m.repos[idx].Path,
		Ref:      "",
		Tool:     t.Tool(),
	}
	return m, tea.Quit
}

// startNewWorktree opens the two-stage new-worktree prompt for the target repo:
// the highlighted sidebar repo when the sidebar has focus, else the scoped (●) repo.
// It works from any view. Stage 1 collects a name (placeholder = a random
// adjective-noun slug that dodges existing names), stage 2 a ref to branch from
// (placeholder = the repo's detected default branch). An empty field accepts the
// placeholder, so two Enters = "random name off the default branch".
func (m Model) startNewWorktree() (tea.Model, tea.Cmd) {
	idx := m.scopedIdx
	if m.focus == focusSidebar {
		idx = m.sideCur
	}
	if idx < 0 || idx >= len(m.repos) {
		m.status = "no repo to create a worktree in"
		return m, nil
	}
	if m.repos[idx].Plain {
		m.status = "not a git repo — can't create a worktree here"
		return m, nil
	}
	m.wtRepoPath = m.repos[idx].Path
	m.nameInput.SetValue("")
	m.nameInput.Placeholder = randomNameAvoiding(m.takenNames(idx))
	m.baseInput.SetValue("")
	m.baseInput.Placeholder = m.defaultBase(idx)
	m.baseInput.Blur()
	m.inMode = inputNewWtName
	return m, m.nameInput.Focus()
}

// takenNames is the set of branch + worktree-branch names already in use in the
// target repo, so the random suggestion never collides. Only the scoped repo's
// branches/worktrees are loaded, so an empty set is returned for any other repo.
func (m Model) takenNames(idx int) map[string]bool {
	taken := map[string]bool{}
	if idx != m.scopedIdx {
		return taken
	}
	for _, b := range m.branches {
		taken[b.Name] = true
	}
	for _, wt := range m.worktrees {
		if wt.Branch != "" {
			taken[wt.Branch] = true
		}
	}
	return taken
}

// defaultBase is the branch shown as the stage-2 placeholder: the repo's default
// branch, else "main". Only informational — an empty base field emits an empty Base so
// worktree-setup.sh re-detects (and fetches) origin's default; the placeholder just
// tells the user what that will be, so it has to resolve the default branch the same
// way, and not — as it once did — report whatever branch the primary checkout is
// parked on, which is exactly the branch you are NOT about to base a worktree on.
func (m Model) defaultBase(idx int) string {
	if idx >= 0 && idx < len(m.repos) && !m.repos[idx].Plain && m.defaultBranch != nil {
		if branch := m.defaultBranch(m.repos[idx].Path); branch != "" {
			return branch
		}
	}
	return "main"
}

// handleInputKey drives the two-stage modal new-worktree prompt. Stage 1 captures
// the name (empty → the random placeholder, since the name must be concrete or the
// downstream script would prompt on stdin); stage 2 captures the base, where empty
// stays empty so the script auto-detects origin's default. esc cancels the flow.
func (m Model) handleInputKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc":
		return m.cancelNewWorktree(), nil
	case "enter":
		if m.inMode == inputNewWtName {
			name := strings.TrimSpace(m.nameInput.Value())
			if name == "" {
				name = m.nameInput.Placeholder
			}
			m.wtName = name
			m.nameInput.Blur()
			m.inMode = inputNewWtBase
			return m, m.baseInput.Focus()
		}
		base := strings.TrimSpace(m.baseInput.Value())
		m.baseInput.Blur()
		m.inMode = inputNone
		return m.emitNewWorktree(m.wtName, base)
	}
	var cmd tea.Cmd
	if m.inMode == inputNewWtName {
		m.nameInput, cmd = m.nameInput.Update(msg)
	} else {
		m.baseInput, cmd = m.baseInput.Update(msg)
	}
	return m, cmd
}

// cancelNewWorktree drops the in-progress new-worktree flow and clears its state.
func (m Model) cancelNewWorktree() Model {
	m.nameInput.Blur()
	m.baseInput.Blur()
	m.inMode = inputNone
	m.wtName, m.wtRepoPath = "", ""
	return m
}

func (m *Model) move(d int) {
	if m.focus == focusSidebar {
		// Wraps (unlike the panel below): the "~" home entry is always pinned last,
		// so wrapping is what makes it reachable in one keystroke (Up from the top).
		if n := len(m.repos); n > 0 {
			m.sideCur = ((m.sideCur+d)%n + n) % n
		}
		return
	}
	if n := len(m.visibleRows()); n > 0 {
		m.cursor[m.view] = clamp(m.cursor[m.view]+d, 0, n-1)
	}
}

func (m *Model) clampCursor() {
	n := len(m.visibleRows())
	if n == 0 {
		m.cursor[m.view] = 0
		return
	}
	m.cursor[m.view] = clamp(m.cursor[m.view], 0, n-1)
}

func (m Model) emit(t model.Target) (tea.Model, tea.Cmd) {
	if m.state[m.view] != stateReady {
		return m, nil
	}
	vis := m.visibleRows()
	c := m.cursor[m.view]
	if c < 0 || c >= len(vis) {
		return m, nil
	}
	rd := vis[c]
	if rd.repoRoot == "" {
		// A cross-repo actionable item whose repo isn't cloned locally: nothing to
		// cd into, so it's display-only rather than launchable.
		m.status = "#" + rd.ref + " isn't cloned locally — open it with gh pr view --web"
		return m, nil
	}
	m.selection = &model.Selection{Kind: rd.kind, RepoRoot: rd.repoRoot, Ref: rd.ref, Tool: t.Tool()}
	return m, tea.Quit
}

// emitNewWorktree records the new-branch-worktree pick: Kind=branch with the chosen
// name as Ref and the chosen base (possibly empty → script auto-detect) as Base.
func (m Model) emitNewWorktree(name, base string) (tea.Model, tea.Cmd) {
	if m.wtRepoPath == "" || name == "" {
		return m, nil
	}
	m.selection = &model.Selection{
		Kind:     model.KindBranch,
		RepoRoot: m.wtRepoPath,
		Ref:      name,
		Base:     base,
		Tool:     model.TargetDefault.Tool(),
	}
	return m, tea.Quit
}

// --- rows + filtering ---

func (m Model) scopedPath() string {
	if m.scopedIdx >= 0 && m.scopedIdx < len(m.repos) {
		return m.repos[m.scopedIdx].Path
	}
	return ""
}

func (m Model) rows(v model.View) []rowData {
	scoped := m.scopedPath()
	switch v {
	case model.ViewPRs:
		out := make([]rowData, 0, len(m.prs))
		for _, pr := range m.prs {
			pr := pr
			out = append(out, rowData{
				kind: model.KindPR, repoRoot: scoped, ref: fmt.Sprintf("%d", pr.Number),
				filter: fmt.Sprintf("%d %s %s %s", pr.Number, pr.Title, pr.HeadRefName, pr.Author),
				render: func(w int, sel bool) string { return renderPRRow(pr, w, sel) },
			})
		}
		return out
	case model.ViewBranches:
		out := make([]rowData, 0, len(m.branches))
		for _, b := range m.branches {
			b := b
			out = append(out, rowData{
				kind: model.KindBranch, repoRoot: scoped, ref: b.Name,
				filter: b.Name + " " + b.Subject,
				render: func(w int, sel bool) string { return renderBranchRow(b, w, sel) },
			})
		}
		return out
	case model.ViewWorktrees:
		out := make([]rowData, 0, len(m.worktrees))
		for _, wt := range m.worktrees {
			wt := wt
			out = append(out, rowData{
				kind: model.KindWorktree, repoRoot: scoped, ref: wt.Path,
				filter: wt.Path + " " + wt.Branch,
				render: func(w int, sel bool) string { return renderWorktreeRow(wt, w, sel) },
			})
		}
		return out
	case model.ViewActionable:
		showRepo := m.actScope == scopeAllRepos
		out := make([]rowData, 0, len(m.actionItems))
		for _, it := range m.actionItems {
			it := it
			out = append(out, rowData{
				// Each item carries its own repoRoot (it may span repos), so the
				// existing KindPR contract resolves cross-repo opens unchanged.
				kind: model.KindPR, repoRoot: it.RepoRoot, ref: strconv.Itoa(it.Number),
				filter: it.FilterText(),
				render: func(w int, sel bool) string { return renderActionableRow(it, showRepo, w, sel) },
			})
		}
		return out
	}
	return nil
}

func (m Model) filterQuery() string {
	return strings.ToLower(strings.TrimSpace(m.filterStr))
}

func (m Model) visibleRows() []rowData {
	all := m.rows(m.view)
	q := m.filterQuery()
	if q == "" {
		return all
	}
	out := make([]rowData, 0, len(all))
	for _, rd := range all {
		if strings.Contains(strings.ToLower(rd.filter), q) {
			out = append(out, rd)
		}
	}
	return out
}

// --- rendering ---

func (m Model) View() string {
	if !m.ready || m.width < 24 || m.height < 6 {
		return "starting…"
	}
	sidebarW := clamp(m.width*28/100, 20, 40)
	contentH := m.height - 3 // header + footer + a blank line
	if contentH < 3 {
		contentH = 3
	}
	panelW := m.width - sidebarW - 1

	sep := styHint.Render(strings.TrimRight(strings.Repeat("│\n", contentH), "\n"))
	middle := lipgloss.JoinHorizontal(
		lipgloss.Top,
		m.renderSidebar(sidebarW, contentH),
		sep,
		m.renderPanel(panelW, contentH),
	)
	return lipgloss.JoinVertical(lipgloss.Left,
		m.renderHeader(m.width),
		middle,
		m.renderFooter(m.width),
	)
}

func (m Model) renderHeader(w int) string {
	tab := func(label string, active bool) string {
		if active {
			return styTabActive.Render(" " + label + " ")
		}
		return styTabInactive.Render(" " + label + " ")
	}
	left := tab("PRs", m.view == model.ViewPRs) + " " +
		tab("Branches", m.view == model.ViewBranches) + " " +
		tab("Worktrees", m.view == model.ViewWorktrees) + " " +
		tab("Actionable", m.view == model.ViewActionable)
	if q := strings.TrimSpace(m.filterStr); q != "" {
		right := styMeta.Render("🔎 " + q)
		gap := w - lipgloss.Width(left) - lipgloss.Width(right)
		if gap > 1 {
			return renderer.NewStyle().MaxWidth(w).Render(left + strings.Repeat(" ", gap) + right)
		}
	}
	return renderer.NewStyle().Width(w).MaxWidth(w).Render(left)
}

func (m Model) renderFooter(w int) string {
	var hint string
	switch {
	case m.confirm == confirmRemoveOne:
		name := "this worktree"
		if len(m.confirmPaths) > 0 {
			name = filepath.Base(m.confirmPaths[0])
		}
		hint = styErr.Render("Remove worktree "+name+"? (branch kept)  ") +
			styHeading.Render("y") + styHint.Render(" yes · ") + styHeading.Render("n") + styHint.Render(" cancel")
	case m.confirm == confirmRemoveAll:
		hint = styErr.Render(fmt.Sprintf("Remove ALL %d worktrees here? (branches kept)  ", len(m.confirmPaths))) +
			styHeading.Render("y") + styHint.Render(" yes · ") + styHeading.Render("n") + styHint.Render(" cancel")
	case m.confirm == confirmDeleteBranch:
		hint = styErr.Render("Delete branch "+m.delBranch+"?  ") +
			styHeading.Render("y") + styHint.Render(" yes · ") + styHeading.Render("n") + styHint.Render(" cancel")
	case m.confirm == confirmForceDeleteBranch:
		hint = styErr.Render(m.delBranch+" isn't merged — force-delete?  ") +
			styHeading.Render("y") + styHint.Render(" force · ") + styHeading.Render("n") + styHint.Render(" cancel")
	case m.confirm == confirmCleanBranches:
		hint = styErr.Render(fmt.Sprintf("Clean %d branch(es)? gone forced, unpushed safe  ", len(m.cleanTargets))) +
			styHeading.Render("y") + styHint.Render(" yes · ") + styHeading.Render("n") + styHint.Render(" cancel")
	case m.confirm == confirmRemoveWtAndBranch:
		hint = styErr.Render(m.delBranch+" is in a worktree — remove the worktree and delete the branch?  ") +
			styHeading.Render("y") + styHint.Render(" yes · ") + styHeading.Render("n") + styHint.Render(" cancel")
	case m.confirm == confirmForceRemoveWt:
		what := "uncommitted changes"
		if m.dirtyFiles > 0 {
			what = fmt.Sprintf("%d uncommitted file(s)", m.dirtyFiles)
		}
		verb := "Force-remove worktree"
		if len(m.confirmPaths) > 1 {
			verb = fmt.Sprintf("Force-remove %d worktrees", len(m.confirmPaths))
		}
		hint = styErr.Render(verb+" with "+what+" — DISCARD them?  ") +
			styHeading.Render("y") + styHint.Render(" discard · ") + styHeading.Render("n") + styHint.Render(" keep")
	case m.awaiting: // leader pressed: show the tool/action menu it unlocks
		menu := "c claude · x codex · l lazygit · s serie · o shell · n new"
		if m.focus == focusMain && m.view == model.ViewBranches {
			menu += " · f fetch · p pull · d del · D clean"
		} else if m.focus == focusMain && m.view == model.ViewWorktrees {
			menu += " · d remove · D all"
		} else if m.focus == focusMain && m.view == model.ViewActionable {
			menu += " · a scope · r refresh"
		}
		hint = styHeading.Render("; ") + styHint.Render(menu)
	case m.status != "":
		hint = styHeading.Render(m.status)
	case m.inMode == inputNewWtName:
		hint = "new worktree — name: " + m.nameInput.View() + styHint.Render("   enter next · esc cancel")
	case m.inMode == inputNewWtBase:
		hint = "new worktree — base (branch from): " + m.baseInput.View() + styHint.Render("   enter create · esc cancel")
	default:
		clear := ""
		if m.filterStr != "" {
			clear = "esc clear · "
		}
		nav := "type to filter · ↑↓ move · ←→ view/sidebar · enter/tab claude · ⌥enter codex · ⇧enter shell · ⇧tab back · "
		if m.focus == focusSidebar {
			nav = "type to filter · ↑↓ repo · enter/tab claude · ⌥enter codex · ⇧enter shell · ←→ browse · "
		}
		hint = styHint.Render(clear+nav) +
			styHeading.Render("; tools/actions") + styHint.Render(" · ^C quit")
	}
	// MaxWidth (not Width) so an over-long hint truncates to one line rather than
	// wrapping; the most useful hints lead, the trailing ones drop first.
	return renderer.NewStyle().MaxWidth(w).Render(hint)
}

func (m Model) renderSidebar(w, h int) string {
	focused := m.focus == focusSidebar
	// Heading mirrors renderPanel: always the accent heading style, only the
	// ▸/space marker tracks focus — so "REPOS" matches the panel's "▸ PRs" instead
	// of dimming to grey when the panel has focus.
	heading := "  REPOS"
	if focused {
		heading = "▸ REPOS"
	}
	rows := []string{styHeading.Render(heading)}
	listH := h - 1
	start := windowStart(m.sideCur, listH, len(m.repos))
	for i := start; i < start+listH && i < len(m.repos); i++ {
		marker := "  "
		if i == m.scopedIdx {
			marker = "● " // the repo the panel is currently scoped to
		}
		label := padTrunc(marker+m.repos[i].Name, w)
		switch {
		case focused && i == m.sideCur:
			rows = append(rows, rowStyle().Render(label))
		case i == m.scopedIdx:
			rows = append(rows, styHeading.Render(label))
		default:
			rows = append(rows, styText.Render(label))
		}
	}
	return renderer.NewStyle().Width(w).Height(h).MaxWidth(w).Render(strings.Join(rows, "\n"))
}

func (m Model) renderPanel(w, h int) string {
	// No repo name in the heading — the sidebar's ● marker already shows the scoped
	// repo, so repeating it here was the redundant "repos line".
	heading := "  " + m.view.Label()
	if m.focus == focusMain {
		heading = "▸ " + m.view.Label()
	}
	if m.view == model.ViewActionable {
		if m.actScope == scopeAllRepos {
			heading += styMeta.Render("  · all repos")
		} else {
			heading += styMeta.Render("  · this repo")
		}
	}

	var body string
	switch m.state[m.view] {
	case stateIdle:
		body = styMeta.Render(m.spinner.View() + " Loading " + m.view.Label() + "…")
	case stateLoading:
		body = styMeta.Render(m.spinner.View() + " Loading " + m.view.Label() + "…")
	case stateError:
		body = styErr.Render("⚠ " + m.errMsg[m.view])
	case stateEmpty:
		if m.scopedIdx >= 0 && m.scopedIdx < len(m.repos) && m.repos[m.scopedIdx].Plain {
			body = styMeta.Render("Not a git repo — nothing to browse. ⏎ claude · ⌥⏎ codex · ⇧⏎ shell.")
		} else {
			body = styMeta.Render(emptyMsg(m.view))
		}
	case stateReady:
		vis := m.visibleRows()
		if len(vis) == 0 {
			body = styMeta.Render("No matches for filter.")
		} else {
			body = renderList(vis, m.cursor[m.view], w, h-1, m.focus == focusMain)
		}
	}
	content := styHeading.Render(heading) + "\n" + body
	return renderer.NewStyle().Width(w).Height(h).MaxWidth(w).Render(content)
}

// renderList draws the visible window of rows. Only the FOCUSED pane marks its
// cursor row: an unfocused panel drawing a highlight bar reads as "this is what
// Enter opens", when a sidebar-focused Enter actually launches the sidebar's repo.
// The cursor position itself is kept, so it reappears on arrowing back in.
func renderList(rows []rowData, cursor, w, h int, focused bool) string {
	if h < 1 {
		h = 1
	}
	start := windowStart(cursor, h, len(rows))
	var lines []string
	for i := start; i < start+h && i < len(rows); i++ {
		lines = append(lines, rows[i].render(w, focused && i == cursor))
	}
	return strings.Join(lines, "\n")
}

func emptyMsg(v model.View) string {
	switch v {
	case model.ViewPRs:
		return "No open PRs. Press c to start fresh, or → for branches & worktrees."
	case model.ViewBranches:
		return "No local branches."
	case model.ViewWorktrees:
		return "No linked worktrees yet."
	case model.ViewActionable:
		return "Nothing needs you right now. ←→ for PRs, branches & worktrees."
	}
	return ""
}

// --- per-row renderers: fixed-width columns summing to exactly w (no wrap) ---

func renderPRRow(pr model.PR, w int, selected bool) string {
	numCol := padTrunc(fmt.Sprintf("#%d", pr.Number), 5)
	avail := w - 2 - 5 - 2
	if avail < 16 {
		avail = 16
	}
	titleW := avail * 48 / 100
	branchW := avail * 30 / 100
	authorW := avail - titleW - branchW
	titleCol := padTrunc(pr.Title, titleW)
	branchCol := padTrunc("⎇ "+pr.HeadRefName, branchW)
	authorCol := padTrunc("@"+pr.Author, authorW)
	if selected {
		return rowStyle().Render("▸ " + numCol + titleCol + " " + branchCol + " " + authorCol)
	}
	return "  " + styNum.Render(numCol) + styText.Render(titleCol) + " " + styMeta.Render(branchCol+" "+authorCol)
}

func renderBranchRow(b model.Branch, w int, selected bool) string {
	avail := w - 2 - 3
	if avail < 20 {
		avail = 20
	}
	nameW := clamp(avail*34/100, 8, 32)
	trackW, dateW := 7, 5
	subjW := avail - nameW - trackW - dateW
	if subjW < 6 {
		subjW = 6
	}
	nameCol := padTrunc(b.Name, nameW)
	trackCol := padTrunc(branchTrack(b), trackW)
	dateCol := padTrunc(relTime(b.LastCommitUnix), dateW)
	subjCol := padTrunc(b.Subject, subjW)
	if selected {
		return rowStyle().Render("▸ " + nameCol + " " + trackCol + " " + dateCol + " " + subjCol)
	}
	nameRender := styText.Render(nameCol)
	if b.IsCurrent {
		nameRender = styNum.Render(nameCol)
	}
	return "  " + nameRender + " " + styMeta.Render(trackCol) + " " + styMeta.Render(dateCol) + " " + styMeta.Render(subjCol)
}

func branchTrack(b model.Branch) string {
	switch {
	case b.Gone:
		return "gone"
	case b.Ahead > 0 && b.Behind > 0:
		return fmt.Sprintf("↑%d↓%d", b.Ahead, b.Behind)
	case b.Ahead > 0:
		return fmt.Sprintf("↑%d", b.Ahead)
	case b.Behind > 0:
		return fmt.Sprintf("↓%d", b.Behind)
	case b.Upstream != "":
		return "✓"
	}
	return "local" // no upstream: a local-only branch (nothing to pull; cleanup safe-deletes)
}

func renderWorktreeRow(wt model.Worktree, w int, selected bool) string {
	avail := w - 2 - 3
	if avail < 20 {
		avail = 20
	}
	nameW := clamp(avail*30/100, 8, 28)
	branchW := clamp(avail*34/100, 8, 30)
	headW := 8
	badgeW := avail - nameW - branchW - headW
	if badgeW < 4 {
		badgeW = 4
	}
	branchStr := wt.Branch
	switch {
	case wt.Bare:
		branchStr = "(bare)"
	case wt.Detached:
		branchStr = "(detached)"
	}
	badges := []string{}
	if wt.IsMain {
		badges = append(badges, "main")
	}
	if wt.Locked {
		badges = append(badges, "locked")
	}
	if wt.Prunable {
		badges = append(badges, "prunable")
	}
	nameCol := padTrunc(filepath.Base(wt.Path), nameW)
	branchCol := padTrunc("⎇ "+branchStr, branchW)
	headCol := padTrunc(wt.HEAD, headW)
	badgeCol := padTrunc(strings.Join(badges, " "), badgeW)
	if selected {
		return rowStyle().Render("▸ " + nameCol + " " + branchCol + " " + headCol + " " + badgeCol)
	}
	return "  " + styText.Render(nameCol) + " " + styMeta.Render(branchCol) + " " + styMeta.Render(headCol) + " " + styMeta.Render(badgeCol)
}

// markerStyle colors an actionable row's marker/summary by severity: red for
// blocked/changes, accent for review/threads, dim for ready/stale/waiting.
func markerStyle(it model.ActionItem) lipgloss.Style {
	switch it.Marker {
	case "✗", "⚠":
		return styErr
	case "◆":
		return styNum
	default: // "✓", "·"
		return styMeta
	}
}

// renderActionableRow lays out: marker · #num · summary · [repo] · title. In
// all-repos scope a repo column is shown so cross-repo items are distinguishable.
func renderActionableRow(it model.ActionItem, showRepo bool, w int, selected bool) string {
	markerCol := padTrunc(it.Marker, 2)
	numCol := padTrunc(fmt.Sprintf("#%d", it.Number), 6)
	avail := w - 2 - 2 - 6 // leading 2 + marker 2 + num 6
	if avail < 20 {
		avail = 20
	}
	sumW := clamp(avail*24/100, 8, 16)
	repoW := 0
	if showRepo {
		repoW = clamp(avail*26/100, 8, 22)
	}
	titleW := avail - sumW - repoW - 2 // inter-column spaces
	if titleW < 6 {
		titleW = 6
	}
	sumCol := padTrunc(it.Summary, sumW)
	titleCol := padTrunc(it.Title, titleW)
	repoCol := ""
	if showRepo {
		repoCol = padTrunc(it.RepoName, repoW) + " "
	}
	if selected {
		return rowStyle().Render("▸ " + markerCol + numCol + sumCol + " " + repoCol + titleCol)
	}
	ms := markerStyle(it)
	return "  " + ms.Render(markerCol) + styNum.Render(numCol) + ms.Render(sumCol) + " " +
		styMeta.Render(repoCol) + styText.Render(titleCol)
}

func relTime(unix int64) string {
	if unix <= 0 {
		return ""
	}
	d := time.Since(time.Unix(unix, 0))
	switch {
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh", int(d.Hours()))
	case d < 14*24*time.Hour:
		return fmt.Sprintf("%dd", int(d.Hours()/24))
	default:
		return fmt.Sprintf("%dw", int(d.Hours()/24/7))
	}
}
