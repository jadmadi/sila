package goals

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestTaskPacketDispatchValidation(t *testing.T) {
	tmpDir, slug, pkg := setupTaskPacketFixture(t)

	// Create an artifact file for dependency testing
	artifactPath := filepath.Join(tmpDir, "predecessor_artifact.txt")
	artifactContent := "Predecessor artifact payload"
	if err := os.WriteFile(artifactPath, []byte(artifactContent), 0644); err != nil {
		t.Fatalf("write artifact: %v", err)
	}
	hArtifact := sha256.Sum256([]byte(artifactContent))
	artifactSHA := hex.EncodeToString(hArtifact[:])

	// Build the initial valid packet
	packet, err := BuildTaskPacket(tmpDir, slug, "T-001")
	if err != nil {
		t.Fatalf("BuildTaskPacket failed: %v", err)
	}

	// Attach dependency output to test dependency drift
	packet.DependencyOutputs = []DependencyOutput{
		{
			GoalSlug: slug,
			TaskID:   "T-000",
			Artifact: "predecessor_artifact.txt",
			SHA256:   artifactSHA,
		},
	}
	// Recompute packet digest with dependency output
	newDigest, err := ComputeTaskPacketDigest(packet)
	if err != nil {
		t.Fatalf("recompute digest: %v", err)
	}
	packet.PacketDigest = newDigest

	// Snapshot original packet JSON to verify immutability (AC-012)
	originalPacketJSON, err := json.Marshal(packet)
	if err != nil {
		t.Fatalf("marshal original packet: %v", err)
	}

	// 1. Clean validation: unchanged repository state -> zero diagnostics
	diags := ValidateTaskPacket(tmpDir, packet)
	if len(diags) != 0 {
		t.Fatalf("expected 0 diagnostics for clean repository state, got: %+v", diags)
	}

	// Verify AC-012: packet was not mutated by validation
	postValidationJSON, _ := json.Marshal(packet)
	if string(originalPacketJSON) != string(postValidationJSON) {
		t.Fatalf("AC-012 violation: packet was mutated during validation")
	}

	// 2. AC-011: Unrelated unpinned file edit does NOT invalidate the packet
	readmePath := filepath.Join(tmpDir, "README.md")
	if err := os.WriteFile(readmePath, []byte("# New Unrelated Title\n"), 0644); err != nil {
		t.Fatalf("write README.md: %v", err)
	}
	diags = ValidateTaskPacket(tmpDir, packet)
	if len(diags) != 0 {
		t.Errorf("AC-011 violation: unpinned README edit caused unexpected diagnostics: %+v", diags)
	}

	// 3. AC-010: Edit a pinned file -> PACKET_SOURCE_STALE
	engineFile := filepath.Join(tmpDir, "engine.go")
	if err := os.WriteFile(engineFile, []byte("package engine\n// Modified content\ntype Core struct { Capacity int }\n"), 0644); err != nil {
		t.Fatalf("modify engine.go: %v", err)
	}
	diags = ValidateTaskPacket(tmpDir, packet)
	var foundSourceStale bool
	for _, d := range diags {
		if d.Code == DiagPacketSourceStale && d.Path == "engine.go" {
			foundSourceStale = true
			break
		}
	}
	if !foundSourceStale {
		t.Errorf("AC-010 violation: expected PACKET_SOURCE_STALE on engine.go, got: %+v", diags)
	}

	// Restore engine.go to clean state
	origEngine := "package engine\n\ntype Core struct { Capacity int }\n"
	_ = os.WriteFile(engineFile, []byte(origEngine), 0644)

	// 4. AC-010: Edit package -> PACKET_SPEC_STALE
	pkg.Purpose = "Altered purpose causing spec digest change"
	pkgData, err := json.MarshalIndent(pkg, "", "  ")
	if err != nil {
		t.Fatalf("marshal modified pkg: %v", err)
	}
	wpPath := filepath.Join(tmpDir, "goals", slug, "work-package.json")
	if err := os.WriteFile(wpPath, pkgData, 0644); err != nil {
		t.Fatalf("write modified work-package.json: %v", err)
	}
	diags = ValidateTaskPacket(tmpDir, packet)
	var foundSpecStale bool
	for _, d := range diags {
		if d.Code == DiagPacketSpecStale {
			foundSpecStale = true
			break
		}
	}
	if !foundSpecStale {
		t.Errorf("AC-010 violation: expected PACKET_SPEC_STALE on modified package, got: %+v", diags)
	}

	// Restore original work-package.json
	pkg.Purpose = "Deliver deterministic engine execution packets"
	origPkgData, _ := json.MarshalIndent(pkg, "", "  ")
	_ = os.WriteFile(wpPath, origPkgData, 0644)

	// 5. AC-011: Delete predecessor artifact -> PACKET_DEPENDENCY_STALE
	if err := os.Remove(artifactPath); err != nil {
		t.Fatalf("remove artifact: %v", err)
	}
	diags = ValidateTaskPacket(tmpDir, packet)
	var foundDepStale bool
	for _, d := range diags {
		if d.Code == DiagPacketDependencyStale && d.Path == "predecessor_artifact.txt" {
			foundDepStale = true
			break
		}
	}
	if !foundDepStale {
		t.Errorf("AC-011 violation: expected PACKET_DEPENDENCY_STALE on deleted artifact, got: %+v", diags)
	}
}
