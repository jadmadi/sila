package store

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type idfPair struct {
	Name       string   `json:"name"`
	Kind       string   `json:"kind"`
	SeedKind   string   `json:"seed_kind"`
	SeedClaims []string `json:"seed_claims"`
	ClaimA     string   `json:"claim_a"`
	ClaimB     string   `json:"claim_b"`
	Expect     string   `json:"expect"`
}

func loadIDFPair(t *testing.T, name string) idfPair {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", "idf", name))
	if err != nil {
		t.Fatal(err)
	}
	var pair idfPair
	if err := json.Unmarshal(data, &pair); err != nil {
		t.Fatal(err)
	}
	return pair
}

func TestIDFVersionFixture(t *testing.T) {
	pair := loadIDFPair(t, "version_pair.json")
	st := guardStore(t)
	if err := st.UpsertFact(Fact{ID: "v1", ProjectID: "fx", Claim: pair.ClaimA, Kind: pair.Kind, FirstSeen: "2026-11-06T10:00:00Z", ReviewStatus: "approved"}); err != nil {
		t.Fatal(err)
	}
	if err := st.UpsertFact(Fact{ID: "v2", ProjectID: "fx", Claim: pair.ClaimB, Kind: pair.Kind, FirstSeen: "2026-11-06T11:00:00Z", ReviewStatus: "approved"}); err != nil {
		t.Fatal(err)
	}
	var count int
	if err := st.DB.QueryRow(`SELECT COUNT(*) FROM facts WHERE project_id = 'fx' AND kind = ?`, pair.Kind).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if pair.Expect == "veto" && count != 2 {
		t.Fatalf("version pair merged: %d rows", count)
	}
}

func TestIDFBoilerplateFixture(t *testing.T) {
	pair := loadIDFPair(t, "boilerplate_pair.json")
	st := guardStore(t)
	for i, claim := range pair.SeedClaims {
		if err := st.UpsertFact(Fact{ProjectID: "fx", Claim: claim, Kind: pair.SeedKind, FirstSeen: "2026-11-06T09:00:00Z", ReviewStatus: "approved"}); err != nil {
			t.Fatalf("seed %d: %v", i, err)
		}
	}
	if err := st.UpsertFact(Fact{ProjectID: "fx", Claim: pair.ClaimA, Kind: pair.Kind, FirstSeen: "2026-11-06T10:00:00Z", ReviewStatus: "approved", RepoFiles: []string{"a.go"}}); err != nil {
		t.Fatal(err)
	}
	if err := st.UpsertFact(Fact{ProjectID: "fx", Claim: pair.ClaimB, Kind: pair.Kind, FirstSeen: "2026-11-06T11:00:00Z", ReviewStatus: "approved", RepoFiles: []string{"b.go"}}); err != nil {
		t.Fatal(err)
	}
	var count int
	if err := st.DB.QueryRow(`SELECT COUNT(*) FROM facts WHERE project_id = 'fx' AND kind = ?`, pair.Kind).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if pair.Expect == "merge" && count != 1 {
		t.Fatalf("boilerplate pair not merged: %d rows", count)
	}
	var repos string
	if err := st.DB.QueryRow(`SELECT repo_files FROM facts WHERE project_id = 'fx' AND kind = ?`, pair.Kind).Scan(&repos); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(repos, "b.go") {
		t.Fatalf("merged evidence missing: %s", repos)
	}
}
