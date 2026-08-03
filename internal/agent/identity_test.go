package agent

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func shrinkRetryDelay(t *testing.T) {
	t.Helper()
	previous := metadataRetryDelay
	metadataRetryDelay = time.Millisecond
	t.Cleanup(func() { metadataRetryDelay = previous })
}

func metadataServer(t *testing.T, handler http.HandlerFunc) string {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	return server.URL
}

func TestResolveIdentityReadsBothEndpoints(t *testing.T) {
	baseURL := metadataServer(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case hostnamePath:
			_, _ = w.Write([]byte("burst-0\n"))
		case instanceIDPath:
			_, _ = w.Write([]byte(" 4711 "))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	})

	identity, err := resolveIdentity(context.Background(), baseURL, http.DefaultClient)
	if err != nil {
		t.Fatalf("resolveIdentity: %v", err)
	}
	if identity.Name != "burst-0" {
		t.Errorf("name = %q, want %q", identity.Name, "burst-0")
	}
	if identity.InstanceID != "4711" {
		t.Errorf("instance id = %q, want %q", identity.InstanceID, "4711")
	}
}

func TestResolveIdentityRetriesAFailingEndpoint(t *testing.T) {
	shrinkRetryDelay(t)
	var hostnameCalls atomic.Int32

	baseURL := metadataServer(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case hostnamePath:
			if hostnameCalls.Add(1) == 1 {
				w.WriteHeader(http.StatusInternalServerError)
				return
			}
			_, _ = w.Write([]byte("burst-1"))
		case instanceIDPath:
			_, _ = w.Write([]byte("99"))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	})

	identity, err := resolveIdentity(context.Background(), baseURL, http.DefaultClient)
	if err != nil {
		t.Fatalf("resolveIdentity: %v", err)
	}
	if identity.Name != "burst-1" {
		t.Errorf("name = %q, want %q", identity.Name, "burst-1")
	}
	if got := hostnameCalls.Load(); got != 2 {
		t.Errorf("hostname requests = %d, want 2", got)
	}
}

func TestResolveIdentityRejectsAnEmptyBody(t *testing.T) {
	shrinkRetryDelay(t)
	baseURL := metadataServer(t, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("   \n"))
	})

	_, err := resolveIdentity(context.Background(), baseURL, http.DefaultClient)
	if err == nil {
		t.Fatal("resolveIdentity must reject an empty metadata response")
	}
	if !strings.Contains(err.Error(), hostnamePath) {
		t.Errorf("error = %q, want it to name the endpoint that failed", err)
	}
}

func TestResolveIdentityStopsWhenTheContextIsCancelled(t *testing.T) {
	shrinkRetryDelay(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var calls atomic.Int32
	baseURL := metadataServer(t, func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		cancel()
		w.WriteHeader(http.StatusInternalServerError)
	})

	_, err := resolveIdentity(ctx, baseURL, http.DefaultClient)
	if err == nil {
		t.Fatal("resolveIdentity must stop once the context is cancelled")
	}
	if got := calls.Load(); got != 1 {
		t.Errorf("metadata requests = %d, want the retry loop to stop after 1", got)
	}
}
