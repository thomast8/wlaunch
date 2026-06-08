// Package git backs the branches and worktrees views by parsing for-each-ref /
// porcelain output. The parsers are pure and unit-tested against fixtures.
package git

import (
	"bufio"
	"bytes"
	"context"
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
