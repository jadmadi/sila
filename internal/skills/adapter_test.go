package skills

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// TestAdapter_ManagedSkills_Serialization verifies that ManagedSkills is serialized,
// omitted when empty (omitempty), and preserved in round-trip serialization.
func TestAdapter_ManagedSkills_Serialization(t *testing.T) {
	docWithSkills := &ManagedDocument{
		Envelope: &ManagedEnvelope{
			Managed:         true,
			Version:         1,
			ProjectID:       "proj-test",
			ManagedSkills:   []string{"skill-alpha", "skill-beta"},
			ManagedExcludes: []string{"*"},
		},
		Entries: []PathEntry{
			{Path: ".agents/skills", IncludeOnly: []string{"^skill-alpha$", "^skill-beta$"}},
		},
	}

	bytes, err := docWithSkills.MarshalJSON()
	if err != nil {
		t.Fatalf("marshal failed: %v", err)
	}

	if !strings.Contains(string(bytes), `"managed_skills"`) {
		t.Errorf("expected JSON to contain 'managed_skills', got:\n%s", string(bytes))
	}
	if !strings.Contains(string(bytes), `"skill-alpha"`) || !strings.Contains(string(bytes), `"skill-beta"`) {
		t.Errorf("expected JSON to contain skills, got:\n%s", string(bytes))
	}

	// Round-trip unmarshal
	var unmarshaled ManagedDocument
	if err := json.Unmarshal(bytes, &unmarshaled); err != nil {
		t.Fatalf("unmarshal failed: %v", err)
	}
	if unmarshaled.Envelope == nil || len(unmarshaled.Envelope.ManagedSkills) != 2 {
		t.Fatalf("expected 2 managed skills after round-trip, got: %v", unmarshaled.Envelope)
	}
	if unmarshaled.Envelope.ManagedSkills[0] != "skill-alpha" || unmarshaled.Envelope.ManagedSkills[1] != "skill-beta" {
		t.Errorf("skill values mismatched: %v", unmarshaled.Envelope.ManagedSkills)
	}

	// Document with empty/nil ManagedSkills should omit the field
	docWithoutSkills := &ManagedDocument{
		Envelope: &ManagedEnvelope{
			Managed:   true,
			Version:   1,
			ProjectID: "proj-legacy",
		},
	}
	legacyBytes, err := docWithoutSkills.MarshalJSON()
	if err != nil {
		t.Fatalf("marshal failed: %v", err)
	}
	if strings.Contains(string(legacyBytes), `"managed_skills"`) {
		t.Errorf("expected JSON to omit 'managed_skills' when nil, got:\n%s", string(legacyBytes))
	}
}

// TestAdapter_BackwardCompatibility_LegacyDoc verifies that an M1 document lacking
// managed_skills computes the identical payload hash and triggers no false collision.
func TestAdapter_BackwardCompatibility_LegacyDoc(t *testing.T) {
	legacyJSON := `{
  "_sila": {
    "managed": true,
    "version": 1,
    "project_id": "legacy-proj",
    "content_hash": "",
    "updated_at": "0001-01-01T00:00:00Z",
    "managed_excludes": ["*"]
  },
  "entries": [
    {
      "path": ".agents/skills",
      "include_only": ["^test-skill$"]
    }
  ],
  "exclude": ["*"]
}`
	var doc ManagedDocument
	if err := json.Unmarshal([]byte(legacyJSON), &doc); err != nil {
		t.Fatalf("failed to unmarshal legacy document: %v", err)
	}

	if doc.Envelope.ManagedSkills != nil {
		t.Errorf("expected ManagedSkills to be nil on legacy doc, got: %v", doc.Envelope.ManagedSkills)
	}

	// Compute hash
	hash, err := ComputePayloadHash(&doc)
	if err != nil {
		t.Fatalf("compute hash failed: %v", err)
	}
	if hash == "" || !strings.HasPrefix(hash, "sha256:") {
		t.Fatalf("invalid computed hash: %s", hash)
	}

	doc.Envelope.ContentHash = hash
	hasCollision, recorded, current, err := DetectCollision(&doc)
	if err != nil {
		t.Fatalf("detect collision failed: %v", err)
	}
	if hasCollision {
		t.Fatalf("false collision detected on legacy document: recorded %s vs current %s", recorded, current)
	}
}

