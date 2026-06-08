package gh

import "testing"

func TestParsePRs(t *testing.T) {
	in := []byte(`[
	  {"number":289,"title":"fix: reasoning effort","headRefName":"fix/re","author":{"login":"tho"}},
	  {"number":232,"title":"feat: per-stage","headRefName":"feat/p","author":{"login":"moh"}}
	]`)
	prs, err := parsePRs(in)
	if err != nil {
		t.Fatalf("parsePRs: %v", err)
	}
	if len(prs) != 2 {
		t.Fatalf("len = %d, want 2", len(prs))
	}
	if prs[0].Number != 289 || prs[0].HeadRefName != "fix/re" || prs[0].Author != "tho" {
		t.Errorf("pr0 = %+v", prs[0])
	}
	if prs[1].Author != "moh" {
		t.Errorf("pr1.Author = %q, want moh", prs[1].Author)
	}
}

func TestParsePRsEmpty(t *testing.T) {
	prs, err := parsePRs([]byte(`[]`))
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if len(prs) != 0 {
		t.Fatalf("want empty, got %d", len(prs))
	}
}

func TestParsePRsMalformed(t *testing.T) {
	if _, err := parsePRs([]byte(`not json`)); err == nil {
		t.Fatal("expected error on malformed JSON")
	}
}
