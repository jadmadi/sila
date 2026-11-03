package gate

import (
	"os"
	"path/filepath"
	"testing"
)

func TestCheckLineForPhysicalCSS(t *testing.T) {
	tests := []struct {
		name          string
		line          string
		wantViolation bool
		wantPhysical  string
		wantSuggested string
	}{
		{
			name:          "Tailwind pl-4 flagged",
			line:          `<div className="flex pl-4 items-center">`,
			wantViolation: true,
			wantPhysical:  "pl-4",
			wantSuggested: "ps-4",
		},
		{
			name:          "Tailwind ps-4 passes",
			line:          `<div className="flex ps-4 items-center">`,
			wantViolation: false,
		},
		{
			name:          "Tailwind mr-auto flagged",
			line:          `<span class="mr-auto text-sm">`,
			wantViolation: true,
			wantPhysical:  "mr-auto",
			wantSuggested: "me-auto",
		},
		{
			name:          "Tailwind me-auto passes",
			line:          `<span class="me-auto text-sm">`,
			wantViolation: false,
		},
		{
			name:          "Tailwind text-left flagged",
			line:          `<p className="text-left font-bold">`,
			wantViolation: true,
			wantPhysical:  "text-left",
			wantSuggested: "text-start",
		},
		{
			name:          "Tailwind text-start passes",
			line:          `<p className="text-start font-bold">`,
			wantViolation: false,
		},
		{
			name:          "Tailwind left-0 flagged",
			line:          `<div className="absolute left-0 top-0">`,
			wantViolation: true,
			wantPhysical:  "left-0",
			wantSuggested: "start-0",
		},
		{
			name:          "Tailwind start-0 passes",
			line:          `<div className="absolute start-0 top-0">`,
			wantViolation: false,
		},
		{
			name:          "Raw CSS padding-left flagged",
			line:          `padding-left: 16px;`,
			wantViolation: true,
			wantPhysical:  "padding-left",
			wantSuggested: "padding-inline-start",
		},
		{
			name:          "Raw CSS padding-inline-start passes",
			line:          `padding-inline-start: 16px;`,
			wantViolation: false,
		},
		{
			name:          "Comment line skipped",
			line:          `// Note: previously used pl-4, now using ps-4`,
			wantViolation: false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			v := CheckLineForPhysicalCSS("test.tsx", 10, tc.line)
			if tc.wantViolation {
				if len(v) == 0 {
					t.Fatalf("expected violation for %q, got none", tc.line)
				}
				if v[0].Physical != tc.wantPhysical {
					t.Errorf("expected physical %q, got %q", tc.wantPhysical, v[0].Physical)
				}
				if v[0].Suggested != tc.wantSuggested {
					t.Errorf("expected suggested %q, got %q", tc.wantSuggested, v[0].Suggested)
				}
			} else {
				if len(v) > 0 {
					t.Fatalf("expected no violation for %q, got %v", tc.line, v)
				}
			}
		})
	}
}

func TestScanRTLDiff(t *testing.T) {
	diff := `diff --git a/components/Button.tsx b/components/Button.tsx
index 1111111..2222222 100644
--- a/components/Button.tsx
+++ b/components/Button.tsx
@@ -10,3 +10,4 @@
  export function Button() {
+   return <button className="px-4 pl-2 text-right">Click</button>;
  }
diff --git a/main.go b/main.go
index 3333333..4444444 100644
--- a/main.go
+++ b/main.go
@@ -5,2 +5,3 @@
+// should not be scanned even with pl-4
+var pl = 4
`
	violations := ScanRTLDiff(diff)
	if len(violations) != 2 {
		t.Fatalf("expected 2 violations in Button.tsx, got %d", len(violations))
	}

	if violations[0].File != "components/Button.tsx" {
		t.Errorf("expected file components/Button.tsx, got %s", violations[0].File)
	}
	if violations[0].Line != 11 {
		t.Errorf("expected line 11, got %d", violations[0].Line)
	}
	if violations[0].Physical != "pl-2" {
		t.Errorf("expected physical pl-2, got %s", violations[0].Physical)
	}
	if violations[1].Physical != "text-right" {
		t.Errorf("expected physical text-right, got %s", violations[1].Physical)
	}
}

func TestIsRTLProjectEnv(t *testing.T) {
	tempDir := t.TempDir()

	// Default false
	os.Unsetenv("SILA_CHECK_RTL")
	os.Unsetenv("SILA_SKIP_RTL")
	if IsRTLProject(tempDir, nil) {
		t.Errorf("expected false for empty directory")
	}

	// Env override
	os.Setenv("SILA_CHECK_RTL", "1")
	defer os.Unsetenv("SILA_CHECK_RTL")
	if !IsRTLProject(tempDir, nil) {
		t.Errorf("expected true when SILA_CHECK_RTL=1")
	}

	// AGENTS.md invariant
	os.Unsetenv("SILA_CHECK_RTL")
	agentsFile := filepath.Join(tempDir, "AGENTS.md")
	os.WriteFile(agentsFile, []byte("# Rules\nThis project enforces an RTL invariant with Arabic support dir=\"rtl\""), 0644)
	if !IsRTLProject(tempDir, nil) {
		t.Errorf("expected true when AGENTS.md specifies RTL invariant")
	}
}
