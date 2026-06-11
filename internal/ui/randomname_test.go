package ui

import (
	"regexp"
	"testing"
)

var slugRe = regexp.MustCompile(`^[a-z]+-[a-z]+$`)

func TestRandomNameIsSlug(t *testing.T) {
	for i := 0; i < 100; i++ {
		n := randomName()
		if !slugRe.MatchString(n) {
			t.Fatalf("randomName() = %q, want adjective-noun slug", n)
		}
	}
}

func TestRandomNameAvoidingDodgesTaken(t *testing.T) {
	// Seed taken with every plain adjective-noun combination, forcing the suffix
	// fallback path, and confirm the result is genuinely unused.
	taken := map[string]bool{}
	for _, a := range randomAdjectives {
		for _, n := range randomNouns {
			taken[a+"-"+n] = true
		}
	}
	got := randomNameAvoiding(taken)
	if taken[got] {
		t.Fatalf("randomNameAvoiding returned a taken name: %q", got)
	}
	if !regexp.MustCompile(`^[a-z]+-[a-z]+-\d+$`).MatchString(got) {
		t.Fatalf("expected a numeric-suffixed fallback, got %q", got)
	}
}
