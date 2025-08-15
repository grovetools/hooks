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
	}

	if err := app.Execute(context.Background(), scenarios); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}