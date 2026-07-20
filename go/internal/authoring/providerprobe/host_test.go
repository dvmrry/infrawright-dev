package providerprobe

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/dvmrry/infrawright-dev/go/internal/collectors"
	"github.com/dvmrry/infrawright-dev/go/internal/httptransport"
)

type hostRoundTripFunc func(*http.Request) (*http.Response, error)

func (f hostRoundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return f(request)
}

func TestDefaultLegacyHostDownloadFileHTTPAndRedaction(t *testing.T) {
	root := t.TempDir()
	input := filepath.Join(root, "input.json")
	if err := os.WriteFile(input, []byte(`{"from":"file"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	host := newDefaultLegacyHost(map[string]string{})
	fileDestination := filepath.Join(root, "nested", "file.json")
	if err := host.Download(context.Background(), DownloadRequest{URL: "file://" + input, Destination: fileDestination}); err != nil {
		t.Fatalf("Download(file) error = %v", err)
	}
	if got, err := os.ReadFile(fileDestination); err != nil || string(got) != `{"from":"file"}` {
		t.Fatalf("Download(file) bytes = %q, %v; want exact file bytes", got, err)
	}
	if mode, err := os.Stat(fileDestination); err != nil || mode.Mode().Perm() != 0o600 {
		t.Fatalf("Download(file) mode = %v, %v; want 0600", mode, err)
	}

	var gotURL string
	host.newHTTPTransport = hostHTTPTransport(t, hostRoundTripFunc(func(request *http.Request) (*http.Response, error) {
		gotURL = request.URL.String()
		return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{"from":"http"}`))}, nil
	}))
	httpDestination := filepath.Join(root, "http.json")
	if err := host.Download(context.Background(), DownloadRequest{URL: "https://example.test/openapi", Destination: httpDestination}); err != nil {
		t.Fatalf("Download(http) error = %v", err)
	}
	if gotURL != "https://example.test/openapi" {
		t.Errorf("Download(http) URL = %q, want exact request URL", gotURL)
	}
	if got, err := os.ReadFile(httpDestination); err != nil || string(got) != `{"from":"http"}` {
		t.Fatalf("Download(http) bytes = %q, %v; want exact HTTP bytes", got, err)
	}

	secretURL := "https://user:pass@example.test/openapi?token=secret#fragment"
	secretDestination := filepath.Join(root, "sensitive-destination")
	host.newHTTPTransport = func(context.Context) (collectors.HttpTransport, error) {
		return nil, errors.New(secretURL + " " + secretDestination)
	}
	err := host.Download(context.Background(), DownloadRequest{URL: secretURL, Destination: secretDestination})
	if err == nil {
		t.Fatal("Download(redaction) error = nil, want error")
	}
	for _, secret := range []string{"user:pass", "token=secret", "fragment", secretDestination} {
		if strings.Contains(err.Error(), secret) {
			t.Errorf("Download(redaction) error = %q, contains %q", err, secret)
		}
	}
}

func TestDefaultLegacyHostDownloadRejectsNon2xxOversizeAndCancelled(t *testing.T) {
	root := t.TempDir()
	host := newDefaultLegacyHost(map[string]string{})
	host.newHTTPTransport = hostHTTPTransport(t, hostRoundTripFunc(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusTeapot, Header: make(http.Header), Body: io.NopCloser(strings.NewReader("no"))}, nil
	}))
	if err := host.Download(context.Background(), DownloadRequest{URL: "https://example.test/no", Destination: filepath.Join(root, "no.json")}); err == nil {
		t.Fatal("Download(non-2xx) error = nil, want error")
	}
	host.newHTTPTransport = hostHTTPTransport(t, hostRoundTripFunc(func(*http.Request) (*http.Response, error) {
		header := make(http.Header)
		header.Set("Content-Length", "67108865")
		return &http.Response{StatusCode: http.StatusOK, Header: header, Body: io.NopCloser(strings.NewReader("x"))}, nil
	}))
	if err := host.Download(context.Background(), DownloadRequest{URL: "https://example.test/large", Destination: filepath.Join(root, "large.json")}); err == nil {
		t.Fatal("Download(oversize) error = nil, want error")
	}
	if err := host.Download(context.Background(), DownloadRequest{URL: "gopher://example.test/input", Destination: filepath.Join(root, "bad.json")}); err == nil {
		t.Fatal("Download(unsupported scheme) error = nil, want error")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	cancelledDestination := filepath.Join(root, "not-created", "input.json")
	err := host.Download(ctx, DownloadRequest{URL: "file:///not-read", Destination: cancelledDestination})
	if !errors.Is(err, context.Canceled) {
		t.Errorf("Download(cancelled) error = %v, want context.Canceled", err)
	}
	if _, err := os.Stat(filepath.Dir(cancelledDestination)); !os.IsNotExist(err) {
		t.Errorf("Download(cancelled) created destination parent: %v", err)
	}
}

