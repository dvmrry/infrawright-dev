package adopt

import (
	"errors"
	"fmt"

	"github.com/dvmrry/infrawright-dev/go/internal/procerr"
	"github.com/dvmrry/infrawright-dev/go/internal/terraformcmd"
)

// BoundedOracleRunner executes each exact oracle request through the existing
// bounded, non-shell Terraform process boundary. That boundary supplies the
// trusted absolute-executable check, platform gate, isolated process group,
// complete explicit child environment, timeout, and stdout/stderr caps used by
// the rest of the Go runtime.
type BoundedOracleRunner struct {
	TerraformExecutable string
}

// CreateOracleCommandRunner ports createOracleCommandRunner from
// node-src/domain/import-oracle.ts. It deliberately reuses terraformcmd rather
// than adding a second Terraform process implementation.
func CreateOracleCommandRunner(terraformExecutable string) *BoundedOracleRunner {
	return &BoundedOracleRunner{TerraformExecutable: terraformExecutable}
}

func oracleCommandFailure(debugName string, cause error) error {
	code := "TERRAFORM_COMMAND_FAILED"
	var failure *procerr.ProcessFailure
	if errors.As(cause, &failure) && failure.Code == "UNSUPPORTED_TERRAFORM_EXECUTION_PLATFORM" {
		return failure
	}
	if errors.As(cause, &failure) && failure.Code != "" {
		code = failure.Code
	}
	return procerr.NewProcessFailure(procerr.NewProcessFailureOptions{
		Code:     code,
		Category: procerr.CategoryDomain,
		Message:  fmt.Sprintf("terraform %s failed; provider diagnostics and import IDs were redacted", debugName),
	})
}

// Run implements OracleCommandRunner. Argv and output disposition are passed
// through exactly; no library reconstructs, reorders, or silently adds flags.
func (r *BoundedOracleRunner) Run(request OracleCommandRequest) (OracleCommandResult, error) {
	timeoutMS, err := OracleTimeoutMS(request.Environment)
	if err != nil {
		return OracleCommandResult{}, err
	}
	timeout := int64(timeoutMS)
	limits := terraformcmd.TerraformCommandLimits{
		TimeoutMs:      &timeout,
		MaxStdoutBytes: 8 * 1024 * 1024,
		MaxStderrBytes: 1024 * 1024,
	}
	output := terraformcmd.TerraformCommandOutputDiscard
	if request.CaptureOutput {
		output = terraformcmd.TerraformCommandOutputCapture
	}
	result, err := terraformcmd.RunTerraformCommand(terraformcmd.TerraformCommandOptions{
		TerraformExecutable: r.TerraformExecutable,
		Argv:                append([]string(nil), request.Argv...),
		CWD:                 request.CWD,
		Environment:         cloneStringMap(request.Environment),
		Limits:              &limits,
		Output:              output,
	})
	if err != nil {
		return OracleCommandResult{}, oracleCommandFailure(request.DebugName, err)
	}
	return OracleCommandResult{Stdout: append([]byte(nil), result.Stdout...)}, nil
}

func cloneStringMap(input map[string]string) map[string]string {
	output := make(map[string]string, len(input))
	for key, value := range input {
		output[key] = value
	}
	return output
}
