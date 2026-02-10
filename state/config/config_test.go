package config

import (
	"testing"
	"time"
)

// TestDefaultConfigHasExpectedValues verifies that Default() returns the same
// defaults as the Ruby agent's config.rb. These values are relied upon by the
// service for timeout calculations and polling intervals.
func TestDefaultConfigHasExpectedValues(t *testing.T) {
	cfg := Default()

	if cfg.ProgramName != "codedeploy-agent" {
		t.Errorf("ProgramName = %q", cfg.ProgramName)
	}
	if cfg.PollInterval != 30*time.Second {
		t.Errorf("PollInterval = %v", cfg.PollInterval)
	}
	if cfg.KillAgentMaxWait != 7200*time.Second {
		t.Errorf("KillAgentMaxWait = %v", cfg.KillAgentMaxWait)
	}
	if cfg.MaxRevisions != 5 {
		t.Errorf("MaxRevisions = %d", cfg.MaxRevisions)
	}
	if !cfg.EnableDeploymentsLog {
		t.Error("EnableDeploymentsLog should be true by default")
	}
	if cfg.OnPremisesConfigFile != "/etc/codedeploy-agent/conf/codedeploy.onpremises.yml" {
		t.Errorf("OnPremisesConfigFile = %q", cfg.OnPremisesConfigFile)
	}
}

// TestFIPSEnabledRegionsContainsExpectedRegions verifies the FIPS region set
// matches the Ruby agent's FIPS_ENABLED_REGIONS constant.
func TestFIPSEnabledRegionsContainsExpectedRegions(t *testing.T) {
	regions := FIPSEnabledRegions()
	expected := []string{"us-east-1", "us-east-2", "us-west-1", "us-west-2", "us-gov-west-1", "us-gov-east-1"}
	for _, r := range expected {
		if !regions[r] {
			t.Errorf("missing FIPS region %q", r)
		}
	}
	if len(regions) != len(expected) {
		t.Errorf("expected %d regions, got %d", len(expected), len(regions))
	}
}

// TestDefaultConfigBooleanDefaults verifies that security-related booleans
// default to the safe (off) value.
func TestDefaultConfigBooleanDefaults(t *testing.T) {
	cfg := Default()
	if cfg.UseFIPSMode {
		t.Error("UseFIPSMode should default to false")
	}
	if cfg.EnableAuthPolicy {
		t.Error("EnableAuthPolicy should default to false")
	}
	if cfg.DisableIMDSv1 {
		t.Error("DisableIMDSv1 should default to false")
	}
}
