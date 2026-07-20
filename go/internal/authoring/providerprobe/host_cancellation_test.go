package providerprobe

import (
	"context"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/dvmrry/infrawright-dev/go/internal/collectors"
	"github.com/dvmrry/infrawright-dev/go/internal/httptransport"
)

func TestDefaultLegacyHostDownloadCancelsInFlightHTTP(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	started := make(chan struct{})
	host := newDefaultLegacyHost(map[string]string{})
	host.newHTTPTransport = func(parent context.Context) (collectors.HttpTransport, error) {
		timeout := 60_000
		limit := 64 * 1024 * 1024
		return httptransport.NewContext(parent, map[string]string{}, httptransport.Options{
			RequestTimeoutMs:   &timeout,
			ResponseLimitBytes: &limit,
			RoundTripper: hostRoundTripFunc(func(request *http.Request) (*http.Response, error) {
				close(started)
				<-request.Context().Done()
				return nil, request.Context().Err()
			}),
		})
	}
	destination := filepath.Join(t.TempDir(), "openapi.json")
	result := make(chan error, 1)
	go func() {
		result <- host.Download(ctx, DownloadRequest{URL: "https://example.test/openapi", Destination: destination})
	}()
	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("HTTP request did not start")
	}
	cancel()
	select {
	case err := <-result:
		if err != context.Canceled {
			t.Errorf("Download() error = %v, want raw context.Canceled", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Download did not return after cancellation")
	}
	if _, err := os.Stat(destination); !os.IsNotExist(err) {
		t.Errorf("Download(cancelled) materialized destination: stat = %v", err)
	}
}

func TestDefaultLegacyHostCaptureTerraformSchemaCancelsActiveCommand(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Terraform execution is intentionally unsupported on Windows")
	}
	root := t.TempDir()
	pidFile := filepath.Join(root, "schema.pid")
	writeHostExecutable(t, root, "terraform", `
if [ "$1" = init ]; then exit 0; fi
printf '%s' "$$" > "$PID_FILE"
while :; do :; done
`)
	host := newDefaultLegacyHost(map[string]string{})
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	result := make(chan error, 1)
	go func() {
		_, err := host.CaptureTerraformSchema(ctx, TerraformSchemaRequest{
			TerraformExecutable: "terraform",
			Directory:           filepath.Join(root, "schema"),
			MainHCL:             []byte("terraform {}\n"),
			Environment: map[string]string{
				"PATH":     root,
				"PID_FILE": pidFile,
			},
		})
		result <- err
	}()
	deadline := time.Now().Add(5 * time.Second)
	for {
		if _, err := os.Stat(pidFile); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("Terraform schema command did not start")
		}
		time.Sleep(10 * time.Millisecond)
	}
	cancel()
	select {
	case err := <-result:
		if err != context.Canceled {
			t.Errorf("CaptureTerraformSchema() error = %v, want raw context.Canceled", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("CaptureTerraformSchema did not return after cancellation")
	}
}
