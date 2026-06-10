// Package gh queries GitHub via the gh CLI for the PRs view.
package gh

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/thomast8/wlaunch/internal/data"
	"github.com/thomast8/wlaunch/internal/model"
)

type ghAuthor struct {
	Login string `json:"login"`
}

type ghPR struct {
	Number      int      `json:"number"`
	Title       string   `json:"title"`
	HeadRefName string   `json:"headRefName"`
	Author      ghAuthor `json:"author"`
}

// parsePRs converts the `gh pr list --json` payload into domain PRs. Pure, so it
// is unit-tested directly against fixture JSON.
func parsePRs(b []byte) ([]model.PR, error) {
	var raw []ghPR
	if err := json.Unmarshal(b, &raw); err != nil {
		return nil, err
	}
	out := make([]model.PR, 0, len(raw))
	for _, p := range raw {
		out = append(out, model.PR{
			Number:      p.Number,
			Title:       p.Title,
			HeadRefName: p.HeadRefName,
			Author:      p.Author.Login,
		})
	}
	return out, nil
}

// accountFor returns the gh account the repo's git config pins via
// wlaunch.ghaccount, or "" when unset. The key rides git's includeIf routing
// (e.g. hasconfig:remote.*.url matching a work org), so each repo resolves to
// the right account no matter which one is gh's active account. `git config
// --get` exits 1 for an unset key; that and any real failure both mean "no
// pin", which falls back to gh's active-account behavior.
func accountFor(ctx context.Context, repo string) string {
	b, err := data.Run(ctx, repo, "git", "config", "--get", "wlaunch.ghaccount")
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(b))
}

// ghEnv resolves the env overlay for gh calls in repo: when wlaunch.ghaccount
// is set, GH_TOKEN is pinned to that account's token. A configured account
// whose token can't be resolved is an error, not a silent fallback — the
// active account would likely 404 on the repo with a misleading message.
func ghEnv(ctx context.Context, repo string) ([]string, error) {
	account := accountFor(ctx, repo)
	if account == "" {
		return nil, nil
	}
	b, err := data.Run(ctx, "", "gh", "auth", "token", "-u", account)
	if err != nil {
		return nil, fmt.Errorf("token for gh account %q (wlaunch.ghaccount): %w", account, err)
	}
	token := strings.TrimSpace(string(b))
	if token == "" {
		return nil, fmt.Errorf("gh auth token -u %s returned an empty token", account)
	}
	return []string{"GH_TOKEN=" + token}, nil
}

// ListPRs runs the same gh invocation the existing pickers use, scoped to repo.
func ListPRs(ctx context.Context, repo string) ([]model.PR, error) {
	env, err := ghEnv(ctx, repo)
	if err != nil {
		return nil, err
	}
	b, err := data.RunEnv(ctx, repo, env, "gh", "pr", "list",
		"--limit", "50",
		"--json", "number,title,headRefName,author")
	if err != nil {
		return nil, err
	}
	return parsePRs(b)
}
