// Package terraformcmd runs trusted Terraform subprocesses under the bounded,
// non-shell contract used by Infrawright's Node implementation.
package terraformcmd

import "github.com/dvmrry/infrawright-dev/go/internal/procerr"

const (
	maxTerraformCommandStdoutBytes   int64 = 8 * 1024 * 1024
	maxTerraformCommandStderrBytes   int64 = 16 * 1024 * 1024
	maxTerraformCommandArguments           = 128
	maxTerraformCommandArgumentBytes int64 = 256 * 1024
	maxTerraformEnvironmentEntries         = 256
	maxTerraformEnvironmentBytes     int64 = 256 * 1024
	maximumJavaScriptSafeInteger     int64 = 9_007_199_254_740_991
)

// UnsupportedTerraformExecutionPlatformMessage is the frozen platform-gate
// diagnostic from node-src/io/terraform-command.ts.
const UnsupportedTerraformExecutionPlatformMessage = "Terraform execution through Infrawright is supported on Linux and macOS; Windows is not a supported operational platform."

// TerraformCommandLimits bounds one Terraform subprocess. A nil TimeoutMs is
// the source contract's null timeout (no execution deadline).
type TerraformCommandLimits struct {
	TimeoutMs      *int64
	MaxStdoutBytes int64
	MaxStderrBytes int64
}

// DefaultTerraformCommandLimits returns a detached copy of the source
// defaults. A function is used so callers cannot mutate a shared default.
func DefaultTerraformCommandLimits() TerraformCommandLimits {
	return TerraformCommandLimits{
		TimeoutMs:      nil,
		MaxStdoutBytes: 8 * 1024 * 1024,
		MaxStderrBytes: 1024 * 1024,
	}
}

// TerraformCommandOutput is the subprocess output policy.
type TerraformCommandOutput string

const (
	TerraformCommandOutputCapture       TerraformCommandOutput = "capture"
	TerraformCommandOutputDiscard       TerraformCommandOutput = "discard"
	TerraformCommandOutputInherit       TerraformCommandOutput = "inherit"
	TerraformCommandOutputInheritStderr TerraformCommandOutput = "inherit-stderr"
)

// TerraformCommandOptions describes one fixed, trusted, non-shell command.
// Environment is the complete child environment and is never merged with the
// host environment. A nil map is invalid; use an allocated empty map for an
// intentionally empty environment.
type TerraformCommandOptions struct {
	TerraformExecutable string
	Argv                []string
	CWD                 string
	Environment         map[string]string
	Limits              *TerraformCommandLimits
	Output              TerraformCommandOutput
}

// TerraformCommandResultKind identifies the successful output disposition.
type TerraformCommandResultKind string

const (
	TerraformCommandResultCaptured  TerraformCommandResultKind = "captured"
	TerraformCommandResultDiscarded TerraformCommandResultKind = "discarded"
	TerraformCommandResultInherited TerraformCommandResultKind = "inherited"
)

// TerraformCommandResult is returned only after the direct child is reaped and
// pipe-holding members of its isolated process group have been terminated.
type TerraformCommandResult struct {
	Kind   TerraformCommandResultKind
	Stdout []byte
}

func failure(code, message string, category procerr.Category) *procerr.ProcessFailure {
	return procerr.NewProcessFailure(procerr.NewProcessFailureOptions{
		Code:     code,
		Category: category,
		Message:  message,
	})
}

func domainFailure(code, message string) *procerr.ProcessFailure {
	return failure(code, message, procerr.CategoryDomain)
}

func ioFailure(code, message string) *procerr.ProcessFailure {
	return failure(code, message, procerr.CategoryIO)
}
