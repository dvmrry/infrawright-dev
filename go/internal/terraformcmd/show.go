package terraformcmd

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/dvmrry/infrawright-dev/go/internal/canonjson"
	"github.com/dvmrry/infrawright-dev/go/internal/procerr"
)

const (
	maxTerraformJSONStructureTokens  = 100_000
	maxTerraformJSONStringCharacters = 4 * 1024 * 1024
	maxTerraformJSONScalarCharacters = 1024 * 1024
)

var terraformShowContextNames = [...]string{
	"HOME",
	"TEMP",
	"TMP",
	"TMPDIR",
	"XDG_CONFIG_HOME",
	"XDG_DATA_HOME",
	"TERRAFORM_CONFIG",
	"TF_CLI_CONFIG_FILE",
	"TF_DATA_DIR",
	"TF_PLUGIN_CACHE_DIR",
}

// TerraformShowLimits bounds the complete show operation, including trusted
// file inspection, subprocess execution, UTF-8 decoding, preflight, and parse.
type TerraformShowLimits struct {
	TimeoutMs      int64
	MaxStdoutBytes int64
	MaxStderrBytes int64
}

// DefaultTerraformShowLimits returns a detached copy of the source defaults.
func DefaultTerraformShowLimits() TerraformShowLimits {
	return TerraformShowLimits{
		TimeoutMs:      120_000,
		MaxStdoutBytes: 8 * 1024 * 1024,
		MaxStderrBytes: 1024 * 1024,
	}
}

// TerraformShowOptions describes a private saved-plan render. A nil
// Environment selects OperationalTerraformShowEnvironment; an allocated empty
// map is a complete, intentionally empty child environment.
type TerraformShowOptions struct {
	TerraformExecutable string
	EnvDir              string
	SnapshotPath        string
	Environment         map[string]string
	Limits              *TerraformShowLimits
}

// OperationalTerraformShowEnvironment preserves only provider-installation
// context and adds the three deterministic Terraform settings. A nil input
// snapshots the current process environment.
func OperationalTerraformShowEnvironment(environment map[string]string) (map[string]string, error) {
	if environment == nil {
		environment = processEnvironment()
	}
	selected := make(map[string]string, len(terraformShowContextNames)+3)
	for _, name := range terraformShowContextNames {
		if value, ok := environment[name]; ok {
			selected[name] = value
		}
	}
	selected["CHECKPOINT_DISABLE"] = "1"
	selected["LANG"] = "C"
	selected["LC_ALL"] = "C"
	return snapshotShowEnvironment(selected)
}

func processEnvironment() map[string]string {
	result := make(map[string]string)
	for _, entry := range os.Environ() {
		key, value, ok := strings.Cut(entry, "=")
		if ok {
			result[key] = value
		}
	}
	return result
}

func snapshotShowLimits(value TerraformShowLimits) (TerraformShowLimits, error) {
	timeout := value.TimeoutMs
	limits, err := SnapshotTerraformCommandLimits(TerraformCommandLimits{
		TimeoutMs:      &timeout,
		MaxStdoutBytes: value.MaxStdoutBytes,
		MaxStderrBytes: value.MaxStderrBytes,
	})
	if err != nil || limits.TimeoutMs == nil {
		return TerraformShowLimits{}, domainFailure(
			"INVALID_TERRAFORM_SHOW_LIMIT",
			"Terraform show limits must be positive",
		)
	}
	return TerraformShowLimits{
		TimeoutMs:      *limits.TimeoutMs,
		MaxStdoutBytes: limits.MaxStdoutBytes,
		MaxStderrBytes: limits.MaxStderrBytes,
	}, nil
}

func snapshotShowEnvironment(value map[string]string) (map[string]string, error) {
	environment, err := SnapshotTerraformCommandEnvironment(value)
	if err != nil {
		return nil, domainFailure(
			"INVALID_TERRAFORM_SHOW_ENVIRONMENT",
			"Terraform show environment is not allowed",
		)
	}
	return environment, nil
}

