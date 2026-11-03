package skills

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
)

// TestChallenger_GenerateLCSUnifiedDiff_Adversarial probes GenerateLCSUnifiedDiff
// with exhaustive edge cases, hostile boundary conditions, and stress inputs.
func TestChallenger_GenerateLCSUnifiedDiff_Adversarial(t *testing.T) {
	t.Run("both_empty_strings", func(t *testing.T) {
		diff := GenerateLCSUnifiedDiff("file.txt", "file.txt", "", "", 3)
		if diff != "" {
			t.Errorf("expected empty diff for both empty strings, got: %q", diff)
		}
	})

	t.Run("empty_old_file_with_newlines_in_new", func(t *testing.T) {
		diff := GenerateLCSUnifiedDiff("old.txt", "new.txt", "", "alpha\nbeta\ngamma\n", 3)
		expectedHeader := "--- /dev/null\n+++ new.txt\n@@ -0,0 +1,3 @@\n+alpha\n+beta\n+gamma\n"
		if diff != expectedHeader {
			t.Errorf("expected exact creation diff:\n%q\ngot:\n%q", expectedHeader, diff)
		}
	})

	t.Run("empty_new_file_with_newlines_in_old", func(t *testing.T) {
		diff := GenerateLCSUnifiedDiff("old.txt", "new.txt", "alpha\nbeta\ngamma\n", "", 3)
		expectedHeader := "--- old.txt\n+++ /dev/null\n@@ -1,3 +0,0 @@\n-alpha\n-beta\n-gamma\n"
		if diff != expectedHeader {
			t.Errorf("expected exact deletion diff:\n%q\ngot:\n%q", expectedHeader, diff)
		}
	})

	t.Run("identical_single_line_no_newline", func(t *testing.T) {
		diff := GenerateLCSUnifiedDiff("a.txt", "b.txt", "same content", "same content", 3)
		if diff != "" {
			t.Errorf("expected empty diff for identical single line, got: %q", diff)
		}
	})

	t.Run("completely_disjoint_lines", func(t *testing.T) {
		oldContent := "red\ngreen\nblue\n"
		newContent := "cyan\nmagenta\nyellow\nblack\n"
		diff := GenerateLCSUnifiedDiff("colors_old.txt", "colors_new.txt", oldContent, newContent, 3)

		if !strings.Contains(diff, "--- colors_old.txt\n+++ colors_new.txt\n") {
			t.Errorf("missing diff file headers, got:\n%s", diff)
		}
		if !strings.Contains(diff, "@@ -1,3 +1,4 @@") {
			t.Errorf("expected hunk header @@ -1,3 +1,4 @@, got:\n%s", diff)
		}
		for _, color := range []string{"red", "green", "blue"} {
			if !strings.Contains(diff, "-"+color) {
				t.Errorf("missing deleted line -%s in diff:\n%s", color, diff)
			}
		}
		for _, color := range []string{"cyan", "magenta", "yellow", "black"} {
			if !strings.Contains(diff, "+"+color) {
				t.Errorf("missing added line +%s in diff:\n%s", color, diff)
			}
		}
	})

	t.Run("multi_hunks_separated_by_more_than_six_lines", func(t *testing.T) {
		// Construct file with 40 lines.
		// Edits at line 2 (index 1), line 16 (index 15), line 35 (index 34).
		// Distance between index 1 and index 15 is 13 lines (> 2*3 = 6).
		// Distance between index 15 and index 34 is 18 lines (> 2*3 = 6).
		var oldLines, newLines []string
		for i := 1; i <= 40; i++ {
			line := fmt.Sprintf("content line %02d", i)
			oldLines = append(oldLines, line)
			newLines = append(newLines, line)
		}

		oldLines[1] = "content line 02 OLD"
		newLines[1] = "content line 02 NEW"

		oldLines[15] = "content line 16 OLD"
		newLines[15] = "content line 16 NEW"

		oldLines[34] = "content line 35 OLD"
		newLines[34] = "content line 35 NEW"

		diff := GenerateLCSUnifiedDiff("orig.txt", "mod.txt", strings.Join(oldLines, "\n"), strings.Join(newLines, "\n"), 3)
		hunkCount := strings.Count(diff, "@@ ")
		if hunkCount != 3 {
			t.Fatalf("expected exactly 3 distinct hunks separated by >6 lines, got %d. Diff:\n%s", hunkCount, diff)
		}

		if !strings.Contains(diff, "-content line 02 OLD") || !strings.Contains(diff, "+content line 02 NEW") {
			t.Errorf("missing hunk 1 changes")
		}
		if !strings.Contains(diff, "-content line 16 OLD") || !strings.Contains(diff, "+content line 16 NEW") {
			t.Errorf("missing hunk 2 changes")
		}
		if !strings.Contains(diff, "-content line 35 OLD") || !strings.Contains(diff, "+content line 35 NEW") {
			t.Errorf("missing hunk 3 changes")
		}
	})

	t.Run("multi_hunk_boundary_exact_six_lines_vs_seven_lines", func(t *testing.T) {
		// 15 lines. Edit at line 3 and line 10.
		// Unchanged lines between line 3 and line 10: lines 4, 5, 6, 7, 8, 9 (exactly 6 unchanged lines).
		// With contextLines = 3, 2 * contextLines = 6.
		// Since run (6) is NOT > 6 (run > contextLines*2 is false), they bridge into 1 hunk!
		var old6, new6 []string
		for i := 1; i <= 15; i++ {
			old6 = append(old6, fmt.Sprintf("line %02d", i))
			new6 = append(new6, fmt.Sprintf("line %02d", i))
		}
		old6[2] = "line 03 old"
		new6[2] = "line 03 new"
		old6[9] = "line 10 old"
		new6[9] = "line 10 new"

		diff6 := GenerateLCSUnifiedDiff("f6.txt", "f6.txt", strings.Join(old6, "\n"), strings.Join(new6, "\n"), 3)
		hunks6 := strings.Count(diff6, "@@ ")
		if hunks6 != 1 {
			t.Errorf("expected 1 bridged hunk for gap == 6, got %d. Diff:\n%s", hunks6, diff6)
		}

		// 16 lines. Edit at line 3 and line 11.
		// Unchanged lines between line 3 and line 11: lines 4, 5, 6, 7, 8, 9, 10 (exactly 7 unchanged lines).
		// Gap is 7 > 2*3 = 6. They MUST split into 2 hunks!
		var old7, new7 []string
		for i := 1; i <= 16; i++ {
			old7 = append(old7, fmt.Sprintf("line %02d", i))
			new7 = append(new7, fmt.Sprintf("line %02d", i))
		}
		old7[2] = "line 03 old"
		new7[2] = "line 03 new"
		old7[10] = "line 11 old"
		new7[10] = "line 11 new"

		diff7 := GenerateLCSUnifiedDiff("f7.txt", "f7.txt", strings.Join(old7, "\n"), strings.Join(new7, "\n"), 3)
		hunks7 := strings.Count(diff7, "@@ ")
		if hunks7 != 2 {
			t.Errorf("expected 2 split hunks for gap == 7 (>6), got %d. Diff:\n%s", hunks7, diff7)
		}
	})

	t.Run("negative_context_lines_defaults_to_three", func(t *testing.T) {
		oldC := "1\n2\n3\n4\n5_old\n6\n7\n8\n9\n"
		newC := "1\n2\n3\n4\n5_new\n6\n7\n8\n9\n"
		diffNeg := GenerateLCSUnifiedDiff("a.txt", "b.txt", oldC, newC, -1)
		diffDef := GenerateLCSUnifiedDiff("a.txt", "b.txt", oldC, newC, 3)
		if diffNeg != diffDef {
			t.Errorf("expected negative contextLines to default to 3, got diffNeg != diffDef:\nneg:\n%s\ndef:\n%s", diffNeg, diffDef)
		}
	})

	t.Run("zero_context_lines", func(t *testing.T) {
		oldC := "lineA\nlineB\nlineC\nlineD\nlineE\n"
		newC := "lineA\nlineB\nlineC_MOD\nlineD\nlineE\n"
		diff0 := GenerateLCSUnifiedDiff("file1.txt", "file2.txt", oldC, newC, 0)
		lines := strings.Split(diff0, "\n")
		for _, l := range lines {
			if strings.HasPrefix(l, " ") {
				t.Errorf("zero context diff should have 0 unchanged context lines starting with space, got line: %q in diff:\n%s", l, diff0)
			}
		}
		if !strings.Contains(diff0, "-lineC") || !strings.Contains(diff0, "+lineC_MOD") {
			t.Errorf("missing replacement in zero context diff:\n%s", diff0)
		}
	})

	t.Run("unicode_and_arabic_text_diff", func(t *testing.T) {
		oldArabic := "مرحبا بكم\nصلة محرك المعرفة\nالنسخة الأولى\n"
		newArabic := "مرحبا بكم\nصلة حاكم المهارات الكامل\nالنسخة الأولى\n"
		diff := GenerateLCSUnifiedDiff("arabic.txt", "arabic.txt", oldArabic, newArabic, 3)
		if !strings.Contains(diff, "-صلة محرك المعرفة") || !strings.Contains(diff, "+صلة حاكم المهارات الكامل") {
			t.Errorf("failed to handle Arabic unicode line diff correctly:\n%s", diff)
		}
	})

	t.Run("stress_large_input_200_lines", func(t *testing.T) {
		var oldLines, newLines []string
		for i := 1; i <= 200; i++ {
			oldLines = append(oldLines, fmt.Sprintf("line %03d: data payload key_%d", i, i))
			if i%20 == 0 {
				newLines = append(newLines, fmt.Sprintf("line %03d: MODIFIED data payload key_%d", i, i))
			} else {
				newLines = append(newLines, fmt.Sprintf("line %03d: data payload key_%d", i, i))
			}
		}
		diff := GenerateLCSUnifiedDiff("old.txt", "new.txt", strings.Join(oldLines, "\n"), strings.Join(newLines, "\n"), 3)
		if diff == "" {
			t.Fatalf("expected non-empty diff for large input")
		}
		hunks := strings.Count(diff, "@@ ")
		if hunks != 10 {
			t.Errorf("expected 10 hunks for edits at every 20th line in 200 lines, got %d", hunks)
		}
	})
}

