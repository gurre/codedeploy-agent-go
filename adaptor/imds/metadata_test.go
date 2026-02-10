package imds

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

// newTestClient creates a Client that talks to the given httptest.Server
// instead of the real IMDS endpoint. This allows testing without network access.
func newTestClient(t *testing.T, serverURL string, disableIMDSv1 bool) *Client {
	t.Helper()
	return &Client{
		httpClient:    &http.Client{Timeout: 5 * time.Second},
		baseURL:       serverURL,
		logger:        slog.Default(),
		disableIMDSv1: disableIMDSv1,
	}
}

// fakeIMDS is a configurable handler for an httptest.Server that mimics
// IMDS token and metadata endpoints. Fields are ordered largest to smallest.
type fakeIMDS struct {
	identityBody  string
	partitionBody string
	domainBody    string
	instanceBody  string
	tokenBody     string
	tokenStatus   int
	identityFails int32 // how many times identity should return 500 before succeeding
	tokenPUTs     atomic.Int32
	identityGETs  atomic.Int32
}

func (f *fakeIMDS) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	switch {
	case r.Method == http.MethodPut && r.URL.Path == tokenPath:
		f.tokenPUTs.Add(1)
		status := f.tokenStatus
		if status == 0 {
			status = http.StatusOK
		}
		if status != http.StatusOK {
			w.WriteHeader(status)
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(f.tokenBody))

	case r.Method == http.MethodGet && r.URL.Path == identityPath:
		count := f.identityGETs.Add(1)
		if count <= f.identityFails {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(f.identityBody))

	case r.Method == http.MethodGet && r.URL.Path == partitionPath:
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(f.partitionBody))

	case r.Method == http.MethodGet && r.URL.Path == domainPath:
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(f.domainBody))

	case r.Method == http.MethodGet && r.URL.Path == instancePath:
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(f.instanceBody))

	default:
		w.WriteHeader(http.StatusNotFound)
	}
}

const testIdentityJSON = `{"region":"us-east-1","accountId":"123456789012","instanceId":"i-abc123"}`

// TestIdentityDocument verifies that a well-formed identity document JSON
// response is parsed into the correct struct fields. This test exists because
// incorrect field mapping would silently break every downstream consumer
// (Region, HostIdentifier, etc.).
func TestIdentityDocument(t *testing.T) {
	fake := &fakeIMDS{
		identityBody: testIdentityJSON,
		tokenBody:    "test-token-v2",
	}
	srv := httptest.NewServer(fake)
	defer srv.Close()

	client := newTestClient(t, srv.URL, false)
	doc, err := client.IdentityDocument(context.Background())
	if err != nil {
		t.Fatalf("IdentityDocument() unexpected error: %v", err)
	}

	if doc.Region != "us-east-1" {
		t.Errorf("Region = %q, want %q", doc.Region, "us-east-1")
	}
	if doc.AccountID != "123456789012" {
		t.Errorf("AccountID = %q, want %q", doc.AccountID, "123456789012")
	}
	if doc.InstanceID != "i-abc123" {
		t.Errorf("InstanceID = %q, want %q", doc.InstanceID, "i-abc123")
	}
}

// TestRegion verifies that Region delegates to IdentityDocument and returns
// only the region string. This test exists to confirm the extraction layer
// between the raw document and the convenience accessor.
func TestRegion(t *testing.T) {
	fake := &fakeIMDS{
		identityBody: testIdentityJSON,
		tokenBody:    "test-token-v2",
	}
	srv := httptest.NewServer(fake)
	defer srv.Close()

	client := newTestClient(t, srv.URL, false)
	region, err := client.Region(context.Background())
	if err != nil {
		t.Fatalf("Region() unexpected error: %v", err)
	}
	if region != "us-east-1" {
		t.Errorf("Region() = %q, want %q", region, "us-east-1")
	}
}

// TestHostIdentifier verifies the ARN is assembled from the identity document
// and partition endpoint. This test exists because the ARN format is a contract
// with CodeDeploy and any deviation would cause registration failures.
func TestHostIdentifier(t *testing.T) {
	fake := &fakeIMDS{
		identityBody:  testIdentityJSON,
		partitionBody: "aws",
		tokenBody:     "test-token-v2",
	}
	srv := httptest.NewServer(fake)
	defer srv.Close()

	client := newTestClient(t, srv.URL, false)
	arn, err := client.HostIdentifier(context.Background())
	if err != nil {
		t.Fatalf("HostIdentifier() unexpected error: %v", err)
	}

	want := "arn:aws:ec2:us-east-1:123456789012:instance/i-abc123"
	if arn != want {
		t.Errorf("HostIdentifier() = %q, want %q", arn, want)
	}
}

