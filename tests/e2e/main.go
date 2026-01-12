package main

import (
	"context"
	"fmt"
	"os"

	"github.com/mattsolo1/grove-tend/pkg/app"
	"github.com/mattsolo1/grove-tend/pkg/harness"
)

func main() {
	scenarios := []*harness.Scenario{
		HooksDirectExecutionScenario(),
		HooksSymlinkExecutionScenario(),
		InstallCommandScenario(),
		LocalStorageScenario(),
		SessionQueriesScenario(),
		SessionBrowseScenario(),
		OfflineOperationScenario(),
		OneshotJobScenario(),
		OneshotJobValidationScenario(),
		// FlowOneshotTrackingScenario(), // Disabled - needs API updates
		FlowWorktreeScenario(),
		FlowRealLLMScenario(),
		// TODO: Fix MixedSessionTypesScenario - it's not properly isolating the test database
		// MixedSessionTypesScenario(),
		// PID-based session tracking tests
		PIDBasedSessionTracking(),
		SessionCleanupOnStop(),
		SessionDiscoveryService(),
		SessionsListIntegration(),
		// Real-time status update tests
		RealtimeStatusUpdateScenario(),
		// Plan preservation tests
		PlanPreservationScenario(),
		PlanPreservationDisabledScenario(),
		PlanPreservationNoPlanScenario(),
		PlanPreservationEmptyPlanScenario(),
	}

	if err := app.Execute(context.Background(), scenarios); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}
