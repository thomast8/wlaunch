// Package git backs the branches and worktrees views by parsing for-each-ref /
// porcelain output. The parsers are pure and unit-tested against fixtures.
package git

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/thomast8/wlaunch/internal/data"
	"github.com/thomast8/wlaunch/internal/model"
)

// branchFormat keeps contents:subject LAST so a tab in any earlier field can't
// shift columns; we SplitN into exactly 6 parts. %09 is a literal tab.
const branchFormat = `%(refname:short)%09%(upstream:short)%09%(upstream:track)%09%(committerdate:unix)%09%(HEAD)%09%(contents:subject)`

var (
	aheadRe  = regexp.MustCompile(`ahead (\d+)`)
	behindRe = regexp.MustCompile(`behind (\d+)`)
)

func parseBranches(b []byte) []model.Branch {
	var out []model.Branch
	sc := bufio.NewScanner(bytes.NewReader(b))
	sc.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	for sc.Scan() {
		line := sc.Text()
		if line == "" {
			continue
		}
		f := strings.SplitN(line, "\t", 6)
		if len(f) < 6 {
			continue
		}
		br := model.Branch{
			Name:      f[0],
			Upstream:  f[1],
			IsCurrent: f[4] == "*",
			Subject:   f[5],
		}
		if t := f[2]; t != "" {
			if strings.Contains(t, "gone") {
				br.Gone = true
			}
			if m := aheadRe.FindStringSubmatch(t); m != nil {
				br.Ahead, _ = strconv.Atoi(m[1])
			}
			if m := behindRe.FindStringSubmatch(t); m != nil {
				br.Behind, _ = strconv.Atoi(m[1])
			}
		}
		if u, err := strconv.ParseInt(f[3], 10, 64); err == nil {
			br.LastCommitUnix = u
		}
		out = append(out, br)
	}
	return out
}

// ListBranches returns local branches, most-recent-commit first.
func ListBranches(ctx context.Context, repo string) ([]model.Branch, error) {
	b, err := data.Run(ctx, repo, "git", "-C", repo, "for-each-ref",
		"--sort=-committerdate",
		"--format="+branchFormat,
		"refs/heads")
	if err != nil {
		return nil, err
	}
	return parseBranches(b), nil
}

func parseWorktrees(b []byte) []model.Worktree {
	var out []model.Worktree
	var cur *model.Worktree
	first := true
	flush := func() {
		if cur != nil {
			out = append(out, *cur)
			cur = nil
		}
	}
	sc := bufio.NewScanner(bytes.NewReader(b))
	for sc.Scan() {
		line := sc.Text()
		switch {
		case line == "":
			flush()
		case strings.HasPrefix(line, "worktree "):
			flush()
			cur = &model.Worktree{Path: strings.TrimPrefix(line, "worktree ")}
			if first {
				cur.IsMain = true
				first = false
			}
		case cur == nil:
			// stray line outside a record; ignore
		case strings.HasPrefix(line, "HEAD "):
			h := strings.TrimPrefix(line, "HEAD ")
			if len(h) > 12 {
				h = h[:12]
			}
			cur.HEAD = h
		case strings.HasPrefix(line, "branch "):
			cur.Branch = strings.TrimPrefix(strings.TrimPrefix(line, "branch "), "refs/heads/")
		case line == "detached":
			cur.Detached = true
		case line == "bare":
			cur.Bare = true
		case line == "locked" || strings.HasPrefix(line, "locked "):
			cur.Locked = true
		case line == "prunable" || strings.HasPrefix(line, "prunable "):
			cur.Prunable = true
		}
	}
	flush()
	return out
}

// ListWorktrees returns all worktrees; the first (main checkout) has IsMain set.
func ListWorktrees(ctx context.Context, repo string) ([]model.Worktree, error) {
	b, err := data.Run(ctx, repo, "git", "-C", repo, "worktree", "list", "--porcelain")
	if err != nil {
		return nil, err
	}
	return parseWorktrees(b), nil
}

