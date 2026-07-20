package deployment

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dvmrry/infrawright-dev/go/internal/controlevidence"
	"github.com/dvmrry/infrawright-dev/go/internal/procerr"
)

func TestLoadBoundAssessmentDeploymentBindsPresentSource(t *testing.T) {
	dir := t.TempDir()
	content := []byte(`{"overlay":"estate/prod","roots":{"zpa":{"strategy":"explicit"}}}`)
	deploymentPath := filepath.Join(dir, "deployment.json")
	if err := os.WriteFile(deploymentPath, content, 0o600); err != nil {
		t.Fatalf("os.WriteFile(%q) error = %v, want nil", deploymentPath, err)
	}

	bound, err := LoadBoundAssessmentDeployment(
		deploymentPath,
		controlevidence.BindOptions{},
	)
	if err != nil {
		t.Fatalf("LoadBoundAssessmentDeployment(%q) error = %v, want nil", deploymentPath, err)
	}
	if got, want := bound.Deployment.Overlay, any("estate/prod"); got != want {
		t.Errorf("LoadBoundAssessmentDeployment(%q).Deployment.Overlay = %v, want %v", deploymentPath, got, want)
	}
	if got, want := bound.Deployment.Roots["zpa"].Strategy, "explicit"; got != want {
		t.Errorf("LoadBoundAssessmentDeployment(%q).Deployment.Roots[zpa].Strategy = %q, want %q", deploymentPath, got, want)
	}
	if got, want := bound.File.Path, deploymentPath; got != want {
		t.Errorf("LoadBoundAssessmentDeployment(%q).File.Path = %q, want %q", deploymentPath, got, want)
	}
	wantSHA256 := fmt.Sprintf("%x", sha256.Sum256(content))
	if bound.File.Digest == nil {
		t.Fatalf("LoadBoundAssessmentDeployment(%q).File.Digest = nil, want digest", deploymentPath)
	}
	if got, want := bound.File.Digest.SHA256, wantSHA256; got != want {
		t.Errorf("LoadBoundAssessmentDeployment(%q).File.Digest.SHA256 = %q, want %q", deploymentPath, got, want)
	}
	if got, want := bound.File.Digest.Size, int64(len(content)); got != want {
		t.Errorf("LoadBoundAssessmentDeployment(%q).File.Digest.Size = %d, want %d", deploymentPath, got, want)
	}
	if bound.File.Identity == nil {
		t.Errorf("LoadBoundAssessmentDeployment(%q).File.Identity = nil, want stable identity", deploymentPath)
	}
	if bound.File.FollowSymlinks != nil {
		t.Errorf("LoadBoundAssessmentDeployment(%q).File.FollowSymlinks = %v, want omitted default", deploymentPath, *bound.File.FollowSymlinks)
	}
}

func TestLoadBoundAssessmentDeploymentBindsMissingSourceToDefaults(t *testing.T) {
	deploymentPath := filepath.Join(t.TempDir(), "deployment.json")

	bound, err := LoadBoundAssessmentDeployment(
		deploymentPath,
		controlevidence.BindOptions{},
	)
	if err != nil {
		t.Fatalf("LoadBoundAssessmentDeployment(%q) error = %v, want nil", deploymentPath, err)
	}
	if got, want := bound.Deployment.Overlay, any("."); got != want {
		t.Errorf("LoadBoundAssessmentDeployment(%q).Deployment.Overlay = %v, want %v", deploymentPath, got, want)
	}
	if got := len(bound.Deployment.Roots); got != 0 {
		t.Errorf("LoadBoundAssessmentDeployment(%q).Deployment.Roots length = %d, want 0", deploymentPath, got)
	}
	if got, want := bound.File.Path, deploymentPath; got != want {
		t.Errorf("LoadBoundAssessmentDeployment(%q).File.Path = %q, want %q", deploymentPath, got, want)
	}
	if bound.File.Digest != nil || bound.File.Identity != nil || bound.File.FollowSymlinks != nil {
		t.Errorf("LoadBoundAssessmentDeployment(%q).File = %+v, want absent default-follow binding", deploymentPath, bound.File)
	}

	if err := os.WriteFile(deploymentPath, []byte(`{"overlay":"changed"}`), 0o600); err != nil {
		t.Fatalf("os.WriteFile(%q) error = %v, want nil", deploymentPath, err)
	}
	assertProcessFailure(
		t,
		"controlevidence.RecheckAssessmentControlFiles(absent deployment after creation)",
		controlevidence.RecheckAssessmentControlFiles([]controlevidence.BoundAssessmentControlFile{bound.File}),
		"ASSESSMENT_CONTROL_CHANGED",
		procerr.CategoryDomain,
	)
}

