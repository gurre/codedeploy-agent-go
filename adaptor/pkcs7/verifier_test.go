package pkcs7

import (
	"bytes"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/asn1"
	"encoding/pem"
	"fmt"
	"math/big"
	"strings"
	"testing"
	"time"

	gopkcs7 "github.com/gurre/pkcs7"
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

// TestVerify_SkipChainVerification constructs a valid PKCS7 envelope signed by
// a self-signed certificate that is NOT in the embedded CA chain. The verifier
// must return the enclosed content without error, confirming that certificate
// chain validation is skipped (matching the Ruby agent's NOVERIFY behavior).
func TestVerify_SkipChainVerification(t *testing.T) {
	// Generate a throwaway self-signed certificate + key.
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "test-signer"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
	}
	certDER, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	cert, err := x509.ParseCertificate(certDER)
	if err != nil {
		t.Fatal(err)
	}

	// Build a PKCS7 SignedData envelope with content that is deliberately not
	// a real deployment spec — proves the verifier doesn't inspect content.
	payload := []byte("wrong content")
	sd, err := gopkcs7.NewSignedData()
	if err != nil {
		t.Fatal(err)
	}
	sd.SetContent(payload)
	if err := sd.AddSigner(cert, key, nil, nil, gopkcs7.SignerInfoConfig{}); err != nil {
		t.Fatal(err)
	}
	der, err := sd.Finish()
	if err != nil {
		t.Fatal(err)
	}

	// PEM-encode to match the format CodeDeploy returns.
	var buf bytes.Buffer
	if err := pem.Encode(&buf, &pem.Block{Type: "PKCS7", Bytes: der}); err != nil {
		t.Fatal(err)
	}

	v, err := NewVerifier()
	if err != nil {
		t.Fatal(err)
	}
	got, err := v.Verify(buf.Bytes())
	if err != nil {
		t.Fatalf("Verify should accept a valid envelope from an untrusted signer: %v", err)
	}
	if !bytes.Equal(got, payload) {
		t.Errorf("content mismatch: got %q, want %q", got, payload)
	}
}

// testCert generates a self-signed certificate and RSA private key for testing.
func testCert(t *testing.T) (*x509.Certificate, *rsa.PrivateKey) {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "test-signer"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatal(err)
	}
	return cert, key
}

// signAndPEM signs content with the library and PEM-encodes the result.
func signAndPEM(t *testing.T, content []byte, cert *x509.Certificate, key *rsa.PrivateKey) []byte {
	t.Helper()
	sd, err := gopkcs7.NewSignedData()
	if err != nil {
		t.Fatal(err)
	}
	sd.SetContent(content)
	if err := sd.AddSigner(cert, key, nil, nil, gopkcs7.SignerInfoConfig{}); err != nil {
		t.Fatal(err)
	}
	der, err := sd.Finish()
	if err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	if err := pem.Encode(&buf, &pem.Block{Type: "PKCS7", Bytes: der}); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// TestVerify_ContentPreservedThroughCycle checks that content bytes survive a
// full sign → marshal → PEM-encode → Verify round-trip without modification.
// Different payload shapes are tested because the library's parseSignedData
// uses asn1.Unmarshal on the content OCTET STRING — bytes that happen to look
// like valid ASN.1 structures could be silently re-encoded or re-interpreted.
func TestVerify_ContentPreservedThroughCycle(t *testing.T) {
	cert, key := testCert(t)
	v, err := NewVerifier()
	if err != nil {
		t.Fatal(err)
	}

	cases := []struct {
		name    string
		content []byte
	}{
		// Baseline text content.
		{"plain text", []byte("hello world")},
		// JSON resembling a deployment spec — the actual payload the agent sees.
		{"JSON object", []byte(`{"version":0.0,"os":"linux","files":[{"source":"s3://bucket/key"}]}`)},
		// Null bytes and high bytes exercise OCTET STRING boundaries.
		{"binary with nulls", []byte{0x00, 0x01, 0x02, 0x00, 0xff, 0xfe}},
		// Bytes that look like a primitive OCTET STRING (tag 0x04, length, data).
		// parseSignedData does asn1.Unmarshal(Content.Bytes, &compound) — if the
		// content itself starts with 0x04 the parser must not confuse it with
		// the wrapper OCTET STRING.
		{"bytes resembling OCTET STRING header", []byte{0x04, 0x03, 0x41, 0x42, 0x43}},
		// Bytes that look like a DER SEQUENCE. If asn1.Unmarshal treats these as
		// a structured type it would strip the tag+length wrapper and lose bytes.
		{"bytes resembling DER SEQUENCE", []byte{0x30, 0x06, 0x02, 0x01, 0x01, 0x02, 0x01, 0x02}},
		// Single byte — edge case for length encoding.
		{"single byte", []byte{0x42}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			pemData := signAndPEM(t, tc.content, cert, key)
			got, err := v.Verify(pemData)
			if err != nil {
				t.Fatalf("Verify failed: %v", err)
			}
			if !bytes.Equal(got, tc.content) {
				t.Errorf("content not preserved\n  want: %x\n  got:  %x", tc.content, got)
			}
		})
	}
}

