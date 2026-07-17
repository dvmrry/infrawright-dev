package resthttp

import (
	"errors"
	"io"
	"net/http"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/dvmrry/infrawright-dev/go/internal/collectors"
)

type coordinatedBody struct {
	started chan struct{}
	release chan struct{}
	once    sync.Once
}

func (b *coordinatedBody) Read([]byte) (int, error) {
	b.once.Do(func() { close(b.started) })
	<-b.release
	return 0, io.EOF
}

func (*coordinatedBody) Close() error { return nil }

func TestCloseWaitsForActiveResponseBody(t *testing.T) {
	body := &coordinatedBody{started: make(chan struct{}), release: make(chan struct{})}
	cleanupStarted := make(chan struct{})
	transport, err := CreateRestHTTPTransport(collectors.Environment{}, RestHTTPTransportOptions{
		RoundTripper: roundTripFunc(func(*http.Request) (*http.Response, error) {
			return &http.Response{StatusCode: 200, Header: make(http.Header), Body: body}, nil
		}),
		Close: func() error {
			close(cleanupStarted)
			return nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	requestDone := make(chan error, 1)
	go func() {
		_, requestErr := transport.Request(collectors.HTTPRequest{
			Method: "GET", URL: mustURL(t, "https://api.example.test/"),
		})
		requestDone <- requestErr
	}()
	<-body.started
	closeDone := make(chan error, 1)
	go func() { closeDone <- transport.Close() }()
	select {
	case <-cleanupStarted:
		t.Fatal("cleanup started while response body work was active")
	case <-closeDone:
		t.Fatal("Close returned while response body work was active")
	case <-time.After(20 * time.Millisecond):
	}
	close(body.release)
	if err := <-requestDone; err != nil {
		t.Fatalf("Request() failed: %v", err)
	}
	if err := <-closeDone; err != nil {
		t.Fatalf("Close() failed: %v", err)
	}
	select {
	case <-cleanupStarted:
	default:
		t.Fatal("cleanup did not run after response body drained")
	}
}

func TestCloseDoesNotWaitForRetrySleepAndSleepingRequestResumesClosed(t *testing.T) {
	server := startCaptureServer(t, rawHTTPResponse(http.StatusTooManyRequests, nil, "rate"))
	sleepStarted := make(chan struct{})
	releaseSleep := make(chan struct{})
	transport, err := CreateRestHTTPTransport(collectors.Environment{}, RestHTTPTransportOptions{
		Sleep: func(float64) error {
			close(sleepStarted)
			<-releaseSleep
			return nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	requestDone := make(chan error, 1)
	go func() {
		_, requestErr := transport.Request(collectors.HTTPRequest{
			Method: "GET", URL: mustURL(t, "http://"+server.address+"/rate"),
		})
		requestDone <- requestErr
	}()
	<-sleepStarted
	closeDone := make(chan error, 1)
	go func() { closeDone <- transport.Close() }()
	select {
	case err := <-closeDone:
		if err != nil {
			t.Fatalf("Close() failed: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("Close waited for a retry sleep")
	}
	_, err = transport.Request(collectors.HTTPRequest{
		Method: "GET", URL: mustURL(t, "http://"+server.address+"/new"),
	})
	requireProcessFailure(t, err, "REST_HTTP_CLOSED")
	close(releaseSleep)
	requestErr := <-requestDone
	requireProcessFailure(t, requestErr, "REST_HTTP_TRANSPORT_FAILED")
	requests := awaitCaptureServer(t, server)
	if len(requests) != 1 || requests[0].line != "GET /rate HTTP/1.1" {
		t.Errorf("wire requests = %#v, want exactly the admitted first attempt", requests)
	}
}

type redirectGapRecorder struct {
	recordStarted chan struct{}
	releaseRecord chan struct{}
	once          sync.Once
}

func (*redirectGapRecorder) Now() float64                               { return 1 }
func (*redirectGapRecorder) DurationSince(float64) float64              { return 1 }
func (*redirectGapRecorder) RecordHTTPRetry(HTTPRetryPerformance) error { return nil }

func (r *redirectGapRecorder) RecordHTTPAttempt(HTTPAttemptPerformance) error {
	r.once.Do(func() { close(r.recordStarted) })
	<-r.releaseRecord
	return nil
}

func TestCloseDoesNotWaitForRedirectProcessingGap(t *testing.T) {
	recorder := &redirectGapRecorder{recordStarted: make(chan struct{}), releaseRecord: make(chan struct{})}
	var calls atomic.Int64
	transport, err := CreateRestHTTPTransport(collectors.Environment{}, RestHTTPTransportOptions{
		RoundTripper: roundTripFunc(func(*http.Request) (*http.Response, error) {
			calls.Add(1)
			return response("", 302, http.Header{"Location": []string{"/next"}}), nil
		}),
		Performance: recorder,
	})
	if err != nil {
		t.Fatal(err)
	}
	requestDone := make(chan error, 1)
	go func() {
		_, requestErr := transport.Request(collectors.HTTPRequest{
			Method: "GET", URL: mustURL(t, "https://api.example.test/start"),
			Performance: &collectors.HTTPRequestPerformanceContext{},
		})
		requestDone <- requestErr
	}()
	<-recorder.recordStarted
	closeDone := make(chan error, 1)
	go func() { closeDone <- transport.Close() }()
	select {
	case err := <-closeDone:
		if err != nil {
			t.Fatalf("Close() failed: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("Close waited for redirect processing after the body closed")
	}
	close(recorder.releaseRecord)
	requireProcessFailure(t, <-requestDone, "REST_HTTP_TRANSPORT_FAILED")
	if calls.Load() != 1 {
		t.Errorf("RoundTrip calls = %d, want 1 before closed dispatcher rejection", calls.Load())
	}
}

func TestConcurrentCloseReturnsImmediatelyWhileOwnerReportsCleanupFailure(t *testing.T) {
	roundTripStarted := make(chan struct{})
	releaseRoundTrip := make(chan struct{})
	transportValue, err := CreateRestHTTPTransport(collectors.Environment{}, RestHTTPTransportOptions{
		RoundTripper: roundTripFunc(func(*http.Request) (*http.Response, error) {
			close(roundTripStarted)
			<-releaseRoundTrip
			return response("ok", 200, nil), nil
		}),
		Close:   func() error { return errors.New("close failed") },
		Destroy: func() error { return errors.New("destroy failed") },
	})
	if err != nil {
		t.Fatal(err)
	}
	transport := transportValue.(*transport)
	requestDone := make(chan error, 1)
	go func() {
		_, requestErr := transport.Request(collectors.HTTPRequest{
			Method: "GET", URL: mustURL(t, "https://api.example.test/"),
		})
		requestDone <- requestErr
	}()
	<-roundTripStarted
	ownerDone := make(chan error, 1)
	go func() { ownerDone <- transport.Close() }()
	for {
		transport.mu.Lock()
		closing := transport.closing
		transport.mu.Unlock()
		if closing {
			break
		}
		time.Sleep(time.Millisecond)
	}
	secondDone := make(chan error, 1)
	go func() { secondDone <- transport.Close() }()
	select {
	case err := <-secondDone:
		if err != nil {
			t.Fatalf("concurrent Close() = %v, want immediate nil", err)
		}
	case <-time.After(time.Second):
		t.Fatal("concurrent Close waited for the owner")
	}
	close(releaseRoundTrip)
	if err := <-requestDone; err != nil {
		t.Fatalf("active Request() failed: %v", err)
	}
	requireProcessFailure(t, <-ownerDone, "REST_HTTP_CLEANUP_FAILED")
}