func TestLoadBoundAssessmentDeploymentRecheckRejectsMutation(t *testing.T) {
	deploymentPath := filepath.Join(t.TempDir(), "deployment.json")
	if err := os.WriteFile(deploymentPath, []byte(`{"overlay":"before"}`), 0o600); err != nil {
		t.Fatalf("os.WriteFile(%q) error = %v, want nil", deploymentPath, err)
	}
	bound, err := LoadBoundAssessmentDeployment(
		deploymentPath,
		controlevidence.BindOptions{},
	)
	if err != nil {
		t.Fatalf("LoadBoundAssessmentDeployment(%q) error = %v, want nil", deploymentPath, err)
	}
	if err := os.WriteFile(deploymentPath, []byte(`{"overlay":"after"}`), 0o600); err != nil {
		t.Fatalf("os.WriteFile(%q) error = %v, want nil", deploymentPath, err)
	}

	recheckErr := controlevidence.RecheckAssessmentControlFiles(
		[]controlevidence.BoundAssessmentControlFile{bound.File},
	)
	assertProcessFailure(
		t,
		"controlevidence.RecheckAssessmentControlFiles(mutated deployment)",
		recheckErr,
		"ASSESSMENT_CONTROL_CHANGED",
		procerr.CategoryDomain,
	)
	if strings.Contains(recheckErr.Error(), deploymentPath) || strings.Contains(recheckErr.Error(), "after") {
		t.Errorf("controlevidence.RecheckAssessmentControlFiles(mutated deployment) error = %q, want path/content redacted", recheckErr)
	}
}

func TestLoadBoundAssessmentDeploymentRejectsInvalidContentWithoutLeakage(t *testing.T) {
	tests := []struct {
		name        string
		content     []byte
		wantCode    string
		wantMessage string
	}{
		{
			name:        "invalid JSON",
			content:     []byte(`{"secret":"do-not-leak"`),
			wantCode:    "INVALID_DEPLOYMENT",
			wantMessage: "deployment is not valid JSON",
		},
		{
			name:        "invalid deployment metadata",
			content:     []byte(`{"roots":{"zpa":{"strategy":"secret-invalid-strategy"}}}`),
			wantCode:    "INVALID_DEPLOYMENT",
			wantMessage: "roots.zpa.strategy must be 'explicit' or 'slug'",
		},
		{
			name:        "invalid UTF-8",
			content:     []byte{0xff, 0xfe, 0xfd},
			wantCode:    "INVALID_UTF8",
			wantMessage: "assessment control input is not valid UTF-8",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			deploymentPath := filepath.Join(t.TempDir(), "private-deployment.json")
			if err := os.WriteFile(deploymentPath, test.content, 0o600); err != nil {
				t.Fatalf("os.WriteFile(%q) error = %v, want nil", deploymentPath, err)
			}

			_, err := LoadBoundAssessmentDeployment(
				deploymentPath,
				controlevidence.BindOptions{},
			)
			failure := assertProcessFailure(
				t,
				fmt.Sprintf("LoadBoundAssessmentDeployment(%q)", deploymentPath),
				err,
				test.wantCode,
				procerr.CategoryDomain,
			)
			if failure.Message != test.wantMessage {
				t.Errorf("LoadBoundAssessmentDeployment(%q) failure.Message = %q, want %q", deploymentPath, failure.Message, test.wantMessage)
			}
			if strings.Contains(failure.Message, deploymentPath) || strings.Contains(failure.Message, "do-not-leak") || strings.Contains(failure.Message, "secret-invalid-strategy") {
				t.Errorf("LoadBoundAssessmentDeployment(%q) failure.Message = %q, want path/content redacted", deploymentPath, failure.Message)
			}
		})
	}
}

