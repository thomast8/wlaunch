// Package ui is the Bubble Tea front end for wlaunch: a persistent repo sidebar
// plus a main panel of four views (PRs, branches, worktrees, recent repos) cycled
// with ←/→. It renders to stderr and emits a model.Selection on a launch pick;
// main reads Selection() and prints its Encode() to stdout.
package ui

import (
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/thomast8/wlaunch/internal/model"
)

type focus int

const (
	focusSidebar focus = iota
	focusMain
)

type loadState int

const (
	stateLoading loadState = iota
	stateReady
	stateError
	stateEmpty
)

type inputMode int

const (
	inputNone inputMode = iota
	inputFilter
	inputNewBranch
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
)

const viewN = 3 // PRs, Branches, Worktrees (repos live in the sidebar, not a view)

// rowData is the per-view uniform row: how to render it, how to filter it, and
// what Selection it produces when launched.
type rowData struct {
	kind     model.Kind
	repoRoot string
	ref      string
	filter   string
	render   func(w int, selected, focused bool) string
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

	gen     uint64 // bumped on repo switch; stamps async loads to drop stale ones
	spinner spinner.Model

	inMode    inputMode
	filter    textinput.Model
	nameInput textinput.Model

	confirm          confirmMode         // pending y/n for a destructive action
	confirmPaths     []string            // worktree paths queued for removal
	delBranch        string              // branch queued for single delete / force escalation
	cleanTargets     []branchCleanTarget // branches queued for batch cleanup
	autoDeleteBranch string              // after a worktree removal, delete this branch w/o re-asking
	status           string              // transient result line (e.g. "removed 3 · 1 skipped")

	selection *model.Selection
	quit      bool
}

// New builds the initial model.
func New() Model {
	sp := spinner.New()
	sp.Spinner = spinner.Dot

	fi := textinput.New()
	fi.Prompt = ""
	fi.Placeholder = "filter…"

	ni := textinput.New()
	ni.Prompt = ""
	ni.Placeholder = "new-branch-name"

	return Model{
		focus:     focusMain,
		view:      model.ViewPRs,
		spinner:   sp,
		filter:    fi,
		nameInput: ni,
	}
}

// Selection is what main reads after Run; nil means the user cancelled.
func (m Model) Selection() *model.Selection { return m.selection }

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
		m.state[model.ViewPRs] = readyOrEmpty(len(m.prs))
		return m, nil

	case branchesLoadedMsg:
		if msg.gen != m.gen {
			return m, nil
		}
		m.branches = msg.branches
		m.state[model.ViewBranches] = readyOrEmpty(len(m.branches))
		return m, nil

	case branchesRefreshedMsg:
		if msg.gen != m.gen {
			return m, nil
		}
		if msg.branches != nil { // nil = re-list failed; keep the old list
			m.branches = msg.branches
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
			m.state[model.ViewWorktrees] = readyOrEmpty(len(m.worktrees))
			if m.view == model.ViewWorktrees {
				m.clampCursor()
			}
		}
		m.status = removalStatus(len(msg.removed), msg.failed)
		return m.afterWorktreeRemoval(freedNames)

	case branchDeletedMsg:
		if msg.gen != m.gen {
			return m, nil
		}
		if msg.err == nil {
			m.removeBranches(map[string]bool{msg.name: true})
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
		}
		m.status = cleanStatus(len(msg.removed), msg.skipped)
		return m, nil

	case loadErrMsg:
		if msg.gen != m.gen {
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
		m.state[model.ViewWorktrees] == stateLoading
}

// scopeReload points the views at repos[idx], invalidating in-flight loads via a
// new generation, and kicks all three async loads (the cheap ones preload so the
// view is warm when the user arrows to it).
func (m *Model) scopeReload(idx int) tea.Cmd {
	m.scopedIdx = idx
	m.gen++
	m.prs, m.branches, m.worktrees = nil, nil, nil
	m.cursor = [viewN]int{}
	m.confirm, m.confirmPaths, m.status = confirmNone, nil, "" // drop any pending remove
	m.delBranch, m.cleanTargets = "", nil                      // drop any pending branch delete
	m.autoDeleteBranch = ""                                    // drop any carried delete intent
	m.state[model.ViewPRs] = stateLoading
	m.state[model.ViewBranches] = stateLoading
	m.state[model.ViewWorktrees] = stateLoading
	repo := m.repos[idx].Path
	return tea.Batch(
		m.spinner.Tick,
		loadPRsCmd(repo, m.gen),
		loadBranchesCmd(repo, m.gen),
		loadWorktreesCmd(repo, m.gen),
	)
}

