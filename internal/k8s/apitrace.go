package k8s

import (
	"net/http"
	"time"

	"github.com/go-logr/logr"
	"k8s.io/client-go/rest"
	"k8s.io/klog/v2"
)

const apiTraceVerbosity = 4

type traceRoundTripper struct {
	rt  http.RoundTripper
	log logr.Logger
}

func (t traceRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	if !t.log.Enabled() {
		return t.rt.RoundTrip(req)
	}
	start := time.Now()
	resp, err := t.rt.RoundTrip(req)
	elapsed := time.Since(start).Round(time.Millisecond)
	if err != nil {
		t.log.Error(err, "kubernetes api call", "method", req.Method, "path", req.URL.Path, "duration", elapsed)
		return resp, err
	}
	t.log.Info("kubernetes api call", "method", req.Method, "path", req.URL.Path, "status", resp.StatusCode, "duration", elapsed)
	return resp, err
}

func WrapAPITrace(cfg *rest.Config) {
	cfg.Wrap(func(rt http.RoundTripper) http.RoundTripper {
		return traceRoundTripper{rt: rt, log: klog.Background().V(apiTraceVerbosity)}
	})
}
