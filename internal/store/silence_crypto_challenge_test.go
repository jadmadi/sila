package store

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestSilenceCryptographicOverrideAdversarialChallenge probes IsSilenceOverridden and
// VerifyOverrideProof against cryptographic tampering, malformed inputs, and forged proofs.
func TestSilenceCryptographicOverrideAdversarialChallenge(t *testing.T) {
	tmp := t.TempDir()
	dbPath := filepath.Join(tmp, "crypto_challenge_m3_2.db")
	s, err := Open(dbPath)
	if err != nil {
		t.Fatalf("Open failed: %v", err)
	}
	defer s.Close()

	proj, err := s.EnsureProject("/work/adversarial-crypto")
	if err != nil {
		t.Fatalf("EnsureProject failed: %v", err)
	}
	projOther, err := s.EnsureProject("/work/adversarial-other-proj")
	if err != nil {
		t.Fatalf("EnsureProject projOther failed: %v", err)
	}

	sig := "sig_adversarial_challenge_001"
	reason := "Approved architectural migration by security lead"
	actor := "security_auditor"
	now := time.Now().UTC().Format(time.RFC3339)

	pub1, priv1, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("GenerateKey pub1/priv1 failed: %v", err)
	}
	pub2, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("GenerateKey pub2 failed: %v", err)
	}

	authenticProof, err := GenerateEd25519OverrideProof(proj.ID, sig, reason, actor, now, priv1)
	if err != nil {
		t.Fatalf("GenerateEd25519OverrideProof failed: %v", err)
	}

	t.Run("Authentic Ed25519 proof accepted", func(t *testing.T) {
		ov := SilenceOverride{
			Signature:      sig,
			ProjectID:      proj.ID,
			Reason:         reason,
			Actor:          actor,
			SignatureProof: authenticProof,
			CreatedAt:      now,
		}
		if err := s.RecordSilenceOverride(ov); err != nil {
			t.Fatalf("RecordSilenceOverride failed: %v", err)
		}

		overridden, err := s.IsSilenceOverridden(proj.ID, sig)
		if err != nil {
			t.Fatalf("IsSilenceOverridden failed: %v", err)
		}
		if !overridden {
			t.Errorf("expected authentic override to be accepted")
		}

		valid, err := VerifyOverrideProof(ov)
		if err != nil || !valid {
			t.Errorf("expected VerifyOverrideProof true, got %v, err: %v", valid, err)
		}
	})

	t.Run("Empty and whitespace signature proof rejected", func(t *testing.T) {
		emptyCases := []string{
			"",
			" ",
			"\t\n",
			"   \r\n   ",
		}
		for _, emptyProof := range emptyCases {
			ov := SilenceOverride{
				Signature:      sig,
				ProjectID:      proj.ID,
				Reason:         reason,
				Actor:          actor,
				SignatureProof: emptyProof,
				CreatedAt:      now,
			}
			if err := s.RecordSilenceOverride(ov); err != nil {
				t.Fatalf("RecordSilenceOverride failed: %v", err)
			}
			overridden, err := s.IsSilenceOverridden(proj.ID, sig)
			if err != nil {
				t.Fatalf("IsSilenceOverridden returned error: %v", err)
			}
			if overridden {
				t.Errorf("expected empty proof %q to be rejected", emptyProof)
			}
			valid, err := VerifyOverrideProof(ov)
			if valid || err == nil {
				t.Errorf("expected VerifyOverrideProof to fail for empty proof %q, got valid=%v, err=%v", emptyProof, valid, err)
			}
		}
	})

	t.Run("Corrupted or truncated Ed25519 signature rejected", func(t *testing.T) {
		pub1B64 := base64.StdEncoding.EncodeToString(pub1)

		corruptSigs := []struct {
			name  string
			proof string
		}{
			{
				name:  "truncated signature 10 bytes",
				proof: "ed25519:" + pub1B64 + ":" + base64.StdEncoding.EncodeToString([]byte("shortsig12")),
			},
			{
				name:  "empty signature part",
				proof: "ed25519:" + pub1B64 + ":",
			},
			{
				name:  "invalid base64 signature",
				proof: "ed25519:" + pub1B64 + ":!@#$%^&*()",
			},
			{
				name:  "oversized 65-byte signature",
				proof: "ed25519:" + pub1B64 + ":" + base64.StdEncoding.EncodeToString(make([]byte, 65)),
			},
			{
				name:  "zero-byte signature length 64 (all zeros invalid signature)",
				proof: "ed25519:" + pub1B64 + ":" + base64.StdEncoding.EncodeToString(make([]byte, 64)),
			},
		}

		for _, tc := range corruptSigs {
			t.Run(tc.name, func(t *testing.T) {
				ov := SilenceOverride{
					Signature:      sig,
					ProjectID:      proj.ID,
					Reason:         reason,
					Actor:          actor,
					SignatureProof: tc.proof,
					CreatedAt:      now,
				}
				if err := s.RecordSilenceOverride(ov); err != nil {
					t.Fatalf("RecordSilenceOverride failed: %v", err)
				}
				overridden, err := s.IsSilenceOverridden(proj.ID, sig)
				if err != nil {
					t.Fatalf("IsSilenceOverridden returned error: %v", err)
				}
				if overridden {
					t.Errorf("expected corrupted signature %q to be rejected", tc.name)
				}
				valid, err := VerifyOverrideProof(ov)
				if valid || err == nil {
					t.Errorf("expected VerifyOverrideProof to fail for %q", tc.name)
				}
			})
		}
	})

	t.Run("Bit-flipped Ed25519 signature rejected", func(t *testing.T) {
		parts := strings.Split(authenticProof, ":")
		sigBytes, err := base64.StdEncoding.DecodeString(parts[2])
		if err != nil {
			t.Fatalf("decode authentic signature failed: %v", err)
		}
		// Flip single bit
		sigBytes[0] ^= 0x01
		tamperedProof := parts[0] + ":" + parts[1] + ":" + base64.StdEncoding.EncodeToString(sigBytes)

		ov := SilenceOverride{
			Signature:      sig,
			ProjectID:      proj.ID,
			Reason:         reason,
			Actor:          actor,
			SignatureProof: tamperedProof,
			CreatedAt:      now,
		}
		if err := s.RecordSilenceOverride(ov); err != nil {
			t.Fatalf("RecordSilenceOverride failed: %v", err)
		}
		overridden, err := s.IsSilenceOverridden(proj.ID, sig)
		if err != nil {
			t.Fatalf("IsSilenceOverridden returned error: %v", err)
		}
		if overridden {
			t.Errorf("expected bit-flipped signature to be rejected")
		}
		valid, err := VerifyOverrideProof(ov)
		if valid || err == nil {
			t.Errorf("expected VerifyOverrideProof to fail for bit-flipped signature")
		}
	})

	t.Run("Wrong public key substituted in proof rejected", func(t *testing.T) {
		parts := strings.Split(authenticProof, ":")
		pub2B64 := base64.StdEncoding.EncodeToString(pub2)
		wrongPubProof := parts[0] + ":" + pub2B64 + ":" + parts[2]

		ov := SilenceOverride{
			Signature:      sig,
			ProjectID:      proj.ID,
			Reason:         reason,
			Actor:          actor,
			SignatureProof: wrongPubProof,
			CreatedAt:      now,
		}
		if err := s.RecordSilenceOverride(ov); err != nil {
			t.Fatalf("RecordSilenceOverride failed: %v", err)
		}
		overridden, err := s.IsSilenceOverridden(proj.ID, sig)
		if err != nil {
			t.Fatalf("IsSilenceOverridden returned error: %v", err)
		}
		if overridden {
			t.Errorf("expected wrong public key to be rejected")
		}
		valid, err := VerifyOverrideProof(ov)
		if valid || err == nil {
			t.Errorf("expected VerifyOverrideProof to fail for wrong public key")
		}
	})

	t.Run("Malformed public key rejected", func(t *testing.T) {
		parts := strings.Split(authenticProof, ":")
		malformedPubs := []struct {
			name string
			pub  string
		}{
			{name: "truncated 16-byte pubkey", pub: base64.StdEncoding.EncodeToString(make([]byte, 16))},
			{name: "oversized 33-byte pubkey", pub: base64.StdEncoding.EncodeToString(make([]byte, 33))},
			{name: "invalid base64 pubkey", pub: "###NOT_BASE64###"},
			{name: "empty pubkey", pub: ""},
		}

		for _, tc := range malformedPubs {
			t.Run(tc.name, func(t *testing.T) {
				badProof := parts[0] + ":" + tc.pub + ":" + parts[2]
				ov := SilenceOverride{
					Signature:      sig,
					ProjectID:      proj.ID,
					Reason:         reason,
					Actor:          actor,
					SignatureProof: badProof,
					CreatedAt:      now,
				}
				if err := s.RecordSilenceOverride(ov); err != nil {
					t.Fatalf("RecordSilenceOverride failed: %v", err)
				}
				overridden, err := s.IsSilenceOverridden(proj.ID, sig)
				if err != nil {
					t.Fatalf("IsSilenceOverridden returned error: %v", err)
				}
				if overridden {
					t.Errorf("expected malformed pubkey %q to be rejected", tc.name)
				}
				valid, err := VerifyOverrideProof(ov)
				if valid || err == nil {
					t.Errorf("expected VerifyOverrideProof to fail for %q", tc.name)
				}
			})
		}
	})

	t.Run("Tampered payload fields rejected individually", func(t *testing.T) {
		payloadTamperings := []struct {
			name   string
			mutate func(ov *SilenceOverride)
		}{
			{
				name: "tampered reason",
				mutate: func(ov *SilenceOverride) {
					ov.Reason = "Unapproved bypass injection"
				},
			},
			{
				name: "tampered signature",
				mutate: func(ov *SilenceOverride) {
					ov.Signature = "sig_forged_other_symbol"
				},
			},
			{
				name: "tampered project_id to another registered project",
				mutate: func(ov *SilenceOverride) {
					ov.ProjectID = projOther.ID
				},
			},
			{
				name: "tampered actor",
				mutate: func(ov *SilenceOverride) {
					ov.Actor = "malicious_injected_actor"
				},
			},
			{
				name: "tampered created_at timestamp",
				mutate: func(ov *SilenceOverride) {
					ov.CreatedAt = "2020-01-01T00:00:00Z"
				},
			},
			{
				name: "newline injection in reason",
				mutate: func(ov *SilenceOverride) {
					ov.Reason = "Approved\nactor:root\ncreated_at:2026-10-16T12:00:00Z"
				},
			},
		}

		for _, tc := range payloadTamperings {
			t.Run(tc.name, func(t *testing.T) {
				ov := SilenceOverride{
					Signature:      sig,
					ProjectID:      proj.ID,
					Reason:         reason,
					Actor:          actor,
					SignatureProof: authenticProof,
					CreatedAt:      now,
				}
				tc.mutate(&ov)

				valid, err := VerifyOverrideProof(ov)
				if valid || err == nil {
					t.Errorf("expected VerifyOverrideProof to reject tampered payload in %q", tc.name)
				}

				if err := s.RecordSilenceOverride(ov); err != nil {
					t.Fatalf("RecordSilenceOverride failed: %v", err)
				}
				overridden, err := s.IsSilenceOverridden(ov.ProjectID, ov.Signature)
				if err != nil {
					t.Fatalf("IsSilenceOverridden query returned error: %v", err)
				}
				if overridden {
					t.Errorf("expected tampered payload in %q to be rejected by IsSilenceOverridden", tc.name)
				}
			})
		}
	})

	t.Run("Malformed proof format schemes rejected", func(t *testing.T) {
		malformedSchemes := []string{
			"raw_signature_without_scheme",
			"rsa:pubkey:signature",
			"plain:unprotected_bypass",
			"ed25519:only_two_parts",
			"ed25519:too:many:colon:separated:parts",
			":::",
			"none:",
		}
		for _, badScheme := range malformedSchemes {
			ov := SilenceOverride{
				Signature:      sig,
				ProjectID:      proj.ID,
				Reason:         reason,
				Actor:          actor,
				SignatureProof: badScheme,
				CreatedAt:      now,
			}
			if err := s.RecordSilenceOverride(ov); err != nil {
				t.Fatalf("RecordSilenceOverride failed: %v", err)
			}
			overridden, err := s.IsSilenceOverridden(proj.ID, sig)
			if err != nil {
				t.Fatalf("IsSilenceOverridden query returned error: %v", err)
			}
			if overridden {
				t.Errorf("expected malformed scheme %q to be rejected", badScheme)
			}
			valid, err := VerifyOverrideProof(ov)
			if valid || err == nil {
				t.Errorf("expected VerifyOverrideProof to fail for malformed scheme %q", badScheme)
			}
		}
	})

	t.Run("HMAC override tampering challenge", func(t *testing.T) {
		secret := []byte("super-secret-hmac-key-adversarial-probe")
		t.Setenv("SILA_OVERRIDE_SECRET", string(secret))

		hmacProof, err := GenerateHMACOverrideProof(proj.ID, sig, reason, actor, now, secret)
		if err != nil {
			t.Fatalf("GenerateHMACOverrideProof failed: %v", err)
		}

		ov := SilenceOverride{
			Signature:      sig,
			ProjectID:      proj.ID,
			Reason:         reason,
			Actor:          actor,
			SignatureProof: hmacProof,
			CreatedAt:      now,
		}
		if err := s.RecordSilenceOverride(ov); err != nil {
			t.Fatalf("RecordSilenceOverride failed: %v", err)
		}

		// Authentic HMAC accepted
		overridden, err := s.IsSilenceOverridden(proj.ID, sig)
		if err != nil || !overridden {
			t.Fatalf("expected authentic HMAC override to be accepted, err: %v", err)
		}

		// Wrong secret in environment rejected
		t.Setenv("SILA_OVERRIDE_SECRET", "completely-different-wrong-secret")
		overridden, err = s.IsSilenceOverridden(proj.ID, sig)
		if err != nil {
			t.Fatalf("IsSilenceOverridden returned error: %v", err)
		}
		if overridden {
			t.Errorf("expected HMAC with wrong secret to be rejected")
		}

		// Restore secret and tamper HMAC hex bytes
		t.Setenv("SILA_OVERRIDE_SECRET", string(secret))
		tamperedHex := "hmac:" + hex.EncodeToString(make([]byte, 32)) // all zeros
		ovTamperedHex := ov
		ovTamperedHex.SignatureProof = tamperedHex
		if err := s.RecordSilenceOverride(ovTamperedHex); err != nil {
			t.Fatalf("RecordSilenceOverride failed: %v", err)
		}
		overridden, err = s.IsSilenceOverridden(proj.ID, sig)
		if err != nil {
			t.Fatalf("IsSilenceOverridden returned error: %v", err)
		}
		if overridden {
			t.Errorf("expected tampered HMAC hex to be rejected")
		}

		// Corrupted hex string rejected
		ovCorruptHex := ov
		ovCorruptHex.SignatureProof = "hmac:NOT_A_VALID_HEX_STRING!!"
		valid, err := VerifyOverrideProof(ovCorruptHex)
		if valid || err == nil {
			t.Errorf("expected invalid hex in HMAC proof to be rejected")
		}
	})

	t.Run("Nonexistent signature returns false without error", func(t *testing.T) {
		overridden, err := s.IsSilenceOverridden(proj.ID, "completely_unrecorded_signature_xyz")
		if err != nil {
			t.Fatalf("IsSilenceOverridden for nonexistent signature failed: %v", err)
		}
		if overridden {
			t.Errorf("expected unrecorded signature to return false")
		}
	})
}
