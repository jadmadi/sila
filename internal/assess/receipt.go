package assess

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"time"

	"github.com/jadmadi/sila/internal/goals"
)

// GenerateAuditorKeypair creates an ephemeral Ed25519 signing keypair for mystery audit certification.
func GenerateAuditorKeypair() (ed25519.PublicKey, ed25519.PrivateKey, error) {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return nil, nil, fmt.Errorf("generate keypair: %w", err)
	}
	return pub, priv, nil
}

// FormatReceiptDigest produces the canonical preimage string for cryptographic isnad signing.
func FormatReceiptDigest(receipt *goals.AuditReceipt) []byte {
	return goals.FormatReceiptDigest(receipt)
}

// SignAuditReceipt signs the receipt with the auditor's Ed25519 private key using base64 encoding.
func SignAuditReceipt(receipt *goals.AuditReceipt, privKey ed25519.PrivateKey) error {
	if receipt == nil {
		return fmt.Errorf("receipt is nil")
	}
	if len(privKey) != ed25519.PrivateKeySize {
		return fmt.Errorf("invalid private key size")
	}

	pubKey := privKey.Public().(ed25519.PublicKey)
	receipt.AuditorPubKey = base64.StdEncoding.EncodeToString(pubKey)
	if receipt.CertifiedAt.IsZero() {
		receipt.CertifiedAt = time.Now().UTC()
	}

	digest := FormatReceiptDigest(receipt)
	sig := ed25519.Sign(privKey, digest)
	receipt.AuditorSignature = base64.StdEncoding.EncodeToString(sig)
	return nil
}

// VerifyAuditReceipt validates the cryptographic signature and boundary invariants of an audit receipt.
func VerifyAuditReceipt(receipt *goals.AuditReceipt) (bool, error) {
	if receipt == nil {
		return false, fmt.Errorf("receipt is nil")
	}
	if err := goals.VerifyAuditReceipt(receipt); err != nil {
		return false, err
	}
	return true, nil
}
