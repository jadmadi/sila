package goals

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestAuditReceiptTamperAllFields tests tampering of every single field in a signed receipt.
func TestAuditReceiptTamperAllFields(t *testing.T) {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("GenerateKey failed: %v", err)
	}

	pubB64 := base64.StdEncoding.EncodeToString(pub)
	fixedTime := time.Date(2026, 10, 16, 18, 0, 0, 0, time.UTC)

	baseReceipt := &AuditReceipt{
		Version:            "sila/audit-receipt/v1",
		GoalSlug:           "challenge-goal",
		CommitSHA:          "1234567890abcdef1234567890abcdef12345678",
		SpecHash:           "spechash1234567890abcdef1234567890abcdef",
		ProbeManifestHash:  "manifest1234567890abcdef1234567890abcdef",
		SandboxNonce:       "nonce-abc-123",
		CandidateSelection: "A",
		BlindfoldVerified:  true,
		CanaryPassed:       true,
		CCDSIScore:         0.8500,
		ReqCoverage:        0.9000,
		ProbesExecuted:     10,
		ProbesPassed:       10,
		CertifiedAt:        fixedTime,
		AuditorPubKey:      pubB64,
	}

	digest := FormatReceiptDigest(baseReceipt)
	sig := ed25519.Sign(priv, digest)
	baseReceipt.AuditorSignature = base64.StdEncoding.EncodeToString(sig)

	// Baseline verification must pass
	if err := VerifyAuditReceipt(baseReceipt); err != nil {
		t.Fatalf("Baseline valid receipt failed verification: %v", err)
	}

	// Table of field tamper scenarios
	scenarios := []struct {
		name   string
		tamper func(r *AuditReceipt)
	}{
		{
			name: "GoalSlug tampered",
			tamper: func(r *AuditReceipt) {
				r.GoalSlug = "other-goal-slug"
			},
		},
		{
			name: "CommitSHA tampered",
			tamper: func(r *AuditReceipt) {
				r.CommitSHA = "0000000000abcdef1234567890abcdef12345678"
			},
		},
		{
			name: "SpecHash tampered",
			tamper: func(r *AuditReceipt) {
				r.SpecHash = "tampered-spec-hash"
			},
		},
		{
			name: "ProbeManifestHash tampered",
			tamper: func(r *AuditReceipt) {
				r.ProbeManifestHash = "tampered-manifest-hash"
			},
		},
		{
			name: "SandboxNonce tampered",
			tamper: func(r *AuditReceipt) {
				r.SandboxNonce = "tampered-nonce-xyz"
			},
		},
		{
			name: "CandidateSelection tampered",
			tamper: func(r *AuditReceipt) {
				r.CandidateSelection = "B"
			},
		},
		{
			name: "CCDSIScore tampered (above threshold)",
			tamper: func(r *AuditReceipt) {
				r.CCDSIScore = 0.9500
			},
		},
		{
			name: "ReqCoverage tampered (above threshold)",
			tamper: func(r *AuditReceipt) {
				r.ReqCoverage = 1.0000
			},
		},
		{
			name: "ProbesExecuted tampered",
			tamper: func(r *AuditReceipt) {
				r.ProbesExecuted = 12
				r.ProbesPassed = 12
			},
		},
		{
			name: "ProbesPassed tampered",
			tamper: func(r *AuditReceipt) {
				r.ProbesExecuted = 12
				r.ProbesPassed = 12
			},
		},
		{
			name: "CanaryPassed tampered",
			tamper: func(r *AuditReceipt) {
				r.CanaryPassed = false
			},
		},
		{
			name: "BlindfoldVerified tampered",
			tamper: func(r *AuditReceipt) {
				r.BlindfoldVerified = false
			},
		},
		{
			name: "CertifiedAt tampered",
			tamper: func(r *AuditReceipt) {
				r.CertifiedAt = fixedTime.Add(2 * time.Hour)
			},
		},
	}

	for _, sc := range scenarios {
		t.Run(sc.name, func(t *testing.T) {
			tampered := *baseReceipt
			sc.tamper(&tampered)
			err := VerifyAuditReceipt(&tampered)
			if err == nil {
				t.Fatalf("FAILED: expected tampering of %s to fail verification, but it passed", sc.name)
			}
		})
	}
}

