package configloader

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// TestLoadAgentOverridesDefaults verifies that YAML values override defaults
// while unset values retain defaults. This is the core config loading behavior.
func TestLoadAgentOverridesDefaults(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yml")
	data := `
root_dir: /custom/root
max_revisions: 10
wait_between_runs: 15
use_fips_mode: true
`
	if err := os.WriteFile(path, []byte(data), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg, err := LoadAgent(path)
	if err != nil {
		t.Fatalf("LoadAgent: %v", err)
	}

	if cfg.RootDir != "/custom/root" {
		t.Errorf("RootDir = %q", cfg.RootDir)
	}
	if cfg.MaxRevisions != 10 {
		t.Errorf("MaxRevisions = %d", cfg.MaxRevisions)
	}
	if cfg.PollInterval != 15*time.Second {
		t.Errorf("PollInterval = %v", cfg.PollInterval)
	}
	if !cfg.UseFIPSMode {
		t.Error("UseFIPSMode should be true")
	}
	// Unset values should keep defaults
	if cfg.ProgramName != "codedeploy-agent" {
		t.Errorf("ProgramName should keep default, got %q", cfg.ProgramName)
	}
}

// TestLoadAgentRubySymbolKeys verifies that config files written by the Ruby
// CodeDeploy agent (with colon-prefixed symbol keys like `:root_dir:`) are
// correctly parsed. The Ruby agent generates this format by default; without
// this support the Go agent silently falls back to defaults.
func TestLoadAgentRubySymbolKeys(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "codedeployagent.yml")
	data := `---
:log_aws_wire: false
:log_dir: '/var/log/test'
:pid_dir: '/tmp/test/pid'
:program_name: codedeploy-agent
:root_dir: '/tmp/test/deployment-root'
:verbose: true
:wait_between_runs: 1
:proxy_uri:
:max_revisions: 3
:disable_imds_v1: true
`
	if err := os.WriteFile(path, []byte(data), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg, err := LoadAgent(path)
	if err != nil {
		t.Fatalf("LoadAgent: %v", err)
	}

	if cfg.RootDir != "/tmp/test/deployment-root" {
		t.Errorf("RootDir = %q, want /tmp/test/deployment-root", cfg.RootDir)
	}
	if cfg.LogDir != "/var/log/test" {
		t.Errorf("LogDir = %q, want /var/log/test", cfg.LogDir)
	}
	if cfg.MaxRevisions != 3 {
		t.Errorf("MaxRevisions = %d, want 3", cfg.MaxRevisions)
	}
	if cfg.PollInterval != 1*time.Second {
		t.Errorf("PollInterval = %v, want 1s", cfg.PollInterval)
	}
	if !cfg.DisableIMDSv1 {
		t.Error("DisableIMDSv1 should be true")
	}
	if cfg.ProgramName != "codedeploy-agent" {
		t.Errorf("ProgramName = %q, want codedeploy-agent", cfg.ProgramName)
	}
}

// TestStripRubySymbolKeys verifies the regex-based transformation of Ruby
// symbol keys to plain YAML keys. Each colon-prefixed key should have its
// leading colon removed while preserving indentation and values.
func TestStripRubySymbolKeys(t *testing.T) {
	input := []byte(":root_dir: /opt/root\n:log_dir: /var/log\nplain_key: value\n")
	got := string(stripRubySymbolKeys(input))
	want := "root_dir: /opt/root\nlog_dir: /var/log\nplain_key: value\n"
	if got != want {
		t.Errorf("stripRubySymbolKeys:\ngot:  %q\nwant: %q", got, want)
	}
}

// TestLoadAgentMissingFileReturnsDefaults verifies that a missing config file
// returns defaults rather than an error. This allows the agent to start with
// just defaults on fresh installs.
func TestLoadAgentMissingFileReturnsDefaults(t *testing.T) {
	cfg, err := LoadAgent("/nonexistent/config.yml")
	if err != nil {
		t.Fatalf("LoadAgent: %v", err)
	}
	if cfg.ProgramName != "codedeploy-agent" {
		t.Errorf("should return defaults, got ProgramName=%q", cfg.ProgramName)
	}
}

// TestLoadAgentInvalidYAML rejects malformed YAML files rather than silently
// using defaults, since a typo could cause unexpected behavior.
func TestLoadAgentInvalidYAML(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "bad.yml")
	// Use truly invalid YAML that cannot be parsed as any valid type
	if err := os.WriteFile(path, []byte("root_dir: [\ninvalid\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := LoadAgent(path)
	if err == nil {
		t.Fatal("expected error for invalid YAML")
	}
}

// TestLoadOnPremises_IAMUser verifies that on-premises credential files with
// IAM User config are correctly parsed.
func TestLoadOnPremises_IAMUser(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "onprem.yml")
	data := `
region: us-west-2
aws_access_key_id: AKID
aws_secret_access_key: SECRET
iam_user_arn: arn:aws:iam::123:user/deploy
`
	if err := os.WriteFile(path, []byte(data), 0o644); err != nil {
		t.Fatal(err)
	}

	onprem, err := LoadOnPremises(path)
	if err != nil {
		t.Fatalf("LoadOnPremises: %v", err)
	}
	if onprem.Region != "us-west-2" {
		t.Errorf("Region = %q", onprem.Region)
	}
	if onprem.AWSAccessKeyID != "AKID" {
		t.Errorf("AWSAccessKeyID = %q", onprem.AWSAccessKeyID)
	}
	if onprem.IAMUserARN != "arn:aws:iam::123:user/deploy" {
		t.Errorf("IAMUserARN = %q", onprem.IAMUserARN)
	}
}

// TestLoadOnPremises_IAMSession verifies that on-premises config with IAM
// Session ARN and aws_credentials_file is correctly parsed. The Ruby agent
// uses the key "aws_credentials_file:" in its YAML config.
func TestLoadOnPremises_IAMSession(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "onprem.yml")
	data := `
region: us-east-1
iam_session_arn: arn:aws:sts::123:assumed-role/role/session
aws_credentials_file: /etc/codedeploy-agent/credentials
`
	if err := os.WriteFile(path, []byte(data), 0o644); err != nil {
		t.Fatal(err)
	}

	onprem, err := LoadOnPremises(path)
	if err != nil {
		t.Fatalf("LoadOnPremises: %v", err)
	}
	if onprem.Region != "us-east-1" {
		t.Errorf("Region = %q", onprem.Region)
	}
	if onprem.IAMSessionARN != "arn:aws:sts::123:assumed-role/role/session" {
		t.Errorf("IAMSessionARN = %q", onprem.IAMSessionARN)
	}
	if onprem.CredentialsFile != "/etc/codedeploy-agent/credentials" {
		t.Errorf("CredentialsFile = %q", onprem.CredentialsFile)
	}
}

