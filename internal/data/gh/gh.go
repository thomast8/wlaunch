// Package gh queries GitHub via the gh CLI for the PRs view.
package gh

import (
	"context"
	"encoding/json"

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

// ListPRs runs the same gh invocation the existing pickers use, scoped to repo.
func ListPRs(ctx context.Context, repo string) ([]model.PR, error) {
	b, err := data.Run(ctx, repo, "gh", "pr", "list",
		"--limit", "50",
		"--json", "number,title,headRefName,author")
	if err != nil {
		return nil, err
	}
	return parsePRs(b)
}
