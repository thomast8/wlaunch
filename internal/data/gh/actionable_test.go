package gh

import "testing"

func TestParsePRsFull(t *testing.T) {
	in := []byte(`[
	  {"number":221,"title":"docs reorg","headRefName":"docs/x","author":{"login":"me"},
	   "isDraft":false,"reviewDecision":"CHANGES_REQUESTED","mergeable":"MERGEABLE",
	   "updatedAt":"2026-06-08T09:46:40Z","reviewRequests":[{"login":"rev"}],
	   "reviews":[{"author":{"login":"me"},"state":"CHANGES_REQUESTED","submittedAt":"2026-06-01T09:00:00Z"}],
	   "comments":[
	     {"author":{"login":"me"},"createdAt":"2026-06-02T09:00:00Z"},
	     {"author":{"login":"me"},"createdAt":"2026-06-03T09:00:00Z"}
	   ],
	   "commits":[{"authoredDate":"2026-06-04T09:00:00Z","committedDate":"2026-06-04T09:30:00Z"}],
	   "statusCheckRollup":[{"conclusion":"SUCCESS"},{"conclusion":"NEUTRAL"}]},
	  {"number":300,"title":"publisher","headRefName":"opa/2","author":{"login":"me"},
	   "isDraft":false,"reviewDecision":"","mergeable":"CONFLICTING","updatedAt":"2026-06-22T17:31:41Z",
	   "reviewRequests":[],"statusCheckRollup":[{"conclusion":"FAILURE"}]}
	]`)
	prs, err := parsePRsFull(in)
	if err != nil {
		t.Fatalf("parsePRsFull: %v", err)
	}
	if len(prs) != 2 {
		t.Fatalf("len = %d, want 2", len(prs))
	}
	if prs[0].ReviewDecision != "CHANGES_REQUESTED" || prs[0].Mergeable != "MERGEABLE" {
		t.Errorf("pr0 = %+v", prs[0])
	}
	if prs[0].ciFailing() {
		t.Error("pr0 should not be CI-failing (SUCCESS+NEUTRAL)")
	}
	if !prs[1].ciFailing() {
		t.Error("pr1 should be CI-failing (FAILURE)")
	}
	if !prs[0].requestsReviewer("rev") || prs[0].requestsReviewer("nobody") {
		t.Error("reviewer matching wrong for pr0")
	}
	if len(prs[1].ReviewRequests) != 0 {
		t.Error("pr1 should have no reviewers")
	}
	turns, err := parseTurnInfo([]byte(`{"data":{"repository":{"pullRequests":{"nodes":[
	  {"number":221,"author":{"login":"charles"},
	   "reviews":{"nodes":[{"author":{"login":"me"},"state":"CHANGES_REQUESTED","submittedAt":"2026-06-01T09:00:00Z"}]},
	   "comments":{"nodes":[
	     {"author":{"login":"me"},"createdAt":"2026-06-03T09:00:00Z"},
	     {"author":{"login":"charles"},"createdAt":"2026-06-04T09:00:00Z"}
	   ]},
	   "commits":{"nodes":[{"commit":{"authoredDate":"2026-06-05T09:00:00Z","committedDate":"2026-06-05T09:30:00Z"}}]}}
	]}}}}`), "me")
	if err != nil {
		t.Fatalf("parseTurnInfo: %v", err)
	}
	raw := prs[0].toRaw("repo", "/repo", nil, turns[221])
	if raw.LastMyReviewState != "CHANGES_REQUESTED" {
		t.Errorf("LastMyReviewState = %q, want CHANGES_REQUESTED", raw.LastMyReviewState)
	}
	if raw.LastMyReviewAt.Format("2006-01-02T15:04:05Z") != "2026-06-01T09:00:00Z" {
		t.Errorf("LastMyReviewAt = %s", raw.LastMyReviewAt)
	}
	if raw.LastMyTurnAt.Format("2006-01-02T15:04:05Z") != "2026-06-03T09:00:00Z" {
		t.Errorf("LastMyTurnAt = %s", raw.LastMyTurnAt)
	}
	if raw.LastAuthorTurnAt.Format("2006-01-02T15:04:05Z") != "2026-06-04T09:00:00Z" {
		t.Errorf("LastAuthorTurnAt = %s", raw.LastAuthorTurnAt)
	}
	if raw.LastAuthorCommitAt.Format("2006-01-02T15:04:05Z") != "2026-06-05T09:30:00Z" {
		t.Errorf("LastAuthorCommitAt = %s", raw.LastAuthorCommitAt)
	}
}

func TestCIFailingStatusContext(t *testing.T) {
	// Legacy StatusContext checks report via .state, not .conclusion.
	p := ghPRFull{Checks: []ghCheck{{State: "ERROR"}}}
	if !p.ciFailing() {
		t.Error("state=ERROR should be CI-failing")
	}
	p = ghPRFull{Checks: []ghCheck{{State: "PENDING"}, {Conclusion: "SKIPPED"}}}
	if p.ciFailing() {
		t.Error("PENDING/SKIPPED should not be CI-failing")
	}
}

func TestParseSearchPRs(t *testing.T) {
	in := []byte(`[
	  {"number":7,"title":"x","repository":{"name":"PolicyAsCode","nameWithOwner":"kyndryl-agentic-ai/PolicyAsCode"},
	   "author":{"login":"me"},"isDraft":true,"reviewDecision":"REVIEW_REQUIRED","updatedAt":"2026-06-01T00:00:00Z"}
	]`)
	prs, err := parseSearchPRs(in)
	if err != nil {
		t.Fatalf("parseSearchPRs: %v", err)
	}
	if len(prs) != 1 || prs[0].Repository.NameWithOwner != "kyndryl-agentic-ai/PolicyAsCode" {
		t.Fatalf("got %+v", prs)
	}
	if !prs[0].IsDraft {
		t.Error("expected draft")
	}
}

func TestParseThreads(t *testing.T) {
	in := []byte(`{"data":{"repository":{"pullRequests":{"nodes":[
	  {"number":299,"reviewThreads":{"nodes":[{"isResolved":false},{"isResolved":false},{"isResolved":true}]}},
	  {"number":323,"reviewThreads":{"nodes":[]}}
	]}}}}`)
	m, err := parseThreads(in)
	if err != nil {
		t.Fatalf("parseThreads: %v", err)
	}
	if m[299] != 2 {
		t.Errorf("PR299 unresolved = %d, want 2", m[299])
	}
	if got, ok := m[323]; !ok || got != 0 {
		t.Errorf("PR323 = %d (ok=%v), want known 0", got, ok)
	}
}

func TestSplitSlug(t *testing.T) {
	owner, name, ok := splitSlug("kyndryl-agentic-ai/PolicyAsCode")
	if !ok || owner != "kyndryl-agentic-ai" || name != "PolicyAsCode" {
		t.Fatalf("got %q %q %v", owner, name, ok)
	}
	if _, _, ok := splitSlug("noslash"); ok {
		t.Error("expected !ok for slug without slash")
	}
}
