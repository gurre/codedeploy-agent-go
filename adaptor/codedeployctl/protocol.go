// Package codedeployctl implements the CodeDeploy Commands service client.
// This is a custom AWS JSON 1.1 protocol client with SigV4 signing.
package codedeployctl

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	v4 "github.com/aws/aws-sdk-go-v2/aws/signer/v4"
	json "github.com/goccy/go-json"
)

const (
	targetPrefix = "CodeDeployCommandService_v20141006"
	jsonVersion  = "1.1"
	serviceName  = "codedeploy-commands"
)

// doRequest makes a signed JSON 1.1 request to the CodeDeploy Commands service.
func (c *Client) doRequest(ctx context.Context, operation string, input interface{}, output interface{}) error {
	body, err := json.Marshal(input)
	if err != nil {
		return fmt.Errorf("codedeployctl: marshal %s: %w", operation, err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.endpoint, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("codedeployctl: create request: %w", err)
	}

	req.Header.Set("Content-Type", "application/x-amz-json-"+jsonVersion)
	req.Header.Set("X-Amz-Target", targetPrefix+"."+operation)
	req.Header.Set("x-amz-codedeploy-agent-version", c.version)

	creds, err := c.credentials.Retrieve(ctx)
	if err != nil {
		return fmt.Errorf("codedeployctl: retrieve credentials: %w", err)
	}

	signer := v4.NewSigner()
	payloadHash := hashPayload(body)
	if err := signer.SignHTTP(ctx, creds, req, payloadHash, serviceName, c.region, time.Now()); err != nil {
		return fmt.Errorf("codedeployctl: sign request: %w", err)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("codedeployctl: %s: %w", operation, err)
	}
	defer func() { _ = resp.Body.Close() }()

	respBody, err := io.ReadAll(io.LimitReader(resp.Body, 4*1024*1024))
	if err != nil {
		return fmt.Errorf("codedeployctl: read response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return parseErrorResponse(operation, resp.StatusCode, respBody)
	}

	if output != nil && len(respBody) > 0 {
		if err := json.Unmarshal(respBody, output); err != nil {
			return fmt.Errorf("codedeployctl: unmarshal %s response: %w", operation, err)
		}
	}

	return nil
}

func parseErrorResponse(operation string, statusCode int, body []byte) error {
	var errResp struct {
		Type    string `json:"__type"`
		Message string `json:"message"`
	}
	if err := json.Unmarshal(body, &errResp); err == nil && errResp.Type != "" {
		return &ServiceError{
			Operation:  operation,
			StatusCode: statusCode,
			Type:       errResp.Type,
			Message:    errResp.Message,
		}
	}
	return &ServiceError{
		Operation:  operation,
		StatusCode: statusCode,
		Message:    string(body),
	}
}

// ServiceError represents an error from the CodeDeploy Commands service.
type ServiceError struct {
	Operation  string
	Type       string
	Message    string
	StatusCode int
}

func (e *ServiceError) Error() string {
	if e.Type != "" {
		return fmt.Sprintf("codedeployctl: %s: %s: %s", e.Operation, e.Type, e.Message)
	}
	return fmt.Sprintf("codedeployctl: %s: HTTP %d: %s", e.Operation, e.StatusCode, e.Message)
}

// IsClientError returns true for 4xx errors (client's fault).
func (e *ServiceError) IsClientError() bool {
	return e.StatusCode >= 400 && e.StatusCode < 500
}

// IsServerError returns true for 5xx errors (server's fault).
func (e *ServiceError) IsServerError() bool {
	return e.StatusCode >= 500
}

// IsThrottle returns true when the service signals rate limiting.
// Detects HTTP 429 status or "throttl"/"rateexceeded" in the error type or message,
// matching the Ruby CodeDeploy agent's throttle detection logic.
// The Type field is checked because the service sends throttle errors as
// {"__type": "ThrottlingException", "message": "Rate exceeded"} which would
// be missed by message-only matching.
//
//	var svcErr *ServiceError
//	if errors.As(err, &svcErr) && svcErr.IsThrottle() {
//	    time.Sleep(backoff.ThrottleDelay)
//	}
func (e *ServiceError) IsThrottle() bool {
	if e.StatusCode == http.StatusTooManyRequests {
		return true
	}
	lower := strings.ToLower(e.Type + " " + e.Message)
	return strings.Contains(lower, "throttl") || strings.Contains(lower, "rateexceeded")
}

func hashPayload(payload []byte) string {
	return aws.ToString(aws.String(fmt.Sprintf("%x", sha256Sum(payload))))
}

func sha256Sum(data []byte) [32]byte {
	// Use crypto/sha256
	var result [32]byte
	h := newSHA256()
	h.Write(data)
	copy(result[:], h.Sum(nil))
	return result
}
