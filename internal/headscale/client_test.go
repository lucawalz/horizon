package headscale_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/lucawalz/horizon/internal/headscale"
)

func TestCreatePreAuthKey(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasPrefix(r.Header.Get("Authorization"), "Bearer ") {
			t.Errorf("missing Bearer auth header")
		}
		w.Header().Set("Content-Type", "application/json")
		if r.Method == http.MethodGet && r.URL.Path == "/api/v1/user" {
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"users": []map[string]string{{"id": "42", "name": "default"}},
			})
			return
		}
		if r.Method != http.MethodPost || r.URL.Path != "/api/v1/preauthkey" {
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
			http.Error(w, "bad", http.StatusBadRequest)
			return
		}
		var body map[string]interface{}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Errorf("decode body: %v", err)
		}
		if body["user"] != "42" {
			t.Errorf("user = %v, want 42 (numeric id)", body["user"])
		}
		if body["reusable"] != false {
			t.Errorf("reusable = %v, want false", body["reusable"])
		}
		if body["ephemeral"] != true {
			t.Errorf("ephemeral = %v, want true", body["ephemeral"])
		}
		if body["expiration"] == "" {
			t.Error("expiration must not be empty")
		}
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"preAuthKey": map[string]interface{}{
				"id":   "key-id",
				"key":  "key-secret",
				"user": map[string]string{"name": "default"},
			},
		})
	}))
	defer srv.Close()

	c := headscale.NewClient(srv.URL, "test-apikey")
	key, err := c.CreatePreAuthKey(context.Background(), "default")
	if err != nil {
		t.Fatalf("CreatePreAuthKey: %v", err)
	}
	if key.ID != "key-id" {
		t.Errorf("ID = %q, want key-id", key.ID)
	}
	if key.Key != "key-secret" {
		t.Errorf("Key = %q, want key-secret", key.Key)
	}
	if key.User != "default" {
		t.Errorf("User = %q, want default", key.User)
	}
}

func TestRevokePreAuthKey(t *testing.T) {
	var gotMethod, gotPath string
	var gotBody map[string]string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasPrefix(r.Header.Get("Authorization"), "Bearer ") {
			t.Errorf("missing Bearer auth header")
		}
		if r.Method == http.MethodGet && r.URL.Path == "/api/v1/user" {
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"users": []map[string]string{{"id": "42", "name": "default"}},
			})
			return
		}
		gotMethod = r.Method
		gotPath = r.URL.Path
		_ = json.NewDecoder(r.Body).Decode(&gotBody)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	c := headscale.NewClient(srv.URL, "test-apikey")
	if err := c.RevokePreAuthKey(context.Background(), "default", "key-secret"); err != nil {
		t.Fatalf("RevokePreAuthKey: %v", err)
	}
	if gotMethod != http.MethodDelete {
		t.Errorf("method = %s, want DELETE", gotMethod)
	}
	if gotPath != "/api/v1/preauthkey" {
		t.Errorf("path = %s, want /api/v1/preauthkey", gotPath)
	}
	if gotBody["user"] != "42" {
		t.Errorf("body user = %q, want 42 (numeric id)", gotBody["user"])
	}
	if gotBody["key"] != "key-secret" {
		t.Errorf("body key = %q, want key-secret", gotBody["key"])
	}
}

func TestDeleteNode(t *testing.T) {
	var gotMethod, gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		gotPath = r.URL.Path
		if !strings.HasPrefix(r.Header.Get("Authorization"), "Bearer ") {
			t.Errorf("missing Bearer auth header")
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	c := headscale.NewClient(srv.URL, "test-apikey")
	if err := c.DeleteNode(context.Background(), "node-42"); err != nil {
		t.Fatalf("DeleteNode: %v", err)
	}
	if gotMethod != http.MethodDelete {
		t.Errorf("method = %s, want DELETE", gotMethod)
	}
	if gotPath != "/api/v1/machine/node-42" {
		t.Errorf("path = %s, want /api/v1/machine/node-42", gotPath)
	}
}

func TestFindNodeByHostname(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/api/v1/machine" {
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
			http.Error(w, "bad", http.StatusBadRequest)
			return
		}
		if !strings.HasPrefix(r.Header.Get("Authorization"), "Bearer ") {
			t.Errorf("missing Bearer auth header")
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"machines": []map[string]string{
				{"id": "node-1", "hostname": "burst-abc"},
				{"id": "node-2", "hostname": "other"},
			},
		})
	}))
	defer srv.Close()

	c := headscale.NewClient(srv.URL, "test-apikey")

	id, err := c.FindNodeByHostname(context.Background(), "burst-abc")
	if err != nil {
		t.Fatalf("FindNodeByHostname: %v", err)
	}
	if id != "node-1" {
		t.Errorf("id = %q, want node-1", id)
	}

	id, err = c.FindNodeByHostname(context.Background(), "nonexistent")
	if err != nil {
		t.Fatalf("FindNodeByHostname (not found): %v", err)
	}
	if id != "" {
		t.Errorf("id = %q, want empty string", id)
	}
}

