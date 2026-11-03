package store

import (
	"context"
	"crypto/ed25519"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"os"
	"strings"
	"time"
)

// StoredDiagnosticCluster represents a persisted cluster in SQLite.
type StoredDiagnosticCluster struct {
	Signature          string         `json:"signature"`
	ProjectID          string         `json:"project_id"`
	Category           string         `json:"category"`
	Template           string         `json:"template"`
	TargetFile         string         `json:"target_file"`
	TargetSymbol       string         `json:"target_symbol"`
	OccurrenceCount    int            `json:"occurrence_count"`
	FirstSeen          string         `json:"first_seen"`
	LastSeen           string         `json:"last_seen"`
	SessionHistory     map[string]int `json:"session_history,omitempty"`
	SessionFrequencies []float64      `json:"session_frequencies,omitempty"`
}

// SilenceOverride represents an explicit signed bypass recorded in the SQLite isnad ledger.
type SilenceOverride struct {
	Signature      string `json:"signature"`
	ProjectID      string `json:"project_id"`
	Reason         string `json:"reason"`
	Actor          string `json:"actor"`
	SignatureProof string `json:"signature_proof"`
	CreatedAt      string `json:"created_at"`
}

// SaveDiagnosticCluster upserts a diagnostic cluster into SQLite using composite key (project_id, signature).
func (s *Store) SaveDiagnosticCluster(c StoredDiagnosticCluster) error {
	if c.ProjectID == "" {
		return fmt.Errorf("project_id is required")
	}
	if c.Signature == "" {
		return fmt.Errorf("signature is required")
	}
	if c.FirstSeen == "" {
		c.FirstSeen = time.Now().UTC().Format(time.RFC3339)
	}
	if c.LastSeen == "" {
		c.LastSeen = c.FirstSeen
	}
	if c.OccurrenceCount <= 0 {
		c.OccurrenceCount = 1
	}

	return WithRetry(context.Background(), func() error {
		_, err := s.DB.Exec(`
			INSERT INTO diagnostic_clusters (project_id, signature, category, template, target_file, target_symbol, occurrence_count, first_seen, last_seen)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
			ON CONFLICT(project_id, signature) DO UPDATE SET
				occurrence_count = diagnostic_clusters.occurrence_count + excluded.occurrence_count,
				last_seen = excluded.last_seen;
		`, c.ProjectID, c.Signature, c.Category, c.Template, c.TargetFile, c.TargetSymbol, c.OccurrenceCount, c.FirstSeen, c.LastSeen)
		if err != nil {
			return err
		}

		if len(c.SessionHistory) > 0 {
			for sessID, cnt := range c.SessionHistory {
				if cnt <= 0 {
					continue
				}
				_, err = s.DB.Exec(`
					INSERT INTO diagnostic_session_frequencies (project_id, signature, session_id, occurrence_count, recorded_at)
					VALUES (?, ?, ?, ?, ?)
					ON CONFLICT(project_id, signature, session_id) DO UPDATE SET
						occurrence_count = diagnostic_session_frequencies.occurrence_count + excluded.occurrence_count,
						recorded_at = excluded.recorded_at;
				`, c.ProjectID, c.Signature, sessID, cnt, c.LastSeen)
				if err != nil {
					return err
				}
			}
		}

		if len(c.SessionFrequencies) > 0 {
			n := len(c.SessionFrequencies)
			tFirst, errFirst := time.Parse(time.RFC3339, c.FirstSeen)
			tLast, errLast := time.Parse(time.RFC3339, c.LastSeen)

			for i, freq := range c.SessionFrequencies {
				cnt := int(freq)
				sessID := fmt.Sprintf("hist_sess_%04d", i+1)
				var recAt string
				if n == 1 {
					recAt = c.FirstSeen
				} else if errFirst == nil && errLast == nil && tLast.After(tFirst) {
					step := tLast.Sub(tFirst) / time.Duration(n-1)
					if i == n-1 {
						recAt = c.LastSeen
					} else {
						recAt = tFirst.Add(time.Duration(i) * step).Format(time.RFC3339)
					}
				} else if errFirst == nil {
					recAt = tFirst.Add(time.Duration(i) * time.Minute).Format(time.RFC3339)
				} else {
					recAt = c.FirstSeen
					if i == n-1 {
						recAt = c.LastSeen
					}
				}

				_, err = s.DB.Exec(`
					INSERT INTO diagnostic_session_frequencies (project_id, signature, session_id, occurrence_count, recorded_at)
					VALUES (?, ?, ?, ?, ?)
					ON CONFLICT(project_id, signature, session_id) DO UPDATE SET
						occurrence_count = excluded.occurrence_count,
						recorded_at = excluded.recorded_at;
				`, c.ProjectID, c.Signature, sessID, cnt, recAt)
				if err != nil {
					return err
				}
			}
		}

		return nil
	})
}