// TestChallenger_ParseDivergedKeys_AllFourZones thoroughly challenges ParseDivergedKeys
// across all 4 zones plus Zone 5 formatting fallback, testing combinations and sorting.
func TestChallenger_ParseDivergedKeys_AllFourZones(t *testing.T) {
	t.Run("nil_document_returns_document", func(t *testing.T) {
		keys := ParseDivergedKeys(nil, nil, nil)
		if len(keys) != 1 || keys[0] != "document" {
			t.Errorf("expected ['document'] for nil document, got: %v", keys)
		}
	})

	t.Run("zone1_envelope_all_fields_tampered", func(t *testing.T) {
		doc := &ManagedDocument{
			Envelope: &ManagedEnvelope{
				Managed:   false,
				Version:   99,
				ProjectID: "tampered-proj",
				Toolchain: "tampered-tool",
			},
		}
		opts := AdapterOptions{
			ProjectID: "expected-proj",
			Toolchain: "antigravity",
		}
		keys := ParseDivergedKeys(doc, []string{"skill-a"}, []string{"*"}, opts)
		assertContains(t, keys, "_sila.managed")
		assertContains(t, keys, "_sila.version")
		assertContains(t, keys, "_sila.project_id")
		assertContains(t, keys, "_sila.toolchain")
	})

	t.Run("zone1_envelope_missing_envelope_struct", func(t *testing.T) {
		doc := &ManagedDocument{
			Envelope: nil,
			Entries:  []PathEntry{{Path: ".agents/skills", IncludeOnly: []string{"^skill-a$"}}},
		}
		keys := ParseDivergedKeys(doc, []string{"skill-a"}, nil)
		assertContains(t, keys, "_sila.managed")
	})

	t.Run("zone2_entries_regex_mismatch_and_unmanaged_entry", func(t *testing.T) {
		doc := &ManagedDocument{
			Envelope: &ManagedEnvelope{
				Managed:       true,
				Version:       1,
				ManagedSkills: []string{"skill-alpha", "skill-beta"},
			},
			Entries: []PathEntry{
				{
					Path:        ".agents/skills",
					IncludeOnly: []string{"^skill-alpha$", "^skill-TAMPERED$"},
				},
				{
					Path:        "external/tooling/skills",
					IncludeOnly: []string{"*"},
				},
			},
		}
		keys := ParseDivergedKeys(doc, []string{"skill-alpha", "skill-beta"}, nil)
		assertContains(t, keys, "skills.include")
		assertContains(t, keys, "entries (unmanaged entries)")
	})

	t.Run("zone2_entries_missing_sila_entry_entirely", func(t *testing.T) {
		doc := &ManagedDocument{
			Envelope: &ManagedEnvelope{
				Managed:       true,
				Version:       1,
				ManagedSkills: []string{"skill-alpha"},
			},
			Entries: []PathEntry{
				{Path: "vendor/skills", IncludeOnly: []string{"*"}},
			},
		}
		keys := ParseDivergedKeys(doc, []string{"skill-alpha"}, nil)
		assertContains(t, keys, "entries (missing Sila entry)")
		assertContains(t, keys, "entries (unmanaged entries)")
	})

	t.Run("zone3_exclude_mismatch", func(t *testing.T) {
		doc := &ManagedDocument{
			Envelope: &ManagedEnvelope{
				Managed:         true,
				Version:         1,
				ManagedExcludes: []string{"*"},
			},
			Exclude: []string{"*-internal"},
		}
		keys := ParseDivergedKeys(doc, nil, []string{"*"})
		assertContains(t, keys, "exclude")
	})

	t.Run("zone4_unmanaged_keys_deterministic_sorting", func(t *testing.T) {
		// Provide keys out of order: "zebra", "corp_policy", "author", "billing"
		doc := &ManagedDocument{
			Envelope: &ManagedEnvelope{
				Managed: true,
				Version: 1,
			},
			Extra: map[string]json.RawMessage{
				"zebra":       json.RawMessage(`"stripes"`),
				"corp_policy": json.RawMessage(`"strict-v2"`),
				"author":      json.RawMessage(`"secops"`),
				"billing":     json.RawMessage(`"tier-1"`),
			},
		}
		keys := ParseDivergedKeys(doc, nil, nil)

		expectedOrder := []string{
			"author (unmanaged key)",
			"billing (unmanaged key)",
			"corp_policy (unmanaged key)",
			"zebra (unmanaged key)",
		}

		var unmanagedFound []string
		for _, k := range keys {
			if strings.HasSuffix(k, "(unmanaged key)") {
				unmanagedFound = append(unmanagedFound, k)
			}
		}

		if !reflect.DeepEqual(unmanagedFound, expectedOrder) {
			t.Errorf("unmanaged keys not deterministically sorted!\nExpected: %v\nGot:      %v", expectedOrder, unmanagedFound)
		}
	})

	t.Run("zone5_formatting_fallback_when_no_structural_divergence", func(t *testing.T) {
		// Document perfectly matches expectations structurally
		doc := &ManagedDocument{
			Envelope: &ManagedEnvelope{
				Managed:         true,
				Version:         1,
				ManagedSkills:   []string{"skill-1"},
				ManagedExcludes: []string{"*"},
			},
			Entries: []PathEntry{
				{Path: ".agents/skills", IncludeOnly: []string{"^skill-1$"}},
			},
			Exclude: []string{"*"},
		}
		keys := ParseDivergedKeys(doc, []string{"skill-1"}, []string{"*"})
		if len(keys) != 1 || keys[0] != "content_hash (internal formatting/whitespace)" {
			t.Errorf("expected formatting fallback, got: %v", keys)
		}
	})

	t.Run("multi_zone_collision_aggregation", func(t *testing.T) {
		// Document with divergence in Zone 1, Zone 2, Zone 3, and Zone 4 simultaneously
		doc := &ManagedDocument{
			Envelope: &ManagedEnvelope{
				Managed:         false, // Zone 1
				Version:         2,     // Zone 1
				ProjectID:       "wrong-project",
				Toolchain:       "wrong-tool",
				ManagedSkills:   []string{"skill-a"},
				ManagedExcludes: []string{"*"},
			},
			Entries: []PathEntry{
				{Path: ".agents/skills", IncludeOnly: []string{"^skill-TAMPERED$"}}, // Zone 2
				{Path: "unmanaged/path", IncludeOnly: []string{"*"}},                // Zone 2
			},
			Exclude: []string{"exclude-tampered"}, // Zone 3
			Extra: map[string]json.RawMessage{
				"corp_policy": json.RawMessage(`"strict"`), // Zone 4
			},
		}
		opts := AdapterOptions{
			ProjectID: "correct-project",
			Toolchain: "antigravity",
		}
		keys := ParseDivergedKeys(doc, []string{"skill-a"}, []string{"*"}, opts)

		assertContains(t, keys, "_sila.managed")
		assertContains(t, keys, "_sila.version")
		assertContains(t, keys, "_sila.project_id")
		assertContains(t, keys, "_sila.toolchain")
		assertContains(t, keys, "skills.include")
		assertContains(t, keys, "entries (unmanaged entries)")
		assertContains(t, keys, "exclude")
		assertContains(t, keys, "corp_policy (unmanaged key)")

		// Verify no duplicate keys
		seen := make(map[string]bool)
		for _, k := range keys {
			if seen[k] {
				t.Errorf("duplicate key detected in ParseDivergedKeys: %s", k)
			}
			seen[k] = true
		}
	})
}

