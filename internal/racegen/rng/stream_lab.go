//go:build gli_lab

package rng

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
)

// NewSeededStream (gli_lab builds only) builds the SAME HMAC-DRBG as
// production but with the deterministic HKDF EntropySource derived from a
// 64-hex seed — bit-exact lab replay. The descriptor carries only a SHA-256
// fingerprint of the seed, never the seed itself (finding H3 of
// docs/AUDITORIA-RNG-GLI19.md).
func NewSeededStream(seedHex string) (CertifiedStream, string, error) {
	seed, err := hex.DecodeString(seedHex)
	if err != nil || len(seed) != 32 {
		return nil, "", fmt.Errorf("rng: seed must be 64 hex chars (32 bytes)")
	}
	le, err := NewLabEntropy(seed)
	if err != nil {
		return nil, "", fmt.Errorf("rng: lab entropy: %w", err)
	}
	src, err := NewHMACDRBG(le, []byte(PersonalizationV1))
	if err != nil {
		return nil, "", fmt.Errorf("rng: hmac-drbg instantiate: %w", err)
	}
	fp := sha256.Sum256(seed)
	desc := fmt.Sprintf("hmac-drbg-sha256/%s seedFP=%s", le.Describe(), hex.EncodeToString(fp[:])[:16])
	return src, desc, nil
}
