package k8s

import (
	"fmt"
	"net/http"
	"sync/atomic"
	"time"

	"k8s.io/client-go/rest"
)

var apiTraceSink atomic.Pointer[func(string)]

func SetAPITrace(sink func(string)) (restore func()) {
	prev := apiTraceSink.Load()
	apiTraceSink.Store(&sink)
	return func() { apiTraceSink.Store(prev) }
}

type traceRoundTripper struct {
	rt http.RoundTripper
}

func (t traceRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	sinkPtr := apiTraceSink.Load()
	if sinkPtr == nil {
		return t.rt.RoundTrip(req)
	}
	sink := *sinkPtr
	if sink == nil {
		return t.rt.RoundTrip(req)
	}
	start := time.Now()
	resp, err := t.rt.RoundTrip(req)
	dur := time.Since(start).Round(time.Millisecond)
	if err != nil {
		sink(fmt.Sprintf("%s %s → error: %v (%s)", req.Method, req.URL.Path, err, dur))
		return resp, err
	}
	sink(fmt.Sprintf("%s %s → %d (%s)", req.Method, req.URL.Path, resp.StatusCode, dur))
	return resp, err
}

func WrapAPITrace(cfg *rest.Config) {
	cfg.Wrap(func(rt http.RoundTripper) http.RoundTripper {
		return traceRoundTripper{rt: rt}
	})
}