// TestVerify_SHA256DigestRoundTrip verifies that SHA-256 signed envelopes
// survive the full cycle. AWS may sign with SHA-256 after migrating signing
// infrastructure; the library defaults to SHA-1.
func TestVerify_SHA256DigestRoundTrip(t *testing.T) {
	cert, key := testCert(t)
	v, err := NewVerifier()
	if err != nil {
		t.Fatal(err)
	}

	payload := []byte(`{"deploy":"sha256-test"}`)

	sd, err := gopkcs7.NewSignedData()
	if err != nil {
		t.Fatal(err)
	}
	sd.SetContent(payload)
	if err := sd.AddSigner(cert, key, nil, gopkcs7.OIDDigestAlgorithmSHA256, gopkcs7.SignerInfoConfig{}); err != nil {
		t.Fatal(err)
	}
	der, err := sd.Finish()
	if err != nil {
		t.Fatal(err)
	}

	var buf bytes.Buffer
	if err := pem.Encode(&buf, &pem.Block{Type: "PKCS7", Bytes: der}); err != nil {
		t.Fatal(err)
	}

	got, err := v.Verify(buf.Bytes())
	if err != nil {
		t.Fatalf("SHA-256 signed envelope should verify: %v", err)
	}
	if !bytes.Equal(got, payload) {
		t.Errorf("content mismatch: got %q, want %q", got, payload)
	}
}

// buildMinimalSignedDataDER constructs a minimal PKCS7 SignedData DER envelope
// with the given raw content DER as the encapContentInfo eContent. No certs or
// signers are included — this is for testing Parse content extraction only.
func buildMinimalSignedDataDER(t *testing.T, contentOctetStringDER []byte) []byte {
	t.Helper()

	type contentInfoType struct {
		ContentType asn1.ObjectIdentifier
		Content     asn1.RawValue `asn1:"explicit,optional,tag:0"`
	}

	type minimalSignedData struct {
		Version     int
		DigestAlgos asn1.RawValue
		ContentInfo contentInfoType
		SignerInfos asn1.RawValue
	}

	emptySet := asn1.RawValue{Class: 0, Tag: 17, IsCompound: true}

	sd := minimalSignedData{
		Version:     1,
		DigestAlgos: emptySet,
		ContentInfo: contentInfoType{
			ContentType: asn1.ObjectIdentifier{1, 2, 840, 113549, 1, 7, 1},
			Content:     asn1.RawValue{Class: 2, Tag: 0, Bytes: contentOctetStringDER, IsCompound: true},
		},
		SignerInfos: emptySet,
	}

	inner, err := asn1.Marshal(sd)
	if err != nil {
		t.Fatal(err)
	}

	outer := contentInfoType{
		ContentType: asn1.ObjectIdentifier{1, 2, 840, 113549, 1, 7, 2},
		Content:     asn1.RawValue{Class: 2, Tag: 0, Bytes: inner, IsCompound: true},
	}

	der, err := asn1.Marshal(outer)
	if err != nil {
		t.Fatal(err)
	}
	return der
}

