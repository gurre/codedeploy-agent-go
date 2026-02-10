package pkcs7

import (
	"bytes"
	"strings"
	"testing"
)

// TestNewVerifier verifies the constructor returns a valid verifier.
// This ensures the embedded CA chain is loadable at startup.
func TestNewVerifier(t *testing.T) {
	v, err := NewVerifier()
	if err != nil {
		t.Fatalf("NewVerifier: %v", err)
	}
	if v == nil {
		t.Fatal("verifier should not be nil")
	}
}

// TestEmbeddedCAChain verifies the compile-time embedded CA chain is present
// and has the expected PEM format. A missing or empty chain would break all
// signature verification at runtime.
func TestEmbeddedCAChain(t *testing.T) {
	chain := EmbeddedCAChain()
	if len(chain) == 0 {
		t.Fatal("embedded CA chain should not be empty")
	}
	if !bytes.HasPrefix(chain, []byte("-----BEGIN")) {
		t.Error("CA chain should start with PEM header")
	}
}

// TestVerify_InvalidData verifies that malformed input produces a clear parse
// error rather than a panic or silent corruption.
func TestVerify_InvalidData(t *testing.T) {
	v, err := NewVerifier()
	if err != nil {
		t.Fatal(err)
	}
	_, err = v.Verify([]byte("not a pkcs7 signature"))
	if err == nil {
		t.Fatal("should return error for invalid data")
	}
	if !strings.Contains(err.Error(), "pkcs7") {
		t.Errorf("error should mention pkcs7, got: %v", err)
	}
}

// TestNewVerifierFromPEM verifies that the custom-PEM constructor succeeds.
// This path is used when testing with non-production certificates.
func TestNewVerifierFromPEM(t *testing.T) {
	v, err := NewVerifierFromPEM([]byte("custom-pem-data"))
	if err != nil {
		t.Fatalf("NewVerifierFromPEM: %v", err)
	}
	if v == nil {
		t.Fatal("verifier should not be nil")
	}
}
