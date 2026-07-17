package terraformcmd

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
)

// TestTerraformExecutableCandidates pins node-src/io/terraform-command.ts at
// f3a86f2d24dddd4ebf95362d55718a81137800f2:241-286.
func TestTerraformExecutableCandidates(t *testing.T) {
	tests := []struct {
		name        string
		selected    string
		environment map[string]string
		options     TerraformExecutableCandidateOptions
		want        []string
	}{
		{
			name:     "windows absolute backslashes",
			selected: `C:\tools\terraform.exe`,
			options:  TerraformExecutableCandidateOptions{CWD: `D:\work`, Platform: "win32"},
			want:     []string{`C:\tools\terraform.exe`},
		},
		{
			name:     "windows absolute forward slashes",
			selected: `C:/tools/terraform.exe`,
			options:  TerraformExecutableCandidateOptions{CWD: `D:\work`, Platform: "win32"},
			want:     []string{`C:\tools\terraform.exe`},
		},
		{
			name:     "windows relative explicit",
			selected: `..\bin\terraform.exe`,
			options:  TerraformExecutableCandidateOptions{CWD: `C:\work\repo`, Platform: "win32"},
			want:     []string{`C:\work\bin\terraform.exe`},
		},
		{
			name:     "windows root relative keeps cwd drive",
			selected: `\root\terraform.exe`,
			options:  TerraformExecutableCandidateOptions{CWD: `C:\work\repo`, Platform: "win32"},
			want:     []string{`C:\root\terraform.exe`},
		},
		{
			name:     "windows slash root relative keeps cwd drive",
			selected: `/root/terraform.exe`,
			options:  TerraformExecutableCandidateOptions{CWD: `C:\work\repo`, Platform: "win32"},
			want:     []string{`C:\root\terraform.exe`},
		},
		{
			name:     "windows unc",
			selected: `\\server\share\tools\terraform.exe`,
			options:  TerraformExecutableCandidateOptions{CWD: `C:\work`, Platform: "win32"},
			want:     []string{`\\server\share\tools\terraform.exe`},
		},
		{
			name:     "windows root relative keeps unc cwd device",
			selected: `\root\terraform.exe`,
			options:  TerraformExecutableCandidateOptions{CWD: `\\server\share\cwd`, Platform: "win32"},
			want:     []string{`\\server\share\root\terraform.exe`},
		},
		{
			name:     "windows unc root retains trailing separator",
			selected: `\\server\share`,
			options:  TerraformExecutableCandidateOptions{CWD: `C:\work`, Platform: "win32"},
			want:     []string{`\\server\share\`},
		},
		{
			name:     "windows repeated unc separators normalize",
			selected: `\\server\\\share\\tools\terraform.exe`,
			options:  TerraformExecutableCandidateOptions{CWD: `C:\work`, Platform: "win32"},
			want:     []string{`\\server\share\tools\terraform.exe`},
		},
		{
			name:     "incomplete unc is root relative",
			selected: `\\server`,
			options:  TerraformExecutableCandidateOptions{CWD: `C:\work`, Platform: "win32"},
			want:     []string{`C:\server`},
		},
		{
			name:     "windows absolute remains lexical on posix",
			selected: `C:\tools\terraform.exe`,
			options:  TerraformExecutableCandidateOptions{CWD: "/work", Platform: "linux"},
			want:     []string{`C:\tools\terraform.exe`},
		},
		{
			name:     "slash-rooted redundant separators remain lexical on posix",
			selected: "/trusted//bin/terraform",
			options:  TerraformExecutableCandidateOptions{CWD: "/work", Platform: "linux"},
			want:     []string{"/trusted//bin/terraform"},
		},
		{
			name:     "slash-rooted parent segment remains lexical on posix",
			selected: "/trusted/bin/../terraform",
			options:  TerraformExecutableCandidateOptions{CWD: "/work", Platform: "linux"},
			want:     []string{"/trusted/bin/../terraform"},
		},
		{
			name:     "posix explicit",
			selected: "./bin/terraform",
			options:  TerraformExecutableCandidateOptions{CWD: "/work/repo", Platform: "linux"},
			want:     []string{"/work/repo/bin/terraform"},
		},
		{
			name:        "posix path",
			selected:    "terraform",
			environment: map[string]string{"PATH": "/first:/second"},
			options:     TerraformExecutableCandidateOptions{CWD: "/work", Platform: "linux"},
			want:        []string{"/first/terraform", "/second/terraform"},
		},
		{
			name:        "posix empty components preserve cwd and duplicates",
			selected:    "terraform",
			environment: map[string]string{"PATH": "::/usr/bin::"},
			options:     TerraformExecutableCandidateOptions{CWD: "/work", Platform: "linux"},
			want: []string{
				"/work/terraform",
				"/work/terraform",
				"/usr/bin/terraform",
				"/work/terraform",
				"/work/terraform",
			},
		},
		{
			name:        "posix backslash is a literal name",
			selected:    `terraform\literal`,
			environment: map[string]string{"PATH": "/first"},
			options:     TerraformExecutableCandidateOptions{CWD: "/work", Platform: "linux"},
			want:        []string{`/first/terraform\literal`},
		},
		{
			name:        "windows pathext",
			selected:    "terraform",
			environment: map[string]string{"PATH": `C:\first;D:\second`, "PATHEXT": ".EXE;.CMD"},
			options:     TerraformExecutableCandidateOptions{CWD: `C:\work`, Platform: "win32"},
			want: []string{
				`C:\first\terraform.exe`,
				`C:\first\terraform.cmd`,
				`D:\second\terraform.exe`,
				`D:\second\terraform.cmd`,
			},
		},
		{
			name:        "windows pathext uses ECMAScript Unicode lowercasing",
			selected:    "terraform",
			environment: map[string]string{"PATH": `C:\tools`, "PATHEXT": ".İXE"},
			options:     TerraformExecutableCandidateOptions{CWD: `C:\work`, Platform: "win32"},
			want:        []string{"C:\\tools\\terraform.i\u0307xe"},
		},
		{
			name:        "windows drops empty path components",
			selected:    "terraform",
			environment: map[string]string{"PATH": `;;C:\tools;;`, "PATHEXT": ".EXE"},
			options:     TerraformExecutableCandidateOptions{CWD: `D:\work`, Platform: "win32"},
			want:        []string{`C:\tools\terraform.exe`},
		},
		{
			name:        "path key precedence",
			selected:    "terraform",
			environment: map[string]string{"PATH": "/upper", "Path": "/title", "path": "/lower"},
			options:     TerraformExecutableCandidateOptions{CWD: "/work", Platform: "linux"},
			want:        []string{"/upper/terraform"},
		},
		{
			name:        "empty uppercase path still wins",
			selected:    "terraform",
			environment: map[string]string{"PATH": "", "Path": "/ignored"},
			options:     TerraformExecutableCandidateOptions{CWD: "/work", Platform: "linux"},
			want:        []string{"/work/terraform"},
		},
		{
			name:        "windows default pathext",
			selected:    "terraform",
			environment: map[string]string{"PATH": `C:\tools`},
			options:     TerraformExecutableCandidateOptions{CWD: `C:\work`, Platform: "win32"},
			want: []string{
				`C:\tools\terraform.com`,
				`C:\tools\terraform.exe`,
				`C:\tools\terraform.bat`,
				`C:\tools\terraform.cmd`,
			},
		},
		{
			name:        "windows empty pathext",
			selected:    "terraform",
			environment: map[string]string{"PATH": `C:\tools`, "PATHEXT": ""},
			options:     TerraformExecutableCandidateOptions{CWD: `C:\work`, Platform: "win32"},
			want:        []string{},
		},
		{
			name:        "windows existing extension suppresses pathext",
			selected:    "terraform.BIN",
			environment: map[string]string{"PATH": `C:\tools`, "PATHEXT": ".EXE"},
			options:     TerraformExecutableCandidateOptions{CWD: `C:\work`, Platform: "win32"},
			want:        []string{`C:\tools\terraform.BIN`},
		},
		{
			name:        "windows dot dot has no extension",
			selected:    "..",
			environment: map[string]string{"PATH": `C:\tools`, "PATHEXT": ".EXE"},
			options:     TerraformExecutableCandidateOptions{CWD: `C:\work`, Platform: "win32"},
			want:        []string{`C:\tools\...exe`},
		},
		{
			name:     "missing path variable",
			selected: "terraform",
			options:  TerraformExecutableCandidateOptions{CWD: "/work", Platform: "linux"},
			want:     []string{},
		},
	}
	processCWD, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	tests = append(tests, struct {
		name        string
		selected    string
		environment map[string]string
		options     TerraformExecutableCandidateOptions
		want        []string
	}{
		name:        "nonempty relative path entry uses process cwd",
		selected:    "terraform",
		environment: map[string]string{"PATH": "relative-bin"},
		options:     TerraformExecutableCandidateOptions{CWD: "/different", Platform: "linux"},
		want:        []string{filepath.Join(processCWD, "relative-bin", "terraform")},
	})
	tests = append(tests, struct {
		name        string
		selected    string
		environment map[string]string
		options     TerraformExecutableCandidateOptions
		want        []string
	}{
		name:        "windows drive relative name does not graft another drive",
		selected:    "D:terraform",
		environment: map[string]string{"PATH": `C:\tools`, "PATHEXT": ".EXE"},
		options:     TerraformExecutableCandidateOptions{CWD: `C:\work`, Platform: "win32"},
		want: []string{
			"D:" + strings.ReplaceAll(filepath.Join(processCWD, "terraform.exe"), "/", `\`),
		},
	})

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := TerraformExecutableCandidates(test.selected, test.environment, &test.options)
			if err != nil {
				t.Fatalf("TerraformExecutableCandidates: %v", err)
			}
			if !reflect.DeepEqual(got, test.want) {
				t.Errorf("candidates = %#v, want %#v", got, test.want)
			}
		})
	}
}

// TestResolveWindowsPathNodeOracle pins node:path.win32.resolve outputs from
// the Node 24 implementation used to run the source authority.
func TestResolveWindowsPathNodeOracle(t *testing.T) {
	tests := []struct {
		name  string
		parts []string
		want  string
	}{
		{name: "absolute drive", parts: []string{`C:\work`, `C:\abs\terraform.exe`}, want: `C:\abs\terraform.exe`},
		{name: "mismatched drive", parts: []string{`D:\work`, `C:\abs\terraform.exe`}, want: `C:\abs\terraform.exe`},
		{name: "drive root relative", parts: []string{`C:\work\repo`, `\root\terraform.exe`}, want: `C:\root\terraform.exe`},
		{name: "unc root relative", parts: []string{`\\server\share\cwd`, `\root\terraform.exe`}, want: `\\server\share\root\terraform.exe`},
		{name: "unc root trailing separator", parts: []string{`C:\work`, `\\server\share`}, want: `\\server\share\`},
		{name: "incomplete unc", parts: []string{`C:\work`, `\\server`}, want: `C:\server`},
		{name: "repeated unc separators", parts: []string{`C:\work`, `\\server\\\share\\tools\..\terraform.exe`}, want: `\\server\share\terraform.exe`},
		{name: "relative parent", parts: []string{`C:\work\repo`, `..\bin\terraform.exe`}, want: `C:\work\bin\terraform.exe`},
		{name: "forward root relative", parts: []string{`C:\work`, `/root/terraform.exe`}, want: `C:\root\terraform.exe`},
		{name: "dot", parts: []string{`C:\work`, `.`}, want: `C:\work`},
		{name: "empty", parts: []string{`C:\work`, ``}, want: `C:\work`},
	}
	unexpectedCWD := func() (string, error) {
		return "", errors.New("unexpected process cwd read")
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := resolveWindowsPath(unexpectedCWD, test.parts...)
			if err != nil {
				t.Fatalf("resolveWindowsPath(%q): %v", test.parts, err)
			}
			if got != test.want {
				t.Errorf("resolveWindowsPath(%q) = %q, want %q", test.parts, got, test.want)
			}
		})
	}
}

func TestWindowsExtNodeOracle(t *testing.T) {
	tests := map[string]string{
		"..":      "",
		"...":     ".",
		".":       "",
		".env":    "",
		"a.":      ".",
		"a..":     ".",
		"foo.txt": ".txt",
	}
	for input, want := range tests {
		if got := windowsExt(input); got != want {
			t.Errorf("windowsExt(%q) = %q, want %q", input, got, want)
		}
	}
}

func TestTerraformExecutableCandidatesHostPlatformSpelling(t *testing.T) {
	platform := runtime.GOOS
	if platform == "windows" {
		platform = "win32"
	}
	_, err := TerraformExecutableCandidates("terraform", map[string]string{}, &TerraformExecutableCandidateOptions{Platform: platform})
	if err != nil {
		t.Fatal(err)
	}
}

func TestTerraformExecutableCandidatesRejectsNUL(t *testing.T) {
	_, err := TerraformExecutableCandidates("terraform\x00secret", nil, nil)
	failure := requireProcessFailure(t, err, "UNRESOLVED_TERRAFORM_COMMAND_PATH")
	if failure.Message != "Terraform executable path contains an embedded null character" {
		t.Errorf("message = %q", failure.Message)
	}
}

func TestTerraformExecutableCandidatesRejectsMalformedUTF8(t *testing.T) {
	_, err := TerraformExecutableCandidates("terraform\xff", nil, nil)
	requireProcessFailure(t, err, "UNRESOLVED_TERRAFORM_COMMAND_PATH")
	_, err = TerraformExecutableCandidates("terraform", map[string]string{"PATH": "/bin\xff"}, &TerraformExecutableCandidateOptions{CWD: "/work", Platform: "linux"})
	requireProcessFailure(t, err, "UNRESOLVED_TERRAFORM_COMMAND_PATH")
}

func TestResolveTerraformExecutable(t *testing.T) {
	requirePOSIX(t)
	root := t.TempDir()
	target := filepath.Join(root, "terraform-from-path")
	if err := os.WriteFile(target, []byte("#!/bin/sh\nexit 0\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(root, "terraform-link")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	resolved, err := ResolveTerraformExecutable("terraform-link", map[string]string{"PATH": root})
	if err != nil {
		t.Fatalf("ResolveTerraformExecutable: %v", err)
	}
	realTarget, err := filepath.EvalSymlinks(target)
	if err != nil {
		t.Fatal(err)
	}
	if resolved != realTarget {
		t.Errorf("resolved = %q, want %q", resolved, realTarget)
	}
}

func TestResolveTerraformExecutableMissingUsesPythonError(t *testing.T) {
	_, err := ResolveTerraformExecutable(`missing-team's\terraform`, map[string]string{})
	if err == nil {
		t.Fatal("error = nil")
	}
	want := `[Errno 2] No such file or directory: 'missing-team\'s\\terraform'`
	if err.Error() != want {
		t.Errorf("error = %q, want %q", err.Error(), want)
	}
	if !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("errors.Is(err, fs.ErrNotExist) = false")
	}
}