// TestParse_ContentExtractionFromConstructedOctetString verifies that the
// library correctly extracts content from a CONSTRUCTED OCTET STRING (tag 0x24)
// containing multiple primitive segments, not just a single primitive OCTET
// STRING (tag 0x04).
//
// RFC 5652 §5.2.1 defines eContent as OCTET STRING which MAY be constructed
// per X.690. AWS's signing infrastructure produces constructed encoding.
// The fork's extractEncapsulatedContent loops over all segments and
// concatenates them (unlike go.mozilla.org/pkcs7 v0.9.0 which only read the
// first segment, causing MessageDigestMismatch).
func TestParse_ContentExtractionFromConstructedOctetString(t *testing.T) {
	// Build two primitive OCTET STRING segments that concatenate to the
	// full payload. This is what a BER/DER encoder (or AWS signer) may emit.
	segA := []byte("Hello")
	segB := []byte("World")
	fullPayload := append(segA, segB...)

	segADER, _ := asn1.Marshal(segA) // 04 05 48 65 6C 6C 6F
	segBDER, _ := asn1.Marshal(segB) // 04 05 57 6F 72 6C 64

	// Constructed OCTET STRING: tag 0x24, containing two primitive segments.
	constructedOctet, err := asn1.Marshal(asn1.RawValue{
		Class: 0, Tag: 4, IsCompound: true,
		Bytes: append(segADER, segBDER...),
	})
	if err != nil {
		t.Fatal(err)
	}

	// Also test the baseline: primitive OCTET STRING (tag 0x04).
	primitiveOctet, _ := asn1.Marshal(fullPayload)

	t.Run("primitive baseline", func(t *testing.T) {
		der := buildMinimalSignedDataDER(t, primitiveOctet)
		p7, err := gopkcs7.Parse(der)
		if err != nil {
			t.Fatalf("Parse: %v", err)
		}
		if !bytes.Equal(p7.Content, fullPayload) {
			t.Errorf("content mismatch\n  want: %x\n  got:  %x", fullPayload, p7.Content)
		}
	})

	t.Run("constructed OCTET STRING", func(t *testing.T) {
		der := buildMinimalSignedDataDER(t, constructedOctet)
		p7, err := gopkcs7.Parse(der)
		if err != nil {
			t.Fatalf("Parse: %v", err)
		}

		// The library must concatenate all primitive segments from the
		// constructed OCTET STRING. If only the first segment is returned,
		// every AWS-signed envelope using this encoding will fail with
		// MessageDigestMismatch.
		if !bytes.Equal(p7.Content, fullPayload) {
			t.Fatalf("constructed OCTET STRING: content not correctly reassembled\n"+
				"  want: %x (%q)\n"+
				"  got:  %x (%q)",
				fullPayload, fullPayload, p7.Content, p7.Content)
		}
	})
}

// TestParse_MessageDigestMismatchOnReEncodedContent verifies that content
// encoded as a constructed OCTET STRING extracts identically to the primitive
// form. The library signs with a primitive OCTET STRING; this test confirms
// Parse extracts the same bytes from a constructed encoding, so Verify will
// not produce a MessageDigestMismatch for AWS-signed envelopes.
func TestParse_MessageDigestMismatchOnReEncodedContent(t *testing.T) {
	cert, key := testCert(t)
	payload := []byte("digest-check-payload")

	// Sign with the library — messageDigest is computed over payload.
	sd, err := gopkcs7.NewSignedData()
	if err != nil {
		t.Fatal(err)
	}
	sd.SetContent(payload)
	if err := sd.AddSigner(cert, key, nil, nil, gopkcs7.SignerInfoConfig{}); err != nil {
		t.Fatal(err)
	}
	goodDER, err := sd.Finish()
	if err != nil {
		t.Fatal(err)
	}

	// Baseline: the unmodified envelope must verify.
	p7, err := gopkcs7.Parse(goodDER)
	if err != nil {
		t.Fatal(err)
	}
	if err := p7.Verify(); err != nil {
		t.Fatalf("baseline Verify should pass: %v", err)
	}
	if !bytes.Equal(p7.Content, payload) {
		t.Fatalf("baseline content mismatch: got %x, want %x", p7.Content, payload)
	}

	// Build a constructed OCTET STRING encoding of the same payload.
	mid := len(payload) / 2
	seg1DER, _ := asn1.Marshal(payload[:mid])
	seg2DER, _ := asn1.Marshal(payload[mid:])
	constructedOctet, _ := asn1.Marshal(asn1.RawValue{
		Class: 0, Tag: 4, IsCompound: true,
		Bytes: append(seg1DER, seg2DER...),
	})

	// Build a minimal envelope with the constructed content. No signers, so
	// we test only the Parse→Content extraction path.
	constructedDER := buildMinimalSignedDataDER(t, constructedOctet)
	p7c, err := gopkcs7.Parse(constructedDER)
	if err != nil {
		t.Fatalf("Parse with constructed OCTET STRING: %v", err)
	}

	// The library must extract the full payload from the constructed OCTET
	// STRING. If it only returns the first segment, any signature computed
	// over the full content will produce a MessageDigestMismatch.
	if !bytes.Equal(p7c.Content, payload) {
		t.Fatalf("constructed OCTET STRING: content not correctly reassembled\n"+
			"  want: %x (%d bytes)\n"+
			"  got:  %x (%d bytes)",
			payload, len(payload),
			p7c.Content, len(p7c.Content))
	}

	// Verify our wrapper surfaces parse/verify errors for malformed envelopes.
	v, err := NewVerifier()
	if err != nil {
		t.Fatal(err)
	}
	var pemBuf bytes.Buffer
	if err := pem.Encode(&pemBuf, &pem.Block{Type: "PKCS7", Bytes: goodDER}); err != nil {
		t.Fatal(err)
	}
	got, err := v.Verify(pemBuf.Bytes())
	if err != nil {
		t.Fatalf("Verifier.Verify should pass for library-signed envelope: %v", err)
	}
	if !bytes.Equal(got, payload) {
		t.Errorf("Verifier.Verify content mismatch: got %q, want %q", got, payload)
	}
}

