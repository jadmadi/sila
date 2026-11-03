package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type ccliGoldenCase struct {
	ID           string   `json:"id"`
	Surface      string   `json:"surface"`
	RelativeFile string   `json:"file"`
	Argv         []string `json:"argv"`
	ExpectedExit int      `json:"expected_exit"`
	StdoutClass  string   `json:"stdout_class"`
	StderrClass  string   `json:"stderr_class"`
	SHA256       string   `json:"sha256,omitempty"`
}

type ccliGoldenManifest struct {
	SchemaVersion string           `json:"schema_version"`
	Cases         []ccliGoldenCase `json:"cases"`
}

func ccliGoldenSpecList() []ccliGoldenCase {
	return []ccliGoldenCase{
		{
			ID:           "explain.save-memo",
			Surface:      "explain",
			RelativeFile: "explain/save-memo.txt",
			Argv:         []string{"ccli", "explain", "save", "memo", "use WAL"},
			ExpectedExit: 0,
			StdoutClass:  "data",
			StderrClass:  "none",
		},
		{
			ID:           "explain.canonical-block",
			Surface:      "explain",
			RelativeFile: "explain/canonical-block.txt",
			Argv:         []string{"ccli", "explain", "sync", "rules"},
			ExpectedExit: 0,
			StdoutClass:  "data",
			StderrClass:  "none",
		},
		{
			ID:           "do.save-memo",
			Surface:      "do",
			RelativeFile: "do/save-memo.txt",
			Argv:         []string{"do", "save memo", "use WAL"},
			ExpectedExit: 0,
			StdoutClass:  "data",
			StderrClass:  "none",
		},
		{
			ID:           "do.sync-rules",
			Surface:      "do",
			RelativeFile: "do/sync-rules.txt",
			Argv:         []string{"do", "sync rules", "--dry-run"},
			ExpectedExit: 0,
			StdoutClass:  "none",
			StderrClass:  "diagnostic",
		},
		{
			ID:           "safety.tier3-refusal",
			Surface:      "safety",
			RelativeFile: "safety/tier3-refusal.txt",
			Argv:         []string{"drop", "goal", "alpha-goal"},
			ExpectedExit: 3,
			StdoutClass:  "none",
			StderrClass:  "diagnostic",
		},
		{
			ID:           "safety.tier3-yes",
			Surface:      "safety",
			RelativeFile: "safety/tier3-yes.txt",
			Argv:         []string{"drop", "goal", "alpha-goal", "--yes"},
			ExpectedExit: 0,
			StdoutClass:  "data",
			StderrClass:  "none",
		},
		{
			ID:           "safety.dry-run",
			Surface:      "safety",
			RelativeFile: "safety/dry-run.txt",
			Argv:         []string{"save", "memo", "use WAL", "--dry-run"},
			ExpectedExit: 0,
			StdoutClass:  "none",
			StderrClass:  "diagnostic",
		},
		{
			ID:           "errors.unknown-phrase",
			Surface:      "errors",
			RelativeFile: "errors/unknown-phrase.txt",
			Argv:         []string{"do", "frobnicate quantum flux"},
			ExpectedExit: 2,
			StdoutClass:  "none",
			StderrClass:  "diagnostic",
		},
		{
			ID:           "errors.secret",
			Surface:      "errors",
			RelativeFile: "errors/secret.txt",
			Argv:         []string{"set", "config", "github_token", "to", "SYNTHETIC_SECRET_SENTINEL_99X"},
			ExpectedExit: 3,
			StdoutClass:  "none",
			StderrClass:  "diagnostic",
		},
		{
			ID:           "json.explain-save-memo",
			Surface:      "json",
			RelativeFile: "json/explain-save-memo.json",
			Argv:         []string{"ccli", "explain", "--json", "save", "memo", "use WAL"},
			ExpectedExit: 0,
			StdoutClass:  "data",
			StderrClass:  "none",
		},
	}
}

func formatCCLIGoldenCapture(c ccliGoldenCase, exitCode int, normOut, normErr string) string {
	if strings.HasSuffix(c.RelativeFile, ".json") {
		return normOut + "\n"
	}
	var b strings.Builder
	b.WriteString(fmt.Sprintf("# id: %s\n", c.ID))
	b.WriteString(fmt.Sprintf("# argv: sila %s\n", strings.Join(c.Argv, " ")))
	b.WriteString(fmt.Sprintf("# exit: %d\n", exitCode))
	b.WriteString("--- stdout ---\n")
	if normOut != "" {
		b.WriteString(normOut)
		b.WriteString("\n")
	}
	b.WriteString("--- stderr ---\n")
	if normErr != "" {
		b.WriteString(normErr)
		b.WriteString("\n")
	}
	return b.String()
}