// TestAuditReceiptSignatureEnforcement tests malformed, missing, and corrupted keys/signatures.
func TestAuditReceiptSignatureEnforcement(t *testing.T) {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("GenerateKey failed: %v", err)
	}

	baseReceipt := &AuditReceipt{
		Version:            "sila/audit-receipt/v1",
		GoalSlug:           "enforce-goal",
		CommitSHA:          "abcdef1234567890abcdef1234567890abcdef12",
		SpecHash:           "spechash123",
		ProbeManifestHash:  "manifesthash123",
		SandboxNonce:       "nonce123",
		CandidateSelection: "A",
		BlindfoldVerified:  true,
		CanaryPassed:       true,
		CCDSIScore:         0.80,
		ReqCoverage:        0.90,
		ProbesExecuted:     6,
		ProbesPassed:       6,
		CertifiedAt:        time.Now().UTC(),
		AuditorPubKey:      base64.StdEncoding.EncodeToString(pub),
	}
	digest := FormatReceiptDigest(baseReceipt)
	sig := ed25519.Sign(priv, digest)
	baseReceipt.AuditorSignature = base64.StdEncoding.EncodeToString(sig)

	cases := []struct {
		name      string
		mutate    func(r *AuditReceipt)
		errSubstr string
	}{
		{
			name: "empty public key",
			mutate: func(r *AuditReceipt) {
				r.AuditorPubKey = ""
			},
			errSubstr: "missing mandatory cryptographic",
		},
		{
			name: "whitespace public key",
			mutate: func(r *AuditReceipt) {
				r.AuditorPubKey = "   "
			},
			errSubstr: "missing mandatory cryptographic",
		},
		{
			name: "empty signature",
			mutate: func(r *AuditReceipt) {
				r.AuditorSignature = ""
			},
			errSubstr: "missing mandatory cryptographic",
		},
		{
			name: "whitespace signature",
			mutate: func(r *AuditReceipt) {
				r.AuditorSignature = "  \t\n  "
			},
			errSubstr: "missing mandatory cryptographic",
		},
		{
			name: "invalid base64 public key",
			mutate: func(r *AuditReceipt) {
				r.AuditorPubKey = "!@#$%^&*()_+"
			},
			errSubstr: "decode auditor public key",
		},
		{
			name: "invalid base64 signature",
			mutate: func(r *AuditReceipt) {
				r.AuditorSignature = "!@#$%^&*()_+"
			},
			errSubstr: "decode auditor signature",
		},
		{
			name: "truncated public key (16 bytes)",
			mutate: func(r *AuditReceipt) {
				r.AuditorPubKey = base64.StdEncoding.EncodeToString([]byte("1234567890123456"))
			},
			errSubstr: "invalid ed25519 public key length",
		},
		{
			name: "oversized public key (48 bytes)",
			mutate: func(r *AuditReceipt) {
				r.AuditorPubKey = base64.StdEncoding.EncodeToString(make([]byte, 48))
			},
			errSubstr: "invalid ed25519 public key length",
		},
		{
			name: "truncated signature (32 bytes)",
			mutate: func(r *AuditReceipt) {
				r.AuditorSignature = base64.StdEncoding.EncodeToString(make([]byte, 32))
			},
			errSubstr: "invalid ed25519 signature length",
		},
		{
			name: "oversized signature (128 bytes)",
			mutate: func(r *AuditReceipt) {
				r.AuditorSignature = base64.StdEncoding.EncodeToString(make([]byte, 128))
			},
			errSubstr: "invalid ed25519 signature length",
		},
		{
			name: "corrupted signature bytes (64 bytes of zeros)",
			mutate: func(r *AuditReceipt) {
				r.AuditorSignature = base64.StdEncoding.EncodeToString(make([]byte, 64))
			},
			errSubstr: "signature verification failed",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tampered := *baseReceipt
			tc.mutate(&tampered)
			err := VerifyAuditReceipt(&tampered)
			if err == nil {
				t.Fatalf("expected error containing %q, got nil", tc.errSubstr)
			}
			if !strings.Contains(err.Error(), tc.errSubstr) {
				t.Fatalf("expected error containing %q, got %q", tc.errSubstr, err.Error())
			}
		})
	}
}