// TestIMDSv2TokenFlow verifies that the client obtains an IMDSv2 token via PUT
// on the first request and caches it for subsequent requests. This test exists
// because redundant token requests waste time and may trigger IMDS rate limits.
func TestIMDSv2TokenFlow(t *testing.T) {
	fake := &fakeIMDS{
		identityBody:  testIdentityJSON,
		partitionBody: "aws",
		tokenBody:     "cached-token-v2",
	}
	srv := httptest.NewServer(fake)
	defer srv.Close()

	client := newTestClient(t, srv.URL, false)

	// First call: should trigger a PUT for the token.
	_, err := client.IdentityDocument(context.Background())
	if err != nil {
		t.Fatalf("first IdentityDocument() unexpected error: %v", err)
	}

	// Second call (different endpoint): should reuse the cached token.
	_, err = client.Partition(context.Background())
	if err != nil {
		t.Fatalf("Partition() unexpected error: %v", err)
	}

	puts := fake.tokenPUTs.Load()
	if puts != 1 {
		t.Errorf("token PUT count = %d, want 1 (token should be cached after first request)", puts)
	}
}

// TestIMDSv1Fallback verifies that when the IMDSv2 token PUT returns 403 the
// client falls back to IMDSv1 (GET without token header). This test exists
// because some EC2 configurations disable IMDSv2 and the agent must still work.
func TestIMDSv1Fallback(t *testing.T) {
	var gotTokenHeader atomic.Value // stores the last token header seen on GET

	mux := http.NewServeMux()
	mux.HandleFunc("PUT "+tokenPath, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusForbidden)
	})
	mux.HandleFunc("GET "+identityPath, func(w http.ResponseWriter, r *http.Request) {
		gotTokenHeader.Store(r.Header.Get("X-aws-ec2-metadata-token"))
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(testIdentityJSON))
	})

	srv := httptest.NewServer(mux)
	defer srv.Close()

	client := newTestClient(t, srv.URL, false)
	doc, err := client.IdentityDocument(context.Background())
	if err != nil {
		t.Fatalf("IdentityDocument() with v1 fallback unexpected error: %v", err)
	}
	if doc.Region != "us-east-1" {
		t.Errorf("Region = %q, want %q", doc.Region, "us-east-1")
	}

	// The GET request must NOT carry a token header when falling back to v1.
	if hdr, ok := gotTokenHeader.Load().(string); ok && hdr != "" {
		t.Errorf("v1 fallback GET included token header %q, want empty", hdr)
	}
}

// TestIMDSv1Disabled verifies that when disableIMDSv1 is true and the token
// PUT fails, the client returns an error instead of falling back to v1. This
// test exists to enforce the security policy of v2-only environments.
func TestIMDSv1Disabled(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("PUT "+tokenPath, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusForbidden)
	})
	mux.HandleFunc("GET "+identityPath, func(w http.ResponseWriter, _ *http.Request) {
		t.Error("GET should never be called when v1 is disabled and token fails")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(testIdentityJSON))
	})

	srv := httptest.NewServer(mux)
	defer srv.Close()

	client := newTestClient(t, srv.URL, true)
	_, err := client.IdentityDocument(context.Background())
	if err == nil {
		t.Fatal("IdentityDocument() should fail when IMDSv2 token fails and v1 is disabled")
	}
}

// TestRetryOnError verifies that transient server errors (HTTP 500) are retried
// and the request succeeds on a subsequent attempt. This test exists because
// IMDS can return transient errors under load and the agent must be resilient.
// Note: the retry backoff is time.Duration(attempt) * time.Second, so this test
// takes ~1 second for the first retry delay.
func TestRetryOnError(t *testing.T) {
	fake := &fakeIMDS{
		identityBody:  testIdentityJSON,
		tokenBody:     "retry-token",
		identityFails: 1, // first GET returns 500, second succeeds
	}
	srv := httptest.NewServer(fake)
	defer srv.Close()

	client := newTestClient(t, srv.URL, false)
	doc, err := client.IdentityDocument(context.Background())
	if err != nil {
		t.Fatalf("IdentityDocument() after retry unexpected error: %v", err)
	}
	if doc.InstanceID != "i-abc123" {
		t.Errorf("InstanceID = %q, want %q", doc.InstanceID, "i-abc123")
	}

	gets := fake.identityGETs.Load()
	if gets < 2 {
		t.Errorf("identity GET count = %d, want >= 2 (retry should have fired)", gets)
	}
}