// TestAdapter_DivergedKeys_Zone1_Envelope verifies detection of tampering in _sila envelope fields.
func TestAdapter_DivergedKeys_Zone1_Envelope(t *testing.T) {
	doc := &ManagedDocument{
		Envelope: &ManagedEnvelope{
			Managed:   false, // tampered
			Version:   2,     // tampered
			ProjectID: "tampered-proj",
			Toolchain: "tampered-tool",
		},
	}
	opts := AdapterOptions{
		ProjectID: "orig-proj",
		Toolchain: "antigravity",
	}
	keys := ParseDivergedKeys(doc, []string{"skill-a"}, []string{"*"}, opts)
	assertContains(t, keys, "_sila.managed")
	assertContains(t, keys, "_sila.version")
	assertContains(t, keys, "_sila.project_id")
	assertContains(t, keys, "_sila.toolchain")
}

// TestAdapter_DivergedKeys_Zone2_Entries verifies detection of modifications in include_only regexes,
// missing Sila entries, and unexpected unmanaged entries.
func TestAdapter_DivergedKeys_Zone2_Entries(t *testing.T) {
	// Case A: include_only regex modified
	docA := &ManagedDocument{
		Envelope: &ManagedEnvelope{
			Managed:       true,
			Version:       1,
			ManagedSkills: []string{"skill-alpha"},
		},
		Entries: []PathEntry{
			{
				Path:        ".agents/skills",
				IncludeOnly: []string{"^skill-beta$"}, // changed from skill-alpha
			},
		},
	}
	keysA := ParseDivergedKeys(docA, []string{"skill-alpha"}, nil)
	assertContains(t, keysA, "skills.include")

	// Case B: missing Sila entry
	docB := &ManagedDocument{
		Envelope: &ManagedEnvelope{
			Managed:       true,
			Version:       1,
			ManagedSkills: []string{"skill-alpha"},
		},
		Entries: []PathEntry{},
	}
	keysB := ParseDivergedKeys(docB, []string{"skill-alpha"}, nil)
	assertContains(t, keysB, "entries (missing Sila entry)")

	// Case C: unmanaged external entry present
	docC := &ManagedDocument{
		Envelope: &ManagedEnvelope{
			Managed:       true,
			Version:       1,
			ManagedSkills: []string{"skill-alpha"},
		},
		Entries: []PathEntry{
			{Path: ".agents/skills", IncludeOnly: []string{"^skill-alpha$"}},
			{Path: ".agents/external", IncludeOnly: []string{"*"}},
		},
	}
	keysC := ParseDivergedKeys(docC, []string{"skill-alpha"}, nil)
	assertContains(t, keysC, "entries (unmanaged entries)")
}

// TestAdapter_DivergedKeys_Zone3_Exclude verifies detection of altered global exclude patterns.
func TestAdapter_DivergedKeys_Zone3_Exclude(t *testing.T) {
	doc := &ManagedDocument{
		Envelope: &ManagedEnvelope{
			Managed:         true,
			Version:         1,
			ManagedExcludes: []string{"*"},
		},
		Exclude: []string{"*-internal", "debug-*"},
	}
	keys := ParseDivergedKeys(doc, nil, []string{"*"})
	assertContains(t, keys, "exclude")
}

// TestAdapter_DivergedKeys_Zone4_UnmanagedKeys verifies detection of arbitrary custom top-level keys.
func TestAdapter_DivergedKeys_Zone4_UnmanagedKeys(t *testing.T) {
	doc := &ManagedDocument{
		Envelope: &ManagedEnvelope{Managed: true, Version: 1},
		Extra: map[string]json.RawMessage{
			"corp_policy": json.RawMessage(`"strict-v2"`),
			"owner":       json.RawMessage(`"secops"`),
		},
	}
	keys := ParseDivergedKeys(doc, nil, nil)
	assertContains(t, keys, "corp_policy (unmanaged key)")
	assertContains(t, keys, "owner (unmanaged key)")
}

// TestAdapter_DivergedKeys_Zone5_FormattingFallback verifies that when hashes diverge without
// field structural mismatch, formatting/whitespace divergence is reported.
func TestAdapter_DivergedKeys_Zone5_FormattingFallback(t *testing.T) {
	doc := &ManagedDocument{
		Envelope: &ManagedEnvelope{Managed: true, Version: 1},
	}
	keys := ParseDivergedKeys(doc, nil, nil)
	assertContains(t, keys, "content_hash (internal formatting/whitespace)")
}