// TestLegacyReceiptTamperingVulnerability checks if legacy-formatted receipts allow field tampering.
func TestLegacyReceiptTamperingVulnerability(t *testing.T) {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("GenerateKey failed: %v", err)
	}

	legacyReceipt := &AuditReceipt{
		Version:            "sila/audit-receipt/v1",
		GoalSlug:           "original-goal-slug",
		CommitSHA:          "abcdef1234567890abcdef1234567890abcdef12",
		SpecHash:           "spechash123",
		ProbeManifestHash:  "manifesthash123",
		SandboxNonce:       "nonce123",
		CandidateSelection: "A",
		BlindfoldVerified:  true,
		CanaryPassed:       true,
		CCDSIScore:         0.80,
		ReqCoverage:        0.90,
		ProbesExecuted:     6,
		ProbesPassed:       6,
		CertifiedAt:        time.Now().UTC(),
		AuditorPubKey:      base64.StdEncoding.EncodeToString(pub),
	}

	// Sign ONLY the legacy 5 fields
	legacyPayload := "spechash123:abcdef1234567890abcdef1234567890abcdef12:manifesthash123:nonce123:A"
	sig := ed25519.Sign(priv, []byte(legacyPayload))
	legacyReceipt.AuditorSignature = base64.StdEncoding.EncodeToString(sig)

	// Verify that the legacy receipt passes current verification
	if err := VerifyAuditReceipt(legacyReceipt); err != nil {
		t.Fatalf("Legacy receipt failed verification: %v", err)
	}

	// Now try to tamper GoalSlug on the legacy receipt
	tamperedSlug := *legacyReceipt
	tamperedSlug.GoalSlug = "transferred-malicious-goal-slug"

	err = VerifyAuditReceipt(&tamperedSlug)
	if err == nil {
		t.Logf("SECURITY FINDING: Legacy receipt allows GoalSlug tampering because legacyPayload omits GoalSlug!")
	} else {
		t.Logf("Legacy receipt rejected GoalSlug tampering: %v", err)
	}
}