func TestDefaultLegacyHostCloneBoundary(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("fixture shell executable requires POSIX")
	}
	root := t.TempDir()
	record := filepath.Join(root, "record")
	writeHostExecutable(t, root, "git", `
printf '%s\n' "$@" > "$RECORD"
printf 'parent=%s\n' "${PARENT_POISON-unset}" >> "$RECORD"
printf 'prompt=%s askpass=%s global=%s\n' "$GIT_TERMINAL_PROMPT" "$GIT_ASKPASS" "$GIT_CONFIG_GLOBAL" >> "$RECORD"
`)
	t.Setenv("PARENT_POISON", "must-not-leak")
	host := newDefaultLegacyHost(map[string]string{"PATH": root + string(os.PathListSeparator) + "/usr/bin:/bin", "RECORD": record})
	request := CloneRequest{Repository: "https://secret.example/repository", Revision: "pinned-ref", Destination: filepath.Join(root, "checkout")}
	if err := host.Clone(context.Background(), request); err != nil {
		t.Fatalf("Clone(success) error = %v", err)
	}
	got, err := os.ReadFile(record)
	if err != nil {
		t.Fatal(err)
	}
	want := "clone\n--depth\n1\n--branch\npinned-ref\n--\nhttps://secret.example/repository\n" + request.Destination + "\n" +
		"parent=unset\nprompt=0 askpass=/bin/false global=/dev/null\n"
	if string(got) != want {
		t.Errorf("Clone exact argv/environment mismatch (-want +got):\nwant %q\ngot  %q", want, got)
	}

	writeHostExecutable(t, root, "git", `printf 'repository=%s output=must-not-leak\n' "$4" >&2; exit 23`)
	err = host.Clone(context.Background(), request)
	if err == nil {
		t.Fatal("Clone(nonzero) error = nil, want error")
	}
	for _, secret := range []string{request.Repository, request.Revision, "must-not-leak"} {
		if strings.Contains(err.Error(), secret) {
			t.Errorf("Clone(nonzero) error = %q, contains %q", err, secret)
		}
	}
	writeHostExecutable(t, root, "git", `head -c 65537 /dev/zero`)
	if err := host.Clone(context.Background(), request); err == nil {
		t.Fatal("Clone(oversized stdout) error = nil, want bounded failure")
	}

	writeHostExecutable(t, root, "git", `while :; do :; done`)
	host.gitTimeout = 20 * time.Millisecond
	if err := host.Clone(context.Background(), request); !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("Clone(timeout) error = %v, want context.DeadlineExceeded", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := host.Clone(ctx, request); !errors.Is(err, context.Canceled) {
		t.Errorf("Clone(cancelled) error = %v, want context.Canceled", err)
	}
}

func TestDefaultLegacyHostNilEnvironmentDoesNotPanic(t *testing.T) {
	provided := map[string]string{"VALUE": "initial"}
	snapshotted := newDefaultLegacyHost(provided)
	provided["VALUE"] = "mutated"
	if snapshotted.environment["VALUE"] != "initial" {
		t.Errorf("newDefaultLegacyHost environment = %q, want detached initial value", snapshotted.environment["VALUE"])
	}
	host := newDefaultLegacyHost(nil)
	if environment := host.gitEnvironment(); environment["GIT_TERMINAL_PROMPT"] != "0" {
		t.Errorf("gitEnvironment(nil) GIT_TERMINAL_PROMPT = %q, want 0", environment["GIT_TERMINAL_PROMPT"])
	}
	if err := host.Clone(context.Background(), CloneRequest{}); err == nil {
		t.Error("Clone(nil environment) error = nil, want clean executable-resolution failure")
	}
}

