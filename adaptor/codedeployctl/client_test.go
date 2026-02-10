package codedeployctl

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	json "github.com/goccy/go-json"
)

// staticCredentials returns a CredentialsProvider with fixed test credentials.
// Used by every test to satisfy SigV4 signing without real AWS access.
func staticCredentials() aws.CredentialsProviderFunc {
	return aws.CredentialsProviderFunc(func(ctx context.Context) (aws.Credentials, error) {
		return aws.Credentials{
			AccessKeyID:     "AKID",
			SecretAccessKey: "SECRET",
		}, nil
	})
}

// newTestClient creates a Client pointed at the given httptest server URL.
func newTestClient(t *testing.T, serverURL string) *Client {
	t.Helper()
	return NewClient(staticCredentials(), "us-east-1", serverURL, slog.Default())
}

// assertHeader verifies a request header has the expected value.
func assertHeader(t *testing.T, r *http.Request, key, expected string) {
	t.Helper()
	got := r.Header.Get(key)
	if got != expected {
		t.Errorf("header %q = %q, want %q", key, got, expected)
	}
}

// TestPollHostCommand_Success verifies that PollHostCommand deserializes a valid
// service response into a HostCommand struct. This is the primary happy-path for
// the polling loop that drives the agent.
func TestPollHostCommand_Success(t *testing.T) {
	want := HostCommand{
		HostCommandIdentifier: "cmd-123",
		HostIdentifier:        "i-abc",
		DeploymentExecutionID: "exec-1",
		CommandName:           "Install",
		Nonce:                 42,
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assertHeader(t, r, "Content-Type", "application/x-amz-json-1.1")
		assertHeader(t, r, "X-Amz-Target", "CodeDeployCommandService_v20141006.PollHostCommand")

		body, _ := io.ReadAll(r.Body)
		var input struct {
			HostIdentifier string `json:"HostIdentifier"`
		}
		if err := json.Unmarshal(body, &input); err != nil {
			t.Errorf("unmarshal request body: %v", err)
		}
		if input.HostIdentifier != "i-abc" {
			t.Errorf("HostIdentifier = %q, want %q", input.HostIdentifier, "i-abc")
		}

		resp := struct {
			HostCommand *HostCommand `json:"HostCommand"`
		}{HostCommand: &want}
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer srv.Close()

	client := newTestClient(t, srv.URL)
	got, err := client.PollHostCommand(context.Background(), "i-abc")
	if err != nil {
		t.Fatalf("PollHostCommand: %v", err)
	}
	if got == nil {
		t.Fatal("expected non-nil HostCommand")
	}
	if *got != want {
		t.Errorf("got %+v, want %+v", *got, want)
	}
}

// TestPollHostCommand_NoCommand verifies that a null HostCommand field in the
// JSON response produces a nil return value. The agent uses nil to signal
// "no work available" so this must be distinguishable from an error.
func TestPollHostCommand_NoCommand(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"HostCommand":null}`))
	}))
	defer srv.Close()

	client := newTestClient(t, srv.URL)
	got, err := client.PollHostCommand(context.Background(), "i-abc")
	if err != nil {
		t.Fatalf("PollHostCommand: %v", err)
	}
	if got != nil {
		t.Errorf("expected nil HostCommand, got %+v", got)
	}
}

// TestAcknowledge_Success verifies that Acknowledge correctly deserializes the
// CommandStatus from the service response. The returned status determines
// whether the agent should proceed with the deployment or abort.
func TestAcknowledge_Success(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assertHeader(t, r, "Content-Type", "application/x-amz-json-1.1")
		assertHeader(t, r, "X-Amz-Target", "CodeDeployCommandService_v20141006.PutHostCommandAcknowledgement")

		body, _ := io.ReadAll(r.Body)
		var input struct {
			HostCommandIdentifier string    `json:"HostCommandIdentifier"`
			Diagnostics           *Envelope `json:"Diagnostics"`
		}
		if err := json.Unmarshal(body, &input); err != nil {
			t.Errorf("unmarshal request body: %v", err)
		}
		if input.HostCommandIdentifier != "cmd-123" {
			t.Errorf("HostCommandIdentifier = %q, want %q", input.HostCommandIdentifier, "cmd-123")
		}

		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"CommandStatus":"Succeeded"}`))
	}))
	defer srv.Close()

	client := newTestClient(t, srv.URL)
	diag := &Envelope{Format: "JSON", Payload: `{"msg":"ack"}`}
	status, err := client.Acknowledge(context.Background(), "cmd-123", diag)
	if err != nil {
		t.Fatalf("Acknowledge: %v", err)
	}
	if status != "Succeeded" {
		t.Errorf("CommandStatus = %q, want %q", status, "Succeeded")
	}
}

