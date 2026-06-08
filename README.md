# wlaunch

A unified launcher TUI for a git / PR / worktree workflow. One terminal UI that
replaces a pile of single-purpose launchers: browse a repo's **PRs**, **branches**,
**worktrees**, or your **recent repos**, then open the selection in `claude`,
`lazygit`, `serie`, or a plain shell — landing in the right worktree every time.

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

| key | action |
|-----|--------|
| `←` / `→` | switch view (PRs · Branches · Worktrees · Repos) |
| `↑` `↓` / `k` `j` | move within the list |
| `tab` | toggle focus between the repo sidebar and the panel |
| `enter` (sidebar) | scope the views to that repo |
| `o` / `enter` | open (default tool) |
| `c` / `l` / `s` | open in claude / lazygit / serie |
| `n` (branches) | create a new branch worktree |
| `/` | filter the current view |
| `q` / `esc` | cancel |

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
