package main

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

const cliReferenceUpdateCommand = "UPDATE_CLI_DOCS=1 go test ./cmd/iw -run '^TestCLIReferenceCurrent$'"

func TestCLIReferenceCurrent(t *testing.T) {
	generated, err := renderCLIReference(newCobraRoot())
	if err != nil {
		t.Fatalf("render CLI reference: %v", err)
	}
	destination := filepath.Join(repoRoot(t), "docs", "cli-reference.md")
	if os.Getenv("UPDATE_CLI_DOCS") == "1" {
		if err := os.WriteFile(destination, generated, 0o644); err != nil {
			t.Fatalf("write %s: %v", destination, err)
		}
		return
	}
	want, err := os.ReadFile(destination)
	if err != nil {
		t.Fatalf("read generated CLI reference %s: %v (update with %q)", destination, err, cliReferenceUpdateCommand)
	}
	if !bytes.Equal(generated, want) {
		t.Fatalf("CLI reference is stale; update it with %q", cliReferenceUpdateCommand)
	}
}

func renderCLIReference(root *cobra.Command) ([]byte, error) {
	var output strings.Builder
	output.WriteString("# Infrawright CLI reference\n\n")
	output.WriteString("<!-- Code generated from the Cobra command tree. DO NOT EDIT. -->\n\n")
	output.WriteString("Regenerate with `")
	output.WriteString(cliReferenceUpdateCommand)
	output.WriteString("` from `go/`.\n")

	for _, command := range documentedCobraCommands(root) {
		var help bytes.Buffer
		command.InitDefaultHelpFlag()
		command.SetOut(&help)
		command.SetErr(&help)
		if err := command.Help(); err != nil {
			return nil, fmt.Errorf("render %s help: %w", command.CommandPath(), err)
		}
		output.WriteString("\n## `")
		output.WriteString(command.CommandPath())
		output.WriteString("`\n\n```text\n")
		output.WriteString(help.String())
		if help.Len() == 0 || help.Bytes()[help.Len()-1] != '\n' {
			output.WriteByte('\n')
		}
		output.WriteString("```\n")
	}
	return []byte(output.String()), nil
}

func documentedCobraCommands(root *cobra.Command) []*cobra.Command {
	root.InitDefaultHelpCmd()
	root.InitDefaultCompletionCmd()
	var commands []*cobra.Command
	var visit func(*cobra.Command)
	visit = func(command *cobra.Command) {
		if command.Hidden || command.Name() == "help" {
			return
		}
		commands = append(commands, command)
		children := append([]*cobra.Command(nil), command.Commands()...)
		sort.Slice(children, func(i, j int) bool { return children[i].Name() < children[j].Name() })
		for _, child := range children {
			visit(child)
		}
	}
	visit(root)
	return commands
}
