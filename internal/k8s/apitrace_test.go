package k8s

import (
	"net/http"
	"net/url"
	"strings"
	"testing"
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

func TestTraceRoundTripperNoSink(t *testing.T) {
	fake := &fakeRoundTripper{}
	rt := traceRoundTripper{rt: fake}

	var got []string
	if _, err := rt.RoundTrip(newReq()); err != nil {
		t.Fatalf("round trip: %v", err)
	}
	if fake.calls != 1 {
		t.Fatalf("delegate calls = %d, want 1", fake.calls)
	}
	if len(got) != 0 {
		t.Fatalf("no sink should emit nothing, got %v", got)
	}
}

func TestTraceRoundTripperEmitsAndRestores(t *testing.T) {
	fake := &fakeRoundTripper{}
	rt := traceRoundTripper{rt: fake}

	var got []string
	restore := SetAPITrace(func(line string) { got = append(got, line) })

	if _, err := rt.RoundTrip(newReq()); err != nil {
		t.Fatalf("round trip: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("emitted lines = %d, want 1: %v", len(got), got)
	}
	if !strings.Contains(got[0], http.MethodGet) || !strings.Contains(got[0], "/api/v1/nodes") {
		t.Fatalf("line missing method or path: %q", got[0])
	}

	restore()
	if _, err := rt.RoundTrip(newReq()); err != nil {
		t.Fatalf("round trip after restore: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("restore should stop emission, got %v", got)
	}
}
