package terraformcmd

import (
	"strings"
	"unicode/utf8"
)

// SnapshotTerraformCommandLimits validates and detaches command limits before
// any filesystem inspection or goroutine starts.
func SnapshotTerraformCommandLimits(value TerraformCommandLimits) (TerraformCommandLimits, error) {
	limits := value
	if value.TimeoutMs != nil {
		timeout := *value.TimeoutMs
		limits.TimeoutMs = &timeout
		if timeout <= 0 || timeout > maximumJavaScriptSafeInteger {
			return TerraformCommandLimits{}, invalidCommandLimit()
		}
	}
	if value.MaxStdoutBytes <= 0 || value.MaxStdoutBytes > maxTerraformCommandStdoutBytes ||
		value.MaxStderrBytes <= 0 || value.MaxStderrBytes > maxTerraformCommandStderrBytes {
		return TerraformCommandLimits{}, invalidCommandLimit()
	}
	return limits, nil
}

func invalidCommandLimit() error {
	return domainFailure(
		"INVALID_TERRAFORM_COMMAND_LIMIT",
		"Terraform command limits are outside the allowed range",
	)
}

func snapshotArgv(value []string) ([]string, error) {
	if value == nil || len(value) > maxTerraformCommandArguments {
		return nil, invalidCommandArguments()
	}
	result := make([]string, 0, len(value))
	var totalBytes int64
	for _, argument := range value {
		if strings.IndexByte(argument, 0) >= 0 || !utf8.ValidString(argument) {
			return nil, invalidCommandArguments()
		}
		totalBytes += int64(len(argument))
		if totalBytes > maxTerraformCommandArgumentBytes {
			return nil, invalidCommandArguments()
		}
		result = append(result, argument)
	}
	return result, nil
}

func invalidCommandArguments() error {
	return domainFailure(
		"INVALID_TERRAFORM_COMMAND_ARGUMENTS",
		"Terraform command arguments are not allowed",
	)
}

// SnapshotTerraformCommandEnvironment validates and detaches the complete
// child environment. Byte limits count UTF-8 bytes, matching Buffer.byteLength.
func SnapshotTerraformCommandEnvironment(value map[string]string) (map[string]string, error) {
	if value == nil || len(value) > maxTerraformEnvironmentEntries {
		return nil, invalidCommandEnvironment()
	}
	result := make(map[string]string, len(value))
	var totalBytes int64
	for key, environmentValue := range value {
		if key == "" || strings.ContainsAny(key, "=\x00") || strings.IndexByte(environmentValue, 0) >= 0 ||
			!utf8.ValidString(key) || !utf8.ValidString(environmentValue) {
			return nil, invalidCommandEnvironment()
		}
		totalBytes += int64(len(key) + len(environmentValue))
		if totalBytes > maxTerraformEnvironmentBytes {
			return nil, invalidCommandEnvironment()
		}
		result[key] = environmentValue
	}
	return result, nil
}

func invalidCommandEnvironment() error {
	return domainFailure(
		"INVALID_TERRAFORM_COMMAND_ENVIRONMENT",
		"Terraform command environment is not allowed",
	)
}
