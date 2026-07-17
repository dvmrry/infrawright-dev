package modulesgen

// formatter.go ports the HclFormatter function-type alias and
// terraformHclFormatter from node-src/modules/generator.ts as an injected
// seam: HclFormatter is a Go interface (rather than a bare func type)
// specifically so GenerateModule/GenerateActiveModules never depend on a
// concrete `terraform` invocation directly -- TerraformFormatter below is
// the real implementation (shells out to `<executable> fmt -` exactly as
// the TS source's terraformHclFormatter does), and tests substitute
// IdentityFormatter or a bespoke FormatterFunc fake, mirroring how
// node-tests/module-generator.test.ts defines its own
// `IDENTITY_FORMATTER: HclFormatter = async (source) => source` fake and
// how the TS source itself accepts an injected `options?.executable`
// terraform path rather than hardcoding one.

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
)

// HclFormatter formats generated Terraform source (.tf / .tftest.hcl
// content) before GenerateModule writes it to disk. Ports the HclFormatter
// type from node-src/modules/generator.ts.
type HclFormatter interface {
	FormatHCL(source string) (string, error)
}

// FormatterFunc adapts a plain function to the HclFormatter interface, the
// Go analogue of the TS type alias `type HclFormatter = (source: string) =>
// Promise<string>` letting any function of the right shape stand in for
// the real formatter.
type FormatterFunc func(source string) (string, error)

// FormatHCL implements HclFormatter.
func (f FormatterFunc) FormatHCL(source string) (string, error) { return f(source) }

// IdentityFormatter returns source unchanged. Ports the Node test suite's
// `IDENTITY_FORMATTER` fake from node-tests/module-generator.test.ts, used
// by every test in this package that is not specifically exercising
// Terraform formatting.
var IdentityFormatter HclFormatter = FormatterFunc(func(source string) (string, error) {
	return source, nil
})

// TerraformFormatterOptions mirrors the options bag terraformHclFormatter
// accepts in node-src/modules/generator.ts (`options?.executable`,
// `options?.environment`).
type TerraformFormatterOptions struct {
	// Executable is the terraform binary path or name. Empty means: fall
	// back to the "TF" environment variable, then to "terraform" --
	// ported from `options?.executable || environment.TF || "terraform"`.
	Executable string
	// Environment is the child process environment. Nil means: inherit
	// this process's own environment (ported from `options?.environment
	// ?? process.env`); a non-nil map (including an empty one) replaces
	// the child's environment entirely, exactly as passing an explicit
	// `env` to Node's child_process.spawn does not merge with
	// process.env.
	Environment map[string]string
}

// terraformFormatter is the real HclFormatter implementation: it shells
// out to `<executable> fmt -`, feeding source on stdin and returning
// stdout, ported from terraformHclFormatter's returned closure in
// node-src/modules/generator.ts.
type terraformFormatter struct {
	executable  string
	environment map[string]string
}

// NewTerraformFormatter ports terraformHclFormatter from
// node-src/modules/generator.ts.
func NewTerraformFormatter(options TerraformFormatterOptions) HclFormatter {
	lookup := func(key string) string {
		if options.Environment != nil {
			return options.Environment[key]
		}
		return os.Getenv(key)
	}
	executable := options.Executable
	if executable == "" {
		executable = lookup("TF")
	}
	if executable == "" {
		executable = "terraform"
	}
	return &terraformFormatter{executable: executable, environment: options.Environment}
}

// FormatHCL implements HclFormatter, porting the async closure
// terraformHclFormatter returns: spawn `<executable> fmt -`, write source
// to stdin, and resolve with stdout on a zero exit code or reject with an
// error carrying the exit code and trimmed stderr otherwise. A failure to
// even start the child process (e.g. the executable does not exist) ports
// the Node source's `child.once("error", reject)` path.
func (f *terraformFormatter) FormatHCL(source string) (string, error) {
	cmd := exec.Command(f.executable, "fmt", "-")
	if f.environment != nil {
		env := make([]string, 0, len(f.environment))
		for key, value := range f.environment {
			env = append(env, key+"="+value)
		}
		cmd.Env = env
	}
	cmd.Stdin = strings.NewReader(source)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err := cmd.Run()
	if err == nil {
		return stdout.String(), nil
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		detail := strings.TrimSpace(stderr.String())
		message := fmt.Sprintf("%s fmt failed with exit %d", f.executable, exitErr.ExitCode())
		if detail != "" {
			message += ": " + detail
		}
		return "", errors.New(message)
	}
	// The child never started (e.g. ENOENT for a missing executable) --
	// return the raw *exec.Error, the closest Go analogue of the Node
	// source's unmodified spawn "error" event.
	return "", err
}
