package main

// Test-only façades over each command's real Cobra constructor. Production
// wires the constructors directly in cobra.go
// (newXCobraCommand(defaultXDependencies())); these wrappers exist so the
// command behavior suites can execute the same constructors with injected
// dependencies and plain argv slices.

func adoptCommandWithDependencies(arguments []string, dependencies blockDCommandDependencies) (int, error) {
	return executeStandaloneCobra(newAdoptCobraCommand(dependencies), arguments)
}

func stageImportsCommandWithDependencies(arguments []string, dependencies blockDCommandDependencies) (int, error) {
	return executeStandaloneCobra(newImportStagingCobraCommand("stage-imports", dependencies), arguments)
}

func unstageImportsCommandWithDependencies(arguments []string, dependencies blockDCommandDependencies) (int, error) {
	return executeStandaloneCobra(newImportStagingCobraCommand("unstage-imports", dependencies), arguments)
}

func applyCommandWithDependencies(arguments []string, dependencies blockDCommandDependencies) (int, error) {
	return executeStandaloneCobra(newApplyCobraCommand(dependencies), arguments)
}

func reconcileCommandWithDependencies(arguments []string, dependencies authoringCoreDependencies) (int, error) {
	return executeStandaloneCobra(newReconcileCobraCommand(dependencies), arguments)
}

func openAPIMapCommandWithDependencies(arguments []string, dependencies authoringCoreDependencies) (int, error) {
	return executeStandaloneCobra(newOpenAPIMapCobraCommand(dependencies), arguments)
}

func transformAdoptParityCommandWithDependencies(arguments []string, dependencies authoringCoreDependencies) (status int, err error) {
	return executeStandaloneCobra(newTransformAdoptParityCobraCommand(dependencies), arguments)
}

func providerProbeCommandWithDependencies(arguments []string, dependencies authoringProbeDependencies) (int, error) {
	return executeStandaloneCobra(newProviderProbeCobraCommand(dependencies), arguments)
}

func planCommandWithDependencies(
	arguments []string,
	dependencies planCommandDependencies,
) (int, error) {
	return executeStandaloneCobra(newPlanCobraCommand(dependencies), arguments)
}

func cleanPlansCommandWithDependencies(
	arguments []string,
	dependencies planCommandDependencies,
) (int, error) {
	return executeStandaloneCobra(newCleanPlansCobraCommand(dependencies), arguments)
}

func sourceOperationMapCommandWithDependencies(arguments []string, dependencies authoringSourceDependencies) (int, error) {
	return executeStandaloneCobra(newSourceOperationMapCobraCommand(dependencies), arguments)
}

func sourceEvidenceEvalCommandWithDependencies(arguments []string, dependencies authoringSourceDependencies) (int, error) {
	return executeStandaloneCobra(newSourceEvidenceEvalCobraCommand(dependencies), arguments)
}

func checkPackCommandWithDependencies(arguments []string, dependencies metadataCommandDependencies) (int, error) {
	return executeStandaloneCobra(newCheckPackCobraCommand(dependencies), arguments)
}

func checkPackSetCommandWithDependencies(arguments []string, dependencies metadataCommandDependencies) (int, error) {
	return executeStandaloneCobra(newCheckPackSetCobraCommand(dependencies), arguments)
}

func deploymentCommandWithDependencies(arguments []string, dependencies metadataCommandDependencies) (int, error) {
	return executeStandaloneCobra(newDeploymentCobraCommand(dependencies), arguments)
}

func refreshCommandWithDependencies(
	arguments []string,
	dependencies refreshCommandDependencies,
) (int, error) {
	return executeStandaloneCobra(newRefreshCobraCommand(dependencies), arguments)
}
