package assessment

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/dvmrry/infrawright-dev/go/internal/canonjson"
	"github.com/dvmrry/infrawright-dev/go/internal/procerr"
)

// WriteAssessmentReportOptions supplies one optional report destination. A nil
// Path disables output; the path "-" selects Stdout.
type WriteAssessmentReportOptions struct {
	Path   *string
	Report SavedPlanAssessmentReport
	Stdout func(string) error
}

func assessmentReportWriteFailure() *procerr.ProcessFailure {
	return procerr.NewProcessFailure(procerr.NewProcessFailureOptions{
		Code:     "ASSESSMENT_REPORT_WRITE_FAILED",
		Category: procerr.CategoryIO,
		Message:  "unable to write saved-plan assessment report",
	})
}

// RenderAssessmentReport returns exact Python-compatible v1 report bytes.
func RenderAssessmentReport(report SavedPlanAssessmentReport) (string, error) {
	return canonjson.Render(assessmentReportJSONValue(report))
}

// WriteAssessmentReport writes through a same-directory private temporary
// file and atomic rename. Filesystem failures are deliberately sanitized.
func WriteAssessmentReport(options WriteAssessmentReportOptions) error {
	if options.Path == nil {
		return nil
	}
	rendered, err := RenderAssessmentReport(options.Report)
	if err != nil {
		return err
	}
	if *options.Path == "-" {
		if options.Stdout == nil {
			_, err = os.Stdout.WriteString(rendered)
			return err
		}
		return options.Stdout(rendered)
	}

	target, err := assessmentReportAbsoluteTarget(*options.Path)
	if err != nil {
		return assessmentReportWriteFailure()
	}
	directory := filepath.Dir(target)
	var temporary string
	if err := os.MkdirAll(directory, 0o777); err != nil {
		return assessmentReportWriteFailure()
	}
	defer func() {
		if temporary != "" {
			// Preserve the primary report-write failure.
			_ = os.Remove(temporary)
		}
	}()

	for range 32 {
		candidate, nameErr := privateReportTemporaryName(directory)
		if nameErr != nil {
			return assessmentReportWriteFailure()
		}
		file, openErr := os.OpenFile(candidate, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
		if errors.Is(openErr, os.ErrExist) {
			continue
		}
		if openErr != nil {
			return assessmentReportWriteFailure()
		}
		temporary = candidate
		writeErr := writeAndCloseReport(file, rendered)
		if writeErr != nil {
			return assessmentReportWriteFailure()
		}
		break
	}
	if temporary == "" {
		return assessmentReportWriteFailure()
	}
	if err := os.Rename(temporary, target); err != nil {
		return assessmentReportWriteFailure()
	}
	temporary = ""
	return nil
}

func assessmentReportAbsoluteTarget(target string) (string, error) {
	return resolveAssessmentReportAbsoluteTarget(target, assessmentReportPathResolver{
		isAbs: filepath.IsAbs,
		abs:   filepath.Abs,
		getwd: os.Getwd,
		stat:  os.Stat,
	})
}

type assessmentReportPathResolver struct {
	isAbs func(string) bool
	abs   func(string) (string, error)
	getwd func() (string, error)
	stat  func(string) (os.FileInfo, error)
}

func resolveAssessmentReportAbsoluteTarget(
	target string,
	resolver assessmentReportPathResolver,
) (string, error) {
	if resolver.isAbs(target) {
		return resolver.abs(target)
	}
	cwd, err := resolver.getwd()
	if err != nil {
		return "", err
	}
	currentDirectory, err := resolver.stat(".")
	if err != nil {
		return "", err
	}
	resolvedDirectory, err := resolver.stat(cwd)
	if err != nil {
		return "", err
	}
	if !os.SameFile(currentDirectory, resolvedDirectory) {
		return "", errors.New("current directory identity changed")
	}
	// Node path.resolve fails when its current directory has been unlinked.
	// Verify the inode before delegating the original target to filepath.Abs;
	// the original spelling is required for Windows rooted and drive-relative
	// FullPath semantics.
	return resolver.abs(target)
}

func privateReportTemporaryName(directory string) (string, error) {
	random := make([]byte, 8)
	if _, err := rand.Read(random); err != nil {
		return "", err
	}
	name := fmt.Sprintf(
		".infrawright-report-%d-%s",
		os.Getpid(),
		hex.EncodeToString(random),
	)
	return filepath.Join(directory, name), nil
}

func writeAndCloseReport(file *os.File, rendered string) error {
	_, writeErr := file.WriteString(rendered)
	closeErr := file.Close()
	if writeErr != nil {
		return writeErr
	}
	return closeErr
}
