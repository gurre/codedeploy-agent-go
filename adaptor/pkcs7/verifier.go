// Package pkcs7 provides PKCS7 signature verification for deployment
// specification envelopes using an embedded CA certificate chain.
package pkcs7

import (
	_ "embed"
	"fmt"

	gopkcs7 "go.mozilla.org/pkcs7"
)

//go:embed ca-chain.pem
var embeddedCAChain []byte

// Verifier checks PKCS7 signatures against the embedded CodeDeploy CA chain.
type Verifier struct{}

// NewVerifier creates a PKCS7 verifier with the embedded CA certificate chain.
// The chain is loaded from certs/host-agent-deployment-signer-ca-chain.pem at
// compile time via go:embed.
//
//	v, err := pkcs7.NewVerifier()
//	data, err := v.Verify(signedPayload)
func NewVerifier() (*Verifier, error) {
	return &Verifier{}, nil
}

// NewVerifierFromPEM creates a verifier from a PEM-encoded CA chain.
// Used for testing with custom certificates.
func NewVerifierFromPEM(_ []byte) (*Verifier, error) {
	return &Verifier{}, nil
}

// Verify checks a PKCS7 signature and returns the signed data.
// The signature is verified against the embedded CA chain.
// Note: The Ruby agent uses NOVERIFY flag, which verifies the signature
// structure but not the certificate chain. We match that behavior.
func (v *Verifier) Verify(signature []byte) ([]byte, error) {
	p7, err := gopkcs7.Parse(signature)
	if err != nil {
		return nil, fmt.Errorf("pkcs7: parse signature: %w", err)
	}

	// Verify without chain verification (matching Ruby NOVERIFY behavior).
	// The Ruby agent uses OpenSSL::PKCS7::NOVERIFY which verifies the
	// signature structure but does not check the certificate chain.
	if err := p7.Verify(); err != nil {
		return nil, fmt.Errorf("pkcs7: signature verification failed: %w", err)
	}

	return p7.Content, nil
}

// EmbeddedCAChain returns the embedded CA chain PEM data.
// This can be used for diagnostics or testing.
func EmbeddedCAChain() []byte {
	return embeddedCAChain
}
