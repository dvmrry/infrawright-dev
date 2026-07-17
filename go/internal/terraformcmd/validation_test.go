package terraformcmd

import (
	"strings"
	"testing"
	"time"
)

// TestCommandSnapshots pins the synchronous validation boundary in
// node-src/io/terraform-command.ts at
// f3a86f2d24dddd4ebf95362d55718a81137800f2:186-220,312-417.
func TestCommandSnapshots(t *testing.T) {
	timeout := int64(25)
	limits, err := SnapshotTerraformCommandLimits(TerraformCommandLimits{
		TimeoutMs:      &timeout,
		MaxStdoutBytes: 10,
		MaxStderrBytes: 11,
	})
	if err != nil {
		t.Fatal(err)
	}
	timeout = 99
	if limits.TimeoutMs == nil || *limits.TimeoutMs != 25 {
		t.Errorf("snapshotted timeout = %v, want 25", limits.TimeoutMs)
	}

	environment := map[string]string{"ORIGINAL": "value"}
	snapshot, err := SnapshotTerraformCommandEnvironment(environment)
	if err != nil {
		t.Fatal(err)
	}
	environment["ORIGINAL"] = "mutated"
	if snapshot["ORIGINAL"] != "value" {
		t.Errorf("snapshot ORIGINAL = %q, want value", snapshot["ORIGINAL"])
	}
}

func TestCommandValidationBounds(t *testing.T) {
	tests := []struct {
		name string
		run  func() error
		code string
	}{
		{
			name: "nil argv",
			run: func() error {
				_, err := snapshotArgv(nil)
				return err
			},
			code: "INVALID_TERRAFORM_COMMAND_ARGUMENTS",
		},
		{
			name: "nul argument",
			run: func() error {
				_, err := snapshotArgv([]string{"plan\x00secret"})
				return err
			},
			code: "INVALID_TERRAFORM_COMMAND_ARGUMENTS",
		},
		{
			name: "malformed utf8 argument",
			run: func() error {
				_, err := snapshotArgv([]string{"plan\xff"})
				return err
			},
			code: "INVALID_TERRAFORM_COMMAND_ARGUMENTS",
		},
		{
			name: "argument bytes",
			run: func() error {
				_, err := snapshotArgv([]string{strings.Repeat("x", int(maxTerraformCommandArgumentBytes)+1)})
				return err
			},
			code: "INVALID_TERRAFORM_COMMAND_ARGUMENTS",
		},
		{
			name: "nil environment",
			run: func() error {
				_, err := SnapshotTerraformCommandEnvironment(nil)
				return err
			},
			code: "INVALID_TERRAFORM_COMMAND_ENVIRONMENT",
		},
		{
			name: "environment key",
			run: func() error {
				_, err := SnapshotTerraformCommandEnvironment(map[string]string{"BAD=KEY": "x"})
				return err
			},
			code: "INVALID_TERRAFORM_COMMAND_ENVIRONMENT",
		},
		{
			name: "environment nul value",
			run: func() error {
				_, err := SnapshotTerraformCommandEnvironment(map[string]string{"TOKEN": "secret\x00suffix"})
				return err
			},
			code: "INVALID_TERRAFORM_COMMAND_ENVIRONMENT",
		},
		{
			name: "malformed utf8 environment",
			run: func() error {
				_, err := SnapshotTerraformCommandEnvironment(map[string]string{"TOKEN": "secret\xff"})
				return err
			},
			code: "INVALID_TERRAFORM_COMMAND_ENVIRONMENT",
		},
		{
			name: "zero timeout",
			run: func() error {
				zero := int64(0)
				_, err := SnapshotTerraformCommandLimits(TerraformCommandLimits{TimeoutMs: &zero, MaxStdoutBytes: 1, MaxStderrBytes: 1})
				return err
			},
			code: "INVALID_TERRAFORM_COMMAND_LIMIT",
		},
		{
			name: "unsafe timeout",
			run: func() error {
				tooLarge := maximumJavaScriptSafeInteger + 1
				_, err := SnapshotTerraformCommandLimits(TerraformCommandLimits{TimeoutMs: &tooLarge, MaxStdoutBytes: 1, MaxStderrBytes: 1})
				return err
			},
			code: "INVALID_TERRAFORM_COMMAND_LIMIT",
		},
		{
			name: "stdout hard maximum",
			run: func() error {
				_, err := SnapshotTerraformCommandLimits(TerraformCommandLimits{MaxStdoutBytes: maxTerraformCommandStdoutBytes + 1, MaxStderrBytes: 1})
				return err
			},
			code: "INVALID_TERRAFORM_COMMAND_LIMIT",
		},
		{
			name: "stderr hard maximum",
			run: func() error {
				_, err := SnapshotTerraformCommandLimits(TerraformCommandLimits{MaxStdoutBytes: 1, MaxStderrBytes: maxTerraformCommandStderrBytes + 1})
				return err
			},
			code: "INVALID_TERRAFORM_COMMAND_LIMIT",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			requireProcessFailure(t, test.run(), test.code)
		})
	}
}

func TestCommandValidationExactBoundaries(t *testing.T) {
	arguments := make([]string, maxTerraformCommandArguments)
	for index := range arguments {
		arguments[index] = ""
	}
	if _, err := snapshotArgv(arguments); err != nil {
		t.Fatalf("128 arguments: %v", err)
	}
	if _, err := snapshotArgv(append(arguments, "overflow")); err == nil {
		t.Fatal("129 arguments unexpectedly accepted")
	}
	if _, err := snapshotArgv([]string{strings.Repeat("é", int(maxTerraformCommandArgumentBytes/2))}); err != nil {
		t.Fatalf("exact argument byte limit: %v", err)
	}

	environment := make(map[string]string, maxTerraformEnvironmentEntries)
	for index := 0; index < maxTerraformEnvironmentEntries; index++ {
		environment["K"+strings.Repeat("x", index)] = ""
	}
	if _, err := SnapshotTerraformCommandEnvironment(environment); err != nil {
		t.Fatalf("256 environment entries: %v", err)
	}
	environment["overflow"] = ""
	if _, err := SnapshotTerraformCommandEnvironment(environment); err == nil {
		t.Fatal("257 environment entries unexpectedly accepted")
	}
	if _, err := SnapshotTerraformCommandEnvironment(map[string]string{
		"K": strings.Repeat("x", int(maxTerraformEnvironmentBytes)-1),
	}); err != nil {
		t.Fatalf("exact environment byte limit: %v", err)
	}

	if _, err := SnapshotTerraformCommandLimits(TerraformCommandLimits{
		MaxStdoutBytes: maxTerraformCommandStdoutBytes,
		MaxStderrBytes: maxTerraformCommandStderrBytes,
	}); err != nil {
		t.Fatalf("exact stream hard limits: %v", err)
	}
	if _, err := snapshotArgv([]string{}); err != nil {
		t.Fatalf("allocated empty argv: %v", err)
	}
	if _, err := SnapshotTerraformCommandEnvironment(map[string]string{}); err != nil {
		t.Fatalf("allocated empty environment: %v", err)
	}
}

func TestMaximumSafeTimeoutDoesNotOverflowTimer(t *testing.T) {
	timeout := maximumJavaScriptSafeInteger
	duration := commandTimerDuration(time.Now(), &timeout)
	if duration <= 0 || duration > time.Duration(maximumTimerChunkMs)*time.Millisecond {
		t.Errorf("timer duration = %v, want positive capped duration", duration)
	}
}