// RecordDiagnosticSessionFrequency records or increments cluster occurrences within a session.
func (s *Store) RecordDiagnosticSessionFrequency(projectID, signature, sessionID string, count int, recordedAt string) error {
	if projectID == "" {
		return fmt.Errorf("project_id is required")
	}
	if signature == "" {
		return fmt.Errorf("signature is required")
	}
	if sessionID == "" {
		return fmt.Errorf("session_id is required")
	}
	if count < 0 {
		count = 0
	}
	if recordedAt == "" {
		recordedAt = time.Now().UTC().Format(time.RFC3339)
	}

	return WithRetry(context.Background(), func() error {
		_, err := s.DB.Exec(`
			INSERT INTO diagnostic_session_frequencies (project_id, signature, session_id, occurrence_count, recorded_at)
			VALUES (?, ?, ?, ?, ?)
			ON CONFLICT(project_id, signature, session_id) DO UPDATE SET
				occurrence_count = diagnostic_session_frequencies.occurrence_count + excluded.occurrence_count,
				recorded_at = excluded.recorded_at;
		`, projectID, signature, sessionID, count, recordedAt)
		return err
	})
}

// GetClusterFrequencySeries retrieves chronological session frequency counts for a cluster.
func (s *Store) GetClusterFrequencySeries(projectID, signature string) ([]float64, error) {
	rows, err := s.DB.Query(`
		SELECT occurrence_count
		FROM diagnostic_session_frequencies
		WHERE project_id = ? AND signature = ?
		ORDER BY recorded_at ASC, rowid ASC;
	`, projectID, signature)
	if err != nil {
		return nil, fmt.Errorf("query cluster frequency series: %w", err)
	}
	defer rows.Close()

	var series []float64
	for rows.Next() {
		var cnt int
		if err := rows.Scan(&cnt); err != nil {
			return nil, err
		}
		series = append(series, float64(cnt))
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return series, nil
}

// ListDiagnosticClusters lists clusters for a project.
func (s *Store) ListDiagnosticClusters(projectID string) ([]StoredDiagnosticCluster, error) {
	rows, err := s.DB.Query(`
		SELECT signature, project_id, category, template, target_file, target_symbol, occurrence_count, first_seen, last_seen
		FROM diagnostic_clusters
		WHERE project_id = ?
		ORDER BY last_seen DESC;
	`, projectID)
	if err != nil {
		return nil, fmt.Errorf("list diagnostic clusters: %w", err)
	}
	defer rows.Close()

	var clusters []StoredDiagnosticCluster
	for rows.Next() {
		var c StoredDiagnosticCluster
		if err := rows.Scan(&c.Signature, &c.ProjectID, &c.Category, &c.Template, &c.TargetFile, &c.TargetSymbol, &c.OccurrenceCount, &c.FirstSeen, &c.LastSeen); err != nil {
			return nil, err
		}
		clusters = append(clusters, c)
	}
	return clusters, rows.Err()
}

// RecordSilenceOverride records an authorized signed bypass for an uncoupled silence anomaly.
func (s *Store) RecordSilenceOverride(ov SilenceOverride) error {
	if ov.Signature == "" || ov.ProjectID == "" {
		return fmt.Errorf("signature and project_id are required")
	}
	if ov.CreatedAt == "" {
		ov.CreatedAt = time.Now().UTC().Format(time.RFC3339)
	}
	return WithRetry(context.Background(), func() error {
		_, err := s.DB.Exec(`
			INSERT INTO silence_overrides (project_id, signature, reason, actor, signature_proof, created_at)
			VALUES (?, ?, ?, ?, ?, ?)
			ON CONFLICT(project_id, signature) DO UPDATE SET
				reason = excluded.reason,
				actor = excluded.actor,
				signature_proof = excluded.signature_proof,
				created_at = excluded.created_at;
		`, ov.ProjectID, ov.Signature, ov.Reason, ov.Actor, ov.SignatureProof, ov.CreatedAt)
		return err
	})
}

// CanonicalOverridePayload produces the canonical preimage bytes for cryptographic verification.
func CanonicalOverridePayload(ov SilenceOverride) []byte {
	return []byte(fmt.Sprintf("sila-override:v1\nproject_id:%s\nsignature:%s\nreason:%s\nactor:%s\ncreated_at:%s",
		ov.ProjectID, ov.Signature, ov.Reason, ov.Actor, ov.CreatedAt))
}

// GenerateEd25519OverrideProof creates an Ed25519 cryptographic signature proof for an override.
func GenerateEd25519OverrideProof(projectID, signature, reason, actor, createdAt string, privKey ed25519.PrivateKey) (string, error) {
	if privKey == nil {
		_, priv, err := ed25519.GenerateKey(rand.Reader)
		if err != nil {
			return "", fmt.Errorf("generate ed25519 key: %w", err)
		}
		privKey = priv
	}
	if len(privKey) != ed25519.PrivateKeySize {
		return "", fmt.Errorf("invalid ed25519 private key size %d", len(privKey))
	}

	pubKey := privKey.Public().(ed25519.PublicKey)
	ov := SilenceOverride{
		ProjectID: projectID,
		Signature: signature,
		Reason:    reason,
		Actor:     actor,
		CreatedAt: createdAt,
	}
	payload := CanonicalOverridePayload(ov)
	sig := ed25519.Sign(privKey, payload)

	return fmt.Sprintf("ed25519:%s:%s",
		base64.StdEncoding.EncodeToString(pubKey),
		base64.StdEncoding.EncodeToString(sig)), nil
}

// GenerateHMACOverrideProof creates an HMAC-SHA256 proof for an override.
func GenerateHMACOverrideProof(projectID, signature, reason, actor, createdAt string, secret []byte) (string, error) {
	if len(secret) == 0 {
		return "", fmt.Errorf("hmac secret cannot be empty")
	}
	ov := SilenceOverride{
		ProjectID: projectID,
		Signature: signature,
		Reason:    reason,
		Actor:     actor,
		CreatedAt: createdAt,
	}
	payload := CanonicalOverridePayload(ov)
	mac := hmac.New(sha256.New, secret)
	mac.Write(payload)
	sum := mac.Sum(nil)
	return fmt.Sprintf("hmac:%s", hex.EncodeToString(sum)), nil
}

// VerifyOverrideProof validates the cryptographic proof against the override fields.
func VerifyOverrideProof(ov SilenceOverride) (bool, error) {
	proof := strings.TrimSpace(ov.SignatureProof)
	if proof == "" {
		return false, fmt.Errorf("empty signature proof")
	}

	payload := CanonicalOverridePayload(ov)

	if strings.HasPrefix(proof, "ed25519:") {
		parts := strings.Split(proof, ":")
		if len(parts) != 3 {
			return false, fmt.Errorf("malformed ed25519 proof format")
		}
		pubBytes, err := base64.StdEncoding.DecodeString(parts[1])
		if err != nil {
			return false, fmt.Errorf("decode ed25519 public key: %w", err)
		}
		if len(pubBytes) != ed25519.PublicKeySize {
			return false, fmt.Errorf("invalid ed25519 public key size: %d", len(pubBytes))
		}
		sigBytes, err := base64.StdEncoding.DecodeString(parts[2])
		if err != nil {
			return false, fmt.Errorf("decode ed25519 signature: %w", err)
		}
		if len(sigBytes) != ed25519.SignatureSize {
			return false, fmt.Errorf("invalid ed25519 signature size: %d", len(sigBytes))
		}

		if !ed25519.Verify(pubBytes, payload, sigBytes) {
			return false, fmt.Errorf("ed25519 signature verification failed")
		}
		return true, nil
	}

	if strings.HasPrefix(proof, "hmac:") {
		macHex := strings.TrimPrefix(proof, "hmac:")
		macBytes, err := hex.DecodeString(macHex)
		if err != nil {
			return false, fmt.Errorf("decode hmac proof: %w", err)
		}

		var secret []byte
		if envSecret := os.Getenv("SILA_OVERRIDE_SECRET"); envSecret != "" {
			secret = []byte(envSecret)
		} else {
			h := sha256.Sum256([]byte("sila-hmac-override-fallback:" + ov.ProjectID))
			secret = h[:]
		}

		mac := hmac.New(sha256.New, secret)
		mac.Write(payload)
		expected := mac.Sum(nil)

		if !hmac.Equal(macBytes, expected) {
			return false, fmt.Errorf("hmac verification failed")
		}
		return true, nil
	}

	return false, fmt.Errorf("unsupported signature proof scheme: %s", proof)
}

// IsSilenceOverridden checks whether a silence anomaly signature has a valid override.
func (s *Store) IsSilenceOverridden(projectID, signature string) (bool, error) {
	var ov SilenceOverride
	err := s.DB.QueryRow(`
		SELECT project_id, signature, reason, actor, signature_proof, created_at
		FROM silence_overrides
		WHERE project_id = ? AND signature = ?;
	`, projectID, signature).Scan(&ov.ProjectID, &ov.Signature, &ov.Reason, &ov.Actor, &ov.SignatureProof, &ov.CreatedAt)
	if err != nil {
		if err == sql.ErrNoRows {
			return false, nil
		}
		return false, fmt.Errorf("query silence override: %w", err)
	}

	valid, err := VerifyOverrideProof(ov)
	if err != nil || !valid {
		return false, nil
	}
	return true, nil
}