// TestParse_ContentDigestOverRawVsExtractedBytes checks whether the library
// hashes the same bytes that NewSignedData originally hashed. The library signs
// over sd.data (the raw input) and during verification hashes p7.Content (the
// bytes extracted from encapContentInfo after ASN.1 parsing). If these differ,
// any externally-produced envelope with the same content will fail verification.
func TestParse_ContentDigestOverRawVsExtractedBytes(t *testing.T) {
	cert, key := testCert(t)

	cases := []struct {
		name    string
		content []byte
	}{
		// Content that is itself a valid DER OCTET STRING. The inner
		// asn1.Unmarshal in parseSignedData must not "unwrap" it further.
		{"content is DER OCTET STRING", func() []byte {
			b, _ := asn1.Marshal([]byte("nested"))
			return b
		}()},
		// Content that is a valid DER SEQUENCE — parseSignedData checks
		// compound.Tag == 4; a SEQUENCE (tag 0x30) takes the else branch
		// and uses compound.Bytes, which strips the SEQUENCE wrapper.
		{"content is DER SEQUENCE", func() []byte {
			b, _ := asn1.Marshal(struct{ A int }{42})
			return b
		}()},
		// Content starting with high tag bytes (multi-byte tag form).
		{"high tag prefix", []byte{0x1f, 0x81, 0x00, 0x03, 0x41, 0x42}},
		// Long-form length encoding — content > 127 bytes.
		{"long form length payload", bytes.Repeat([]byte("A"), 200)},
	}

	v, err := NewVerifier()
	if err != nil {
		t.Fatal(err)
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			pemData := signAndPEM(t, tc.content, cert, key)

			got, err := v.Verify(pemData)
			if err != nil {
				t.Fatalf("Verify failed — content bytes diverged during extraction: %v", err)
			}
			if !bytes.Equal(got, tc.content) {
				t.Errorf("extracted content differs from signed content\n"+
					"  signed:    %x\n"+
					"  extracted: %x", tc.content, got)
			}
		})
	}
}

// constructedOctetStringDER builds a DER-encoded constructed OCTET STRING
// (tag 0x24) from the given segments. Each segment becomes one primitive
// OCTET STRING (tag 0x04) inside the constructed wrapper.
func constructedOctetStringDER(t *testing.T, segments ...[]byte) []byte {
	t.Helper()
	var inner []byte
	for _, seg := range segments {
		b, err := asn1.Marshal(seg)
		if err != nil {
			t.Fatal(err)
		}
		inner = append(inner, b...)
	}
	der, err := asn1.Marshal(asn1.RawValue{
		Class: 0, Tag: 4, IsCompound: true,
		Bytes: inner,
	})
	if err != nil {
		t.Fatal(err)
	}
	return der
}