func TestDefaultLegacyHostCaptureTerraformSchema(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Terraform execution is unsupported on Windows")
	}
	root := t.TempDir()
	record := filepath.Join(root, "terraform-record")
	writeHostExecutable(t, root, "terraform", `
if [ "$1" = init ]; then
  printf '%s\n' "$@" > "$RECORD"
  printf 'parent=%s\n' "${PARENT_POISON-unset}" >> "$RECORD"
  exit 0
fi
printf '%s\n' "$@" >> "$RECORD"
printf '%s' "$SCHEMA"
`)
	t.Setenv("PARENT_POISON", "must-not-leak")
	host := newDefaultLegacyHost(map[string]string{})
	directory := filepath.Join(root, "schema")
	request := TerraformSchemaRequest{
		TerraformExecutable: "terraform",
		Directory:           directory,
		MainHCL:             []byte("terraform {}\n"),
		Environment:         map[string]string{"PATH": root + string(os.PathListSeparator) + "/usr/bin:/bin", "RECORD": record, "SCHEMA": `{"provider_schemas":{}}`},
	}
	got, err := host.CaptureTerraformSchema(context.Background(), request)
	if err != nil {
		t.Fatalf("CaptureTerraformSchema(valid) error = %v", err)
	}
	if string(got) != `{"provider_schemas":{}}` {
		t.Errorf("CaptureTerraformSchema(valid) = %q, want exact JSON", got)
	}
	got[0] = 'X'
	if gotAgain, err := host.CaptureTerraformSchema(context.Background(), request); err != nil || string(gotAgain) != `{"provider_schemas":{}}` {
		t.Errorf("CaptureTerraformSchema(detached) = %q, %v; want fresh exact JSON", gotAgain, err)
	}
	if main, err := os.ReadFile(filepath.Join(directory, "main.tf")); err != nil || !bytes.Equal(main, request.MainHCL) {
		t.Errorf("CaptureTerraformSchema main.tf = %q, %v; want exact HCL", main, err)
	}
	if metadata, err := os.Stat(filepath.Join(directory, "main.tf")); err != nil || metadata.Mode().Perm() != 0o600 {
		t.Errorf("CaptureTerraformSchema main.tf mode = %v, %v; want 0600", metadata, err)
	}
	if recordBytes, err := os.ReadFile(record); err != nil {
		t.Fatal(err)
	} else if want := "init\n-backend=false\nparent=unset\nproviders\nschema\n-json\n"; string(recordBytes) != want {
		t.Errorf("CaptureTerraformSchema argv/environment mismatch:\n got %q\nwant %q", recordBytes, want)
	}

	for _, schema := range []string{"[1]", "null", `{"one":1} trailing`} {
		request.Environment["SCHEMA"] = schema
		if _, err := host.CaptureTerraformSchema(context.Background(), request); err == nil {
			t.Errorf("CaptureTerraformSchema(%q) error = nil, want validation error", schema)
		}
	}
	writeHostExecutable(t, root, "terraform", `
if [ "$1" = init ]; then exit 0; fi
head -c 8388609 /dev/zero
`)
	if _, err := host.CaptureTerraformSchema(context.Background(), request); err == nil {
		t.Error("CaptureTerraformSchema(oversized stdout) error = nil, want Terraform stream-limit failure")
	}
	writeHostExecutable(t, root, "terraform", `while :; do :; done`)
	host.terraformTimeout = 20 * time.Millisecond
	if _, err := host.CaptureTerraformSchema(context.Background(), request); err == nil {
		t.Error("CaptureTerraformSchema(timeout) error = nil, want Terraform timeout")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	noSideEffect := filepath.Join(root, "cancelled")
	request.Directory = noSideEffect
	if _, err := host.CaptureTerraformSchema(ctx, request); !errors.Is(err, context.Canceled) {
		t.Errorf("CaptureTerraformSchema(cancelled) error = %v, want context.Canceled", err)
	}
	if _, err := os.Stat(noSideEffect); !os.IsNotExist(err) {
		t.Errorf("CaptureTerraformSchema(cancelled) created directory: %v", err)
	}
}

func hostHTTPTransport(t *testing.T, roundTripper http.RoundTripper) func(context.Context) (collectors.HttpTransport, error) {
	t.Helper()
	return func(parent context.Context) (collectors.HttpTransport, error) {
		timeout := 60_000
		limit := 64 * 1024 * 1024
		return httptransport.NewContext(parent, map[string]string{}, httptransport.Options{
			RequestTimeoutMs:   &timeout,
			ResponseLimitBytes: &limit,
			RoundTripper:       roundTripper,
		})
	}
}

func writeHostExecutable(t *testing.T, directory, name, body string) {
	t.Helper()
	path := filepath.Join(directory, name)
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"+body+"\n"), 0o700); err != nil {
		t.Fatalf("write executable: %v", err)
	}
	if err := os.Chmod(path, 0o700); err != nil {
		t.Fatalf("chmod executable: %v", err)
	}
}
