package terraformcmd

import "runtime"

// AssertSupportedTerraformExecutionPlatform applies the source platform gate.
// An empty platform selects the current Go runtime platform. "win32" is
// accepted as the Node spelling so portability tests can pin the source API.
func AssertSupportedTerraformExecutionPlatform(platform string) error {
	if platform == "" {
		platform = runtime.GOOS
	}
	if platform == "windows" || platform == "win32" {
		return domainFailure(
			"UNSUPPORTED_TERRAFORM_EXECUTION_PLATFORM",
			UnsupportedTerraformExecutionPlatformMessage,
		)
	}
	if platform == runtime.GOOS && !terraformProcessGroupsSupported {
		return domainFailure(
			"UNSUPPORTED_TERRAFORM_EXECUTION_PLATFORM",
			UnsupportedTerraformExecutionPlatformMessage,
		)
	}
	return nil
}
