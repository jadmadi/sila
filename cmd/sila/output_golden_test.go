package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

var updateOutputGoldens = flag.Bool("update", false, "regenerate golden snapshots in testdata/output")

type goldenManifestFile struct {
	Schema  string `json:"schema"`
	Goldens []struct {
		Label        string `json:"label"`
		Group        string `json:"group"`
		Command      string `json:"command"`
		Argv         string `json:"argv"`
		Mode         string `json:"mode"`
		ExpectedExit int    `json:"expected_exit"`
		GoldenFile   string `json:"golden_file"`
	} `json:"goldens"`
}

func TestOutputGoldenContracts(t *testing.T) {
	repoRoot, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	goldenRoot := filepath.Join(repoRoot, "testdata", "output")

	// Verify canonical CommandSpec inventory from help system is 100% covered
	var helpBuf strings.Builder
	renderUsage(&helpBuf, true)
	specs := allCommandSpecs()
	if len(specs) == 0 {
		t.Fatalf("expected non-empty CommandSpec inventory")
	}

	if *updateOutputGoldens {
		cmd := exec.Command("go", "run", "./tools/outputaudit", "--golden-dir="+goldenRoot, "--update")
		cmd.Dir = repoRoot
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("outputaudit --update failed: %v\n%s", err, string(out))
		}
	}

	// Run the harness into a temporary directory and compare every golden file
	tmpOut := t.TempDir()
	tmpGoldens := filepath.Join(tmpOut, "goldens")
	cmd := exec.Command("go", "run", "./tools/outputaudit", "--out="+filepath.Join(tmpOut, "audit"), "--golden-dir="+tmpGoldens, "--update")
	cmd.Dir = repoRoot
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("outputaudit execution failed: %v\n%s", err, string(out))
	}

	manifestBytes, err := os.ReadFile(filepath.Join(goldenRoot, "manifest.json"))
	if err != nil {
		t.Fatalf("read testdata/output/manifest.json: %v", err)
	}
	var gm goldenManifestFile
	if err := json.Unmarshal(manifestBytes, &gm); err != nil {
		t.Fatalf("unmarshal manifest.json: %v", err)
	}
	if len(gm.Goldens) < 25 {
		t.Fatalf("expected at least 25 golden surfaces, got %d", len(gm.Goldens))
	}

	// Check every canonical command from allCommandSpecs() is covered by the audit manifest
	auditManifestBytes, err := os.ReadFile(filepath.Join(tmpOut, "audit", "manifest.json"))
	if err != nil {
		t.Fatalf("read generated audit manifest: %v", err)
	}
	var auditManifest struct {
		Coverage struct {
			CoveredCommands []string `json:"covered_commands"`
			MissingCommands []string `json:"missing_commands"`
		} `json:"coverage"`
	}
	if err := json.Unmarshal(auditManifestBytes, &auditManifest); err != nil {
		t.Fatalf("unmarshal audit manifest: %v", err)
	}
	if len(auditManifest.Coverage.MissingCommands) > 0 {
		t.Fatalf("audit manifest is missing canonical commands: %v", auditManifest.Coverage.MissingCommands)
	}
	coveredSet := make(map[string]bool, len(auditManifest.Coverage.CoveredCommands))
	for _, c := range auditManifest.Coverage.CoveredCommands {
		coveredSet[c] = true
	}
	for _, spec := range specs {
		if !coveredSet[spec.Name] {
			t.Errorf("canonical CommandSpec %q missing from outputaudit coverage", spec.Name)
		}
	}

	// Byte-for-byte comparison with unified diff on mismatch
	for _, entry := range gm.Goldens {
		wantPath := filepath.Join(goldenRoot, filepath.FromSlash(entry.GoldenFile))
		gotPath := filepath.Join(tmpGoldens, filepath.FromSlash(entry.GoldenFile))

		wantBytes, err := os.ReadFile(wantPath)
		if err != nil {
			t.Errorf("missing committed golden %s: %v", entry.GoldenFile, err)
			continue
		}
		gotBytes, err := os.ReadFile(gotPath)
		if err != nil {
			t.Errorf("missing generated golden %s: %v", entry.GoldenFile, err)
			continue
		}
		if string(wantBytes) != string(gotBytes) {
			t.Errorf("golden drift detected for %s (%s):\n%s",
				entry.Label, entry.GoldenFile, unifiedLineDiff(entry.GoldenFile, string(wantBytes), string(gotBytes)))
		}
	}
}

func unifiedLineDiff(name, want, got string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "--- a/%s\n+++ b/%s\n", name, name)
	wantLines := strings.Split(want, "\n")
	gotLines := strings.Split(got, "\n")
	maxLen := len(wantLines)
	if len(gotLines) > maxLen {
		maxLen = len(gotLines)
	}
	for i := 0; i < maxLen; i++ {
		var w, g string
		hasW := i < len(wantLines)
		hasG := i < len(gotLines)
		if hasW {
			w = wantLines[i]
		}
		if hasG {
			g = gotLines[i]
		}
		if hasW && hasG && w == g {
			continue
		}
		if hasW {
			fmt.Fprintf(&b, "-L%d: %s\n", i+1, w)
		}
		if hasG {
			fmt.Fprintf(&b, "+L%d: %s\n", i+1, g)
		}
	}
	return b.String()
}