// TestComplete_Success verifies that Complete returns no error on a successful
// 200 response. Complete has no return payload, so the only contract is that
// a 200 maps to nil error.
func TestComplete_Success(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assertHeader(t, r, "Content-Type", "application/x-amz-json-1.1")
		assertHeader(t, r, "X-Amz-Target", "CodeDeployCommandService_v20141006.PutHostCommandComplete")

		body, _ := io.ReadAll(r.Body)
		var input struct {
			HostCommandIdentifier string `json:"HostCommandIdentifier"`
			CommandStatus         string `json:"CommandStatus"`
		}
		if err := json.Unmarshal(body, &input); err != nil {
			t.Errorf("unmarshal request body: %v", err)
		}
		if input.CommandStatus != "Succeeded" {
			t.Errorf("CommandStatus = %q, want %q", input.CommandStatus, "Succeeded")
		}

		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{}`))
	}))
	defer srv.Close()

	client := newTestClient(t, srv.URL)
	diag := &Envelope{Format: "JSON", Payload: `{"msg":"done"}`}
	err := client.Complete(context.Background(), "cmd-123", "Succeeded", diag)
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
}

// TestGetDeploymentSpecification_Success verifies that GetDeploymentSpecification
// returns both the DeploymentSpecification and the DeploymentSystem string.
// The deployment system (e.g. "CodeDeploy") determines which downstream handler
// processes the spec.
func TestGetDeploymentSpecification_Success(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assertHeader(t, r, "Content-Type", "application/x-amz-json-1.1")
		assertHeader(t, r, "X-Amz-Target", "CodeDeployCommandService_v20141006.GetDeploymentSpecification")

		body, _ := io.ReadAll(r.Body)
		var input struct {
			DeploymentExecutionID string `json:"DeploymentExecutionId"`
			HostIdentifier        string `json:"HostIdentifier"`
		}
		if err := json.Unmarshal(body, &input); err != nil {
			t.Errorf("unmarshal request body: %v", err)
		}
		if input.DeploymentExecutionID != "exec-1" {
			t.Errorf("DeploymentExecutionId = %q, want %q", input.DeploymentExecutionID, "exec-1")
		}
		if input.HostIdentifier != "i-abc" {
			t.Errorf("HostIdentifier = %q, want %q", input.HostIdentifier, "i-abc")
		}

		resp := struct {
			DeploymentSystem        string                   `json:"DeploymentSystem"`
			DeploymentSpecification *DeploymentSpecification `json:"DeploymentSpecification"`
		}{
			DeploymentSystem: "CodeDeploy",
			DeploymentSpecification: &DeploymentSpecification{
				GenericEnvelope: &Envelope{Format: "JSON", Payload: `{"key":"val"}`},
				VariantID:       "variant-1",
				VariantEnvelope: &Envelope{Format: "YAML", Payload: "kind: deploy"},
			},
		}
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer srv.Close()

	client := newTestClient(t, srv.URL)
	spec, system, err := client.GetDeploymentSpecification(context.Background(), "exec-1", "i-abc")
	if err != nil {
		t.Fatalf("GetDeploymentSpecification: %v", err)
	}
	if system != "CodeDeploy" {
		t.Errorf("DeploymentSystem = %q, want %q", system, "CodeDeploy")
	}
	if spec == nil {
		t.Fatal("expected non-nil DeploymentSpecification")
	}
	if spec.VariantID != "variant-1" {
		t.Errorf("VariantID = %q, want %q", spec.VariantID, "variant-1")
	}
	if spec.GenericEnvelope == nil || spec.GenericEnvelope.Format != "JSON" {
		t.Errorf("GenericEnvelope = %+v, want Format=JSON", spec.GenericEnvelope)
	}
	if spec.VariantEnvelope == nil || spec.VariantEnvelope.Payload != "kind: deploy" {
		t.Errorf("VariantEnvelope = %+v, want Payload='kind: deploy'", spec.VariantEnvelope)
	}
}

// TestServiceError_ClientError verifies that a 4xx response with a typed JSON
// error body is parsed into a *ServiceError with the correct Type, Message,
// and StatusCode fields. The agent uses ServiceError.IsClientError() to decide
// whether to retry or fail permanently.
func TestServiceError_ClientError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"__type":"ValidationException","message":"invalid host identifier"}`))
	}))
	defer srv.Close()

	client := newTestClient(t, srv.URL)
	_, err := client.PollHostCommand(context.Background(), "bad-host")
	if err == nil {
		t.Fatal("expected error for 400 response")
	}

	var svcErr *ServiceError
	if !errors.As(err, &svcErr) {
		t.Fatalf("expected *ServiceError, got %T: %v", err, err)
	}
	if svcErr.StatusCode != http.StatusBadRequest {
		t.Errorf("StatusCode = %d, want %d", svcErr.StatusCode, http.StatusBadRequest)
	}
	if svcErr.Type != "ValidationException" {
		t.Errorf("Type = %q, want %q", svcErr.Type, "ValidationException")
	}
	if svcErr.Message != "invalid host identifier" {
		t.Errorf("Message = %q, want %q", svcErr.Message, "invalid host identifier")
	}
	if !svcErr.IsClientError() {
		t.Error("expected IsClientError() to be true")
	}
	if svcErr.IsServerError() {
		t.Error("expected IsServerError() to be false")
	}
}

