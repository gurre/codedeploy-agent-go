package lifecycle

import (
	"testing"
)

// TestDefaultHookMappingCoversAllEvents verifies that every ordered lifecycle
// event appears in the default hook mapping. This ensures no event is silently
// dropped when the agent processes commands.
func TestDefaultHookMappingCoversAllEvents(t *testing.T) {
	mapping := DefaultHookMapping()
	for _, event := range DefaultOrderedEvents() {
		if _, ok := mapping[string(event)]; !ok {
			t.Errorf("event %q missing from DefaultHookMapping", event)
		}
	}
}

// TestDefaultHookMappingKeysMatchValues verifies each mapping entry maps a
// command name to the single lifecycle event with the same name. This is the
// invariant that the command executor relies on for dispatch.
func TestDefaultHookMappingKeysMatchValues(t *testing.T) {
	for name, events := range DefaultHookMapping() {
		if len(events) != 1 {
			t.Errorf("expected 1 event for %q, got %d", name, len(events))
			continue
		}
		if string(events[0]) != name {
			t.Errorf("mapping key %q doesn't match event %q", name, events[0])
		}
	}
}

// TestSelectDeploymentRootStandardMapping verifies that the standard (non-rollback)
// mapping assigns LastSuccessful to traffic-block and stop hooks, and Current to
// install and post-install hooks.
func TestSelectDeploymentRootStandardMapping(t *testing.T) {
	cases := []struct {
		event Event
		want  DeploymentRoot
	}{
		{BeforeBlockTraffic, LastSuccessful},
		{AfterBlockTraffic, LastSuccessful},
		{ApplicationStop, LastSuccessful},
		{BeforeInstall, Current},
		{AfterInstall, Current},
		{ApplicationStart, Current},
		{BeforeAllowTraffic, Current},
		{AfterAllowTraffic, Current},
		{ValidateService, Current},
	}
	for _, tc := range cases {
		got := SelectDeploymentRoot(tc.event, "user", "IN_PLACE")
		if got != tc.want {
			t.Errorf("SelectDeploymentRoot(%q, user, IN_PLACE) = %d, want %d", tc.event, got, tc.want)
		}
	}
}

// TestSelectDeploymentRootRollbackBlueGreen verifies the rollback-specific
// mapping for BLUE_GREEN deployments. Traffic hooks use MostRecent instead
// of LastSuccessful, and allow-traffic hooks use LastSuccessful instead of Current.
func TestSelectDeploymentRootRollbackBlueGreen(t *testing.T) {
	cases := []struct {
		event Event
		want  DeploymentRoot
	}{
		{BeforeBlockTraffic, MostRecent},
		{AfterBlockTraffic, MostRecent},
		{ApplicationStop, LastSuccessful},
		{BeforeInstall, Current},
		{AfterInstall, Current},
		{ApplicationStart, Current},
		{BeforeAllowTraffic, LastSuccessful},
		{AfterAllowTraffic, LastSuccessful},
		{ValidateService, Current},
	}
	for _, tc := range cases {
		got := SelectDeploymentRoot(tc.event, "codeDeployRollback", "BLUE_GREEN")
		if got != tc.want {
			t.Errorf("SelectDeploymentRoot(%q, codeDeployRollback, BLUE_GREEN) = %d, want %d", tc.event, got, tc.want)
		}
	}
}

// TestSelectDeploymentRootUnknownEventDefaultsToCurrent verifies that an
// unknown event falls back to Current, which is the safe default for
// custom lifecycle events.
func TestSelectDeploymentRootUnknownEventDefaultsToCurrent(t *testing.T) {
	got := SelectDeploymentRoot(Event("CustomEvent"), "user", "IN_PLACE")
	if got != Current {
		t.Errorf("unknown event should default to Current, got %d", got)
	}
}

// TestDefaultOrderedEventsLength ensures all 9 standard lifecycle events are
// present and in order. This is a simple count guard against accidental removal.
func TestDefaultOrderedEventsLength(t *testing.T) {
	events := DefaultOrderedEvents()
	if len(events) != 9 {
		t.Errorf("expected 9 ordered events, got %d", len(events))
	}
}
