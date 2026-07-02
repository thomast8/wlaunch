# wlaunch

A unified launcher TUI for a git / PR / worktree workflow. One terminal UI that
replaces a pile of single-purpose launchers: a repo sidebar on the left, and a
main panel that switches between a repo's **PRs**, **branches**, **worktrees**,
and an **Actionable** view of PRs that need you. Open any selection in `claude`,
`codex`, `lazygit`, `serie`, or a plain shell, landing in the right worktree every time.

The **Actionable** view triages PRs by the least-friction, highest-impact next
thing you can do: ready-to-merge work, easy review requests, addressable review
feedback, stale/no-reviewer nudges, and then louder but heavier blocked work like
conflicts or red CI. It toggles between the scoped repo (rich signals) and
**all repos** across every configured account (`;a`), and opening an item drops
you into that PR's worktree exactly like the PRs view. Launch straight into it
with `wlaunch --view actionable --scope all` (the `Actionable · pick` tab does
this).

In the review bucket, the summary distinguishes why a requested review is still
on you: `review` means you have not engaged yet, `reply` means the author
commented after your last turn, `re-review` means the branch changed after your
changes-requested review, and `waiting` means your turn is already the latest
human turn.

Built with [Bubble Tea](https://github.com/charmbracelet/bubbletea). Designed to
drive the [warp-claude-workflow](https://github.com/thomast8/warp-claude-workflow)
worktree scripts, but the TUI itself is just a picker — it resolves nothing and
launches nothing. It prints one line describing what you chose; a thin shell
wrapper (`wl`) does the `cd` + launch.

## How it works

`wlaunch` renders to **stderr** and, on a pick, prints **one tab-separated line**
to **stdout**, then exits 0. Cancel prints nothing and exits 130. That keeps
stdout a clean data channel so it can be captured:

```
v1<TAB><kind><TAB><repo_root><TAB><ref><TAB><tool><TAB><base>
```

| field | values | meaning |
|-------|--------|---------|
| schema | `v1` | contract version |
| kind | `pr` \| `branch` \| `worktree` \| `repo` | what was picked |
| repo_root | absolute path | the scoped repo's main checkout |
| ref | PR number \| branch name \| worktree path \| (empty) | the thing to open |
| tool | `claude` \| `codex` \| `lazygit` \| `serie` \| `shell` | what to launch after `cd` |
| base | branch/ref \| (empty) | for a new-branch worktree, the ref to branch from; empty lets `worktree-setup.sh` auto-detect origin's default. Empty for every other kind. |

The wrapper maps `kind` → a directory (reusing `pr-worktree.sh` /
`worktree-setup.sh` for PR/branch worktrees) and launches `tool` there.

## Keys

**Just type to filter** — printable keys narrow the current view live; no `/` to enter
a mode. Tools and actions live behind a `;` leader, so the letters stay free for typing.
`esc` clears the filter (it never quits); `Ctrl+C` is the only quit.

| key | action |
|-----|--------|
| type any text | filter the current view live |
| `esc` | clear the filter (does **not** quit) |
| `↑` `↓` | move within the list (repos in the sidebar, rows in the panel) |
| `←` / `→` (panel) | switch view (PRs · Branches · Worktrees · Actionable) |
| `→` (sidebar) | scope the panel to the highlighted repo and move into it to browse |
| `tab` | toggle focus; moving into the panel scopes it to the highlighted repo (no reload if it's already scoped) |
| `enter` (sidebar) | launch claude on the repo's main checkout — the fast path when you just want to jump into a repo (same target as `;c` from the sidebar) |
| `enter` (panel) | launch the selection in claude (the default action) |
| `Ctrl+O` (panel) | open the selection in a plain shell (Enter-modifiers like Shift/Ctrl+Enter can't be detected by terminals, so a Ctrl-chord is used; `o` = open) |
| `;` then `c` `x` `l` `s` `o` | open the selection (or sidebar repo) in claude / codex / lazygit / serie / a plain shell |
| `;` then `n` (any view) | create a new-branch worktree in the scoped repo (or the highlighted sidebar repo). Two-stage prompt: a **name** (placeholder is a random `adjective-noun` slug — empty Enter takes it) then a **base** to branch from (placeholder is the repo's detected default branch — empty Enter lets the script auto-detect origin's default). So two Enters = "random name off the default branch" |
| `;` then `f` (branches) | fetch the selected branch's upstream ref (refmap-scoped, so a broken ref elsewhere can't fail it) |
| `;` then `p` (branches) | pull / fast-forward the selected branch (ff-only, safe; in-place for checked-out branches) |
| `;` then `d` (branches) | delete the selected branch (y/n; safe `-d`, escalates to a force confirm if unmerged). If it's checked out in a worktree, offers to remove that worktree first |
| `;` then `D` (branches) | clean (respects the filter): force-delete `gone` branches + safe-delete no-upstream ones (git skips any with unmerged commits; current + checked-out excluded) |
| `;` then `a` (actionable) | toggle the scope between this repo and all repos |
| `;` then `r` (actionable) | refresh the actionable list |
| `;` then `d` (worktrees) | remove the selected worktree (branch kept). A dirty worktree is skipped, then offers a force-remove confirm naming how many uncommitted files it would discard. On success, offers to delete the freed branch |
| `;` then `D` (worktrees) | remove all worktrees here (respects the filter; main skipped). Dirty ones go into one force-remove confirm; then offers to delete the freed branches |
| `Ctrl+C` | quit / cancel (drops to a shell) |

The sidebar lists your recent repos plus everything under `~/GitRepos`. The `●`
marks the repo the panel is scoped to.

### Theme

Colors adapt to your terminal's background automatically (queried once at
startup). If your terminal doesn't answer that query, set `WLAUNCH_THEME=dark`
or `WLAUNCH_THEME=light` to skip detection and force a palette.

### Branch status column

The middle column in the Branches view summarizes each branch against its upstream:

| marker | meaning |
|--------|---------|
| `✓` | tracks an upstream and is in sync |
| `↑N` / `↓N` / `↑N↓M` | N commits ahead / behind / both of the upstream |
| `gone` | had an upstream, but the remote branch was deleted (PR merged/closed) — what `;D` force-deletes |
| `local` | no upstream at all (never pushed); nothing to pull, and `;D` safe-deletes it if it holds no unmerged commits |

Worktree/branch deletion are linked: removing a worktree offers to delete its branch,
and deleting a checked-out branch offers to remove its worktree first — so a branch
that's stuck behind a worktree is never a dead end.

## Build

```sh
go build -o ~/.warp/bin/wlaunch ./cmd/wlaunch
```

The binary is a build artifact (gitignored); it is rebuilt at setup time, not
committed.

## Test

```sh
go test ./...                                  # hermetic: unit + parser tests
WLAUNCH_IT=1 WLAUNCH_IT_REPO=/path/to/repo \
  go test ./... -run Real -v                   # integration: real git/gh
```

## Data sources

- PRs: `gh pr list --json number,title,headRefName,author`
- Branches: `git for-each-ref refs/heads`
- Worktrees: `git worktree list --porcelain`
- Repos: `repo-default.sh` + `~/.warp/state/recent-repos`
- Actionable (this repo): `gh pr list` with the extended fields
  (`reviewDecision,mergeable,reviewRequests,statusCheckRollup,additions,deletions,…`)
  + one batched `reviewThreads` GraphQL query for unresolved-thread counts
- Actionable (all repos): `gh search prs --author @me` and `--review-requested
  @me` per account, mapped to local clones via each repo's origin slug. `gh
  search` exposes fewer fields, so merge/CI/thread/size signals aren't available
  at this tier yet (per-PR enrichment is a planned follow-up).

### Config

- `wlaunch.staleDays` (git config) — age in days past which a PR with nothing
  else pending is flagged stale (default 14).
- `wlaunch.accounts` (global git config) — space/comma-separated gh logins to
  aggregate in the all-repos Actionable view (e.g. a personal + a work account);
  defaults to the single active account.

All shelled out via a single context-timeout runner so a slow `gh`/`git` never
blocks the UI; loads are async and a generation counter drops stale results when
you switch repos mid-load.