// TestServiceError_ServerError verifies that a 5xx response is parsed into a
// *ServiceError and that IsServerError returns true. Server errors indicate
// transient failures that the agent should retry with backoff.
func TestServiceError_ServerError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`{"__type":"InternalServiceError","message":"service unavailable"}`))
	}))
	defer srv.Close()

	client := newTestClient(t, srv.URL)
	err := client.Complete(context.Background(), "cmd-123", "Succeeded", nil)
	if err == nil {
		t.Fatal("expected error for 500 response")
	}

	var svcErr *ServiceError
	if !errors.As(err, &svcErr) {
		t.Fatalf("expected *ServiceError, got %T: %v", err, err)
	}
	if svcErr.StatusCode != http.StatusInternalServerError {
		t.Errorf("StatusCode = %d, want %d", svcErr.StatusCode, http.StatusInternalServerError)
	}
	if svcErr.Type != "InternalServiceError" {
		t.Errorf("Type = %q, want %q", svcErr.Type, "InternalServiceError")
	}
	if svcErr.Message != "service unavailable" {
		t.Errorf("Message = %q, want %q", svcErr.Message, "service unavailable")
	}
	if svcErr.IsClientError() {
		t.Error("expected IsClientError() to be false")
	}
	if !svcErr.IsServerError() {
		t.Error("expected IsServerError() to be true")
	}
}