// parseContent is a shorthand: build a minimal SignedData DER with the given
// content OCTET STRING encoding, Parse it, and return the extracted content.
func parseContent(t *testing.T, octetStringDER []byte) []byte {
	t.Helper()
	der := buildMinimalSignedDataDER(t, octetStringDER)
	p7, err := gopkcs7.Parse(der)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	return p7.Content
}

// TestParse_ConstructedOctetStringSegmentCounts verifies content extraction for
// constructed OCTET STRINGs with varying numbers of segments. A correct
// implementation must concatenate all segments regardless of count.
func TestParse_ConstructedOctetStringSegmentCounts(t *testing.T) {
	t.Run("single segment", func(t *testing.T) {
		// Degenerate case: constructed tag 0x24 wrapping one primitive 0x04.
		// Some encoders emit this when the content fits in one chunk.
		payload := []byte("only-segment")
		got := parseContent(t, constructedOctetStringDER(t, payload))
		if !bytes.Equal(got, payload) {
			t.Fatalf("single segment: got %x, want %x", got, payload)
		}
	})

	t.Run("three segments", func(t *testing.T) {
		// Three-way split exercises the loop beyond the first two iterations.
		a, b, c := []byte("one-"), []byte("two-"), []byte("three")
		want := []byte("one-two-three")
		got := parseContent(t, constructedOctetStringDER(t, a, b, c))
		if !bytes.Equal(got, want) {
			t.Fatalf("three segments: got %x (%q), want %x (%q)", got, got, want, want)
		}
	})

	t.Run("many small segments", func(t *testing.T) {
		// 20 single-byte segments — tests accumulation across many iterations.
		segments := make([][]byte, 20)
		var want []byte
		for i := range segments {
			segments[i] = []byte{byte('A' + i)}
			want = append(want, byte('A'+i))
		}
		got := parseContent(t, constructedOctetStringDER(t, segments...))
		if !bytes.Equal(got, want) {
			t.Fatalf("many segments: got %x (%d bytes), want %x (%d bytes)",
				got, len(got), want, len(want))
		}
	})

	t.Run("segments with long form length", func(t *testing.T) {
		// Each segment > 127 bytes forces multi-byte DER length encoding.
		seg1 := bytes.Repeat([]byte("X"), 200)
		seg2 := bytes.Repeat([]byte("Y"), 300)
		want := append(seg1, seg2...)
		got := parseContent(t, constructedOctetStringDER(t, seg1, seg2))
		if !bytes.Equal(got, want) {
			t.Fatalf("long segments: got %d bytes, want %d bytes", len(got), len(want))
		}
	})

	t.Run("empty segment among non-empty", func(t *testing.T) {
		// An empty primitive OCTET STRING (04 00) is valid. The parser must
		// not choke on it or treat it as a terminator.
		a := []byte("before")
		b := []byte{}
		c := []byte("after")
		want := []byte("beforeafter")
		got := parseContent(t, constructedOctetStringDER(t, a, b, c))
		if !bytes.Equal(got, want) {
			t.Fatalf("empty middle: got %x (%q), want %x (%q)", got, got, want, want)
		}
	})
}

// TestVerify_SHA384and512RoundTrip verifies that SHA-384 and SHA-512 signed
// envelopes survive the full sign → Verify cycle. AWS may use any SHA-2
// variant after migrating off SHA-1.
func TestVerify_SHA384and512RoundTrip(t *testing.T) {
	cert, key := testCert(t)
	v, err := NewVerifier()
	if err != nil {
		t.Fatal(err)
	}

	cases := []struct {
		name string
		oid  asn1.ObjectIdentifier
	}{
		{"SHA-384", gopkcs7.OIDDigestAlgorithmSHA384},
		{"SHA-512", gopkcs7.OIDDigestAlgorithmSHA512},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			payload := []byte(`{"deploy":"` + tc.name + `"}`)

			sd, err := gopkcs7.NewSignedData()
			if err != nil {
				t.Fatal(err)
			}
			sd.SetContent(payload)
			if err := sd.AddSigner(cert, key, nil, tc.oid, gopkcs7.SignerInfoConfig{}); err != nil {
				t.Fatal(err)
			}
			der, err := sd.Finish()
			if err != nil {
				t.Fatal(err)
			}

			var buf bytes.Buffer
			if err := pem.Encode(&buf, &pem.Block{Type: "PKCS7", Bytes: der}); err != nil {
				t.Fatal(err)
			}

			got, err := v.Verify(buf.Bytes())
			if err != nil {
				t.Fatalf("%s signed envelope should verify: %v", tc.name, err)
			}
			if !bytes.Equal(got, payload) {
				t.Errorf("content mismatch: got %q, want %q", got, payload)
			}
		})
	}
}