func TestLoadBoundAssessmentDeploymentEnforcesPathTypeAndSizeLimits(t *testing.T) {
	t.Run("relative path", func(t *testing.T) {
		_, err := LoadBoundAssessmentDeployment(
			"relative/deployment.json",
			controlevidence.BindOptions{},
		)
		assertProcessFailure(
			t,
			"LoadBoundAssessmentDeployment(relative path)",
			err,
			"UNRESOLVED_ASSESSMENT_CONTROL_PATH",
			procerr.CategoryDomain,
		)
	})

	t.Run("non-regular file", func(t *testing.T) {
		dir := t.TempDir()
		_, err := LoadBoundAssessmentDeployment(dir, controlevidence.BindOptions{})
		assertProcessFailure(
			t,
			fmt.Sprintf("LoadBoundAssessmentDeployment(%q)", dir),
			err,
			"NOT_REGULAR_FILE",
			procerr.CategoryIO,
		)
	})

	t.Run("oversize", func(t *testing.T) {
		deploymentPath := filepath.Join(t.TempDir(), "oversize-deployment.json")
		file, err := os.OpenFile(deploymentPath, os.O_CREATE|os.O_WRONLY, 0o600)
		if err != nil {
			t.Fatalf("os.OpenFile(%q) error = %v, want nil", deploymentPath, err)
		}
		if err := file.Truncate(16*1024*1024 + 1); err != nil {
			_ = file.Close()
			t.Fatalf("(*os.File).Truncate(%q) error = %v, want nil", deploymentPath, err)
		}
		if err := file.Close(); err != nil {
			t.Fatalf("(*os.File).Close(%q) error = %v, want nil", deploymentPath, err)
		}

		_, err = LoadBoundAssessmentDeployment(
			deploymentPath,
			controlevidence.BindOptions{},
		)
		failure := assertProcessFailure(
			t,
			fmt.Sprintf("LoadBoundAssessmentDeployment(%q)", deploymentPath),
			err,
			"FILE_LIMIT_EXCEEDED",
			procerr.CategoryIO,
		)
		if strings.Contains(failure.Message, deploymentPath) {
			t.Errorf("LoadBoundAssessmentDeployment(%q) failure.Message = %q, want path redacted", deploymentPath, failure.Message)
		}
	})
}

func TestLoadBoundAssessmentDeploymentPreservesNoFollowPolicy(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "target.json")
	if err := os.WriteFile(target, []byte(`{"overlay":"estate"}`), 0o600); err != nil {
		t.Fatalf("os.WriteFile(%q) error = %v, want nil", target, err)
	}
	link := filepath.Join(dir, "deployment.json")
	if err := os.Symlink(target, link); err != nil {
		t.Fatalf("os.Symlink(%q, %q) error = %v, want nil", target, link, err)
	}

	bound, err := LoadBoundAssessmentDeployment(link, controlevidence.BindOptions{})
	if err != nil {
		t.Fatalf("LoadBoundAssessmentDeployment(%q, default follow) error = %v, want nil", link, err)
	}
	if got, want := bound.Deployment.Overlay, any("estate"); got != want {
		t.Errorf("LoadBoundAssessmentDeployment(%q, default follow).Deployment.Overlay = %v, want %v", link, got, want)
	}

	followSymlinks := false
	_, err = LoadBoundAssessmentDeployment(
		link,
		controlevidence.BindOptions{FollowSymlinks: &followSymlinks},
	)
	assertProcessFailure(
		t,
		fmt.Sprintf("LoadBoundAssessmentDeployment(%q, no follow)", link),
		err,
		"SYMLINK_NOT_ALLOWED",
		procerr.CategoryIO,
	)

	regular, err := LoadBoundAssessmentDeployment(
		target,
		controlevidence.BindOptions{FollowSymlinks: &followSymlinks},
	)
	if err != nil {
		t.Fatalf("LoadBoundAssessmentDeployment(%q, no follow) error = %v, want nil", target, err)
	}
	if regular.File.FollowSymlinks == nil || *regular.File.FollowSymlinks {
		t.Fatalf("LoadBoundAssessmentDeployment(%q, no follow).File.FollowSymlinks = %v, want false", target, regular.File.FollowSymlinks)
	}
	followSymlinks = true
	if *regular.File.FollowSymlinks {
		t.Errorf("LoadBoundAssessmentDeployment(%q, no follow) retained caller bool pointer", target)
	}
}

func assertProcessFailure(
	t *testing.T,
	operation string,
	err error,
	wantCode string,
	wantCategory procerr.Category,
) *procerr.ProcessFailure {
	t.Helper()
	var failure *procerr.ProcessFailure
	if !errors.As(err, &failure) {
		t.Fatalf("%s error = %T(%v), want *procerr.ProcessFailure", operation, err, err)
	}
	if failure.Code != wantCode {
		t.Errorf("%s failure.Code = %q, want %q", operation, failure.Code, wantCode)
	}
	if failure.Category != wantCategory {
		t.Errorf("%s failure.Category = %q, want %q", operation, failure.Category, wantCategory)
	}
	return failure
}