// TestAdapter_LCSUnifiedDiff_EdgeCases tests LCS unified diff formatting on standard boundary cases.
func TestAdapter_LCSUnifiedDiff_EdgeCases(t *testing.T) {
	t.Run("empty_old_file_creation", func(t *testing.T) {
		diff := GenerateLCSUnifiedDiff("old.txt", "new.txt", "", "line1\nline2\n", 3)
		if !strings.Contains(diff, "--- /dev/null") || !strings.Contains(diff, "+++ new.txt") {
			t.Errorf("unexpected diff header: %s", diff)
		}
		if !strings.Contains(diff, "+line1") || !strings.Contains(diff, "+line2") {
			t.Errorf("missing added lines in diff: %s", diff)
		}
	})

	t.Run("empty_new_file_deletion", func(t *testing.T) {
		diff := GenerateLCSUnifiedDiff("old.txt", "new.txt", "line1\nline2\n", "", 3)
		if !strings.Contains(diff, "--- old.txt") || !strings.Contains(diff, "+++ /dev/null") {
			t.Errorf("unexpected diff header: %s", diff)
		}
		if !strings.Contains(diff, "-line1") || !strings.Contains(diff, "-line2") {
			t.Errorf("missing removed lines in diff: %s", diff)
		}
	})

	t.Run("identical_content", func(t *testing.T) {
		diff := GenerateLCSUnifiedDiff("a.txt", "b.txt", "hello\nworld\n", "hello\nworld\n", 3)
		if diff != "" {
			t.Errorf("expected empty diff for identical content, got: %q", diff)
		}
	})

	t.Run("middle_replacement_with_context", func(t *testing.T) {
		oldContent := "line1\nline2\nline3\nline4_old\nline5\nline6\nline7\n"
		newContent := "line1\nline2\nline3\nline4_new\nline5\nline6\nline7\n"
		diff := GenerateLCSUnifiedDiff("old.txt", "new.txt", oldContent, newContent, 3)
		if !strings.Contains(diff, "-line4_old") || !strings.Contains(diff, "+line4_new") {
			t.Errorf("expected hunk with replacement, got:\n%s", diff)
		}
		if !strings.Contains(diff, " line3") || !strings.Contains(diff, " line5") {
			t.Errorf("expected 3 lines of context, got:\n%s", diff)
		}
	})

	t.Run("crlf_normalization", func(t *testing.T) {
		diff := GenerateLCSUnifiedDiff("a.txt", "b.txt", "line1\r\nline2\r\n", "line1\nline2\n", 3)
		if diff != "" {
			t.Errorf("expected empty diff for CRLF-only differences, got: %q", diff)
		}
	})
}

// TestAdapter_LCSUnifiedDiff_MultipleHunks verifies that distant edits produce multiple hunks
// while adjacent edits within 2*contextLines bridge into a single hunk.
func TestAdapter_LCSUnifiedDiff_MultipleHunks(t *testing.T) {
	// Construct 30 lines
	var oldLines, newLines []string
	for i := 1; i <= 30; i++ {
		oldLines = append(oldLines, fmt.Sprintf("line %02d", i))
		newLines = append(newLines, fmt.Sprintf("line %02d", i))
	}
	// Edit line 2 and line 28
	oldLines[1] = "line 02 old"
	newLines[1] = "line 02 new"
	oldLines[27] = "line 28 old"
	newLines[27] = "line 28 new"

	diff := GenerateLCSUnifiedDiff("old.txt", "new.txt", strings.Join(oldLines, "\n"), strings.Join(newLines, "\n"), 3)
	hunkCount := strings.Count(diff, "@@ ")
	if hunkCount != 2 {
		t.Errorf("expected exactly 2 hunks for distant edits, got %d. Diff:\n%s", hunkCount, diff)
	}

	// Adjacent changes within 4 lines should bridge into 1 hunk
	var oldBridge, newBridge []string
	for i := 1; i <= 15; i++ {
		oldBridge = append(oldBridge, fmt.Sprintf("item %02d", i))
		newBridge = append(newBridge, fmt.Sprintf("item %02d", i))
	}
	oldBridge[2] = "item 03 old"
	newBridge[2] = "item 03 new"
	oldBridge[6] = "item 07 old"
	newBridge[6] = "item 07 new"

	bridgeDiff := GenerateLCSUnifiedDiff("old.txt", "new.txt", strings.Join(oldBridge, "\n"), strings.Join(newBridge, "\n"), 3)
	bridgeHunks := strings.Count(bridgeDiff, "@@ ")
	if bridgeHunks != 1 {
		t.Errorf("expected 1 merged hunk for bridged edits, got %d. Diff:\n%s", bridgeHunks, bridgeDiff)
	}
}

