package assessment

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/dvmrry/infrawright-dev/go/internal/procerr"
)

func TestWriteAssessmentReportNilPathDoesNoWork(t *testing.T) {
	report := buildReportForTest(t, Clean)
	report.Roots[0].Guidance = []map[string]any{{"unsupported": func() {}}}
	if err := WriteAssessmentReport(WriteAssessmentReportOptions{Report: report}); err != nil {
		t.Errorf("WriteAssessmentReport(nil path) error = %v, want nil without rendering", err)
	}
}

func TestWriteAssessmentReportStdoutReceivesExactBytes(t *testing.T) {
	report := buildReportForTest(t, Blocked)
	want, err := RenderAssessmentReport(report)
	if err != nil {
		t.Fatalf("RenderAssessmentReport(stdout fixture) error = %v, want nil", err)
	}
	path := "-"
	var got string
	err = WriteAssessmentReport(WriteAssessmentReportOptions{
		Path: &path, Report: report,
		Stdout: func(text string) error {
			got += text
			return nil
		},
	})
	if err != nil {
		t.Fatalf("WriteAssessmentReport(stdout fixture) error = %v, want nil", err)
	}
	if got != want {
		t.Errorf("WriteAssessmentReport(stdout fixture) bytes = %q, want %q", got, want)
	}
}

func TestWriteAssessmentReportPropagatesStdoutFailure(t *testing.T) {
	report := buildReportForTest(t, Clean)
	path := "-"
	want := errors.New("stdout fixture failed")
	err := WriteAssessmentReport(WriteAssessmentReportOptions{
		Path: &path, Report: report,
		Stdout: func(string) error { return want },
	})
	if !errors.Is(err, want) {
		t.Errorf("WriteAssessmentReport(stdout failure) error = %v, want %v", err, want)
	}
}

func TestWriteAssessmentReportAtomicallyReplacesWithPrivateFile(t *testing.T) {
	report := buildReportForTest(t, Tolerated)
	want, err := RenderAssessmentReport(report)
	if err != nil {
		t.Fatalf("RenderAssessmentReport(file fixture) error = %v, want nil", err)
	}
	target := filepath.Join(t.TempDir(), "nested", "assessment.json")
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		t.Fatalf("os.MkdirAll(%q) error = %v, want nil", filepath.Dir(target), err)
	}
	if err := os.WriteFile(target, []byte("old bytes"), 0o644); err != nil {
		t.Fatalf("os.WriteFile(%q) setup error = %v, want nil", target, err)
	}
	if err := WriteAssessmentReport(WriteAssessmentReportOptions{
		Path: &target, Report: report,
	}); err != nil {
		t.Fatalf("WriteAssessmentReport(%q) error = %v, want nil", target, err)
	}
	got, err := os.ReadFile(target)
	if err != nil {
		t.Fatalf("os.ReadFile(%q) error = %v, want nil", target, err)
	}
	if string(got) != want {
		t.Errorf("os.ReadFile(%q) = %q, want %q", target, got, want)
	}
	info, err := os.Stat(target)
	if err != nil {
		t.Fatalf("os.Stat(%q) error = %v, want nil", target, err)
	}
	if gotMode := info.Mode().Perm(); gotMode != 0o600 {
		t.Errorf("os.Stat(%q).Mode().Perm() = %#o, want %#o", target, gotMode, 0o600)
	}
	temporaries, err := filepath.Glob(filepath.Join(filepath.Dir(target), ".infrawright-report-*"))
	if err != nil {
		t.Fatalf("filepath.Glob(report temporaries) error = %v, want nil", err)
	}
	if len(temporaries) != 0 {
		t.Errorf("WriteAssessmentReport(%q) left temporaries = %#v, want none", target, temporaries)
	}
}