// TestDomain verifies that Domain returns the AWS domain from the IMDS
// services/domain endpoint. This test exists because the domain is used
// to construct service endpoints (e.g. codedeploy.{region}.{domain}).
func TestDomain(t *testing.T) {
	fake := &fakeIMDS{
		identityBody: testIdentityJSON,
		domainBody:   "amazonaws.com",
		tokenBody:    "test-token-v2",
	}
	srv := httptest.NewServer(fake)
	defer srv.Close()

	client := newTestClient(t, srv.URL, false)
	domain, err := client.Domain(context.Background())
	if err != nil {
		t.Fatalf("Domain() unexpected error: %v", err)
	}
	if domain != "amazonaws.com" {
		t.Errorf("Domain() = %q, want %q", domain, "amazonaws.com")
	}
}

// TestInstanceID verifies that InstanceID returns the instance ID from the IMDS
// instance-id endpoint. This test exists because InstanceID is a direct metadata
// accessor distinct from IdentityDocument parsing and must return the raw value.
func TestInstanceID(t *testing.T) {
	fake := &fakeIMDS{
		identityBody: testIdentityJSON,
		instanceBody: "i-0123456789abcdef0",
		tokenBody:    "test-token-v2",
	}
	srv := httptest.NewServer(fake)
	defer srv.Close()

	client := newTestClient(t, srv.URL, false)
	instanceID, err := client.InstanceID(context.Background())
	if err != nil {
		t.Fatalf("InstanceID() unexpected error: %v", err)
	}
	if instanceID != "i-0123456789abcdef0" {
		t.Errorf("InstanceID() = %q, want %q", instanceID, "i-0123456789abcdef0")
	}
}

// TestTokenRefreshOn401 verifies that when the IMDS returns 401 Unauthorized,
// the client clears its cached token, obtains a new one, and retries. This test
// exists because IMDS tokens expire after TTL seconds and the agent must handle
// this transparently rather than failing the deployment.
func TestTokenRefreshOn401(t *testing.T) {
	var requestCount atomic.Int32
	var tokenCount atomic.Int32

	mux := http.NewServeMux()
	mux.HandleFunc("PUT "+tokenPath, func(w http.ResponseWriter, _ *http.Request) {
		count := tokenCount.Add(1)
		// Return different tokens to prove the refresh happened
		w.WriteHeader(http.StatusOK)
		_, _ = fmt.Fprintf(w, "token-%d", count)
	})
	mux.HandleFunc("GET "+identityPath, func(w http.ResponseWriter, r *http.Request) {
		count := requestCount.Add(1)
		if count == 1 {
			// First request: return 401 to trigger token refresh
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		// Subsequent requests: succeed
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(testIdentityJSON))
	})

	srv := httptest.NewServer(mux)
	defer srv.Close()

	client := newTestClient(t, srv.URL, false)
	doc, err := client.IdentityDocument(context.Background())
	if err != nil {
		t.Fatalf("IdentityDocument() unexpected error: %v", err)
	}
	if doc.Region != "us-east-1" {
		t.Errorf("Region = %q, want us-east-1", doc.Region)
	}

	// Should have obtained at least 2 tokens (initial + refresh after 401)
	tokens := tokenCount.Load()
	if tokens < 2 {
		t.Errorf("token PUT count = %d, want >= 2 (refresh should have happened)", tokens)
	}
}

// TestNon200StatusReturnsError verifies that a non-200, non-401 status code
// from IMDS returns a descriptive error rather than empty data.
func TestNon200StatusReturnsError(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("PUT "+tokenPath, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("test-token"))
	})
	mux.HandleFunc("GET "+identityPath, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	})

	srv := httptest.NewServer(mux)
	defer srv.Close()

	client := newTestClient(t, srv.URL, false)
	_, err := client.IdentityDocument(context.Background())
	if err == nil {
		t.Fatal("expected error for 404 response")
	}
}

// TestDomain_ChinaPartition verifies the Domain accessor works with non-standard
// domains. This test exists because the agent must support aws-cn and gov partitions
// where the domain differs from the standard amazonaws.com.
func TestDomain_ChinaPartition(t *testing.T) {
	fake := &fakeIMDS{
		identityBody: testIdentityJSON,
		domainBody:   "amazonaws.com.cn",
		tokenBody:    "test-token-v2",
	}
	srv := httptest.NewServer(fake)
	defer srv.Close()

	client := newTestClient(t, srv.URL, false)
	domain, err := client.Domain(context.Background())
	if err != nil {
		t.Fatalf("Domain() unexpected error: %v", err)
	}
	if domain != "amazonaws.com.cn" {
		t.Errorf("Domain() = %q, want %q", domain, "amazonaws.com.cn")
	}
}