func (m Model) handleKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if msg.String() == "ctrl+c" {
		m.quit = true
		return m, tea.Quit
	}
	if m.confirm != confirmNone {
		return m.handleConfirmKey(msg)
	}
	if m.inMode != inputNone {
		return m.handleInputKey(msg)
	}
	m.status = "" // any normal key clears the transient result line

	switch msg.String() {
	case "q", "esc":
		m.quit = true
		return m, tea.Quit
	case "d":
		if m.focus == focusMain {
			switch m.view {
			case model.ViewWorktrees:
				return m.askRemoveOne()
			case model.ViewBranches:
				return m.askDeleteBranch()
			}
		}
		return m, nil
	case "D":
		if m.focus == focusMain {
			switch m.view {
			case model.ViewWorktrees:
				return m.askRemoveAll()
			case model.ViewBranches:
				return m.askCleanBranches()
			}
		}
		return m, nil
	case "f":
		if m.focus == focusMain && m.view == model.ViewBranches {
			if b := m.selectedBranch(); b != nil {
				m.status = "fetching " + b.Name + "…"
				return m, fetchBranchCmd(m.scopedPath(), *b, m.gen)
			}
		}
		return m, nil
	case "p":
		if m.focus == focusMain && m.view == model.ViewBranches {
			if b := m.selectedBranch(); b != nil {
				m.status = "pulling " + b.Name + "…"
				return m, pullBranchCmd(m.scopedPath(), *b, m.branchCheckoutPath(*b), m.gen)
			}
		}
		return m, nil
	case "/":
		m.inMode = inputFilter
		return m, m.filter.Focus()
	case "tab":
		if m.focus == focusSidebar {
			m.focus = focusMain
		} else {
			m.focus = focusSidebar
		}
		return m, nil
	case "left":
		m.view = m.view.Prev()
		m.clampCursor()
		return m, nil
	case "right":
		m.view = m.view.Next()
		m.clampCursor()
		return m, nil
	case "up", "k":
		m.move(-1)
		return m, nil
	case "down", "j":
		m.move(1)
		return m, nil
	case "l":
		return m.launch(model.TargetLazygit)
	case "n":
		if m.focus == focusMain && m.view == model.ViewBranches {
			m.inMode = inputNewBranch
			m.nameInput.SetValue("")
			return m, m.nameInput.Focus()
		}
		return m, nil
	case "enter":
		// In the sidebar, enter SCOPES the panel to the repo (the common "drill in"
		// action); o/c/l/s launch a tool on the repo root instead.
		if m.focus == focusSidebar {
			cmd := m.scopeReload(m.sideCur)
			m.focus = focusMain
			return m, cmd
		}
		return m.emit(model.TargetDefault)
	case "o":
		return m.launch(model.TargetDefault)
	case "c":
		return m.launch(model.TargetClaude)
	case "s":
		return m.launch(model.TargetSerie)
	}
	return m, nil
}

// handleConfirmKey resolves a pending destructive-action y/n prompt. Any non-yes
// key cancels and clears all pending state.
func (m Model) handleConfirmKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if s := msg.String(); s != "y" && s != "Y" {
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
		return m, removeWorktreesCmd(m.scopedPath(), paths, m.gen)
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
		return m, removeWorktreesCmd(m.scopedPath(), paths, m.gen)
	}
	m.clearConfirm()
	return m, nil
}

// clearConfirm drops the pending destructive action and its queued targets.
func (m *Model) clearConfirm() {
	m.confirm = confirmNone
	m.confirmPaths = nil
	m.delBranch = ""
	m.cleanTargets = nil
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
	return m.emit(t)
}

// emitRepo launches a tool on the highlighted sidebar repo's root (the claude-here
// case): kind=repo, empty ref.
func (m Model) emitRepo(t model.Target) (tea.Model, tea.Cmd) {
	if m.sideCur < 0 || m.sideCur >= len(m.repos) {
		return m, nil
	}
	m.selection = &model.Selection{
		Kind:     model.KindRepo,
		RepoRoot: m.repos[m.sideCur].Path,
		Ref:      "",
		Tool:     t.Tool(),
	}
	return m, tea.Quit
}

func (m Model) handleInputKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc":
		if m.inMode == inputFilter {
			m.filter.SetValue("")
			m.filter.Blur()
			m.clampCursor()
		} else {
			m.nameInput.Blur()
		}
		m.inMode = inputNone
		return m, nil
	case "enter":
		if m.inMode == inputNewBranch {
			val := strings.TrimSpace(m.nameInput.Value())
			m.nameInput.Blur()
			m.inMode = inputNone
			if val != "" {
				return m.emitNewBranch(val)
			}
			return m, nil
		}
		m.filter.Blur()
		m.inMode = inputNone
		return m, nil
	case "up":
		m.move(-1)
		return m, nil
	case "down":
		m.move(1)
		return m, nil
	}
	var cmd tea.Cmd
	if m.inMode == inputFilter {
		m.filter, cmd = m.filter.Update(msg)
		m.clampCursor()
	} else {
		m.nameInput, cmd = m.nameInput.Update(msg)
	}
	return m, cmd
}