// TestVerify_RejectsNonPKCS7PEMBlock confirms the verifier rejects PEM blocks
// with types other than "PKCS7". A CERTIFICATE or other block must not be
// silently accepted.
func TestVerify_RejectsNonPKCS7PEMBlock(t *testing.T) {
	v, err := NewVerifier()
	if err != nil {
		t.Fatal(err)
	}

	var buf bytes.Buffer
	if err := pem.Encode(&buf, &pem.Block{Type: "CERTIFICATE", Bytes: []byte("not-pkcs7")}); err != nil {
		t.Fatal(err)
	}

	_, err = v.Verify(buf.Bytes())
	if err == nil {
		t.Fatal("should reject non-PKCS7 PEM block")
	}
}

// TestVerify_RejectsEmptyInput confirms the verifier returns an error for
// various forms of empty or missing input.
func TestVerify_RejectsEmptyInput(t *testing.T) {
	v, err := NewVerifier()
	if err != nil {
		t.Fatal(err)
	}

	cases := []struct {
		name  string
		input []byte
	}{
		{"nil", nil},
		{"empty", []byte{}},
		{"whitespace only", []byte("   \n\t  ")},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := v.Verify(tc.input)
			if err == nil {
				t.Fatal("should return error for empty/nil input")
			}
		})
	}
}

// TestParse_ContentRoundTripProperty checks the property that for any content
// bytes, Parse(Finish(sign(content))).Content == content. This is a weaker form
// of property-based testing that covers a range of payload sizes including
// boundary values for DER length encoding (0, 1, 127, 128, 255, 256).
func TestParse_ContentRoundTripProperty(t *testing.T) {
	cert, key := testCert(t)

	// Boundary values for DER length encoding transitions.
	sizes := []int{0, 1, 127, 128, 255, 256, 1000}

	for _, size := range sizes {
		t.Run(fmt.Sprintf("%d_bytes", size), func(t *testing.T) {
			// Generate deterministic content.
			content := make([]byte, size)
			for i := range content {
				content[i] = byte(i % 251) // prime modulus avoids repeating patterns
			}

			sd, err := gopkcs7.NewSignedData()
			if err != nil {
				t.Fatal(err)
			}
			sd.SetContent(content)
			if err := sd.AddSigner(cert, key, nil, nil, gopkcs7.SignerInfoConfig{}); err != nil {
				t.Fatal(err)
			}
			der, err := sd.Finish()
			if err != nil {
				t.Fatal(err)
			}

			p7, err := gopkcs7.Parse(der)
			if err != nil {
				t.Fatalf("Parse: %v", err)
			}
			if !bytes.Equal(p7.Content, content) {
				t.Errorf("round-trip failed for %d bytes: got %d bytes", size, len(p7.Content))
			}
		})
	}
}

// fuzzTestCert returns a reusable certificate and key for fuzz targets.
// RSA key generation is expensive, so we generate once per fuzz function
// and reuse across iterations.
func fuzzTestCert() (*x509.Certificate, *rsa.PrivateKey) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		panic(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "fuzz-signer"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(24 * time.Hour),
	}
	certDER, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		panic(err)
	}
	cert, err := x509.ParseCertificate(certDER)
	if err != nil {
		panic(err)
	}
	return cert, key
}