// TestAdapter_Collision_DetailedDiagnostics verifies that ApplyHarnessConfig returns a structured
// CollisionError with valid hashes, diverged keys, and unified visual diff.
func TestAdapter_Collision_DetailedDiagnostics(t *testing.T) {
	tempDir := t.TempDir()
	opts := AdapterOptions{
		Harness: "antigravity",
		Skills:  []string{"test-skill-alpha"},
	}

	// 1. Initial apply
	res, err := ApplyHarnessConfig(tempDir, opts)
	if err != nil || !res.Applied {
		t.Fatalf("initial apply failed: %v", err)
	}

	// 2. Introduce external collision (add custom key & change skill regex)
	configPath := filepath.Join(tempDir, ".agents", "skills.json")
	data, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("read config: %v", err)
	}
	mutated := strings.Replace(string(data), `"^test-skill-alpha$"`, `"^test-skill-beta$"`, 1)
	mutated = strings.Replace(mutated, "{\n", "{\n  \"corp_policy\": \"custom-v1\",\n", 1)
	if err := os.WriteFile(configPath, []byte(mutated), 0644); err != nil {
		t.Fatalf("write mutated: %v", err)
	}

	// 3. Apply without force -> MUST return typed CollisionError
	_, err = ApplyHarnessConfig(tempDir, opts)
	if err == nil {
		t.Fatalf("expected collision error, got nil")
	}

	var colErr *CollisionError
	if !errors.As(err, &colErr) {
		t.Fatalf("expected error to be *CollisionError, got %T: %v", err, err)
	}

	// Verify structured fields
	if colErr.TargetPath == "" {
		t.Errorf("expected non-empty TargetPath")
	}
	if colErr.RecordedHash == "" || colErr.CurrentHash == "" {
		t.Errorf("expected valid recorded and current hashes, got recorded=%s, current=%s", colErr.RecordedHash, colErr.CurrentHash)
	}
	if colErr.RecordedHash == colErr.CurrentHash {
		t.Errorf("recorded and current hashes should differ")
	}
	if len(colErr.DivergedKeys) == 0 {
		t.Errorf("expected diverged keys in collision error, got empty")
	}
	assertContains(t, colErr.DivergedKeys, "skills.include")
	assertContains(t, colErr.DivergedKeys, "corp_policy (unmanaged key)")

	if colErr.Diff == "" {
		t.Errorf("expected non-empty diff in collision error")
	}
	if !strings.Contains(colErr.Diff, "corp_policy") {
		t.Errorf("diff should show corp_policy added, got:\n%s", colErr.Diff)
	}

	// Verify Error() formatting backward compatibility
	errStr := colErr.Error()
	if !strings.Contains(errStr, "collision") {
		t.Errorf("error string must contain 'collision', got: %s", errStr)
	}
	if !strings.Contains(errStr, "modified externally") {
		t.Errorf("error string must contain 'modified externally', got: %s", errStr)
	}
	if !strings.Contains(errStr, "Use --force to overwrite") {
		t.Errorf("error string must contain 'Use --force to overwrite', got: %s", errStr)
	}

	// 4. Force override -> MUST succeed and reseal envelope
	optsForce := opts
	optsForce.Force = true
	resForce, err := ApplyHarnessConfig(tempDir, optsForce)
	if err != nil {
		t.Fatalf("apply with --force failed: %v", err)
	}
	if !resForce.Applied {
		t.Errorf("expected resForce.Applied == true")
	}
	if resForce.ContentHash == "" {
		t.Errorf("expected valid resealed content hash")
	}
}

// TestAdapter_BuildCollisionDiff verifies that BuildCollisionDiff accurately diffs
// the reconstructed recorded baseline against disk content.
func TestAdapter_BuildCollisionDiff(t *testing.T) {
	tempDir := t.TempDir()
	opts := AdapterOptions{
		Harness: "antigravity",
		Skills:  []string{"skill-alpha"},
		Exclude: []string{"*"},
	}
	res, err := ApplyHarnessConfig(tempDir, opts)
	if err != nil {
		t.Fatalf("apply failed: %v", err)
	}

	configPath := filepath.Join(tempDir, ".agents", "skills.json")
	data, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("read: %v", err)
	}

	var doc ManagedDocument
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	// Add an unmanaged key and mutate skill regex
	mutated := strings.Replace(string(data), `"^skill-alpha$"`, `"^skill-gamma$"`, 1)
	mutated = strings.Replace(mutated, "{\n", "{\n  \"custom_setting\": 42,\n", 1)

	diff := BuildCollisionDiff(configPath, &doc, opts, mutated)
	if diff == "" {
		t.Fatalf("expected non-empty collision diff")
	}
	if !strings.Contains(diff, "custom_setting") {
		t.Errorf("expected diff to show custom_setting, got:\n%s", diff)
	}
	if !strings.Contains(diff, "-        \"^skill-alpha$\"") || !strings.Contains(diff, "+        \"^skill-gamma$\"") {
		t.Errorf("expected diff to show regex replacement, got:\n%s", diff)
	}
	_ = res
}