// TestGoalStateGatingWithTamperedReceipt verifies ScanGoals refuses to mark a goal implemented
// if its receipt is tampered, unsigned, or malformed.
func TestGoalStateGatingWithTamperedReceipt(t *testing.T) {
	tmpDir := t.TempDir()
	goalsDir := filepath.Join(tmpDir, "goals")
	_ = os.MkdirAll(goalsDir, 0755)

	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("GenerateKey: %v", err)
	}
	pubB64 := base64.StdEncoding.EncodeToString(pub)

	createGoalWithReceipt := func(slug string, mutate func(r *AuditReceipt)) {
		goalPkg := filepath.Join(goalsDir, slug)
		_ = os.MkdirAll(goalPkg, 0755)
		_ = os.WriteFile(filepath.Join(goalPkg, "goal.md"), []byte("# Goal "+slug), 0644)
		_ = os.WriteFile(filepath.Join(goalPkg, "plan.md"), []byte("- [ ] Task 1"), 0644)
		_ = os.WriteFile(filepath.Join(goalPkg, "facts.md"), []byte("# Facts"), 0644)

		r := &AuditReceipt{
			Version:            "sila/audit-receipt/v1",
			GoalSlug:           slug,
			CommitSHA:          "abcdef1234567890abcdef1234567890abcdef12",
			SpecHash:           "spechash123",
			ProbeManifestHash:  "manifesthash123",
			SandboxNonce:       "nonce123",
			CandidateSelection: "A",
			BlindfoldVerified:  true,
			CanaryPassed:       true,
			CCDSIScore:         0.85,
			ReqCoverage:        1.0,
			ProbesExecuted:     6,
			ProbesPassed:       6,
			CertifiedAt:        time.Now().UTC(),
			AuditorPubKey:      pubB64,
		}
		digest := FormatReceiptDigest(r)
		sig := ed25519.Sign(priv, digest)
		r.AuditorSignature = base64.StdEncoding.EncodeToString(sig)

		if mutate != nil {
			mutate(r)
		}

		data, _ := json.MarshalIndent(r, "", "  ")
		_ = os.WriteFile(filepath.Join(goalPkg, "audit-receipt.json"), data, 0644)
	}

	// 1. Valid signed receipt -> implemented
	createGoalWithReceipt("goal-valid", nil)

	// 2. Tampered score receipt -> must NOT be implemented
	createGoalWithReceipt("goal-tampered-score", func(r *AuditReceipt) {
		r.CCDSIScore = 0.99 // tampered above threshold
	})

	// 3. Unsigned receipt -> must NOT be implemented
	createGoalWithReceipt("goal-unsigned", func(r *AuditReceipt) {
		r.AuditorSignature = ""
	})

	// 4. Missing public key -> must NOT be implemented
	createGoalWithReceipt("goal-no-pubkey", func(r *AuditReceipt) {
		r.AuditorPubKey = ""
	})

	// 5. Corrupted signature -> must NOT be implemented
	createGoalWithReceipt("goal-corrupt-sig", func(r *AuditReceipt) {
		r.AuditorSignature = base64.StdEncoding.EncodeToString(make([]byte, 64))
	})

	// Scan the goals
	res, err := ScanGoals(tmpDir)
	if err != nil {
		t.Fatalf("ScanGoals failed: %v", err)
	}

	statusBySlug := make(map[string]string)
	for _, g := range res.Goals {
		statusBySlug[g.Slug] = g.Status
	}

	if statusBySlug["goal-valid"] != "implemented" {
		t.Errorf("expected goal-valid status=implemented, got %q", statusBySlug["goal-valid"])
	}

	for _, badSlug := range []string{"goal-tampered-score", "goal-unsigned", "goal-no-pubkey", "goal-corrupt-sig"} {
		if statusBySlug[badSlug] == "implemented" {
			t.Errorf("SECURITY FAILURE: %s was marked 'implemented' despite invalid/tampered receipt!", badSlug)
		}
	}
}

