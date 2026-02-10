package codedeployctl

import (
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	json "github.com/goccy/go-json"
)

// BenchmarkDoRequest measures the full request cycle: JSON marshal, SigV4
// signing, HTTP round-trip to a local server, and response unmarshal.
// This benchmarks the protocol overhead per CodeDeploy API call.
func BenchmarkDoRequest(b *testing.B) {
	response := struct {
		HostCommand *HostCommand `json:"HostCommand"`
	}{
		HostCommand: &HostCommand{
			HostCommandIdentifier: "hci-bench",
			HostIdentifier:        "arn:aws:ec2:us-east-1:123:instance/i-bench",
			DeploymentExecutionID: "exec-bench",
			CommandName:           "BeforeInstall",
		},
	}
	respBody, _ := json.Marshal(response)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/x-amz-json-1.1")
		_, _ = w.Write(respBody)
	}))
	defer server.Close()

	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
	creds := aws.CredentialsProviderFunc(func(_ context.Context) (aws.Credentials, error) {
		return aws.Credentials{
			AccessKeyID:     "AKID",
			SecretAccessKey: "SECRET",
		}, nil
	})
	client := NewClient(creds, "us-east-1", server.URL, nil, logger)
	ctx := context.Background()

	b.ResetTimer()
	for range b.N {
		_, _ = client.PollHostCommand(ctx, "arn:aws:ec2:us-east-1:123:instance/i-bench")
	}
}