// TestLoadOnPremises_MissingFile verifies that a non-existent on-premises
// config file returns an error (unlike LoadAgent which returns defaults).
// On-premises config is required when present in the agent config.
func TestLoadOnPremises_MissingFile(t *testing.T) {
	_, err := LoadOnPremises("/nonexistent/onpremises.yml")
	if err == nil {
		t.Fatal("expected error for missing on-premises file")
	}
}

// TestLoadOnPremises_InvalidYAML verifies that malformed YAML in the
// on-premises config is rejected.
func TestLoadOnPremises_InvalidYAML(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "bad.yml")
	if err := os.WriteFile(path, []byte("region: [\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := LoadOnPremises(path)
	if err == nil {
		t.Fatal("expected error for invalid YAML")
	}
}

// TestLoadAgentAllFields verifies that all optional config fields are correctly
// loaded when set. This catches regressions when new fields are added.
func TestLoadAgentAllFields(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "full.yml")
	data := `
program_name: custom-agent
root_dir: /custom/root
log_dir: /custom/log
pid_dir: /custom/pid
ongoing_deployment_tracking: custom-tracking
on_premises_config_file: /custom/onprem.yml
proxy_uri: http://proxy:8080
deploy_control_endpoint: https://custom.endpoint.com
s3_endpoint_override: https://s3.custom.com
wait_between_runs: 30
wait_after_error: 60
http_read_timeout: 120
kill_agent_max_wait_time_seconds: 300
max_revisions: 7
use_fips_mode: true
enable_auth_policy: true
enable_deployments_log: true
disable_imds_v1: true
`
	if err := os.WriteFile(path, []byte(data), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg, err := LoadAgent(path)
	if err != nil {
		t.Fatalf("LoadAgent: %v", err)
	}

	if cfg.ProgramName != "custom-agent" {
		t.Errorf("ProgramName = %q", cfg.ProgramName)
	}
	if cfg.LogDir != "/custom/log" {
		t.Errorf("LogDir = %q", cfg.LogDir)
	}
	if cfg.PIDDir != "/custom/pid" {
		t.Errorf("PIDDir = %q", cfg.PIDDir)
	}
	if cfg.OngoingDeploymentTracking != "custom-tracking" {
		t.Errorf("OngoingDeploymentTracking = %q", cfg.OngoingDeploymentTracking)
	}
	if cfg.OnPremisesConfigFile != "/custom/onprem.yml" {
		t.Errorf("OnPremisesConfigFile = %q", cfg.OnPremisesConfigFile)
	}
	if cfg.ProxyURI != "http://proxy:8080" {
		t.Errorf("ProxyURI = %q", cfg.ProxyURI)
	}
	if cfg.DeployControlEndpoint != "https://custom.endpoint.com" {
		t.Errorf("DeployControlEndpoint = %q", cfg.DeployControlEndpoint)
	}
	if cfg.S3EndpointOverride != "https://s3.custom.com" {
		t.Errorf("S3EndpointOverride = %q", cfg.S3EndpointOverride)
	}
	if cfg.ErrorBackoff != 60*time.Second {
		t.Errorf("ErrorBackoff = %v", cfg.ErrorBackoff)
	}
	if cfg.HTTPReadTimeout != 120*time.Second {
		t.Errorf("HTTPReadTimeout = %v", cfg.HTTPReadTimeout)
	}
	if cfg.KillAgentMaxWait != 300*time.Second {
		t.Errorf("KillAgentMaxWait = %v", cfg.KillAgentMaxWait)
	}
	if cfg.MaxRevisions != 7 {
		t.Errorf("MaxRevisions = %d", cfg.MaxRevisions)
	}
	if !cfg.EnableAuthPolicy {
		t.Error("EnableAuthPolicy should be true")
	}
	if !cfg.EnableDeploymentsLog {
		t.Error("EnableDeploymentsLog should be true")
	}
}

// TestIsRubyStyleYAML verifies the Ruby symbol detection for both prefix and
// mid-file colon patterns. The Go agent must handle both Ruby-generated and
// Go-generated config formats.
func TestIsRubyStyleYAML(t *testing.T) {
	if !isRubyStyleYAML([]byte(":root_dir: /opt")) {
		t.Error("should detect leading colon")
	}
	if !isRubyStyleYAML([]byte("---\n:root_dir: /opt")) {
		t.Error("should detect colon after newline")
	}
	if isRubyStyleYAML([]byte("root_dir: /opt\nlog_dir: /var/log")) {
		t.Error("should not detect plain YAML as Ruby style")
	}
}
