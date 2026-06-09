# wlaunch

A unified launcher TUI for a git / PR / worktree workflow. One terminal UI that
replaces a pile of single-purpose launchers: a repo sidebar on the left, and a
main panel that switches between a repo's **PRs**, **branches**, and **worktrees**.
Open any selection in `claude`, `lazygit`, `serie`, or a plain shell — landing in
the right worktree every time.

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
v1<TAB><kind><TAB><repo_root><TAB><ref><TAB><tool>
```

| field | values | meaning |
|-------|--------|---------|
| schema | `v1` | contract version |
| kind | `pr` \| `branch` \| `worktree` \| `repo` | what was picked |
| repo_root | absolute path | the scoped repo's main checkout |
| ref | PR number \| branch name \| worktree path \| (empty) | the thing to open |
| tool | `claude` \| `lazygit` \| `serie` \| `shell` | what to launch after `cd` |

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
| `↑` `↓` | move within the list |
| `←` / `→` | switch view (PRs · Branches · Worktrees) |
| `tab` | toggle focus between the repo sidebar and the panel |
| `enter` (sidebar) | scope the panel to that repo |
| `enter` (panel) | open the selection with the default tool |
| `;` then `o` `c` `l` `s` | open the selection (or sidebar repo) in default / claude / lazygit / serie |
| `;` then `n` (branches) | create a new branch worktree |
| `;` then `f` (branches) | fetch the selected branch's upstream ref (refmap-scoped, so a broken ref elsewhere can't fail it) |
| `;` then `p` (branches) | pull / fast-forward the selected branch (ff-only, safe; in-place for checked-out branches) |
| `;` then `d` (branches) | delete the selected branch (y/n; safe `-d`, escalates to a force confirm if unmerged). If it's checked out in a worktree, offers to remove that worktree first |
| `;` then `D` (branches) | clean (respects the filter): force-delete `gone` branches + safe-delete no-upstream ones (git skips any with unmerged commits; current + checked-out excluded) |
| `;` then `d` (worktrees) | remove the selected worktree (branch kept). A dirty worktree is skipped, then offers a force-remove confirm naming how many uncommitted files it would discard. On success, offers to delete the freed branch |
| `;` then `D` (worktrees) | remove all worktrees here (respects the filter; main skipped). Dirty ones go into one force-remove confirm; then offers to delete the freed branches |
| `Ctrl+C` | quit / cancel (drops to a shell) |

The sidebar lists your recent repos plus everything under `~/GitRepos`. The `●`
marks the repo the panel is scoped to.

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

All shelled out via a single context-timeout runner so a slow `gh`/`git` never
blocks the UI; loads are async and a generation counter drops stale results when
you switch repos mid-load.