func (m *Model) move(d int) {
	if m.focus == focusSidebar {
		if n := len(m.repos); n > 0 {
			m.sideCur = clamp(m.sideCur+d, 0, n-1)
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
	m.selection = &model.Selection{Kind: rd.kind, RepoRoot: rd.repoRoot, Ref: rd.ref, Tool: t.Tool()}
	return m, tea.Quit
}

func (m Model) emitNewBranch(name string) (tea.Model, tea.Cmd) {
	if m.scopedIdx < 0 || m.scopedIdx >= len(m.repos) {
		return m, nil
	}
	m.selection = &model.Selection{
		Kind:     model.KindBranch,
		RepoRoot: m.repos[m.scopedIdx].Path,
		Ref:      name,
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
				render: func(w int, sel, foc bool) string { return renderPRRow(pr, w, sel, foc) },
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
				render: func(w int, sel, foc bool) string { return renderBranchRow(b, w, sel, foc) },
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
				render: func(w int, sel, foc bool) string { return renderWorktreeRow(wt, w, sel, foc) },
			})
		}
		return out
	}
	return nil
}

func (m Model) filterQuery() string {
	return strings.ToLower(strings.TrimSpace(m.filter.Value()))
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
		tab("Worktrees", m.view == model.ViewWorktrees)
	if q := strings.TrimSpace(m.filter.Value()); q != "" {
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
	case m.status != "":
		hint = styHeading.Render(m.status)
	case m.inMode == inputFilter:
		hint = "filter: " + m.filter.View() + styHint.Render("   enter apply · esc clear")
	case m.inMode == inputNewBranch:
		hint = "new branch: " + m.nameInput.View() + styHint.Render("   enter create · esc cancel")
	case m.focus == focusSidebar:
		hint = styHint.Render("↑↓ repo · enter scope panel · o/c/l/s open repo here · ") +
			styHeading.Render("tab → panel") + styHint.Render(" · q quit")
	default:
		extra := ""
		if m.view == model.ViewBranches {
			extra = "n new · f fetch · p pull · d del · D clean · "
		}
		if m.view == model.ViewWorktrees {
			extra = "d remove · D all · "
		}
		hint = styHint.Render("←→ view · ↑↓ move · o open · c claude · l lazygit · s serie · "+extra+"/ filter · ") +
			styHeading.Render("tab → repos") + styHint.Render(" · q quit")
	}
	// MaxWidth (not Width) so an over-long hint truncates to one line rather than
	// wrapping; the most useful hints lead, the trailing ones drop first.
	return renderer.NewStyle().MaxWidth(w).Render(hint)
}

func (m Model) renderSidebar(w, h int) string {
	focused := m.focus == focusSidebar
	var heading string
	if focused {
		heading = styHeading.Render("▸ REPOS")
	} else {
		heading = styMeta.Render("  REPOS")
	}
	rows := []string{heading}
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
			rows = append(rows, rowStyle(true).Render(label))
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

	var body string
	switch m.state[m.view] {
	case stateLoading:
		body = styMeta.Render(m.spinner.View() + " Loading " + m.view.Label() + "…")
	case stateError:
		body = styErr.Render("⚠ " + m.errMsg[m.view])
	case stateEmpty:
		body = styMeta.Render(emptyMsg(m.view))
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

func renderList(rows []rowData, cursor, w, h int, focused bool) string {
	if h < 1 {
		h = 1
	}
	start := windowStart(cursor, h, len(rows))
	var lines []string
	for i := start; i < start+h && i < len(rows); i++ {
		lines = append(lines, rows[i].render(w, i == cursor, focused))
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
	}
	return ""
}

// --- per-row renderers: fixed-width columns summing to exactly w (no wrap) ---

func renderPRRow(pr model.PR, w int, selected, focused bool) string {
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
		return rowStyle(focused).Render("▸ " + numCol + titleCol + " " + branchCol + " " + authorCol)
	}
	return "  " + styNum.Render(numCol) + styText.Render(titleCol) + " " + styMeta.Render(branchCol+" "+authorCol)
}

func renderBranchRow(b model.Branch, w int, selected, focused bool) string {
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
		return rowStyle(focused).Render("▸ " + nameCol + " " + trackCol + " " + dateCol + " " + subjCol)
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

func renderWorktreeRow(wt model.Worktree, w int, selected, focused bool) string {
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
		return rowStyle(focused).Render("▸ " + nameCol + " " + branchCol + " " + headCol + " " + badgeCol)
	}
	return "  " + styText.Render(nameCol) + " " + styMeta.Render(branchCol) + " " + styMeta.Render(headCol) + " " + styMeta.Render(badgeCol)
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