// TestAdapter_ConcurrentFlockStress runs 20 concurrent goroutines executing config operations
// to verify advisory flock serialization and zero data races under -race.
func TestAdapter_ConcurrentFlockStress(t *testing.T) {
	tempDir := t.TempDir()
	var wg sync.WaitGroup
	workers := 20
	errChan := make(chan error, workers)

	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			opts := AdapterOptions{
				Harness: "antigravity",
				Skills:  []string{fmt.Sprintf("skill-%d", id%3)},
				Force:   true,
			}
			_, err := ApplyHarnessConfig(tempDir, opts)
			if err != nil {
				errChan <- fmt.Errorf("worker %d: %w", id, err)
			}
		}(i)
	}

	wg.Wait()
	close(errChan)

	for err := range errChan {
		t.Errorf("concurrent worker error: %v", err)
	}

	// Verify final file on disk is valid JSON with valid envelope
	configPath := filepath.Join(tempDir, ".agents", "skills.json")
	data, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("read final config: %v", err)
	}
	var doc ManagedDocument
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatalf("final config corrupted: %v", err)
	}
	if doc.Envelope == nil || !doc.Envelope.Managed {
		t.Fatalf("final config missing valid envelope")
	}
}

func assertContains(t *testing.T, slice []string, val string) {
	t.Helper()
	for _, s := range slice {
		if s == val {
			return
		}
	}
	t.Errorf("expected slice to contain %q, but got: %v", val, slice)
}

func TestHarnessCompatibility_AllSupportedHarnesses(t *testing.T) {
	harnesses := SupportedHarnesses()
	expected := []string{"antigravity", "claude-code", "codex", "opencode", "gemini"}
	if len(harnesses) != len(expected) {
		t.Fatalf("expected %d supported harnesses, got %d: %v", len(expected), len(harnesses), harnesses)
	}
	for _, exp := range expected {
		assertContains(t, harnesses, exp)
	}

	testCases := []struct {
		harness       string
		wantConfigDir string
		wantVaultSub  string
		wantSupported bool
	}{
		{harness: "antigravity", wantConfigDir: ".agents", wantVaultSub: filepath.Join(".gemini", "antigravity", "builtin", "skills"), wantSupported: true},
		{harness: "claude-code", wantConfigDir: ".claude", wantVaultSub: filepath.Join(".claude", "skills"), wantSupported: true},
		{harness: "claude", wantConfigDir: ".claude", wantVaultSub: filepath.Join(".claude", "skills"), wantSupported: true},
		{harness: "codex", wantConfigDir: ".agents", wantVaultSub: filepath.Join(".agents", "skills"), wantSupported: true},
		{harness: "opencode", wantConfigDir: ".opencode", wantVaultSub: filepath.Join(".config", "opencode", "skills"), wantSupported: true},
		{harness: "gemini", wantConfigDir: ".gemini", wantVaultSub: filepath.Join(".agents", "skills"), wantSupported: true},
		{harness: "unknown-harness", wantConfigDir: ".agents", wantVaultSub: filepath.Join(".agents", "skills"), wantSupported: false},
	}

	mockHome := "/mock/home/user"
	for _, tc := range testCases {
		t.Run(tc.harness, func(t *testing.T) {
			cfgDir, vault, ok := VerifyHarnessCompatibility(mockHome, tc.harness)
			if ok != tc.wantSupported {
				t.Errorf("harness %q supported=%v, want %v", tc.harness, ok, tc.wantSupported)
			}
			if cfgDir != tc.wantConfigDir {
				t.Errorf("harness %q configDir=%q, want %q", tc.harness, cfgDir, tc.wantConfigDir)
			}
			expectedVault := filepath.Join(mockHome, tc.wantVaultSub)
			if vault != expectedVault {
				t.Errorf("harness %q vault=%q, want %q", tc.harness, vault, expectedVault)
			}
		})
	}
}