// FuzzVerify_NoPanic feeds arbitrary bytes to Verifier.Verify. The verifier
// must never panic regardless of input — it must return an error for anything
// that is not a well-formed PEM-wrapped PKCS7 SignedData envelope.
func FuzzVerify_NoPanic(f *testing.F) {
	// Seed with structurally interesting inputs.
	f.Add([]byte(""))
	f.Add([]byte("not pem"))
	f.Add([]byte("-----BEGIN PKCS7-----\n-----END PKCS7-----\n"))
	f.Add([]byte("-----BEGIN PKCS7-----\nAAAA\n-----END PKCS7-----\n"))
	f.Add([]byte("-----BEGIN CERTIFICATE-----\nAAAA\n-----END CERTIFICATE-----\n"))

	// Seed with a valid signed envelope so the fuzzer can mutate from a known-good state.
	cert, key := fuzzTestCert()
	sd, err := gopkcs7.NewSignedData()
	if err != nil {
		f.Fatal(err)
	}
	sd.SetContent([]byte("seed-payload"))
	if err := sd.AddSigner(cert, key, nil, nil, gopkcs7.SignerInfoConfig{}); err != nil {
		f.Fatal(err)
	}
	der, err := sd.Finish()
	if err != nil {
		f.Fatal(err)
	}
	var pemBuf bytes.Buffer
	if err := pem.Encode(&pemBuf, &pem.Block{Type: "PKCS7", Bytes: der}); err != nil {
		f.Fatal(err)
	}
	f.Add(pemBuf.Bytes())

	v, err := NewVerifier()
	if err != nil {
		f.Fatal(err)
	}

	f.Fuzz(func(t *testing.T, data []byte) {
		// Must not panic. Errors are expected and fine.
		v.Verify(data) //nolint:errcheck
	})
}

// FuzzVerify_ContentRoundTrip feeds arbitrary content to NewSignedData → sign →
// PEM-encode → Verifier.Verify and asserts the returned content is identical.
// This checks the property: for all content c, Verify(Sign(c)) == c.
func FuzzVerify_ContentRoundTrip(f *testing.F) {
	f.Add([]byte(""))
	f.Add([]byte("hello"))
	f.Add([]byte(`{"version":0.0,"os":"linux"}`))
	f.Add(bytes.Repeat([]byte("A"), 256))
	// Content that resembles ASN.1 — the parser must not re-interpret it.
	f.Add([]byte{0x04, 0x03, 0x41, 0x42, 0x43})
	f.Add([]byte{0x30, 0x03, 0x02, 0x01, 0x01})
	f.Add([]byte{0x00})
	f.Add([]byte{0x24, 0x06, 0x04, 0x01, 0x41, 0x04, 0x01, 0x42})

	cert, key := fuzzTestCert()
	v, err := NewVerifier()
	if err != nil {
		f.Fatal(err)
	}

	f.Fuzz(func(t *testing.T, content []byte) {
		sd, err := gopkcs7.NewSignedData()
		if err != nil {
			return
		}
		sd.SetContent(content)
		if err := sd.AddSigner(cert, key, nil, nil, gopkcs7.SignerInfoConfig{}); err != nil {
			return
		}
		der, err := sd.Finish()
		if err != nil {
			return
		}

		var buf bytes.Buffer
		if err := pem.Encode(&buf, &pem.Block{Type: "PKCS7", Bytes: der}); err != nil {
			return
		}

		got, err := v.Verify(buf.Bytes())
		if err != nil {
			t.Fatalf("Verify failed for content len=%d: %v", len(content), err)
		}
		if !bytes.Equal(got, content) {
			t.Fatalf("content not preserved: len(input)=%d len(output)=%d", len(content), len(got))
		}
	})
}

