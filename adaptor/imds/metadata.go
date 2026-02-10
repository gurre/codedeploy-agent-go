// Package imds provides an EC2 Instance Metadata Service (IMDS) client
// that uses IMDSv2 with automatic fallback to IMDSv1.
package imds

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"math/rand/v2"
	"net/http"
	"strings"
	"time"

	json "github.com/goccy/go-json"
)

const (
	imdsAddress   = "169.254.169.254"
	imdsPort      = "80"
	httpTimeout   = 10 * time.Second
	maxRetries    = 2
	tokenTTL      = "21600"
	basePath      = "/latest/meta-data"
	tokenPath     = "/latest/api/token"
	identityPath  = "/latest/dynamic/instance-identity/document"
	partitionPath = "/latest/meta-data/services/partition"
	domainPath    = "/latest/meta-data/services/domain"
	instancePath  = "/latest/meta-data/instance-id"
)

// IdentityDocument holds the EC2 instance identity document fields.
type IdentityDocument struct {
	Region     string `json:"region"`
	AccountID  string `json:"accountId"`
	InstanceID string `json:"instanceId"`
}

// Client accesses EC2 instance metadata via IMDSv2 (with optional v1 fallback).
type Client struct {
	httpClient    *http.Client
	baseURL       string
	logger        *slog.Logger
	token         string
	disableIMDSv1 bool
}

// NewClient creates an IMDS client. Set disableIMDSv1 to prevent v1 fallback.
// Pass a non-nil transport to apply a custom round-tripper (e.g. proxy);
// nil uses Go's default transport. The client always uses a 10s timeout.
//
//	client := imds.NewClient(false, nil, slog.Default())
//	region, err := client.Region(ctx)
func NewClient(disableIMDSv1 bool, transport http.RoundTripper, logger *slog.Logger) *Client {
	return &Client{
		httpClient: &http.Client{
			Transport: transport,
			Timeout:   httpTimeout,
		},
		baseURL:       "http://" + imdsAddress + ":" + imdsPort,
		disableIMDSv1: disableIMDSv1,
		logger:        logger,
	}
}

// Region returns the EC2 instance's region.
func (c *Client) Region(ctx context.Context) (string, error) {
	doc, err := c.IdentityDocument(ctx)
	if err != nil {
		return "", err
	}
	return doc.Region, nil
}

// HostIdentifier returns the ARN-format host identifier used by CodeDeploy.
// Format: arn:{partition}:ec2:{region}:{accountId}:instance/{instanceId}
func (c *Client) HostIdentifier(ctx context.Context) (string, error) {
	doc, err := c.IdentityDocument(ctx)
	if err != nil {
		return "", err
	}
	partition, err := c.Partition(ctx)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("arn:%s:ec2:%s:%s:instance/%s", partition, doc.Region, doc.AccountID, doc.InstanceID), nil
}

// IdentityDocument retrieves and parses the instance identity document.
func (c *Client) IdentityDocument(ctx context.Context) (IdentityDocument, error) {
	body, err := c.get(ctx, identityPath)
	if err != nil {
		return IdentityDocument{}, fmt.Errorf("imds: identity document: %w", err)
	}
	var doc IdentityDocument
	if err := json.Unmarshal([]byte(body), &doc); err != nil {
		return IdentityDocument{}, fmt.Errorf("imds: parse identity document: %w", err)
	}
	return doc, nil
}

// Partition returns the AWS partition (e.g. "aws", "aws-cn", "aws-us-gov").
func (c *Client) Partition(ctx context.Context) (string, error) {
	body, err := c.get(ctx, partitionPath)
	if err != nil {
		return "", fmt.Errorf("imds: partition: %w", err)
	}
	return strings.TrimSpace(body), nil
}

// Domain returns the AWS domain (e.g. "amazonaws.com").
func (c *Client) Domain(ctx context.Context) (string, error) {
	body, err := c.get(ctx, domainPath)
	if err != nil {
		return "", fmt.Errorf("imds: domain: %w", err)
	}
	return strings.TrimSpace(body), nil
}

// InstanceID returns the EC2 instance ID.
func (c *Client) InstanceID(ctx context.Context) (string, error) {
	body, err := c.get(ctx, instancePath)
	if err != nil {
		return "", fmt.Errorf("imds: instance-id: %w", err)
	}
	return strings.TrimSpace(body), nil
}

// get performs an IMDS GET request with IMDSv2 token, falling back to v1.
func (c *Client) get(ctx context.Context, path string) (string, error) {
	var lastErr error

	for attempt := range maxRetries + 1 {
		if attempt > 0 {
			// Jittered backoff: uniform in [base/2, base] to avoid thundering herd
			base := time.Duration(attempt) * time.Second
			half := base / 2
			jitter := time.Duration(rand.Int64N(int64(half + 1)))
			select {
			case <-ctx.Done():
				return "", ctx.Err()
			case <-time.After(half + jitter):
			}
		}

		body, err := c.getWithToken(ctx, path)
		if err == nil {
			return body, nil
		}
		lastErr = err
	}

	return "", lastErr
}

func (c *Client) getWithToken(ctx context.Context, path string) (string, error) {
	// Try IMDSv2 first
	token, err := c.getToken(ctx)
	if err != nil {
		if c.disableIMDSv1 {
			return "", fmt.Errorf("imds: IMDSv2 token failed and v1 disabled: %w", err)
		}
		c.logger.Warn("IMDSv2 token request failed, falling back to IMDSv1")
		return c.doGet(ctx, path, "")
	}
	return c.doGet(ctx, path, token)
}

func (c *Client) getToken(ctx context.Context) (string, error) {
	if c.token != "" {
		return c.token, nil
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPut, c.baseURL+tokenPath, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("X-aws-ec2-metadata-token-ttl-seconds", tokenTTL)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return "", err
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("token request returned %d", resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}

	c.token = string(body)
	return c.token, nil
}

// doGet performs a GET request. On 401 it refreshes the token and retries
// exactly once, eliminating the unbounded recursion of the previous design.
func (c *Client) doGet(ctx context.Context, path, token string) (string, error) {
	body, status, err := c.doGetRaw(ctx, path, token)
	if err != nil {
		return "", err
	}

	if status == http.StatusUnauthorized && token != "" {
		// Token may have expired — refresh once and retry
		c.token = ""
		newToken, tokenErr := c.getToken(ctx)
		if tokenErr != nil {
			return "", fmt.Errorf("imds: token refresh failed: %w", tokenErr)
		}
		body, status, err = c.doGetRaw(ctx, path, newToken)
		if err != nil {
			return "", err
		}
	}

	if status != http.StatusOK {
		return "", fmt.Errorf("imds: %s returned %d", path, status)
	}

	return body, nil
}

// doGetRaw performs a single HTTP GET and returns the body, status code, and error.
// It does not retry or recurse.
func (c *Client) doGetRaw(ctx context.Context, path, token string) (string, int, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+path, nil)
	if err != nil {
		return "", 0, err
	}
	if token != "" {
		req.Header.Set("X-aws-ec2-metadata-token", token)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return "", 0, err
	}
	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", resp.StatusCode, err
	}
	return string(body), resp.StatusCode, nil
}
