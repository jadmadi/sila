package gate

import (
	"encoding/json"
	"os"
	"testing"
)

type trustFixture struct {
	Cases []struct {
		Name      string        `json:"name"`
		SelfEmail string        `json:"self_email"`
		Commits   []TrustCommit `json:"commits"`
		Refused   bool          `json:"refused"`
	} `json:"cases"`
}

func TestTrustFixtures(t *testing.T) {
	data, err := os.ReadFile("testdata/trust/cases.json")
	if err != nil {
		t.Fatal(err)
	}
	var fixture trustFixture
	if err := json.Unmarshal(data, &fixture); err != nil {
		t.Fatal(err)
	}
	if len(fixture.Cases) < 3 {
		t.Fatalf("expected at least 3 cases, got %d", len(fixture.Cases))
	}
	for _, tc := range fixture.Cases {
		decision := EvaluateTrust(tc.Commits, tc.SelfEmail, nil, nil)
		if decision.Passed() == tc.Refused {
			t.Fatalf("%s: refused=%v decision=%+v", tc.Name, tc.Refused, decision)
		}
	}
}
