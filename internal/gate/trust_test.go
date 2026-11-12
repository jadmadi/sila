package gate

import "testing"

func TestEvaluateTrust(t *testing.T) {
	self := "Me@Example.com"
	commits := []TrustCommit{
		{SHA: "aaa111", Author: "Other", Email: "other@example.com", Paths: []string{"AGENTS.md"}},
		{SHA: "bbb222", Author: "Other", Email: "other@example.com", Paths: []string{"src/main.go"}},
		{SHA: "ccc333", Author: "Me", Email: "me@example.com", Paths: []string{".sila/config.json"}},
	}
	decision := EvaluateTrust(commits, self, nil, nil)
	if decision.Checked != 3 || len(decision.Refusals) != 1 {
		t.Fatalf("decision = %+v", decision)
	}
	if decision.Refusals[0].SHA != "aaa111" || len(decision.Refusals[0].Paths) != 1 {
		t.Fatalf("refusal = %+v", decision.Refusals[0])
	}
	trusted := EvaluateTrust(commits, self, map[string]bool{"aaa111": true}, nil)
	if !trusted.Passed() {
		t.Fatalf("trusted decision = %+v", trusted)
	}
}

func TestSensitiveGlobSemantics(t *testing.T) {
	cases := []struct {
		path    string
		pattern string
		want    bool
	}{
		{"scripts/build.sh", "scripts/**", true},
		{"scripts-old/build.sh", "scripts/**", false},
		{".github/workflows/ci.yml", ".github/workflows/**", true},
		{".github/workflows/nested/ci.yml", ".github/workflows/**", true},
		{"AGENTS.md", "AGENTS.md", true},
		{"docs/AGENTS.md", "AGENTS.md", false},
		{"../AGENTS.md", "AGENTS.md", false},
		{"go.mod", "go.mod", true},
	}
	for _, tc := range cases {
		if got := MatchSensitivePaths([]string{tc.path}, []string{tc.pattern}); (len(got) > 0) != tc.want {
			t.Fatalf("%q vs %q = %v, want %v", tc.path, tc.pattern, got, tc.want)
		}
	}
}

func TestEvaluateTrustNoSensitiveHits(t *testing.T) {
	decision := EvaluateTrust([]TrustCommit{{SHA: "x", Email: "other@example.com", Paths: []string{"README.md"}}}, "self@example.com", nil, nil)
	if !decision.Passed() || decision.Checked != 1 {
		t.Fatalf("decision = %+v", decision)
	}
}
