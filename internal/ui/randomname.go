package ui

import (
	"fmt"
	"math/rand/v2"
)

// Word lists for the random branch/worktree name suggested by the new-worktree
// prompt. Kept short, lowercase, and slug-safe ([a-z]) so the result is always a
// valid git branch name and reads cleanly in `git worktree list`. The pool is
// adjectives × nouns combinations — large enough that a collision is rare, and
// randomNameAvoiding handles the rare case.
var (
	randomAdjectives = []string{
		"amber", "brave", "calm", "clever", "cosmic", "crisp", "dapper",
		"eager", "fuzzy", "gentle", "glossy", "jolly", "keen", "lively",
		"lucky", "mellow", "nimble", "plucky", "quiet", "rapid", "sleek",
		"snappy", "spry", "sunny", "swift", "tidy", "vivid", "witty",
		"zesty", "zippy",
	}
	randomNouns = []string{
		"otter", "marmot", "falcon", "badger", "heron", "lynx", "magpie",
		"newt", "panda", "quokka", "raven", "shrew", "tapir", "vole",
		"walrus", "wombat", "yak", "beaver", "civet", "dingo", "egret",
		"ferret", "gecko", "hare", "ibex", "jackal", "kestrel", "lemur",
		"meerkat", "narwhal",
	}
)

// randomName returns a fresh "adjective-noun" slug, e.g. "fuzzy-marmot".
// math/rand/v2 is auto-seeded, so successive calls differ without manual seeding.
func randomName() string {
	return randomAdjectives[rand.IntN(len(randomAdjectives))] + "-" +
		randomNouns[rand.IntN(len(randomNouns))]
}

// randomNameAvoiding returns a random name that is not a key of taken. It retries
// a handful of times, then falls back to appending a short numeric suffix so it
// always terminates with a usable, collision-free name (taken holds the target
// repo's existing branch and worktree names).
func randomNameAvoiding(taken map[string]bool) string {
	for i := 0; i < 16; i++ {
		if n := randomName(); !taken[n] {
			return n
		}
	}
	base := randomName()
	for i := 2; ; i++ {
		n := fmt.Sprintf("%s-%d", base, i)
		if !taken[n] {
			return n
		}
	}
}