func ccliUnifiedDiff(label, want, got string) string {
	wantLines := strings.Split(strings.TrimSuffix(want, "\n"), "\n")
	gotLines := strings.Split(strings.TrimSuffix(got, "\n"), "\n")
	var b strings.Builder
	b.WriteString(fmt.Sprintf("--- a/%s\n+++ b/%s\n", label, label))
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
			b.WriteString(" " + w + "\n")
			continue
		}
		if hasW {
			b.WriteString("-" + w + "\n")
		}
		if hasG {
			b.WriteString("+" + g + "\n")
		}
	}
	return b.String()
}

func TestCCLIGolden(t *testing.T) {
	repoRoot, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	ccliGoldenDir := filepath.Join(repoRoot, "testdata", "ccli")

	sb := newCCLISandbox(t)
	// Pre-claim alpha-goal so both safety/tier3-refusal and safety/tier3-yes operate against a claimed goal
	if code, _, stderr := sb.run("claim", "goal", "alpha-goal"); code != 0 {
		t.Fatalf("pre-claim alpha-goal failed (%d): %s", code, stderr)
	}

	specs := ccliGoldenSpecList()
	manifest := ccliGoldenManifest{
		SchemaVersion: "sila/ccli-goldens/v1",
		Cases:         make([]ccliGoldenCase, 0, len(specs)),
	}

	for _, sc := range specs {
		code, rawOut, rawErr := sb.run(sc.Argv...)
		if code != sc.ExpectedExit {
			t.Fatalf("case %s (argv=%v): exit = %d, want %d\nstdout:\n%s\nstderr:\n%s",
				sc.ID, sc.Argv, code, sc.ExpectedExit, rawOut, rawErr)
		}
		normOut := normalizeCCLISandboxOutput(rawOut, sb)
		normErr := normalizeCCLISandboxOutput(rawErr, sb)

		rendered := formatCCLIGoldenCapture(sc, code, normOut, normErr)
		targetPath := filepath.Join(ccliGoldenDir, sc.RelativeFile)

		if *updateOutputGoldens {
			if err := os.MkdirAll(filepath.Dir(targetPath), 0o755); err != nil {
				t.Fatalf("mkdir %s: %v", filepath.Dir(targetPath), err)
			}
			if err := os.WriteFile(targetPath, []byte(rendered), 0o644); err != nil {
				t.Fatalf("write golden %s: %v", targetPath, err)
			}
		}

		diskBytes, err := os.ReadFile(targetPath)
		if err != nil {
			t.Fatalf("read golden %s: %v (run `go test -run TestCCLIGolden -update ./cmd/sila`)", targetPath, err)
		}
		want := string(diskBytes)
		if want != rendered {
			t.Errorf("golden mismatch for %s:\n%s", sc.RelativeFile, ccliUnifiedDiff(sc.RelativeFile, want, rendered))
		}

		sum := sha256.Sum256([]byte(rendered))
		scWithHash := sc
		scWithHash.SHA256 = hex.EncodeToString(sum[:])
		manifest.Cases = append(manifest.Cases, scWithHash)
	}

	manifestPath := filepath.Join(ccliGoldenDir, "manifest.json")
	manifestBytes, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		t.Fatalf("marshal manifest: %v", err)
	}
	manifestBytes = append(manifestBytes, '\n')

	if *updateOutputGoldens {
		if err := os.WriteFile(manifestPath, manifestBytes, 0o644); err != nil {
			t.Fatalf("write manifest.json: %v", err)
		}
		readmePath := filepath.Join(ccliGoldenDir, "README.md")
		readmeContent := `# CCLI Golden Snapshots

This directory contains deterministic golden snapshots for Sila's Conversational CLI (CCLI)
surfaces:

- ` + "`explain/`" + `: human explain traces for routed and canonical-blocked phrases.
- ` + "`do/`" + `: explicit ` + "`sila do`" + ` execution and dry-run previews.
- ` + "`safety/`" + `: Tier 3 non-TTY refusal, ` + "`--yes`" + ` bypass, and Tier 2 ` + "`--dry-run`" + `.
- ` + "`errors/`" + `: unknown conversational phrase suggestions and secret refusal.
- ` + "`json/`" + `: machine-readable JSON explain envelope.

To regenerate after an intentional output change:

` + "```bash\ncd cli\ngo test -run TestCCLIGolden -update ./cmd/sila\n```\n"
		if err := os.WriteFile(readmePath, []byte(readmeContent), 0o644); err != nil {
			t.Fatalf("write README.md: %v", err)
		}
	}

	diskManifest, err := os.ReadFile(manifestPath)
	if err != nil {
		t.Fatalf("read %s: %v", manifestPath, err)
	}
	if string(diskManifest) != string(manifestBytes) {
		t.Errorf("manifest.json mismatch:\n%s", ccliUnifiedDiff("testdata/ccli/manifest.json", string(diskManifest), string(manifestBytes)))
	}
}