// TerraformShowPlan renders a saved plan with the source-pinned fixed argv and
// parses its JSON losslessly. Shape validation (including complete === true)
// belongs to the downstream plan-contract layer and is intentionally absent.
func TerraformShowPlan(options TerraformShowOptions) (canonjson.Value, error) {
	if err := AssertSupportedTerraformExecutionPlatform(runtime.GOOS); err != nil {
		return nil, err
	}
	if strings.IndexByte(options.TerraformExecutable, 0) >= 0 ||
		strings.IndexByte(options.EnvDir, 0) >= 0 ||
		strings.IndexByte(options.SnapshotPath, 0) >= 0 ||
		!utf8.ValidString(options.TerraformExecutable) ||
		!utf8.ValidString(options.EnvDir) ||
		!utf8.ValidString(options.SnapshotPath) ||
		!filepath.IsAbs(options.TerraformExecutable) ||
		!filepath.IsAbs(options.EnvDir) ||
		!filepath.IsAbs(options.SnapshotPath) {
		return nil, domainFailure(
			"UNRESOLVED_TERRAFORM_SHOW_PATH",
			"Terraform show requires resolved absolute paths",
		)
	}

	limitsValue := DefaultTerraformShowLimits()
	if options.Limits != nil {
		limitsValue = *options.Limits
	}
	limits, err := snapshotShowLimits(limitsValue)
	if err != nil {
		return nil, err
	}
	var environment map[string]string
	if options.Environment == nil {
		environment, err = OperationalTerraformShowEnvironment(nil)
	} else {
		environment, err = snapshotShowEnvironment(options.Environment)
	}
	if err != nil {
		return nil, err
	}

	deadline := time.Now().UnixMilli() + limits.TimeoutMs
	if err := requireRegularTerraformInput(options.TerraformExecutable, "UNTRUSTED_TERRAFORM_EXECUTABLE", true); err != nil {
		return nil, err
	}
	if err := requireRegularTerraformInput(options.SnapshotPath, "INVALID_PLAN_SNAPSHOT", false); err != nil {
		return nil, err
	}
	if err := checkShowDeadline(deadline); err != nil {
		return nil, err
	}
	remainingTimeoutMs := deadline - time.Now().UnixMilli()
	if remainingTimeoutMs <= 0 {
		return nil, showDeadlineFailure()
	}

	result, err := RunTerraformCommand(TerraformCommandOptions{
		TerraformExecutable: options.TerraformExecutable,
		Argv: []string{
			"-chdir=" + options.EnvDir,
			"show",
			"-json",
			options.SnapshotPath,
		},
		CWD:         options.EnvDir,
		Environment: environment,
		Limits: &TerraformCommandLimits{
			TimeoutMs:      &remainingTimeoutMs,
			MaxStdoutBytes: limits.MaxStdoutBytes,
			MaxStderrBytes: limits.MaxStderrBytes,
		},
		Output: TerraformCommandOutputCapture,
	})
	if err != nil {
		return nil, mapTerraformCommandFailure(err)
	}
	stdout := result.Stdout
	if !utf8.Valid(stdout) {
		clearBytes(stdout)
		return nil, domainFailure(
			"INVALID_TERRAFORM_SHOW_UTF8",
			"Terraform show did not emit valid UTF-8 plan JSON",
		)
	}
	// TextDecoder(ignoreBOM: true) preserves a leading BOM instead of consuming
	// it. A direct byte-to-string conversion has that exact behavior.
	text := string(stdout)
	clearBytes(stdout)
	if err := checkShowDeadline(deadline); err != nil {
		return nil, err
	}
	if err := preflightTerraformJSON(text, deadline); err != nil {
		return nil, err
	}
	plan, err := canonjson.ParseDataJSONLosslessly(text)
	if err != nil {
		var decodeError *canonjson.PythonJSONDecodeError
		if errors.As(err, &decodeError) {
			return nil, domainFailure("INVALID_TERRAFORM_SHOW_JSON", decodeError.Error())
		}
		return nil, domainFailure(
			"INVALID_TERRAFORM_SHOW_JSON",
			"Terraform show did not emit valid plan JSON",
		)
	}
	if err := checkShowDeadline(deadline); err != nil {
		return nil, err
	}
	return plan, nil
}

func showDeadlineFailure() *procerr.ProcessFailure {
	return ioFailure(
		"TERRAFORM_SHOW_TIMEOUT",
		"Terraform show exceeded its execution deadline",
	)
}

func checkShowDeadline(deadline int64) error {
	if time.Now().UnixMilli() > deadline {
		return showDeadlineFailure()
	}
	return nil
}

func requireRegularTerraformInput(filePath, code string, executable bool) error {
	metadata, err := os.Lstat(filePath)
	if err != nil {
		return ioFailure(code, "unable to inspect trusted Terraform input")
	}
	if !metadata.Mode().IsRegular() || executable && metadata.Mode().Perm()&0o111 == 0 {
		return ioFailure(code, "trusted Terraform input is not an allowed regular file")
	}
	return nil
}