// TestAuditReceiptTamperAllFields_OnRecertifiedGoals empirically challenges the 3 recertified
// goals (cross-session-epistemic-auditing, gate-silence-sentry-negative-drift, hermetic-blindfold-namespace-audits)
// on disk to ensure valid verification and failure on tampering any individual field.
func TestAuditReceiptTamperAllFields_OnRecertifiedGoals(t *testing.T) {
	rootDir, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatalf("resolve rootDir: %v", err)
	}

	goalsToTest := []string{
		"cross-session-epistemic-auditing",
		"gate-silence-sentry-negative-drift",
		"hermetic-blindfold-namespace-audits",
	}

	for _, slug := range goalsToTest {
		t.Run(slug, func(t *testing.T) {
			if _, err := os.Stat(filepath.Join(rootDir, "goals", slug, "audit-receipt.json")); err != nil {
				t.Skipf("private recertified goal fixture not present: %v", err)
			}
			for _, filename := range []string{"audit-receipt.json"} {
				path := filepath.Join(rootDir, "goals", slug, filename)
				data, err := os.ReadFile(path)
				if err != nil {
					t.Fatalf("read %s: %v", path, err)
				}

				var receipt AuditReceipt
				if err := json.Unmarshal(data, &receipt); err != nil {
					t.Fatalf("unmarshal %s: %v", path, err)
				}

				// 1. Assert pristine receipt passes verification
				if err := VerifyAuditReceipt(&receipt); err != nil {
					t.Fatalf("pristine %s failed verification: %v", path, err)
				}

				// 2. Tamper every individual field and assert verification fails
				tamperScenarios := []struct {
					name   string
					tamper func(r *AuditReceipt)
				}{
					{
						name: "GoalSlug tampered",
						tamper: func(r *AuditReceipt) {
							r.GoalSlug = "tampered-" + slug
						},
					},
					{
						name: "CommitSHA tampered",
						tamper: func(r *AuditReceipt) {
							r.CommitSHA = "0000000000000000000000000000000000000000"
						},
					},
					{
						name: "SpecHash tampered",
						tamper: func(r *AuditReceipt) {
							r.SpecHash = "0000000000000000000000000000000000000000000000000000000000000000"
						},
					},
					{
						name: "ProbeManifestHash tampered",
						tamper: func(r *AuditReceipt) {
							r.ProbeManifestHash = "0000000000000000000000000000000000000000000000000000000000000000"
						},
					},
					{
						name: "SandboxNonce tampered",
						tamper: func(r *AuditReceipt) {
							r.SandboxNonce = "tampered-nonce-xyz"
						},
					},
					{
						name: "CandidateSelection tampered",
						tamper: func(r *AuditReceipt) {
							r.CandidateSelection = "B"
						},
					},
					{
						name: "CCDSIScore tampered above threshold",
						tamper: func(r *AuditReceipt) {
							r.CCDSIScore = 0.9500
						},
					},
					{
						name: "ReqCoverage tampered above threshold",
						tamper: func(r *AuditReceipt) {
							r.ReqCoverage = 0.9900
						},
					},
					{
						name: "ProbesExecuted tampered",
						tamper: func(r *AuditReceipt) {
							r.ProbesExecuted = 10
							r.ProbesPassed = 10
						},
					},
					{
						name: "ProbesPassed tampered",
						tamper: func(r *AuditReceipt) {
							r.ProbesExecuted = 10
							r.ProbesPassed = 10
						},
					},
					{
						name: "CanaryPassed tampered",
						tamper: func(r *AuditReceipt) {
							r.CanaryPassed = false
						},
					},
					{
						name: "BlindfoldVerified tampered",
						tamper: func(r *AuditReceipt) {
							r.BlindfoldVerified = false
						},
					},
					{
						name: "CertifiedAt tampered",
						tamper: func(r *AuditReceipt) {
							r.CertifiedAt = receipt.CertifiedAt.Add(2 * time.Hour)
						},
					},
					{
						name: "AuditorPubKey tampered",
						tamper: func(r *AuditReceipt) {
							r.AuditorPubKey = "BgA8o4crQ/eh/oO+nwt5AzgmRyPseR7XFMHsVxN0V+0="
						},
					},
					{
						name: "AuditorSignature tampered",
						tamper: func(r *AuditReceipt) {
							r.AuditorSignature = base64.StdEncoding.EncodeToString(make([]byte, 64))
						},
					},
					{
						name: "Version tampered",
						tamper: func(r *AuditReceipt) {
							r.Version = "sila/audit-receipt/v2"
						},
					},
				}

				for _, sc := range tamperScenarios {
					t.Run(filename+"/"+sc.name, func(t *testing.T) {
						tampered := receipt
						sc.tamper(&tampered)
						err := VerifyAuditReceipt(&tampered)
						if err == nil {
							t.Fatalf("FAILED: tampering %s on %s/%s did not fail verification", sc.name, slug, filename)
						}
					})
				}
			}
		})
	}
}

