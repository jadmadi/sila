package assess

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"testing"
)

func TestProbeDistinctness_EmpiricalChallenge(t *testing.T) {
	rootDir, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatalf("resolve rootDir: %v", err)
	}

	targets := []string{
		"cross-session-epistemic-auditing",
		"gate-silence-sentry-negative-drift",
		"hermetic-blindfold-namespace-audits",
	}

	manifests := make(map[string]string)
	hashes := make(map[string]string)
	scores := make(map[string]float64)
	coverages := make(map[string]float64)

	for _, slug := range targets {
		goalMdPath := filepath.Join(rootDir, "goals", slug, "goal.md")
		if _, err := os.Stat(goalMdPath); err != nil {
			t.Skipf("private goal fixture not present: %v", err)
		}
		contract, err := ExtractStrippedContract(goalMdPath)
		if err != nil {
			t.Fatalf("ExtractStrippedContract failed for %s: %v", slug, err)
		}

		probes := SynthesizeProbes(contract, SurfaceCLIBinary, "sila")
		if len(probes) < 6 {
			t.Fatalf("goal %s: expected >= 6 probes, got %d", slug, len(probes))
		}

		var results []ProbeResult
		for _, p := range probes {
			results = append(results, ProbeResult{
				ProbeID:       p.ID,
				Type:          p.Type,
				RequirementID: p.RequirementID,
				Passed:        true,
			})
		}

		manifest := CalculateProbeManifest(results)
		manifestHash := CalculateProbeManifestHash(results)

		// Also compute raw SHA256 of manifest text to verify CalculateProbeManifestHash
		rawSum := sha256.Sum256([]byte(manifest))
		rawHash := hex.EncodeToString(rawSum[:])
		if rawHash != manifestHash {
			t.Fatalf("goal %s: raw sha256 %s != CalculateProbeManifestHash %s", slug, rawHash, manifestHash)
		}

		ccdsi := CalculateCCDSI(results, contract.Requirements)
		if !ccdsi.GatingPassed {
			t.Fatalf("goal %s: CCDSI gating failed: %s", slug, ccdsi.FailureReason)
		}

		t.Logf("Goal: %s", slug)
		t.Logf("  Probes total: %d (neg=%d, bnd=%d, conc=%d, hap=%d)",
			ccdsi.TotalProbes, ccdsi.NegativeProbes, ccdsi.BoundaryProbes, ccdsi.ConcurrencyProbes, ccdsi.PassedProbes-ccdsi.NegativeProbes-ccdsi.BoundaryProbes-ccdsi.ConcurrencyProbes)
		t.Logf("  CCDSI Score: %.4f (min required: 0.40)", ccdsi.CCDSIScore)
		t.Logf("  Requirement Coverage: %.4f (min required: 0.50)", ccdsi.ReqCoverage)
		t.Logf("  Manifest Hash: %s", manifestHash)
		t.Logf("  Manifest Text:\n%s", manifest)

		// Assert CCDSI >= 0.40
		if ccdsi.CCDSIScore < 0.40 {
			t.Errorf("goal %s: CCDSI score %.4f < 0.40", slug, ccdsi.CCDSIScore)
		}

		// Assert Coverage >= 0.50
		if ccdsi.ReqCoverage < 0.50 {
			t.Errorf("goal %s: requirement coverage %.4f < 0.50", slug, ccdsi.ReqCoverage)
		}

		manifests[slug] = manifest
		hashes[slug] = manifestHash
		scores[slug] = ccdsi.CCDSIScore
		coverages[slug] = ccdsi.ReqCoverage
	}

	// Pairwise distinctness check across all pairs of goals
	for i := 0; i < len(targets); i++ {
		for j := i + 1; j < len(targets); j++ {
			g1 := targets[i]
			g2 := targets[j]

			if manifests[g1] == manifests[g2] {
				t.Errorf("COLLISION: probe manifest for %s and %s are identical:\n%s", g1, g2, manifests[g1])
			}
			if hashes[g1] == hashes[g2] {
				t.Errorf("COLLISION: probe manifest hash for %s and %s are identical: %s", g1, g2, hashes[g1])
			}
		}
	}
}
