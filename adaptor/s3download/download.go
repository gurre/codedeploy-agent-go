// Package s3download provides S3 artifact download with ETag verification.
package s3download

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
)

// Downloader fetches deployment bundles from S3.
type Downloader struct {
	client *s3.Client
	logger *slog.Logger
}

// NewDownloader creates an S3 downloader from an AWS config.
// Pass a non-nil httpClient to use a custom transport (e.g. for proxy support);
// nil uses the default from the AWS config.
//
//	dl := s3download.NewDownloader(cfg, "us-east-1", "", false, nil, slog.Default())
//	err := dl.Download(ctx, bucket, key, version, etag, destPath)
func NewDownloader(awsCfg aws.Config, region, endpointOverride string, useFIPS bool, httpClient *http.Client, logger *slog.Logger) *Downloader {
	opts := func(o *s3.Options) {
		o.Region = region
		if endpointOverride != "" {
			o.BaseEndpoint = aws.String(endpointOverride)
		} else if useFIPS {
			o.BaseEndpoint = aws.String(fmt.Sprintf("https://s3-fips.%s.amazonaws.com", region))
		}
		if httpClient != nil {
			o.HTTPClient = httpClient
		}
	}

	return &Downloader{
		client: s3.NewFromConfig(awsCfg, opts),
		logger: logger,
	}
}

// Download fetches an object from S3 and writes it to destPath.
// If etag is non-empty, verifies the downloaded object's ETag matches.
//
//	err := dl.Download(ctx, "my-bucket", "app.tar", "v1", "abc123", "/tmp/bundle.tar")
func (d *Downloader) Download(ctx context.Context, bucket, key, version, etag, destPath string) error {
	d.logger.Info("downloading artifact", "bucket", bucket, "key", key, "version", version)

	input := &s3.GetObjectInput{
		Bucket: aws.String(bucket),
		Key:    aws.String(key),
	}
	if version != "" {
		input.VersionId = aws.String(version)
	}

	output, err := d.client.GetObject(ctx, input)
	if err != nil {
		return fmt.Errorf("s3download: GetObject %s/%s: %w", bucket, key, err)
	}
	defer func() { _ = output.Body.Close() }()

	f, err := os.Create(destPath)
	if err != nil {
		return fmt.Errorf("s3download: create %s: %w", destPath, err)
	}
	defer func() { _ = f.Close() }()

	if _, err := io.Copy(f, output.Body); err != nil {
		return fmt.Errorf("s3download: write %s: %w", destPath, err)
	}

	if etag != "" && output.ETag != nil {
		actualETag := strings.Trim(*output.ETag, `"`)
		expectedETag := strings.Trim(etag, `"`)
		if actualETag != expectedETag {
			return fmt.Errorf("s3download: ETag mismatch: expected %q, got %q", expectedETag, actualETag)
		}
	}

	d.logger.Info("download complete", "bucket", bucket, "key", key)
	return nil
}