func mapTerraformCommandFailure(err error) error {
	var processFailure *procerr.ProcessFailure
	code := ""
	if errors.As(err, &processFailure) {
		code = processFailure.Code
	}
	switch code {
	case "UNSUPPORTED_TERRAFORM_EXECUTION_PLATFORM":
		return domainFailure(
			"UNSUPPORTED_TERRAFORM_EXECUTION_PLATFORM",
			UnsupportedTerraformExecutionPlatformMessage,
		)
	case "TERRAFORM_COMMAND_TIMEOUT":
		return showDeadlineFailure()
	case "TERRAFORM_COMMAND_STDOUT_LIMIT":
		return ioFailure("TERRAFORM_SHOW_STDOUT_LIMIT", "Terraform show exceeded its output limit")
	case "TERRAFORM_COMMAND_STDERR_LIMIT":
		return ioFailure("TERRAFORM_SHOW_STDERR_LIMIT", "Terraform show exceeded its diagnostic-output limit")
	case "TERRAFORM_COMMAND_STDOUT_FAILED":
		return ioFailure("TERRAFORM_SHOW_STDOUT_FAILED", "unable to read Terraform show output")
	case "TERRAFORM_COMMAND_STDERR_FAILED":
		return ioFailure("TERRAFORM_SHOW_STDERR_FAILED", "unable to read Terraform show diagnostic output")
	case "UNTRUSTED_TERRAFORM_EXECUTABLE":
		return ioFailure("UNTRUSTED_TERRAFORM_EXECUTABLE", "trusted Terraform input is not an allowed regular file")
	case "UNRESOLVED_TERRAFORM_COMMAND_PATH":
		return domainFailure("UNRESOLVED_TERRAFORM_SHOW_PATH", "Terraform show requires resolved absolute paths")
	case "INVALID_TERRAFORM_COMMAND_LIMIT":
		return domainFailure("INVALID_TERRAFORM_SHOW_LIMIT", "Terraform show limits must be positive")
	case "INVALID_TERRAFORM_COMMAND_ENVIRONMENT":
		return domainFailure("INVALID_TERRAFORM_SHOW_ENVIRONMENT", "Terraform show environment is not allowed")
	case "TERRAFORM_COMMAND_FAILED":
		return domainFailure("TERRAFORM_SHOW_FAILED", "Terraform could not render the saved plan")
	default:
		return ioFailure("TERRAFORM_SHOW_SPAWN_FAILED", "unable to start Terraform show")
	}
}

func preflightTerraformJSON(text string, deadline int64) error {
	escaped := false
	inString := false
	scalarCharacters := 0
	stringCharacters := 0
	structureTokens := 0
	unitIndex := 0
	consume := func(character uint16) error {
		index := unitIndex
		unitIndex++
		if index&0xfff == 0 {
			if err := checkShowDeadline(deadline); err != nil {
				return err
			}
		}
		if inString {
			stringCharacters++
			if stringCharacters > maxTerraformJSONStringCharacters {
				return domainFailure(
					"TERRAFORM_SHOW_COMPLEXITY_LIMIT",
					"Terraform show JSON exceeds its string-content limit",
				)
			}
			if escaped {
				escaped = false
			} else if character == '\\' {
				escaped = true
			} else if character == '"' {
				inString = false
			}
			return nil
		}
		if character == '"' {
			inString = true
			scalarCharacters = 0
			return nil
		}
		if isTerraformJSONStructure(character) {
			structureTokens++
			scalarCharacters = 0
			if structureTokens > maxTerraformJSONStructureTokens {
				return domainFailure(
					"TERRAFORM_SHOW_COMPLEXITY_LIMIT",
					"Terraform show JSON exceeds its structural limit",
				)
			}
			return nil
		}
		if isJavaScriptWhitespace(character) {
			scalarCharacters = 0
			return nil
		}
		scalarCharacters++
		if scalarCharacters > maxTerraformJSONScalarCharacters {
			return domainFailure(
				"TERRAFORM_SHOW_COMPLEXITY_LIMIT",
				"Terraform show JSON exceeds its scalar-token limit",
			)
		}
		return nil
	}
	for _, character := range text {
		if character <= 0xffff {
			if err := consume(uint16(character)); err != nil {
				return err
			}
			continue
		}
		// JavaScript indexes non-BMP characters as their two UTF-16 surrogate
		// code units. Generate those units in place instead of allocating a
		// whole []rune plus []uint16 copy before the first deadline check.
		value := uint32(character) - 0x10000
		if err := consume(uint16(0xd800 + value>>10)); err != nil {
			return err
		}
		if err := consume(uint16(0xdc00 + value&0x3ff)); err != nil {
			return err
		}
	}
	return checkShowDeadline(deadline)
}

func isTerraformJSONStructure(character uint16) bool {
	return character == '{' || character == '}' || character == '[' ||
		character == ']' || character == ',' || character == ':'
}

func isJavaScriptWhitespace(character uint16) bool {
	switch character {
	case 0x0009, 0x000a, 0x000b, 0x000c, 0x000d, 0x0020, 0x00a0,
		0x1680, 0x2028, 0x2029, 0x202f, 0x205f, 0x3000, 0xfeff:
		return true
	default:
		return character >= 0x2000 && character <= 0x200a
	}
}
