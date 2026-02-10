// Package githubdownload provides GitHub tarball/zipball download with retry.
package githubdownload

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"time"
)

// retryDelays are the backoff intervals matching the Ruby agent: 10s, 30s, 90s.
var retryDelays = []time.Duration{10 * time.Second, 30 * time.Second, 90 * time.Second}

// Downloader fetches deployment bundles from GitHub.
type Downloader struct {
	httpClient *http.Client
	logger     *slog.Logger
}

// NewDownloader creates a GitHub downloader.
//
//	dl := githubdownload.NewDownloader(slog.Default())
//	err := dl.Download(ctx, "octocat", "hello-world", "abc123", "tar", "ghp_token", "/tmp/bundle.tar")
func NewDownloader(logger *slog.Logger) *Downloader {
	return &Downloader{
		httpClient: &http.Client{
			Timeout:       60 * time.Second,
			CheckRedirect: func(req *http.Request, via []*http.Request) error { return nil },
		},
		logger: logger,
	}
}

// Download fetches a tarball or zipball from GitHub and writes it to destPath.
// Empty token means anonymous access. Retries up to 3 times with backoff.
//
//	err := dl.Download(ctx, "owner", "repo", "commitSHA", "tar", "", "/tmp/bundle.tar")
func (d *Downloader) Download(ctx context.Context, account, repo, commit, bundleType, token, destPath string) error {
	format := "tarball"
	if bundleType == "zip" {
		format = "zipball"
	}

	url := fmt.Sprintf("https://api.github.com/repos/%s/%s/%s/%s", account, repo, format, commit)

	var lastErr error
	for attempt := range len(retryDelays) + 1 {
		err := d.downloadOnce(ctx, url, token, destPath)
		if err == nil {
			return nil
		}
		lastErr = err
		d.logger.Error("github download failed", "url", url, "attempt", attempt+1, "error", err)

		if attempt < len(retryDelays) {
			delay := retryDelays[attempt]
			d.logger.Info("retrying download", "delay", delay)
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(delay):
			}
		}
	}

	return fmt.Errorf("githubdownload: failed after %d retries: %w", len(retryDelays), lastErr)
}

func (d *Downloader) downloadOnce(ctx context.Context, url, token, destPath string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}

	if token != "" {
		req.Header.Set("Authorization", "token "+token)
	}
	req.Header.Set("Accept", "application/vnd.github.v3+json")

	d.logger.Info("requesting GitHub URL", "url", url)

	resp, err := d.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		return fmt.Errorf("HTTP %d: %s", resp.StatusCode, string(body))
	}

	f, err := os.Create(destPath)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()

	buf := make([]byte, 8*1024*1024) // 8MB buffer matching Ruby agent
	_, err = io.CopyBuffer(f, resp.Body, buf)
	return err
}
