// Package configloader loads agent configuration from YAML files on disk.
package configloader

import (
	"bytes"
	"fmt"
	"os"
	"regexp"
	"time"

	"github.com/gurre/codedeploy-agent-go/state/config"
	"gopkg.in/yaml.v3"
)

// rubySymbolKeyRe matches YAML lines starting with a colon-prefixed key
// (Ruby symbol style), e.g. ":root_dir: /opt/..." → "root_dir: /opt/...".
var rubySymbolKeyRe = regexp.MustCompile(`(?m)^(\s*):(\w+):`)

// stripRubySymbolKeys converts Ruby-style YAML symbol keys to plain keys.
// The Ruby CodeDeploy agent writes config with `:key:` notation. This
// pre-processing allows the Go agent to read those files unmodified.
func stripRubySymbolKeys(data []byte) []byte {
	return rubySymbolKeyRe.ReplaceAll(data, []byte("${1}${2}:"))
}

// isRubyStyleYAML returns true if the data contains colon-prefixed keys.
func isRubyStyleYAML(data []byte) bool {
	return bytes.Contains(data, []byte("\n:")) || bytes.HasPrefix(data, []byte(":"))
}

// rawConfig mirrors the YAML structure of codedeployagent.yml.
type rawConfig struct {
	ProgramName               string `yaml:"program_name"`
	RootDir                   string `yaml:"root_dir"`
	LogDir                    string `yaml:"log_dir"`
	PIDDir                    string `yaml:"pid_dir"`
	OngoingDeploymentTracking string `yaml:"ongoing_deployment_tracking"`
	OnPremisesConfigFile      string `yaml:"on_premises_config_file"`
	ProxyURI                  string `yaml:"proxy_uri"`
	DeployControlEndpoint     string `yaml:"deploy_control_endpoint"`
	S3EndpointOverride        string `yaml:"s3_endpoint_override"`
	WaitBetweenRuns           *int   `yaml:"wait_between_runs"`
	WaitAfterError            *int   `yaml:"wait_after_error"`
	HTTPReadTimeout           *int   `yaml:"http_read_timeout"`
	KillAgentMaxWaitTime      *int   `yaml:"kill_agent_max_wait_time_seconds"`
	MaxRevisions              *int   `yaml:"max_revisions"`
	UseFIPSMode               *bool  `yaml:"use_fips_mode"`
	EnableAuthPolicy          *bool  `yaml:"enable_auth_policy"`
	EnableDeploymentsLog      *bool  `yaml:"enable_deployments_log"`
	DisableIMDSv1             *bool  `yaml:"disable_imds_v1"`
}

// rawOnPremises mirrors the YAML structure of codedeploy.onpremises.yml.
type rawOnPremises struct {
	Region             string `yaml:"region"`
	AWSAccessKeyID     string `yaml:"aws_access_key_id"`
	AWSSecretAccessKey string `yaml:"aws_secret_access_key"`
	IAMUserARN         string `yaml:"iam_user_arn"`
	IAMSessionARN      string `yaml:"iam_session_arn"`
	CredentialsFile    string `yaml:"aws_credentials_file"`
}

// LoadAgent loads the agent config file, overlaying values onto defaults.
// Missing or empty fields retain their default values.
//
//	cfg, err := configloader.LoadAgent("/etc/codedeploy-agent/conf/codedeployagent.yml")
func LoadAgent(path string) (config.Agent, error) {
	cfg := config.Default()

	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return cfg, nil // Use defaults if file doesn't exist
		}
		return config.Agent{}, fmt.Errorf("configloader: %w", err)
	}

	// Support Ruby agent config files that use colon-prefixed symbol keys.
	if isRubyStyleYAML(data) {
		data = stripRubySymbolKeys(data)
	}

	var raw rawConfig
	if err := yaml.Unmarshal(data, &raw); err != nil {
		return config.Agent{}, fmt.Errorf("configloader: parse %s: %w", path, err)
	}

	if raw.ProgramName != "" {
		cfg.ProgramName = raw.ProgramName
	}
	if raw.RootDir != "" {
		cfg.RootDir = raw.RootDir
	}
	if raw.LogDir != "" {
		cfg.LogDir = raw.LogDir
	}
	if raw.PIDDir != "" {
		cfg.PIDDir = raw.PIDDir
	}
	if raw.OngoingDeploymentTracking != "" {
		cfg.OngoingDeploymentTracking = raw.OngoingDeploymentTracking
	}
	if raw.OnPremisesConfigFile != "" {
		cfg.OnPremisesConfigFile = raw.OnPremisesConfigFile
	}
	if raw.ProxyURI != "" {
		cfg.ProxyURI = raw.ProxyURI
	}
	if raw.DeployControlEndpoint != "" {
		cfg.DeployControlEndpoint = raw.DeployControlEndpoint
	}
	if raw.S3EndpointOverride != "" {
		cfg.S3EndpointOverride = raw.S3EndpointOverride
	}
	if raw.WaitBetweenRuns != nil {
		cfg.PollInterval = time.Duration(*raw.WaitBetweenRuns) * time.Second
	}
	if raw.WaitAfterError != nil {
		cfg.ErrorBackoff = time.Duration(*raw.WaitAfterError) * time.Second
	}
	if raw.HTTPReadTimeout != nil {
		cfg.HTTPReadTimeout = time.Duration(*raw.HTTPReadTimeout) * time.Second
	}
	if raw.KillAgentMaxWaitTime != nil {
		cfg.KillAgentMaxWait = time.Duration(*raw.KillAgentMaxWaitTime) * time.Second
	}
	if raw.MaxRevisions != nil {
		cfg.MaxRevisions = *raw.MaxRevisions
	}
	if raw.UseFIPSMode != nil {
		cfg.UseFIPSMode = *raw.UseFIPSMode
	}
	if raw.EnableAuthPolicy != nil {
		cfg.EnableAuthPolicy = *raw.EnableAuthPolicy
	}
	if raw.EnableDeploymentsLog != nil {
		cfg.EnableDeploymentsLog = *raw.EnableDeploymentsLog
	}
	if raw.DisableIMDSv1 != nil {
		cfg.DisableIMDSv1 = *raw.DisableIMDSv1
	}

	return cfg, nil
}

// LoadOnPremises loads on-premises credentials from the given YAML file.
//
//	onprem, err := configloader.LoadOnPremises("/etc/codedeploy-agent/conf/codedeploy.onpremises.yml")
func LoadOnPremises(path string) (config.OnPremises, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return config.OnPremises{}, fmt.Errorf("configloader: %w", err)
	}

	var raw rawOnPremises
	if err := yaml.Unmarshal(data, &raw); err != nil {
		return config.OnPremises{}, fmt.Errorf("configloader: parse %s: %w", path, err)
	}

	return config.OnPremises{
		Region:             raw.Region,
		AWSAccessKeyID:     raw.AWSAccessKeyID,
		AWSSecretAccessKey: raw.AWSSecretAccessKey,
		IAMUserARN:         raw.IAMUserARN,
		IAMSessionARN:      raw.IAMSessionARN,
		CredentialsFile:    raw.CredentialsFile,
	}, nil
}
