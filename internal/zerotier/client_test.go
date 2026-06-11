package zerotier_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/lucawalz/horizon/internal/zerotier"
)

func TestAuthorize(t *testing.T) {
	var gotMethod, gotPath, gotAuth string
	var gotBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		gotPath = r.URL.Path
		gotAuth = r.Header.Get("Authorization")
		_ = json.NewDecoder(r.Body).Decode(&gotBody)
		_, _ = w.Write([]byte(`{"id":"m456","nodeId":"abc","name":"horizon-burst-x","config":{"authorized":true,"ipAssignments":["10.147.20.5"]}}`))
	}))
	defer srv.Close()

	c := zerotier.NewClient(srv.URL, "test-tok")
	if err := c.Authorize(context.Background(), "nw123", "m456", "horizon-burst-aabb1122"); err != nil {
		t.Fatalf("Authorize: %v", err)
	}
	if gotMethod != http.MethodPost {
		t.Errorf("method = %s, want POST", gotMethod)
	}
	if gotPath != "/api/v1/network/nw123/member/m456" {
		t.Errorf("path = %s", gotPath)
	}
	if gotAuth != "Bearer test-tok" {
		t.Errorf("auth = %q, want %q", gotAuth, "Bearer test-tok")
	}
	cfg, _ := gotBody["config"].(map[string]any)
	if cfg == nil || cfg["authorized"] != true {
		t.Errorf("body = %v, want config.authorized=true", gotBody)
	}
	if gotBody["name"] != "horizon-burst-aabb1122" {
		t.Errorf("body name = %v, want horizon-burst-aabb1122", gotBody["name"])
	}
}

func TestAuthorizeOmitsEmptyName(t *testing.T) {
	var gotBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&gotBody)
		_, _ = w.Write([]byte(`{}`))
	}))
	defer srv.Close()

	c := zerotier.NewClient(srv.URL, "tok")
	if err := c.Authorize(context.Background(), "nw", "m", ""); err != nil {
		t.Fatalf("Authorize: %v", err)
	}
	if _, ok := gotBody["name"]; ok {
		t.Errorf("body must omit name when empty: %v", gotBody)
	}
}

func TestDeauthorize(t *testing.T) {
	var gotBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&gotBody)
		_, _ = w.Write([]byte(`{}`))
	}))
	defer srv.Close()

	c := zerotier.NewClient(srv.URL, "tok")
	if err := c.Deauthorize(context.Background(), "nw", "m"); err != nil {
		t.Fatalf("Deauthorize: %v", err)
	}
	cfg, _ := gotBody["config"].(map[string]any)
	if cfg == nil || cfg["authorized"] != false {
		t.Errorf("body = %v, want config.authorized=false", gotBody)
	}
	if _, ok := gotBody["name"]; ok {
		t.Errorf("deauthorize body must not contain name: %v", gotBody)
	}
}

func TestFindMemberByName(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/api/v1/network/nw/member" {
			t.Errorf("unexpected req: %s %s", r.Method, r.URL.Path)
		}
		_, _ = w.Write([]byte(`[{"id":"m1","name":"other","nodeId":"n1","config":{"authorized":true}},{"id":"m2","name":"horizon-burst-abcd","nodeId":"n2","config":{"authorized":true}}]`))
	}))
	defer srv.Close()

	c := zerotier.NewClient(srv.URL, "tok")

	id, err := c.FindMemberByName(context.Background(), "nw", "horizon-burst-abcd")
	if err != nil {
		t.Fatalf("FindMemberByName: %v", err)
	}
	if id != "n2" {
		t.Errorf("id = %q, want n2", id)
	}

	id, err = c.FindMemberByName(context.Background(), "nw", "missing")
	if err != nil {
		t.Fatalf("FindMemberByName missing: %v", err)
	}
	if id != "" {
		t.Errorf("id = %q, want empty", id)
	}
}