// TestChallenger_BackwardCompatibility_And_CollisionContracts verifies that:
// 1. Legacy documents lacking ManagedSkills compute payload hashes and do not trigger false collisions.
// 2. CollisionError satisfies both structured unpacking and substring contracts ("collision", "modified externally").
func TestChallenger_BackwardCompatibility_And_CollisionContracts(t *testing.T) {
	t.Run("legacy_document_hash_stability_and_zero_false_collision", func(t *testing.T) {
		legacyDoc := &ManagedDocument{
			Envelope: &ManagedEnvelope{
				Managed:         true,
				Version:         1,
				ProjectID:       "legacy-repo",
				Toolchain:       "antigravity",
				ManagedExcludes: []string{"*"},
				ManagedSkills:   nil, // explicitly nil legacy state
			},
			Entries: []PathEntry{
				{Path: ".agents/skills", IncludeOnly: []string{"^golang-testing$", "^golang-benchmark$"}},
			},
			Exclude: []string{"*"},
		}

		hash1, err := ComputePayloadHash(legacyDoc)
		if err != nil {
			t.Fatalf("hash computation 1 failed: %v", err)
		}

		// Marshalling and unmarshalling should produce exact same hash
		legacyDoc.Envelope.ContentHash = hash1
		data, err := legacyDoc.MarshalJSON()
		if err != nil {
			t.Fatalf("marshal failed: %v", err)
		}

		var unmarshaled ManagedDocument
		if err := json.Unmarshal(data, &unmarshaled); err != nil {
			t.Fatalf("unmarshal failed: %v", err)
		}

		if unmarshaled.Envelope.ManagedSkills != nil {
			t.Errorf("unmarshaled legacy doc should have nil ManagedSkills, got: %v", unmarshaled.Envelope.ManagedSkills)
		}

		hasCollision, recorded, current, err := DetectCollision(&unmarshaled)
		if err != nil {
			t.Fatalf("DetectCollision failed: %v", err)
		}
		if hasCollision {
			t.Fatalf("false collision detected on legacy document! recorded=%s, current=%s", recorded, current)
		}
		if recorded != current {
			t.Errorf("recorded hash (%s) does not match current hash (%s)", recorded, current)
		}
	})

	t.Run("collision_error_contract_and_type_assertion", func(t *testing.T) {
		colErr := &CollisionError{
			TargetPath:   ".agents/skills.json",
			RecordedHash: "sha256:1111111111111111111111111111111111111111111111111111111111111111",
			CurrentHash:  "sha256:2222222222222222222222222222222222222222222222222222222222222222",
			DivergedKeys: []string{"skills.include", "corp_policy (unmanaged key)"},
			Diff:         "--- .agents/skills.json (recorded)\n+++ .agents/skills.json (on disk)\n@@ -1,3 +1,4 @@\n",
		}

		// Contract 1: Error() implements error interface
		var err error = colErr
		errStr := err.Error()

		// Contract 2: Substring matching for backward compatibility
		if !strings.Contains(errStr, "collision") {
			t.Errorf("expected error string to contain 'collision', got: %s", errStr)
		}
		if !strings.Contains(errStr, "modified externally") {
			t.Errorf("expected error string to contain 'modified externally', got: %s", errStr)
		}
		if !strings.Contains(errStr, "Use --force to overwrite") {
			t.Errorf("expected error string to contain 'Use --force to overwrite', got: %s", errStr)
		}

		// Contract 3: Typed error unpacking
		var targetColErr *CollisionError
		if !errors.As(err, &targetColErr) {
			t.Fatalf("errors.As(*CollisionError) failed")
		}
		if targetColErr.TargetPath != ".agents/skills.json" {
			t.Errorf("target path mismatch: %s", targetColErr.TargetPath)
		}
		if len(targetColErr.DivergedKeys) != 2 {
			t.Errorf("diverged keys length mismatch: %v", targetColErr.DivergedKeys)
		}
		if targetColErr.Diff == "" {
			t.Errorf("diff empty")
		}
	})

	t.Run("end_to_end_collision_lifecycle_with_visual_diff_and_force_override", func(t *testing.T) {
		tempDir := t.TempDir()
		opts := AdapterOptions{
			Harness: "antigravity",
			Skills:  []string{"initial-skill"},
		}

		// Step 1: Initial apply
		res1, err := ApplyHarnessConfig(tempDir, opts)
		if err != nil || !res1.Applied {
			t.Fatalf("initial apply failed: %v", err)
		}

		// Step 2: Out-of-band external modification
		skillsPath := filepath.Join(tempDir, ".agents", "skills.json")
		content, err := os.ReadFile(skillsPath)
		if err != nil {
			t.Fatalf("read: %v", err)
		}
		// Insert corp_policy and modify include
		tampered := strings.Replace(string(content), `"^initial-skill$"`, `"^external-skill$"`, 1)
		tampered = strings.Replace(tampered, "{\n", "{\n  \"corp_policy\": \"restricted\",\n", 1)
		if err := os.WriteFile(skillsPath, []byte(tampered), 0644); err != nil {
			t.Fatalf("write tampered: %v", err)
		}

		// Step 3: Apply without force -> MUST return CollisionError with visual diff
		_, err = ApplyHarnessConfig(tempDir, opts)
		if err == nil {
			t.Fatalf("expected collision error, got nil")
		}
		var colErr *CollisionError
		if !errors.As(err, &colErr) {
			t.Fatalf("expected *CollisionError, got %T", err)
		}

		if len(colErr.DivergedKeys) == 0 {
			t.Fatalf("expected non-empty diverged keys")
		}
		assertContains(t, colErr.DivergedKeys, "corp_policy (unmanaged key)")
		assertContains(t, colErr.DivergedKeys, "skills.include")

		if !strings.Contains(colErr.Diff, "corp_policy") {
			t.Errorf("diff does not show corp_policy added:\n%s", colErr.Diff)
		}
		if !strings.Contains(colErr.Diff, "+        \"^external-skill$\"") {
			t.Errorf("diff does not show external-skill added:\n%s", colErr.Diff)
		}

		// Step 4: Apply with force -> MUST succeed, preserve unmanaged extra key, and reseal
		forceOpts := opts
		forceOpts.Force = true
		resForce, err := ApplyHarnessConfig(tempDir, forceOpts)
		if err != nil {
			t.Fatalf("force apply failed: %v", err)
		}
		if !resForce.Applied {
			t.Errorf("expected force apply to report Applied=true")
		}

		// Read back from disk to verify envelope was resealed cleanly
		afterForceData, err := os.ReadFile(skillsPath)
		if err != nil {
			t.Fatalf("read after force: %v", err)
		}
		var afterForceDoc ManagedDocument
		if err := json.Unmarshal(afterForceData, &afterForceDoc); err != nil {
			t.Fatalf("unmarshal after force: %v", err)
		}
		if afterForceDoc.Envelope == nil || !afterForceDoc.Envelope.Managed {
			t.Fatalf("missing valid envelope after force")
		}
		if afterForceDoc.Envelope.ContentHash == "" {
			t.Fatalf("missing ContentHash after force")
		}
		// Confirm collision is now resolved
		hasColAfter, _, _, err := DetectCollision(&afterForceDoc)
		if err != nil || hasColAfter {
			t.Fatalf("collision still present after force override! hasCollision=%t, err=%v", hasColAfter, err)
		}
	})

	t.Run("concurrent_stress_diff_and_diverged_keys", func(t *testing.T) {
		// Run 50 concurrent goroutines executing GenerateLCSUnifiedDiff and ParseDivergedKeys
		// to verify 100% thread safety and zero race conditions.
		var wg sync.WaitGroup
		goroutines := 50
		for i := 0; i < goroutines; i++ {
			wg.Add(1)
			go func(idx int) {
				defer wg.Done()
				oldS := fmt.Sprintf("content_%d_a\ncontent_%d_b\n", idx, idx)
				newS := fmt.Sprintf("content_%d_a\ncontent_%d_b_MOD\ncontent_%d_c\n", idx, idx, idx)
				diff := GenerateLCSUnifiedDiff("old.txt", "new.txt", oldS, newS, 3)
				if diff == "" {
					t.Errorf("worker %d: unexpected empty diff", idx)
				}

				doc := &ManagedDocument{
					Envelope: &ManagedEnvelope{
						Managed:         true,
						Version:         1,
						ManagedSkills:   []string{fmt.Sprintf("skill-%d", idx)},
						ManagedExcludes: []string{"*"},
					},
					Entries: []PathEntry{
						{Path: ".agents/skills", IncludeOnly: []string{fmt.Sprintf("^skill-%d$", idx)}},
					},
					Extra: map[string]json.RawMessage{
						fmt.Sprintf("key_%d", idx): json.RawMessage(`"val"`),
					},
				}
				keys := ParseDivergedKeys(doc, []string{fmt.Sprintf("skill-%d", idx)}, []string{"*"})
				if len(keys) == 0 {
					t.Errorf("worker %d: expected diverged keys", idx)
				}
			}(i)
		}
		wg.Wait()
	})
}
