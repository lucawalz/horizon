package netbird_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/lucawalz/horizon/internal/netbird"
)

func TestCreateSetupKey(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/api/setup-keys" {
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
			http.Error(w, "bad", http.StatusBadRequest)
			return
		}
		if !strings.HasPrefix(r.Header.Get("Authorization"), "Token ") {
			t.Errorf("missing Token auth header")
		}
		var body map[string]interface{}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Errorf("decode body: %v", err)
		}
		if body["name"] != "burst-test" {
			t.Errorf("name = %v, want burst-test", body["name"])
		}
		if body["type"] != "one-off" {
			t.Errorf("type = %v, want one-off", body["type"])
		}
		if body["expires_in"] != float64(3600) {
			t.Errorf("expires_in = %v, want 3600", body["expires_in"])
		}
		groups, _ := body["auto_groups"].([]interface{})
		if len(groups) != 1 || groups[0] != "burst-nodes" {
			t.Errorf("auto_groups = %v, want [burst-nodes]", groups)
		}
		if body["usage_limit"] != float64(1) {
			t.Errorf("usage_limit = %v, want 1", body["usage_limit"])
		}
		if body["ephemeral"] != true {
			t.Errorf("ephemeral = %v, want true", body["ephemeral"])
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]string{"id": "key-id", "key": "key-secret"})
	}))
	defer srv.Close()

	c := netbird.NewClient(srv.URL, "test-token")
	sk, err := c.CreateSetupKey(context.Background(), "burst-test", "burst-nodes")
	if err != nil {
		t.Fatalf("CreateSetupKey: %v", err)
	}
	if sk.ID != "key-id" {
		t.Errorf("ID = %q, want key-id", sk.ID)
	}
	if sk.Key != "key-secret" {
		t.Errorf("Key = %q, want key-secret", sk.Key)
	}
}

func TestRevokeSetupKey(t *testing.T) {
	var gotMethod, gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		gotPath = r.URL.Path
		if !strings.HasPrefix(r.Header.Get("Authorization"), "Token ") {
			t.Errorf("missing Token auth header")
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()

	c := netbird.NewClient(srv.URL, "test-token")
	if err := c.RevokeSetupKey(context.Background(), "test-key-id"); err != nil {
		t.Fatalf("RevokeSetupKey: %v", err)
	}
	if gotMethod != http.MethodDelete {
		t.Errorf("method = %s, want DELETE", gotMethod)
	}
	if gotPath != "/api/setup-keys/test-key-id" {
		t.Errorf("path = %s, want /api/setup-keys/test-key-id", gotPath)
	}
}

func TestDeletePeer(t *testing.T) {
	var gotMethod, gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		gotPath = r.URL.Path
		if !strings.HasPrefix(r.Header.Get("Authorization"), "Token ") {
			t.Errorf("missing Token auth header")
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()

	c := netbird.NewClient(srv.URL, "test-token")
	if err := c.DeletePeer(context.Background(), "test-peer-id"); err != nil {
		t.Fatalf("DeletePeer: %v", err)
	}
	if gotMethod != http.MethodDelete {
		t.Errorf("method = %s, want DELETE", gotMethod)
	}
	if gotPath != "/api/peers/test-peer-id" {
		t.Errorf("path = %s, want /api/peers/test-peer-id", gotPath)
	}
}

func TestFindPeerByHostname(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/api/peers" {
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
			http.Error(w, "bad", http.StatusBadRequest)
			return
		}
		if !strings.HasPrefix(r.Header.Get("Authorization"), "Token ") {
			t.Errorf("missing Token auth header")
		}
		peers := []map[string]interface{}{
			{"id": "peer-1", "hostname": "burst-abc"},
			{"id": "peer-2", "hostname": "other"},
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(peers)
	}))
	defer srv.Close()

	c := netbird.NewClient(srv.URL, "test-token")

	id, err := c.FindPeerByHostname(context.Background(), "burst-abc")
	if err != nil {
		t.Fatalf("FindPeerByHostname: %v", err)
	}
	if id != "peer-1" {
		t.Errorf("id = %q, want peer-1", id)
	}

	id, err = c.FindPeerByHostname(context.Background(), "nonexistent")
	if err != nil {
		t.Fatalf("FindPeerByHostname (not found): %v", err)
	}
	if id != "" {
		t.Errorf("id = %q, want empty string", id)
	}
}

func TestFindSetupKeyByName(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/api/setup-keys" {
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
			http.Error(w, "bad", http.StatusBadRequest)
			return
		}
		if !strings.HasPrefix(r.Header.Get("Authorization"), "Token ") {
			t.Errorf("missing Token auth header")
		}
		keys := []map[string]interface{}{
			{"id": "key-id", "key": "key-val", "name": "burst-abc"},
			{"id": "key-id-2", "key": "key-val-2", "name": "other"},
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(keys)
	}))
	defer srv.Close()

	c := netbird.NewClient(srv.URL, "test-token")

	sk, err := c.FindSetupKeyByName(context.Background(), "burst-abc")
	if err != nil {
		t.Fatalf("FindSetupKeyByName: %v", err)
	}
	if sk.ID != "key-id" {
		t.Errorf("ID = %q, want key-id", sk.ID)
	}

	sk, err = c.FindSetupKeyByName(context.Background(), "nonexistent")
	if err != nil {
		t.Fatalf("FindSetupKeyByName (not found): %v", err)
	}
	if sk.ID != "" {
		t.Errorf("ID = %q, want empty string", sk.ID)
	}
}

func TestClientAuthHeader(t *testing.T) {
	var headers []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		headers = append(headers, r.Header.Get("Authorization"))
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/setup-keys":
			if r.Method == http.MethodGet {
				_ = json.NewEncoder(w).Encode([]interface{}{})
			} else {
				_ = json.NewEncoder(w).Encode(map[string]string{"id": "x", "key": "y"})
			}
		case "/api/peers":
			_ = json.NewEncoder(w).Encode([]interface{}{})
		default:
			w.WriteHeader(http.StatusNoContent)
		}
	}))
	defer srv.Close()

	c := netbird.NewClient(srv.URL, "abc")
	ctx := context.Background()
	_, _ = c.CreateSetupKey(ctx, "n", "g")
	_ = c.RevokeSetupKey(ctx, "k")
	_, _ = c.FindPeerByHostname(ctx, "h")
	_ = c.DeletePeer(ctx, "p")
	_, _ = c.FindSetupKeyByName(ctx, "n")

	for _, h := range headers {
		if !strings.HasPrefix(h, "Token ") {
			t.Errorf("Authorization header = %q, want prefix 'Token '", h)
		}
	}
	if len(headers) == 0 {
		t.Error("no requests captured")
	}
}

func TestNon2xxReturnsError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
	}))
	defer srv.Close()

	c := netbird.NewClient(srv.URL, "test-token")
	ctx := context.Background()

	if _, err := c.CreateSetupKey(ctx, "n", "g"); err == nil {
		t.Error("CreateSetupKey: expected error for 401")
	}
	if err := c.RevokeSetupKey(ctx, "k"); err == nil {
		t.Error("RevokeSetupKey: expected error for 401")
	}
	if _, err := c.FindPeerByHostname(ctx, "h"); err == nil {
		t.Error("FindPeerByHostname: expected error for 401")
	}
	if err := c.DeletePeer(ctx, "p"); err == nil {
		t.Error("DeletePeer: expected error for 401")
	}
	if _, err := c.FindSetupKeyByName(ctx, "n"); err == nil {
		t.Error("FindSetupKeyByName: expected error for 401")
	}
}

var _ = bytes.NewBuffer
