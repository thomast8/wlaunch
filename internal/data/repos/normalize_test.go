package repos

import "testing"

func TestNormalizeSlug(t *testing.T) {
	cases := map[string]string{
		"git@github.com:thomast8/wlaunch.git":                    "thomast8/wlaunch",
		"git@github.com:thomast8/wlaunch":                        "thomast8/wlaunch",
		"https://github.com/kyndryl-agentic-ai/PolicyAsCode.git": "kyndryl-agentic-ai/PolicyAsCode",
		"https://github.com/kyndryl-agentic-ai/PolicyAsCode":     "kyndryl-agentic-ai/PolicyAsCode",
		"ssh://git@github.com/owner/name.git":                    "owner/name",
		"https://user@dev.azure.com/org/proj/_git/name":          "org/proj/_git/name", // best-effort; non-github hosts
		"": "",
	}
	for in, want := range cases {
		if got := NormalizeSlug(in); got != want {
			t.Errorf("NormalizeSlug(%q) = %q, want %q", in, got, want)
		}
	}
}