func TestWriteAssessmentReportResolvesRelativePathFromCurrentDirectory(t *testing.T) {
	directory := t.TempDir()
	t.Chdir(directory)
	report := buildReportForTest(t, Clean)
	want, err := RenderAssessmentReport(report)
	if err != nil {
		t.Fatalf("RenderAssessmentReport(relative-path fixture) error = %v, want nil", err)
	}
	target := filepath.Join("nested", "assessment.json")
	if err := WriteAssessmentReport(WriteAssessmentReportOptions{
		Path: &target, Report: report,
	}); err != nil {
		t.Fatalf("WriteAssessmentReport(%q) error = %v, want nil", target, err)
	}
	got, err := os.ReadFile(filepath.Join(directory, target))
	if err != nil {
		t.Fatalf("os.ReadFile(resolved relative report) error = %v, want nil", err)
	}
	if string(got) != want {
		t.Errorf("os.ReadFile(resolved relative report) = %q, want %q", got, want)
	}
}

func TestAssessmentReportTargetDelegatesWindowsSpecificPathsUnchanged(t *testing.T) {
	info, err := os.Stat(t.TempDir())
	if err != nil {
		t.Fatalf("os.Stat(resolver fixture) error = %v, want nil", err)
	}
	tests := []struct {
		name         string
		target       string
		absolute     bool
		want         string
		wantIdentity bool
	}{
		{
			name: "rooted without volume", target: `\foo`,
			want: `C:\foo`, wantIdentity: true,
		},
		{
			name: "drive relative", target: `C:foo`,
			want: `C:\work\foo`, wantIdentity: true,
		},
		{
			name: "drive absolute", target: `C:\foo`, absolute: true,
			want: `C:\foo`, wantIdentity: false,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var identityCalls int
			got, err := resolveAssessmentReportAbsoluteTarget(
				test.target,
				assessmentReportPathResolver{
					isAbs: func(target string) bool {
						if target != test.target {
							t.Errorf("isAbs target = %q, want original %q", target, test.target)
						}
						return test.absolute
					},
					abs: func(target string) (string, error) {
						if target != test.target {
							t.Errorf("abs target = %q, want original %q", target, test.target)
						}
						return test.want, nil
					},
					getwd: func() (string, error) {
						identityCalls++
						return `C:\work`, nil
					},
					stat: func(path string) (os.FileInfo, error) {
						identityCalls++
						if path != "." && path != `C:\work` {
							t.Errorf("stat path = %q, want . or C:\\work", path)
						}
						return info, nil
					},
				},
			)
			if err != nil {
				t.Fatalf("resolveAssessmentReportAbsoluteTarget(%q) error = %v, want nil", test.target, err)
			}
			if got != test.want {
				t.Errorf("resolveAssessmentReportAbsoluteTarget(%q) = %q, want %q", test.target, got, test.want)
			}
			if gotIdentity := identityCalls != 0; gotIdentity != test.wantIdentity {
				t.Errorf("resolveAssessmentReportAbsoluteTarget(%q) identity check = %v, want %v", test.target, gotIdentity, test.wantIdentity)
			}
		})
	}
}

