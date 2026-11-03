package assess

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestAuditCovenant_Compliant(t *testing.T) {
	tempDir := t.TempDir()
	storePath := filepath.Join(tempDir, "sila.db")
	_ = os.WriteFile(storePath, []byte("fake sqlite"), 0600)

	rep, err := AuditCovenant("../..", storePath)
	if err != nil {
		t.Fatalf("AuditCovenant failed: %v", err)
	}

	if !rep.Passed {
		t.Errorf("expected covenant audit to pass, got overall: %s", rep.Overall)
	}
	if rep.Overall != "COMPLIANT" {
		t.Errorf("expected COMPLIANT, got %s", rep.Overall)
	}
	if len(rep.Pillars) != 4 {
		t.Fatalf("expected 4 pillars, got %d", len(rep.Pillars))
	}

	for _, p := range rep.Pillars {
		if !p.Passed {
			t.Errorf("pillar %s failed: %s", p.Name, p.Evidence)
		}
	}

	toon := RenderCovenantTOON(rep)
	if !strings.Contains(toon, "covenant_summary:") {
		t.Errorf("expected TOON to contain covenant_summary, got:\n%s", toon)
	}
	if !strings.Contains(toon, "local_custody") {
		t.Errorf("expected TOON to contain local_custody, got:\n%s", toon)
	}
}

func TestAuditCovenant_RemoteStoreDetection(t *testing.T) {
	rep, err := AuditCovenant(".", "https://remote-cloud-db.internal/db.sqlite")
	if err != nil {
		t.Fatalf("AuditCovenant failed: %v", err)
	}

	if rep.Passed {
		t.Errorf("expected remote store path to fail local custody pillar")
	}
	if rep.Overall != "NON_COMPLIANT" {
		t.Errorf("expected NON_COMPLIANT, got %s", rep.Overall)
	}

	found := false
	for _, p := range rep.Pillars {
		if p.ID == "local_custody" {
			found = true
			if p.Passed {
				t.Errorf("expected local_custody pillar to fail on remote store path")
			}
			if !strings.Contains(p.Evidence, "remote endpoint") {
				t.Errorf("expected evidence to mention remote endpoint, got: %s", p.Evidence)
			}
		}
	}
	if !found {
		t.Errorf("local_custody pillar not found in report")
	}
}

func TestAuditCovenant_MissingStoreFile_FailsClosed(t *testing.T) {
	nonExistentPath := filepath.Join(t.TempDir(), "nonexistent_store.db")
	rep, err := AuditCovenant(".", nonExistentPath)
	if err != nil {
		t.Fatalf("AuditCovenant failed: %v", err)
	}

	if rep.Passed {
		t.Errorf("expected missing store file to fail covenant audit")
	}
	if rep.Overall != "NON_COMPLIANT" {
		t.Errorf("expected NON_COMPLIANT, got %s", rep.Overall)
	}

	for _, p := range rep.Pillars {
		if p.ID == "local_custody" {
			if p.Passed {
				t.Errorf("expected local_custody to fail when store does not exist")
			}
			if !strings.Contains(p.Evidence, "does not exist") {
				t.Errorf("expected evidence to mention 'does not exist', got: %s", p.Evidence)
			}
		}
		if p.ID == "cognitive_interoperability" {
			if p.Passed {
				t.Errorf("expected cognitive_interoperability to fail when store does not exist")
			}
			if !strings.Contains(p.Evidence, "does not exist") {
				t.Errorf("expected evidence to mention 'does not exist', got: %s", p.Evidence)
			}
		}
	}
}

func TestAuditCovenant_EmptyStorePath_FailsClosed(t *testing.T) {
	rep, err := AuditCovenant(".", "   ")
	if err != nil {
		t.Fatalf("AuditCovenant failed: %v", err)
	}

	if rep.Passed {
		t.Errorf("expected empty store path to fail covenant audit")
	}
	if rep.Overall != "NON_COMPLIANT" {
		t.Errorf("expected NON_COMPLIANT, got %s", rep.Overall)
	}
}

func TestAuditCovenant_DirectoryStorePath_FailsClosed(t *testing.T) {
	dir := t.TempDir()
	rep, err := AuditCovenant(".", dir)
	if err != nil {
		t.Fatalf("AuditCovenant failed: %v", err)
	}

	if rep.Passed {
		t.Errorf("expected directory store path to fail covenant audit")
	}
	if rep.Overall != "NON_COMPLIANT" {
		t.Errorf("expected NON_COMPLIANT, got %s", rep.Overall)
	}
}