// FuzzParse_ConstructedOctetStringConcatenation feeds arbitrary byte slices as
// two segments of a constructed OCTET STRING and asserts the library
// concatenates them correctly. This is the core property the fork must satisfy.
func FuzzParse_ConstructedOctetStringConcatenation(f *testing.F) {
	f.Add([]byte("Hello"), []byte("World"))
	f.Add([]byte(""), []byte("non-empty"))
	f.Add([]byte("non-empty"), []byte(""))
	f.Add([]byte(""), []byte(""))
	f.Add([]byte{0x00}, []byte{0xff})
	f.Add(bytes.Repeat([]byte("X"), 200), bytes.Repeat([]byte("Y"), 200))
	// Segments that look like ASN.1 TLVs themselves.
	f.Add([]byte{0x04, 0x01, 0x41}, []byte{0x30, 0x00})

	f.Fuzz(func(t *testing.T, seg1, seg2 []byte) {
		// Defensive copy: the fuzzer may provide slices that share
		// underlying arrays, and append can alias memory.
		want := make([]byte, len(seg1)+len(seg2))
		copy(want, seg1)
		copy(want[len(seg1):], seg2)

		// Build primitive OCTET STRING DER for each segment.
		seg1DER, err := asn1.Marshal(seg1)
		if err != nil {
			return
		}
		seg2DER, err := asn1.Marshal(seg2)
		if err != nil {
			return
		}

		// Build constructed OCTET STRING containing both segments.
		innerDER := make([]byte, len(seg1DER)+len(seg2DER))
		copy(innerDER, seg1DER)
		copy(innerDER[len(seg1DER):], seg2DER)
		constructed, err := asn1.Marshal(asn1.RawValue{
			Class: 0, Tag: 4, IsCompound: true,
			Bytes: innerDER,
		})
		if err != nil {
			return
		}

		// Wrap in minimal SignedData and parse.
		der := buildMinimalSignedDataDER(t, constructed)
		p7, err := gopkcs7.Parse(der)
		if err != nil {
			t.Fatalf("Parse: %v", err)
		}

		if !bytes.Equal(p7.Content, want) {
			t.Fatalf("segments not concatenated:\n  seg1=%x\n  seg2=%x\n  want=%x\n  got=%x",
				seg1, seg2, want, p7.Content)
		}
	})
}

// FuzzParse_DERInput feeds arbitrary bytes directly to gopkcs7.Parse. The
// parser must never panic regardless of how malformed the input is.
func FuzzParse_DERInput(f *testing.F) {
	f.Add([]byte{})
	f.Add([]byte{0x30, 0x00})
	f.Add([]byte{0x30, 0x80, 0x00, 0x00})

	// Seed with a valid SignedData DER.
	cert, key := fuzzTestCert()
	sd, err := gopkcs7.NewSignedData()
	if err != nil {
		f.Fatal(err)
	}
	sd.SetContent([]byte("fuzz-seed"))
	if err := sd.AddSigner(cert, key, nil, nil, gopkcs7.SignerInfoConfig{}); err != nil {
		f.Fatal(err)
	}
	der, err := sd.Finish()
	if err != nil {
		f.Fatal(err)
	}
	f.Add(der)

	// Seed with a minimal constructed OCTET STRING envelope.
	primOctet, _ := asn1.Marshal([]byte("fuzz"))
	f.Add(buildMinimalSignedDataDERForFuzz(primOctet))

	f.Fuzz(func(_ *testing.T, data []byte) {
		// Must not panic. Errors are expected.
		gopkcs7.Parse(data) //nolint:errcheck
	})
}

// buildMinimalSignedDataDERForFuzz is the non-testing.T variant for fuzz seed
// construction. Panics on error since seeds are built during Fuzz setup.
func buildMinimalSignedDataDERForFuzz(contentOctetStringDER []byte) []byte {
	type contentInfoType struct {
		ContentType asn1.ObjectIdentifier
		Content     asn1.RawValue `asn1:"explicit,optional,tag:0"`
	}
	type minimalSignedData struct {
		Version     int
		DigestAlgos asn1.RawValue
		ContentInfo contentInfoType
		SignerInfos asn1.RawValue
	}

	emptySet := asn1.RawValue{Class: 0, Tag: 17, IsCompound: true}
	sd := minimalSignedData{
		Version:     1,
		DigestAlgos: emptySet,
		ContentInfo: contentInfoType{
			ContentType: asn1.ObjectIdentifier{1, 2, 840, 113549, 1, 7, 1},
			Content:     asn1.RawValue{Class: 2, Tag: 0, Bytes: contentOctetStringDER, IsCompound: true},
		},
		SignerInfos: emptySet,
	}
	inner, err := asn1.Marshal(sd)
	if err != nil {
		panic(err)
	}
	outer := contentInfoType{
		ContentType: asn1.ObjectIdentifier{1, 2, 840, 113549, 1, 7, 2},
		Content:     asn1.RawValue{Class: 2, Tag: 0, Bytes: inner, IsCompound: true},
	}
	der, err := asn1.Marshal(outer)
	if err != nil {
		panic(err)
	}
	return der
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