// TestSigV4Signing verifies that outbound requests carry a valid SigV4
// Authorization header. Without proper signing, the real service rejects
// every request. We check the header prefix to confirm the signer ran.
func TestSigV4Signing(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth := r.Header.Get("Authorization")
		if auth == "" {
			t.Error("missing Authorization header")
		}
		if !strings.HasPrefix(auth, "AWS4-HMAC-SHA256") {
			t.Errorf("Authorization header = %q, want prefix AWS4-HMAC-SHA256", auth)
		}

		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"HostCommand":null}`))
	}))
	defer srv.Close()

	client := newTestClient(t, srv.URL)
	_, err := client.PollHostCommand(context.Background(), "i-test")
	if err != nil {
		t.Fatalf("PollHostCommand: %v", err)
	}
}

// TestPostUpdate_Success verifies that PostUpdate sends a progress update and
// returns the command status from the service response. PostUpdate is called
// during long-running deployments to report progress.
func TestPostUpdate_Success(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assertHeader(t, r, "X-Amz-Target", "CodeDeployCommandService_v20141006.PostHostCommandUpdate")

		body, _ := io.ReadAll(r.Body)
		var input struct {
			HostCommandIdentifier string    `json:"HostCommandIdentifier"`
			Diagnostics           *Envelope `json:"Diagnostics"`
		}
		if err := json.Unmarshal(body, &input); err != nil {
			t.Errorf("unmarshal request body: %v", err)
		}
		if input.HostCommandIdentifier != "cmd-456" {
			t.Errorf("HostCommandIdentifier = %q, want %q", input.HostCommandIdentifier, "cmd-456")
		}

		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"CommandStatus":"InProgress"}`))
	}))
	defer srv.Close()

	client := newTestClient(t, srv.URL)
	diag := &Envelope{Format: "JSON", Payload: `{"step":"downloading"}`}
	status, err := client.PostUpdate(context.Background(), "cmd-456", diag)
	if err != nil {
		t.Fatalf("PostUpdate: %v", err)
	}
	if status != "InProgress" {
		t.Errorf("CommandStatus = %q, want %q", status, "InProgress")
	}
}

// TestServiceError_Error_WithType verifies the Error() string format when a
// typed error is returned. The format includes operation, type, and message
// so operators can identify the failure from logs.
func TestServiceError_Error_WithType(t *testing.T) {
	err := &ServiceError{
		Operation:  "PollHostCommand",
		Type:       "ValidationException",
		Message:    "bad input",
		StatusCode: 400,
	}
	got := err.Error()
	if !strings.Contains(got, "PollHostCommand") {
		t.Errorf("Error() = %q, want to contain operation name", got)
	}
	if !strings.Contains(got, "ValidationException") {
		t.Errorf("Error() = %q, want to contain error type", got)
	}
}

// TestServiceError_Error_WithoutType verifies the Error() fallback format when
// the service returns a non-JSON or untyped error body. The format includes
// the HTTP status code for debugging.
func TestServiceError_Error_WithoutType(t *testing.T) {
	err := &ServiceError{
		Operation:  "Complete",
		Message:    "gateway timeout",
		StatusCode: 504,
	}
	got := err.Error()
	if !strings.Contains(got, "504") {
		t.Errorf("Error() = %q, want to contain status code", got)
	}
	if !strings.Contains(got, "gateway timeout") {
		t.Errorf("Error() = %q, want to contain message", got)
	}
}

// TestServiceError_UnparsableBody verifies that a non-JSON error response body
// is captured in the ServiceError.Message field. Some load balancers return
// plain text errors that must still be surfaced.
func TestServiceError_UnparsableBody(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
		_, _ = w.Write([]byte("upstream connect error"))
	}))
	defer srv.Close()

	client := newTestClient(t, srv.URL)
	_, err := client.PollHostCommand(context.Background(), "i-test")
	if err == nil {
		t.Fatal("expected error for 502 response")
	}

	var svcErr *ServiceError
	if !errors.As(err, &svcErr) {
		t.Fatalf("expected *ServiceError, got %T: %v", err, err)
	}
	if svcErr.Type != "" {
		t.Errorf("Type = %q, want empty for non-JSON body", svcErr.Type)
	}
	if !strings.Contains(svcErr.Message, "upstream connect error") {
		t.Errorf("Message = %q, want to contain raw body", svcErr.Message)
	}
}

// TestNewClient_DefaultEndpoint verifies that NewClient constructs the correct
// default endpoint URL when no override is provided. The endpoint format is a
// contract with the CodeDeploy Commands service.
func TestNewClient_DefaultEndpoint(t *testing.T) {
	client := NewClient(staticCredentials(), "eu-west-1", "", slog.Default())
	want := "https://codedeploy-commands.eu-west-1.amazonaws.com"
	if client.endpoint != want {
		t.Errorf("endpoint = %q, want %q", client.endpoint, want)
	}
}