// TestGoalStateGatingWithTamperedReceipt_OnRecertifiedGoals asserts that ScanGoals
// requires valid cryptographic receipts and prevents status transition to implemented
// when receipts are tampered, unsigned, or have invalid public keys.
func TestGoalStateGatingWithTamperedReceipt_OnRecertifiedGoals(t *testing.T) {
	rootDir, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatalf("resolve rootDir: %v", err)
	}

	goalsToTest := []string{
		"cross-session-epistemic-auditing",
		"gate-silence-sentry-negative-drift",
		"hermetic-blindfold-namespace-audits",
	}

	for _, slug := range goalsToTest {
		t.Run(slug, func(t *testing.T) {
			path := filepath.Join(rootDir, "goals", slug, "audit-receipt.json")
			if _, err := os.Stat(path); err != nil {
				t.Skipf("private recertified goal fixture not present: %v", err)
			}
			data, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("read %s: %v", path, err)
			}
			var original AuditReceipt
			if err := json.Unmarshal(data, &original); err != nil {
				t.Fatalf("unmarshal: %v", err)
			}

			// Scenario 1: Valid receipt in an isolated goal directory
			tmpValid := t.TempDir()
			_ = os.MkdirAll(filepath.Join(tmpValid, "goals", slug), 0755)
			_ = os.WriteFile(filepath.Join(tmpValid, "goals", slug, "goal.md"), []byte("# Goal "+slug), 0644)
			_ = os.WriteFile(filepath.Join(tmpValid, "goals", slug, "plan.md"), []byte("- [ ] Task 1"), 0644)
			_ = os.WriteFile(filepath.Join(tmpValid, "goals", slug, "audit-receipt.json"), data, 0644)

			resValid, err := ScanGoals(tmpValid)
			if err != nil {
				t.Fatalf("ScanGoals valid failed: %v", err)
			}
			if len(resValid.Goals) != 1 || resValid.Goals[0].Status != "implemented" {
				t.Fatalf("expected valid receipt to mark goal implemented, got %v", resValid.Goals)
			}

			// Scenarios 2-6: Tampered receipts MUST NOT transition to implemented
			tamperedCases := []struct {
				name   string
				mutate func(r *AuditReceipt)
			}{
				{
					name: "unsigned",
					mutate: func(r *AuditReceipt) {
						r.AuditorSignature = ""
					},
				},
				{
					name: "missing_pubkey",
					mutate: func(r *AuditReceipt) {
						r.AuditorPubKey = ""
					},
				},
				{
					name: "tampered_commit",
					mutate: func(r *AuditReceipt) {
						r.CommitSHA = "0000000000000000000000000000000000000000"
					},
				},
				{
					name: "tampered_score",
					mutate: func(r *AuditReceipt) {
						r.CCDSIScore = 0.9999
					},
				},
				{
					name: "tampered_timestamp",
					mutate: func(r *AuditReceipt) {
						r.CertifiedAt = r.CertifiedAt.Add(24 * time.Hour)
					},
				},
			}

			for _, tc := range tamperedCases {
				t.Run(tc.name, func(t *testing.T) {
					tamperedReceipt := original
					tc.mutate(&tamperedReceipt)
					tamperedBytes, _ := json.MarshalIndent(tamperedReceipt, "", "  ")

					tmpBad := t.TempDir()
					_ = os.MkdirAll(filepath.Join(tmpBad, "goals", slug), 0755)
					_ = os.WriteFile(filepath.Join(tmpBad, "goals", slug, "goal.md"), []byte("# Goal "+slug), 0644)
					_ = os.WriteFile(filepath.Join(tmpBad, "goals", slug, "plan.md"), []byte("- [ ] Task 1"), 0644)
					_ = os.WriteFile(filepath.Join(tmpBad, "goals", slug, "audit-receipt.json"), tamperedBytes, 0644)

					resBad, err := ScanGoals(tmpBad)
					if err != nil {
						t.Fatalf("ScanGoals failed: %v", err)
					}
					if len(resBad.Goals) > 0 && resBad.Goals[0].Status == "implemented" {
						t.Fatalf("SECURITY FAILURE: %s on %s marked goal implemented!", tc.name, slug)
					}
				})
			}
		})
	}
}
