package k8s

import (
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/go-logr/logr"
	"github.com/go-logr/logr/funcr"
)

type fakeRoundTripper struct {
	calls int
}

func (f *fakeRoundTripper) RoundTrip(*http.Request) (*http.Response, error) {
	f.calls++
	return &http.Response{StatusCode: http.StatusOK, Body: http.NoBody}, nil
}

func newReq() *http.Request {
	return &http.Request{Method: http.MethodGet, URL: &url.URL{Path: "/api/v1/nodes"}}
}

func TestTraceRoundTripperStaysSilentBelowVerbosity(t *testing.T) {
	fake := &fakeRoundTripper{}
	var got []string
	sink := funcr.New(func(_, args string) { got = append(got, args) }, funcr.Options{})
	rt := traceRoundTripper{rt: fake, log: sink.V(apiTraceVerbosity)}

	if _, err := rt.RoundTrip(newReq()); err != nil {
		t.Fatalf("round trip: %v", err)
	}
	if fake.calls != 1 {
		t.Fatalf("delegate calls = %d, want 1", fake.calls)
	}
	if len(got) != 0 {
		t.Fatalf("a disabled logger must emit nothing, got %v", got)
	}
}

func TestTraceRoundTripperLogsMethodAndPath(t *testing.T) {
	fake := &fakeRoundTripper{}
	var got []string
	sink := funcr.New(func(_, args string) { got = append(got, args) }, funcr.Options{Verbosity: apiTraceVerbosity})
	rt := traceRoundTripper{rt: fake, log: sink.V(apiTraceVerbosity)}

	if _, err := rt.RoundTrip(newReq()); err != nil {
		t.Fatalf("round trip: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("emitted lines = %d, want 1: %v", len(got), got)
	}
	if !strings.Contains(got[0], http.MethodGet) || !strings.Contains(got[0], "/api/v1/nodes") {
		t.Fatalf("line missing method or path: %q", got[0])
	}
}

func TestTraceRoundTripperDiscardLoggerPassesThrough(t *testing.T) {
	fake := &fakeRoundTripper{}
	rt := traceRoundTripper{rt: fake, log: logr.Discard()}

	if _, err := rt.RoundTrip(newReq()); err != nil {
		t.Fatalf("round trip: %v", err)
	}
	if fake.calls != 1 {
		t.Fatalf("delegate calls = %d, want 1", fake.calls)
	}
}
