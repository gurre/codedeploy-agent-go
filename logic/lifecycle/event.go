// Package lifecycle defines deployment lifecycle event constants,
// hook-to-deployment-root mappings, and deployment root selection.
package lifecycle

// Event represents a deployment lifecycle event name.
type Event string

const (
	BeforeBlockTraffic Event = "BeforeBlockTraffic"
	AfterBlockTraffic  Event = "AfterBlockTraffic"
	ApplicationStop    Event = "ApplicationStop"
	BeforeInstall      Event = "BeforeInstall"
	AfterInstall       Event = "AfterInstall"
	ApplicationStart   Event = "ApplicationStart"
	BeforeAllowTraffic Event = "BeforeAllowTraffic"
	AfterAllowTraffic  Event = "AfterAllowTraffic"
	ValidateService    Event = "ValidateService"
)

// DeploymentRoot indicates which deployment archive to use for a hook.
type DeploymentRoot int

const (
	// LastSuccessful uses the last successfully installed deployment's appspec.
	LastSuccessful DeploymentRoot = iota
	// MostRecent uses the most recently downloaded deployment's appspec.
	MostRecent
	// Current uses the current (new) deployment's appspec.
	Current
)

// DefaultHookMapping returns the standard command-to-lifecycle-event mapping.
// Each command name maps to the lifecycle events it triggers.
//
//	mapping := lifecycle.DefaultHookMapping()
//	events := mapping["BeforeInstall"] // []Event{BeforeInstall}
func DefaultHookMapping() map[string][]Event {
	return map[string][]Event{
		"BeforeBlockTraffic": {BeforeBlockTraffic},
		"AfterBlockTraffic":  {AfterBlockTraffic},
		"ApplicationStop":    {ApplicationStop},
		"BeforeInstall":      {BeforeInstall},
		"AfterInstall":       {AfterInstall},
		"ApplicationStart":   {ApplicationStart},
		"BeforeAllowTraffic": {BeforeAllowTraffic},
		"AfterAllowTraffic":  {AfterAllowTraffic},
		"ValidateService":    {ValidateService},
	}
}

// DefaultOrderedEvents returns all lifecycle events in execution order.
//
//	for _, ev := range lifecycle.DefaultOrderedEvents() { ... }
func DefaultOrderedEvents() []Event {
	return []Event{
		BeforeBlockTraffic,
		AfterBlockTraffic,
		ApplicationStop,
		BeforeInstall,
		AfterInstall,
		ApplicationStart,
		BeforeAllowTraffic,
		AfterAllowTraffic,
		ValidateService,
	}
}

// hookDeploymentMapping maps each lifecycle event to which deployment root it uses.
var hookDeploymentMapping = map[Event]DeploymentRoot{
	BeforeBlockTraffic: LastSuccessful,
	AfterBlockTraffic:  LastSuccessful,
	ApplicationStop:    LastSuccessful,
	BeforeInstall:      Current,
	AfterInstall:       Current,
	ApplicationStart:   Current,
	BeforeAllowTraffic: Current,
	AfterAllowTraffic:  Current,
	ValidateService:    Current,
}

// rollbackBlueGreenMapping overrides traffic hooks for rollback + BLUE_GREEN deployments.
var rollbackBlueGreenMapping = map[Event]DeploymentRoot{
	BeforeBlockTraffic: MostRecent,
	AfterBlockTraffic:  MostRecent,
	ApplicationStop:    LastSuccessful,
	BeforeInstall:      Current,
	AfterInstall:       Current,
	ApplicationStart:   Current,
	BeforeAllowTraffic: LastSuccessful,
	AfterAllowTraffic:  LastSuccessful,
	ValidateService:    Current,
}

// SelectDeploymentRoot determines which deployment archive root to use for a
// given lifecycle event based on the deployment creator and type.
//
//	root := lifecycle.SelectDeploymentRoot(lifecycle.BeforeInstall, "user", "IN_PLACE")
//	// root == Current
func SelectDeploymentRoot(event Event, deploymentCreator, deploymentType string) DeploymentRoot {
	m := hookDeploymentMapping
	if deploymentCreator == "codeDeployRollback" && deploymentType == "BLUE_GREEN" {
		m = rollbackBlueGreenMapping
	}
	if root, ok := m[event]; ok {
		return root
	}
	return Current
}
