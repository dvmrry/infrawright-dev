package httptransport

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/dvmrry/infrawright-dev/go/internal/collectors"
)

func TestNewContextCancelsBlockedRoundTrip(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	started := make(chan struct{})
	client, err := NewContext(ctx, collectors.Environment{}, Options{
		RoundTripper: roundTripFunc(func(request *http.Request) (*http.Response, error) {
			close(started)
			<-request.Context().Done()
			return nil, request.Context().Err()
		}),
	})
	if err != nil {
		t.Fatalf("NewContext() error = %v", err)
	}
	t.Cleanup(func() { _ = client.Close() })

	result := make(chan error, 1)
	go func() {
		_, requestErr := client.Request(collectors.HTTPRequest{
			Method: http.MethodGet,
			URL:    mustURL(t, "https://api.example.test/blocked"),
		})
		result <- requestErr
	}()
	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("RoundTrip did not start")
	}
	cancel()
	select {
	case err := <-result:
		if err != context.Canceled {
			t.Errorf("Request() error = %v, want raw context.Canceled", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Request did not return after context cancellation")
	}
}

func TestNewContextCancelsProductionRetryBackoff(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	sleepStarted := make(chan struct{})
	client, err := NewContext(ctx, collectors.Environment{}, Options{
		RoundTripper: roundTripFunc(func(*http.Request) (*http.Response, error) {
			return response("rate limited", http.StatusTooManyRequests, nil), nil
		}),
	})
	if err != nil {
		t.Fatalf("NewContext() error = %v", err)
	}
	t.Cleanup(func() { _ = client.Close() })
	concrete := client.(*transport)
	concrete.contextSleep = func(parent context.Context, _ float64) error {
		close(sleepStarted)
		<-parent.Done()
		return parent.Err()
	}

	result := make(chan error, 1)
	go func() {
		_, requestErr := client.Request(collectors.HTTPRequest{
			Method: http.MethodGet,
			URL:    mustURL(t, "https://api.example.test/retry"),
		})
		result <- requestErr
	}()
	select {
	case <-sleepStarted:
	case <-time.After(2 * time.Second):
		t.Fatal("production retry sleep did not begin")
	}
	cancel()
	select {
	case err := <-result:
		if err != context.Canceled {
			t.Errorf("Request() error = %v, want raw context.Canceled", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Request did not return after retry-backoff cancellation")
	}
}
