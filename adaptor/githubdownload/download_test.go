package githubdownload

import (
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// testTransport redirects all HTTP requests to a local handler, allowing tests
// to intercept calls to api.github.com without network access.
type testTransport struct {
	handler http.Handler
}

func (t *testTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	rr := httptest.NewRecorder()
	t.handler.ServeHTTP(rr, req)
	return rr.Result(), nil
}

func newTestDownloader(handler http.Handler) *Downloader {
	return &Downloader{
		httpClient: &http.Client{Transport: &testTransport{handler: handler}},
		logger:     slog.Default(),
	}
}

// TestDownload_Tarball verifies the default download format is "tarball" and
// the response body is written to destPath.
func TestDownload_Tarball(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.Contains(r.URL.Path, "/tarball/") {
			t.Errorf("expected tarball in path, got %s", r.URL.Path)
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("tarball-content"))
	})
	dl := newTestDownloader(handler)

	dest := t.TempDir() + "/bundle.tar"
	err := dl.Download(context.Background(), "octocat", "hello", "abc123", "tar", "", dest)
	if err != nil {
		t.Fatalf("Download: %v", err)
	}

	data, err := os.ReadFile(dest)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "tarball-content" {
		t.Errorf("file content = %q, want tarball-content", data)
	}
}

// TestDownload_WithToken verifies the Authorization header is set when a
// token is provided. GitHub uses this to authenticate private repo access.
func TestDownload_WithToken(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth := r.Header.Get("Authorization")
		if auth != "token ghp_xxx" {
			t.Errorf("Authorization = %q, want 'token ghp_xxx'", auth)
		}
		w.WriteHeader(http.StatusOK)
	})
	dl := newTestDownloader(handler)

	dest := t.TempDir() + "/bundle.tar"
	err := dl.Download(context.Background(), "owner", "repo", "sha", "tar", "ghp_xxx", dest)
	if err != nil {
		t.Fatalf("Download: %v", err)
	}
}

// TestDownload_AnonymousAccess verifies no Authorization header is sent when
// the token is empty, allowing anonymous access to public repositories.
func TestDownload_AnonymousAccess(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if auth := r.Header.Get("Authorization"); auth != "" {
			t.Errorf("Authorization should be empty for anonymous, got %q", auth)
		}
		w.WriteHeader(http.StatusOK)
	})
	dl := newTestDownloader(handler)

	dest := t.TempDir() + "/bundle.tar"
	err := dl.Download(context.Background(), "owner", "repo", "sha", "tar", "", dest)
	if err != nil {
		t.Fatalf("Download: %v", err)
	}
}

// TestDownload_ServerError verifies that a persistent server error surfaces
// after exhausting all retries, with the HTTP status code in the error message.
func TestDownload_ServerError(t *testing.T) {
	origDelays := retryDelays
	retryDelays = []time.Duration{time.Millisecond, time.Millisecond, time.Millisecond}
	defer func() { retryDelays = origDelays }()

	handler := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = w.Write([]byte("service unavailable"))
	})
	dl := newTestDownloader(handler)

	dest := t.TempDir() + "/bundle.tar"
	err := dl.Download(context.Background(), "owner", "repo", "sha", "tar", "", dest)
	if err == nil {
		t.Fatal("expected error for 503 response")
	}
	if !strings.Contains(err.Error(), "503") {
		t.Errorf("error should contain status code 503, got: %v", err)
	}
}

// TestDownload_RetryOnFailure verifies the retry mechanism: a transient 500
// on the first attempt is followed by a successful 200 on the second attempt.
func TestDownload_RetryOnFailure(t *testing.T) {
	origDelays := retryDelays
	retryDelays = []time.Duration{time.Millisecond, time.Millisecond, time.Millisecond}
	defer func() { retryDelays = origDelays }()

	var calls atomic.Int32
	handler := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		n := calls.Add(1)
		if n == 1 {
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte("temporary failure"))
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("retry-success"))
	})
	dl := newTestDownloader(handler)

	dest := t.TempDir() + "/bundle.tar"
	err := dl.Download(context.Background(), "owner", "repo", "sha", "tar", "", dest)
	if err != nil {
		t.Fatalf("Download should succeed on retry: %v", err)
	}

	data, err := os.ReadFile(dest)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "retry-success" {
		t.Errorf("file content = %q, want retry-success", data)
	}
}
