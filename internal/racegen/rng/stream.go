package rng

import "fmt"

// PersonalizationV1 is THE SP 800-90A personalization string of the certified
// DRBG instance, shared by every binary that must instantiate the exact
// certified stream (cmd/race-generator and the GLI extraction harness
// cmd/rngextract — "same RNG and methods", Composite Submission Requirements
// §2.2). Single sourced here so the binaries cannot drift apart. Bump the
// suffix on any change to the certified number→symbol mapping.
const PersonalizationV1 = "vg-racegen/race-generator/v1"

// NewOSStream builds the PRODUCTION certified stream: HMAC-DRBG SHA-256
// seeded from the operating system CSPRNG. Returns the stream and an audit
// descriptor (never seed material).
func NewOSStream() (CertifiedStream, string, error) {
	src, err := NewHMACDRBG(CryptoEntropy{}, []byte(PersonalizationV1))
	if err != nil {
		return nil, "", fmt.Errorf("rng: hmac-drbg instantiate: %w", err)
	}
	return src, "hmac-drbg-sha256/" + CryptoEntropy{}.Describe(), nil
}