// RemoveWorktree removes a worktree (its working dir + admin link). With force=false
// git refuses a worktree that has uncommitted/untracked files (the branch and commits
// always stay); force=true (`--force`) overrides that, DISCARDING those files. A
// *locked* worktree needs `-f -f` and is not handled here — it surfaces as an error.
func RemoveWorktree(ctx context.Context, repo, path string, force bool) error {
	args := []string{"-C", repo, "worktree", "remove"}
	if force {
		args = append(args, "--force")
	}
	args = append(args, path)
	_, err := data.Run(ctx, repo, "git", args...)
	return err
}

// IsForceRemovable reports whether a failed (non-force) worktree removal would succeed
// with a single --force — i.e. it was refused for a dirty working tree. git prints
// "use --force to delete it" in that case. A locked worktree prints "use 'remove
// -f -f'" instead, so it is correctly NOT treated as force-removable here.
func IsForceRemovable(err error) bool {
	return err != nil && strings.Contains(err.Error(), "use --force")
}

// DirtyFileCount counts the modified/untracked entries in a worktree (the files a
// force-remove would discard). Returns 0 on any error — it's only used for a warning.
func DirtyFileCount(ctx context.Context, path string) int {
	out, err := data.Run(ctx, path, "git", "-C", path, "status", "--porcelain")
	if err != nil {
		return 0
	}
	n := 0
	for _, line := range strings.Split(strings.TrimRight(string(out), "\n"), "\n") {
		if line != "" {
			n++
		}
	}
	return n
}

// DeleteBranch removes a local branch. force=false uses `-d` (git refuses a branch
// that still holds commits not merged into HEAD); force=true uses `-D`. Either way
// git refuses if the branch is checked out in the main repo or a worktree, so the
// caller need not pre-check that — but checking lets it give a clearer message.
func DeleteBranch(ctx context.Context, repo, name string, force bool) error {
	flag := "-d"
	if force {
		flag = "-D"
	}
	_, err := data.Run(ctx, repo, "git", "-C", repo, "branch", flag, name)
	return err
}

// FetchBranch updates only the selected branch's upstream remote-tracking ref.
// `--refmap=` disables git's opportunistic refresh of EVERY remote-tracking ref (the
// configured `refs/heads/*:refs/remotes/...` refspec) — that opportunistic pass is
// what trips over a single broken/un-lockable ref in the repo and fails the whole
// fetch. We instead name just this branch's tracking ref via an explicit refspec.
func FetchBranch(ctx context.Context, repo string, b model.Branch) error {
	remote, ref, err := splitUpstream(b.Upstream)
	if err != nil {
		return err
	}
	_, err = data.Run(ctx, repo, "git", "-C", repo, "fetch", "--refmap=", remote, trackRefspec(remote, ref))
	return err
}

// PullBranch fast-forwards a branch to its upstream. It refreshes the branch's
// tracking ref (refmap-scoped, so a broken ref elsewhere can't fail it) then
// fast-forwards: in place via `merge --ff-only` for a checked-out branch (you can't
// update a checked-out ref from outside it), or via an explicit ff-only refspec for
// one checked out nowhere. The ff-only step refuses a non-fast-forward, so a diverged
// branch errors rather than losing commits.
func PullBranch(ctx context.Context, repo string, b model.Branch, checkoutPath string) error {
	remote, ref, err := splitUpstream(b.Upstream)
	if err != nil {
		return err
	}
	if checkoutPath != "" {
		if _, err := data.Run(ctx, repo, "git", "-C", repo, "fetch", "--refmap=", remote, trackRefspec(remote, ref)); err != nil {
			return err
		}
		_, err := data.Run(ctx, checkoutPath, "git", "-C", checkoutPath, "merge", "--ff-only", "@{u}")
		return err
	}
	_, err = data.Run(ctx, repo, "git", "-C", repo, "fetch", "--refmap=", remote, trackRefspec(remote, ref), ref+":"+b.Name)
	return err
}

// trackRefspec force-updates exactly one remote-tracking ref.
func trackRefspec(remote, ref string) string {
	return "+" + ref + ":refs/remotes/" + remote + "/" + ref
}

func splitUpstream(upstream string) (remote, ref string, err error) {
	if upstream == "" {
		return "", "", errors.New("no upstream")
	}
	r, br, ok := strings.Cut(upstream, "/")
	if !ok || r == "" || br == "" {
		return "", "", fmt.Errorf("unexpected upstream %q", upstream)
	}
	return r, br, nil
}