func TestListPreAuthKeys(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/api/v1/preauthkey" {
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
			http.Error(w, "bad", http.StatusBadRequest)
			return
		}
		if r.URL.Query().Get("user") != "default" {
			t.Errorf("user query param = %q, want default", r.URL.Query().Get("user"))
		}
		if !strings.HasPrefix(r.Header.Get("Authorization"), "Bearer ") {
			t.Errorf("missing Bearer auth header")
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"preAuthKeys": []map[string]string{
				{"id": "key-1", "key": "key-val-1", "user": "default"},
				{"id": "key-2", "key": "key-val-2", "user": "default"},
			},
		})
	}))
	defer srv.Close()

	c := headscale.NewClient(srv.URL, "test-apikey")
	keys, err := c.ListPreAuthKeys(context.Background(), "default")
	if err != nil {
		t.Fatalf("ListPreAuthKeys: %v", err)
	}
	if len(keys) != 2 {
		t.Fatalf("len = %d, want 2", len(keys))
	}
	if keys[0].ID != "key-1" {
		t.Errorf("keys[0].ID = %q, want key-1", keys[0].ID)
	}
}

func TestClientAuthHeader(t *testing.T) {
	var headers []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		headers = append(headers, r.Header.Get("Authorization"))
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.URL.Path == "/api/v1/user" && r.Method == http.MethodGet:
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"users": []map[string]string{{"id": "42", "name": "default"}},
			})
		case r.URL.Path == "/api/v1/preauthkey" && r.Method == http.MethodGet:
			_ = json.NewEncoder(w).Encode(map[string]interface{}{"preAuthKeys": []interface{}{}})
		case r.URL.Path == "/api/v1/preauthkey" && r.Method == http.MethodPost:
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"preAuthKey": map[string]interface{}{
					"id":   "x",
					"key":  "y",
					"user": map[string]string{"name": "default"},
				},
			})
		case r.URL.Path == "/api/v1/machine":
			_ = json.NewEncoder(w).Encode(map[string]interface{}{"machines": []interface{}{}})
		default:
			w.WriteHeader(http.StatusOK)
		}
	}))
	defer srv.Close()

	c := headscale.NewClient(srv.URL, "abc")
	ctx := context.Background()
	_, _ = c.CreatePreAuthKey(ctx, "default")
	_ = c.RevokePreAuthKey(ctx, "default", "k")
	_, _ = c.FindNodeByHostname(ctx, "h")
	_ = c.DeleteNode(ctx, "p")
	_, _ = c.ListPreAuthKeys(ctx, "default")

	for _, h := range headers {
		if !strings.HasPrefix(h, "Bearer ") {
			t.Errorf("Authorization header = %q, want prefix 'Bearer '", h)
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

	c := headscale.NewClient(srv.URL, "test-apikey")
	ctx := context.Background()

	if _, err := c.CreatePreAuthKey(ctx, "default"); err == nil {
		t.Error("CreatePreAuthKey: expected error for 401")
	}
	if err := c.RevokePreAuthKey(ctx, "default", "k"); err == nil {
		t.Error("RevokePreAuthKey: expected error for 401")
	}
	if _, err := c.FindNodeByHostname(ctx, "h"); err == nil {
		t.Error("FindNodeByHostname: expected error for 401")
	}
	if err := c.DeleteNode(ctx, "p"); err == nil {
		t.Error("DeleteNode: expected error for 401")
	}
	if _, err := c.ListPreAuthKeys(ctx, "default"); err == nil {
		t.Error("ListPreAuthKeys: expected error for 401")
	}
}
