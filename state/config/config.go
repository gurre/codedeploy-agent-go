// Package config defines the agent's configuration structs and their defaults.
// These are pure data types with no I/O; loading is handled by adaptor/configloader.
package config

import "time"

// Agent holds the main agent configuration loaded from codedeployagent.yml.
// Fields are aligned from largest to smallest for memory efficiency.
type Agent struct {
	// ProgramName is the agent program identifier.
	ProgramName string
	// RootDir is the base directory for deployment artifacts.
	RootDir string
	// LogDir is the directory for log files.
	LogDir string
	// PIDDir is the directory for PID files.
	PIDDir string
	// OngoingDeploymentTracking is the subdirectory name for tracking files.
	OngoingDeploymentTracking string
	// OnPremisesConfigFile is the path to on-premises credentials config.
	OnPremisesConfigFile string
	// ProxyURI is the HTTP proxy URI, if any.
	ProxyURI string
	// DeployControlEndpoint overrides the CodeDeploy Commands endpoint.
	DeployControlEndpoint string
	// S3EndpointOverride overrides the S3 endpoint.
	S3EndpointOverride string

	// KillAgentMaxWait is the graceful shutdown timeout.
	KillAgentMaxWait time.Duration
	// PollInterval is the delay between polling cycles.
	PollInterval time.Duration
	// ErrorBackoff is the delay after a polling error.
	ErrorBackoff time.Duration
	// HTTPReadTimeout is the HTTP read timeout for API calls.
	HTTPReadTimeout time.Duration

	// MaxRevisions is the number of deployment archives to retain.
	MaxRevisions int

	// UseFIPSMode enables FIPS-compliant endpoints.
	UseFIPSMode bool
	// EnableAuthPolicy enables authorization policy enforcement.
	EnableAuthPolicy bool
	// EnableDeploymentsLog enables the per-deployment log file.
	EnableDeploymentsLog bool
	// DisableIMDSv1 disables fallback to IMDSv1.
	DisableIMDSv1 bool
}

// Default returns an Agent config with the same defaults as the Ruby agent.
//
//	cfg := config.Default()
//	cfg.RootDir = "/opt/codedeploy-agent"
func Default() Agent {
	return Agent{
		ProgramName:               "codedeploy-agent",
		RootDir:                   "/opt/codedeploy-agent/deployment-root",
		LogDir:                    "/var/log/aws/codedeploy-agent",
		OngoingDeploymentTracking: "ongoing-deployment",
		OnPremisesConfigFile:      "/etc/codedeploy-agent/conf/codedeploy.onpremises.yml",
		KillAgentMaxWait:          7200 * time.Second,
		PollInterval:              30 * time.Second,
		ErrorBackoff:              30 * time.Second,
		HTTPReadTimeout:           80 * time.Second,
		MaxRevisions:              5,
		EnableDeploymentsLog:      true,
	}
}

// OnPremises holds credentials for on-premises (non-EC2) instances.
// Fields aligned from largest to smallest.
type OnPremises struct {
	// Region is the AWS region.
	Region string
	// AWSAccessKeyID is the IAM access key.
	AWSAccessKeyID string
	// AWSSecretAccessKey is the IAM secret key.
	AWSSecretAccessKey string
	// IAMUserARN is the on-premises instance's IAM user ARN.
	IAMUserARN string
	// IAMSessionARN is used for STS session-based credentials.
	IAMSessionARN string
	// CredentialsFile is the path to a rotating credentials file.
	CredentialsFile string
}

// FIPSEnabledRegions returns the set of regions where FIPS mode is valid.
func FIPSEnabledRegions() map[string]bool {
	return map[string]bool{
		"us-east-1":     true,
		"us-east-2":     true,
		"us-west-1":     true,
		"us-west-2":     true,
		"us-gov-west-1": true,
		"us-gov-east-1": true,
	}
}
