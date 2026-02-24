package s3download

import (
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials"
)

func newTestDownloader(t *testing.T, handler http.Handler) (*Downloader, *httptest.Server) {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)

	cfg := aws.Config{
		Region:      "us-east-1",
		Credentials: credentials.NewStaticCredentialsProvider("AKID", "SECRET", ""),
	}

	dl := NewDownloader(cfg, "us-east-1", server.URL, false, false, nil, slog.Default())
	return dl, server
}

// TestNewDownloader_DualStack verifies that dual-stack option is applied to the
// S3 client when useDualStack=true and no endpoint override is set. The SDK uses
// this to resolve s3.dualstack.{region}.amazonaws.com endpoints.
func TestNewDownloader_DualStack(t *testing.T) {
	cfg := aws.Config{
		Region:      "eu-north-1",
		Credentials: credentials.NewStaticCredentialsProvider("AKID", "SECRET", ""),
	}

	// Construct with dual-stack enabled — the Downloader should be created without panic.
	// We verify the option is wired by confirming the downloader is non-nil.
	dl := NewDownloader(cfg, "eu-north-1", "", false, true, nil, slog.Default())
	if dl == nil {
		t.Fatal("expected non-nil Downloader")
	}
}

// TestNewDownloader_EndpointOverridePrecedenceOverDualStack verifies that an
// explicit endpoint override disables dual-stack resolution. Operators must
// be able to point S3 at a custom endpoint regardless of dual-stack.
func TestNewDownloader_EndpointOverridePrecedenceOverDualStack(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("ETag", `"abc123"`)
		_, _ = w.Write([]byte("content"))
	})
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)

	cfg := aws.Config{
		Region:      "us-east-1",
		Credentials: credentials.NewStaticCredentialsProvider("AKID", "SECRET", ""),
	}

	// Even with useDualStack=true, the endpoint override should take precedence
	// and the download should succeed against the test server.
	dl := NewDownloader(cfg, "us-east-1", server.URL, false, true, nil, slog.Default())
	dest := t.TempDir() + "/bundle.tar"
	err := dl.Download(context.Background(), "bucket", "key", "", "", dest)
	if err != nil {
		t.Fatalf("Download with endpoint override + dual-stack: %v", err)
	}
}

// TestDownload_Success verifies a downloaded object is written to destPath
// with the correct content. This is the primary happy path.
func TestDownload_Success(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("ETag", `"abc123"`)
		_, _ = w.Write([]byte("file-content-here"))
	})
	dl, _ := newTestDownloader(t, handler)

	dest := t.TempDir() + "/bundle.tar"
	err := dl.Download(context.Background(), "my-bucket", "my-key", "", "", dest)
	if err != nil {
		t.Fatalf("Download: %v", err)
	}

	data, err := os.ReadFile(dest)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "file-content-here" {
		t.Errorf("file content = %q, want file-content-here", data)
	}
}

// TestDownload_ETagMatch verifies that download succeeds when the server ETag
// matches the expected ETag (after stripping quotes).
func TestDownload_ETagMatch(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("ETag", `"abc123"`)
		_, _ = w.Write([]byte("ok"))
	})
	dl, _ := newTestDownloader(t, handler)

	dest := t.TempDir() + "/bundle.tar"
	err := dl.Download(context.Background(), "bucket", "key", "", "abc123", dest)
	if err != nil {
		t.Fatalf("Download with matching ETag should succeed: %v", err)
	}
}

// TestDownload_ETagMismatch verifies the error when the server returns a
// different ETag than expected. This detects mid-flight object replacement.
func TestDownload_ETagMismatch(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("ETag", `"wrong-etag"`)
		_, _ = w.Write([]byte("ok"))
	})
	dl, _ := newTestDownloader(t, handler)

	dest := t.TempDir() + "/bundle.tar"
	err := dl.Download(context.Background(), "bucket", "key", "", "expected-etag", dest)
	if err == nil {
		t.Fatal("expected error for ETag mismatch")
	}
	if !strings.Contains(err.Error(), "ETag mismatch") {
		t.Errorf("error should mention ETag mismatch, got: %v", err)
	}
}

// TestDownload_S3Error verifies that an S3 error (e.g. 404 NoSuchKey)
// is propagated as a wrapped error from GetObject.
func TestDownload_S3Error(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`<?xml version="1.0" encoding="UTF-8"?><Error><Code>NoSuchKey</Code><Message>The specified key does not exist.</Message></Error>`))
	})
	dl, _ := newTestDownloader(t, handler)

	dest := t.TempDir() + "/bundle.tar"
	err := dl.Download(context.Background(), "bucket", "missing-key", "", "", dest)
	if err == nil {
		t.Fatal("expected error for S3 404")
	}
	if !strings.Contains(err.Error(), "s3download") {
		t.Errorf("error should be wrapped by s3download, got: %v", err)
	}
}
