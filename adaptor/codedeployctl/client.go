package codedeployctl

import (
	"context"
	"crypto/sha256"
	"fmt"
	"hash"
	"log/slog"
	"net/http"
	"runtime/debug"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
)

func newSHA256() hash.Hash {
	return sha256.New()
}

// HostCommand represents a command received from PollHostCommand.
type HostCommand struct {
	HostCommandIdentifier string `json:"HostCommandIdentifier"`
	HostIdentifier        string `json:"HostIdentifier"`
	DeploymentExecutionID string `json:"DeploymentExecutionId"`
	CommandName           string `json:"CommandName"`
	Nonce                 int64  `json:"Nonce,omitempty"`
}

// DeploymentSpecification holds the specification envelope returned by GetDeploymentSpecification.
type DeploymentSpecification struct {
	GenericEnvelope *Envelope `json:"GenericEnvelope"`
	VariantID       string    `json:"VariantId"`
	VariantEnvelope *Envelope `json:"VariantEnvelope"`
}

// Envelope holds the format and payload of a specification or diagnostic.
type Envelope struct {
	Format  string `json:"Format"`
	Payload string `json:"Payload"`
}

// Client communicates with the CodeDeploy Commands service.
type Client struct {
	endpoint    string
	region      string
	version     string
	httpClient  *http.Client
	credentials aws.CredentialsProvider
	logger      *slog.Logger
}

// NewClient creates a CodeDeploy Commands service client.
// Pass a non-nil transport to apply a custom round-tripper (e.g. proxy);
// nil uses Go's default transport. The client always uses an 80s timeout.
//
//	client := codedeployctl.NewClient(cfg.Credentials(), "us-east-1", "", nil, slog.Default())
//	cmd, err := client.PollHostCommand(ctx, "arn:aws:ec2:...")
func NewClient(creds aws.CredentialsProvider, region, endpointOverride string, transport http.RoundTripper, logger *slog.Logger) *Client {
	endpoint := endpointOverride
	if endpoint == "" {
		endpoint = fmt.Sprintf("https://codedeploy-commands.%s.amazonaws.com", region)
	}

	return &Client{
		httpClient: &http.Client{
			Transport: transport,
			Timeout:   80 * time.Second,
		},
		credentials: creds,
		endpoint:    endpoint,
		region:      region,
		version:     resolveVersion(),
		logger:      logger,
	}
}

// resolveVersion reads the module version from Go build info.
// Falls back to "unknown" when build info is unavailable (e.g. go run).
func resolveVersion() string {
	bi, ok := debug.ReadBuildInfo()
	if !ok || bi.Main.Version == "" || bi.Main.Version == "(devel)" {
		return "unknown"
	}
	return bi.Main.Version
}

// PollHostCommand polls for the next deployment command.
// Returns nil HostCommand if no command is available.
//
//	cmd, err := client.PollHostCommand(ctx, hostIdentifier)
//	if cmd == nil { /* no work */ }
func (c *Client) PollHostCommand(ctx context.Context, hostIdentifier string) (*HostCommand, error) {
	input := struct {
		HostIdentifier string `json:"HostIdentifier"`
	}{HostIdentifier: hostIdentifier}

	var output struct {
		HostCommand *HostCommand `json:"HostCommand"`
	}

	if err := c.doRequest(ctx, "PollHostCommand", input, &output); err != nil {
		return nil, err
	}

	return output.HostCommand, nil
}

// Acknowledge sends a command acknowledgement to the service.
// Returns the command status (may be "Failed" if the command was already cancelled).
//
//	status, err := client.Acknowledge(ctx, hostCommandID, diagnostics)
func (c *Client) Acknowledge(ctx context.Context, hostCommandIdentifier string, diagnostics *Envelope) (string, error) {
	input := struct {
		HostCommandIdentifier string    `json:"HostCommandIdentifier"`
		Diagnostics           *Envelope `json:"Diagnostics,omitempty"`
	}{
		HostCommandIdentifier: hostCommandIdentifier,
		Diagnostics:           diagnostics,
	}

	var output struct {
		CommandStatus string `json:"CommandStatus"`
	}

	if err := c.doRequest(ctx, "PutHostCommandAcknowledgement", input, &output); err != nil {
		return "", err
	}

	return output.CommandStatus, nil
}

// Complete reports command completion to the service.
//
//	err := client.Complete(ctx, hostCommandID, "Succeeded", diagnostics)
func (c *Client) Complete(ctx context.Context, hostCommandIdentifier, commandStatus string, diagnostics *Envelope) error {
	input := struct {
		HostCommandIdentifier string    `json:"HostCommandIdentifier"`
		CommandStatus         string    `json:"CommandStatus"`
		Diagnostics           *Envelope `json:"Diagnostics,omitempty"`
	}{
		HostCommandIdentifier: hostCommandIdentifier,
		CommandStatus:         commandStatus,
		Diagnostics:           diagnostics,
	}

	return c.doRequest(ctx, "PutHostCommandComplete", input, nil)
}

// GetDeploymentSpecification retrieves the deployment specification for a command.
//
//	spec, err := client.GetDeploymentSpecification(ctx, executionID, hostID)
func (c *Client) GetDeploymentSpecification(ctx context.Context, deploymentExecutionID, hostIdentifier string) (*DeploymentSpecification, string, error) {
	input := struct {
		DeploymentExecutionID string `json:"DeploymentExecutionId"`
		HostIdentifier        string `json:"HostIdentifier"`
	}{
		DeploymentExecutionID: deploymentExecutionID,
		HostIdentifier:        hostIdentifier,
	}

	var output struct {
		DeploymentSystem        string                   `json:"DeploymentSystem"`
		DeploymentSpecification *DeploymentSpecification `json:"DeploymentSpecification"`
	}

	if err := c.doRequest(ctx, "GetDeploymentSpecification", input, &output); err != nil {
		return nil, "", err
	}

	return output.DeploymentSpecification, output.DeploymentSystem, nil
}

// PostUpdate sends a progress update for a command.
// Pass a non-nil estimatedCompletionTime to inform the service when the command
// is expected to finish; nil omits the field from the wire payload.
//
//	status, err := client.PostUpdate(ctx, hostCommandID, nil, diagnostics)
func (c *Client) PostUpdate(ctx context.Context, hostCommandIdentifier string, estimatedCompletionTime *time.Time, diagnostics *Envelope) (string, error) {
	input := struct {
		HostCommandIdentifier   string     `json:"HostCommandIdentifier"`
		EstimatedCompletionTime *time.Time `json:"EstimatedCompletionTime,omitempty"`
		Diagnostics             *Envelope  `json:"Diagnostics,omitempty"`
	}{
		HostCommandIdentifier:   hostCommandIdentifier,
		EstimatedCompletionTime: estimatedCompletionTime,
		Diagnostics:             diagnostics,
	}

	var output struct {
		CommandStatus string `json:"CommandStatus"`
	}

	if err := c.doRequest(ctx, "PostHostCommandUpdate", input, &output); err != nil {
		return "", err
	}

	return output.CommandStatus, nil
}