func TestWaitForMemberByName(t *testing.T) {
	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := atomic.AddInt32(&calls, 1)
		if n < 3 {
			_, _ = w.Write([]byte(`[]`))
			return
		}
		_, _ = w.Write([]byte(`[{"id":"mz","name":"horizon-burst-late","nodeId":"nz","config":{"authorized":false}}]`))
	}))
	defer srv.Close()

	c := zerotier.NewClient(srv.URL, "tok")
	id, err := c.WaitForMemberByName(context.Background(), "nw", "horizon-burst-late", 500*time.Millisecond, 25*time.Millisecond)
	if err != nil {
		t.Fatalf("WaitForMemberByName: %v", err)
	}
	if id != "nz" {
		t.Errorf("id = %q, want nz", id)
	}
}

func TestWaitForMemberByNameTimeout(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`[]`))
	}))
	defer srv.Close()
	c := zerotier.NewClient(srv.URL, "tok")
	_, err := c.WaitForMemberByName(context.Background(), "nw", "never", 80*time.Millisecond, 20*time.Millisecond)
	if err == nil || !strings.Contains(err.Error(), "timeout") {
		t.Fatalf("expected timeout error, got %v", err)
	}
}

func TestNon2xxReturnsError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "nope", http.StatusUnauthorized)
	}))
	defer srv.Close()

	c := zerotier.NewClient(srv.URL, "tok")
	if err := c.Authorize(context.Background(), "nw", "m", ""); err == nil || !strings.Contains(err.Error(), "401") {
		t.Errorf("Authorize 401: got %v", err)
	}
	if err := c.Deauthorize(context.Background(), "nw", "m"); err == nil || !strings.Contains(err.Error(), "401") {
		t.Errorf("Deauthorize 401: got %v", err)
	}
	if _, err := c.FindMemberByName(context.Background(), "nw", "x"); err == nil || !strings.Contains(err.Error(), "401") {
		t.Errorf("FindMemberByName 401: got %v", err)
	}
}

func TestRetriesTransientThenSucceeds(t *testing.T) {
	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if atomic.AddInt32(&calls, 1) < 3 {
			http.Error(w, "busy", http.StatusServiceUnavailable)
			return
		}
		_, _ = w.Write([]byte(`{}`))
	}))
	defer srv.Close()

	c := zerotier.NewClient(srv.URL, "tok")
	if err := c.Authorize(context.Background(), "nw", "m", "horizon-burst-x"); err != nil {
		t.Fatalf("Authorize after transient 503s: %v", err)
	}
	if got := atomic.LoadInt32(&calls); got != 3 {
		t.Errorf("calls = %d, want 3", got)
	}
}

func TestContextDeadlineStopsRetries(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "busy", http.StatusServiceUnavailable)
	}))
	defer srv.Close()

	c := zerotier.NewClient(srv.URL, "tok")
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	err := c.Authorize(ctx, "nw", "m", "")
	if err == nil {
		t.Fatal("expected error when context deadline exceeded")
	}
}

func TestDeleteMember(t *testing.T) {
	var gotMethod, gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		gotPath = r.URL.Path
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()

	c := zerotier.NewClient(srv.URL, "tok")
	if err := c.DeleteMember(context.Background(), "nw123", "m456"); err != nil {
		t.Fatalf("DeleteMember: %v", err)
	}
	if gotMethod != http.MethodDelete {
		t.Errorf("method = %s, want DELETE", gotMethod)
	}
	if gotPath != "/api/v1/network/nw123/member/m456" {
		t.Errorf("path = %s", gotPath)
	}
}

func TestAuthHeaderFormat(t *testing.T) {
	var seen []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = append(seen, r.Header.Get("Authorization"))
		if r.Method == http.MethodGet {
			_, _ = w.Write([]byte(`[]`))
			return
		}
		_, _ = w.Write([]byte(`{}`))
	}))
	defer srv.Close()

	c := zerotier.NewClient(srv.URL, "abc")
	ctx := context.Background()
	_ = c.Authorize(ctx, "nw", "m", "")
	_ = c.Deauthorize(ctx, "nw", "m")
	_, _ = c.FindMemberByName(ctx, "nw", "x")
	if len(seen) == 0 {
		t.Fatal("no headers captured")
	}
	for _, h := range seen {
		if h != "Bearer abc" {
			t.Errorf("header = %q, want %q", h, "Bearer abc")
		}
	}
}