func TestWriteAssessmentReportRejectsUnlinkedOrReplacedCurrentDirectory(t *testing.T) {
	const helperEnvironment = "INFRAWRIGHT_TEST_ASSESSMENT_CWD_IDENTITY"
	if mode := os.Getenv(helperEnvironment); mode != "" {
		directory, err := os.MkdirTemp("", "infrawright-assessment-cwd-identity-")
		if err != nil {
			t.Fatalf("os.MkdirTemp(cwd identity) error = %v, want nil", err)
		}
		if err := os.Chdir(directory); err != nil {
			t.Fatalf("os.Chdir(%q) error = %v, want nil", directory, err)
		}
		if err := os.Remove(directory); err != nil {
			t.Fatalf("os.Remove(current directory %q) error = %v, want nil", directory, err)
		}
		if mode == "replaced" {
			if err := os.Mkdir(directory, 0o700); err != nil {
				t.Fatalf("os.Mkdir(replacement cwd path %q) error = %v, want nil", directory, err)
			}
			defer func() { _ = os.RemoveAll(directory) }()
		}

		// Relative report paths bind to the current directory's identity, matching
		// Node path.resolve instead of recreating an unlinked or replaced pathname.
		target := filepath.Join("nested", "assessment.json")
		err = WriteAssessmentReport(WriteAssessmentReportOptions{
			Path: &target, Report: buildReportForTest(t, Clean),
		})
		var failure *procerr.ProcessFailure
		if !errors.As(err, &failure) || failure.Code != "ASSESSMENT_REPORT_WRITE_FAILED" ||
			failure.Category != procerr.CategoryIO ||
			failure.Message != "unable to write saved-plan assessment report" {
			t.Fatalf("WriteAssessmentReport(%s cwd) error = %v, want sanitized I/O failure", mode, err)
		}
		if strings.Contains(err.Error(), target) || strings.Contains(err.Error(), directory) {
			t.Fatalf("WriteAssessmentReport(%s cwd) error = %q, want paths sanitized", mode, err)
		}
		if _, statErr := os.Stat(filepath.Join(directory, target)); !errors.Is(statErr, os.ErrNotExist) {
			t.Fatalf("os.Stat(%s cwd report) error = %v, want os.ErrNotExist", mode, statErr)
		}
		return
	}
	if runtime.GOOS == "windows" {
		t.Skip("Windows does not permit unlinking a process current directory")
	}
	for _, mode := range []string{"unlinked", "replaced"} {
		t.Run(mode, func(t *testing.T) {
			command := exec.Command(
				os.Args[0], "-test.run=^TestWriteAssessmentReportRejectsUnlinkedOrReplacedCurrentDirectory$",
			)
			command.Env = append(os.Environ(), helperEnvironment+"="+mode)
			if output, err := command.CombinedOutput(); err != nil {
				t.Fatalf("%s-current-directory helper error = %v; output = %s", mode, err, output)
			}
		})
	}
}

func TestWriteAssessmentReportSanitizesFilesystemFailures(t *testing.T) {
	directory := t.TempDir()
	blocker := filepath.Join(directory, "not-a-directory")
	if err := os.WriteFile(blocker, []byte("fixture"), 0o600); err != nil {
		t.Fatalf("os.WriteFile(%q) setup error = %v, want nil", blocker, err)
	}
	target := filepath.Join(blocker, "assessment-secret-path.json")
	err := WriteAssessmentReport(WriteAssessmentReportOptions{
		Path: &target, Report: buildReportForTest(t, Clean),
	})
	var failure *procerr.ProcessFailure
	if !errors.As(err, &failure) {
		t.Fatalf("WriteAssessmentReport(filesystem failure) error = %v, want ProcessFailure", err)
	}
	if failure.Code != "ASSESSMENT_REPORT_WRITE_FAILED" || failure.Category != procerr.CategoryIO ||
		failure.Message != "unable to write saved-plan assessment report" {
		t.Errorf("WriteAssessmentReport(filesystem failure) = %+v, want sanitized ASSESSMENT_REPORT_WRITE_FAILED", failure)
	}
	if strings.Contains(err.Error(), target) || strings.Contains(err.Error(), "assessment-secret-path") {
		t.Errorf("WriteAssessmentReport(filesystem failure) error = %q, want target path sanitized", err)
	}
}

func TestWriteAssessmentReportRenderingFailurePrecedesFilesystemMutation(t *testing.T) {
	report := buildReportForTest(t, Clean)
	report.Roots[0].Guidance = []map[string]any{{"unsupported": func() {}}}
	target := filepath.Join(t.TempDir(), "must-not-exist", "assessment.json")
	err := WriteAssessmentReport(WriteAssessmentReportOptions{Path: &target, Report: report})
	if err == nil {
		t.Fatal("WriteAssessmentReport(unsupported report value) error = nil, want rendering error")
	}
	var failure *procerr.ProcessFailure
	if errors.As(err, &failure) {
		t.Errorf("WriteAssessmentReport(unsupported report value) error = %+v, want raw rendering error", failure)
	}
	if _, statErr := os.Stat(filepath.Dir(target)); !errors.Is(statErr, os.ErrNotExist) {
		t.Errorf("os.Stat(%q) after rendering failure error = %v, want os.ErrNotExist", filepath.Dir(target), statErr)
	}
}
