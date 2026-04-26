package netbird

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
)

type SetupKey struct {
	ID  string
	Key string
}

type Client struct {
	apiURL string
	token  string
}

func NewClient(apiURL, token string) *Client {
	return &Client{apiURL: apiURL, token: token}
}

func (c *Client) do(ctx context.Context, method, path string, body []byte) (*http.Response, error) {
	var bodyReader io.Reader
	if body != nil {
		bodyReader = bytes.NewReader(body)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.apiURL+path, bodyReader)
	if err != nil {
		return nil, fmt.Errorf("netbird: build request %s %s: %w", method, path, err)
	}
	req.Header.Set("Authorization", "Token "+c.token)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("netbird: %s %s: %w", method, path, err)
	}
	return resp, nil
}

func (c *Client) CreateSetupKey(ctx context.Context, name, group string) (SetupKey, error) {
	payload, err := json.Marshal(map[string]interface{}{
		"name":        name,
		"type":        "one-off",
		"expires_in":  3600,
		"auto_groups": []string{group},
		"usage_limit": 1,
		"ephemeral":   true,
	})
	if err != nil {
		return SetupKey{}, fmt.Errorf("netbird: setup-key create encode: %w", err)
	}
	resp, err := c.do(ctx, http.MethodPost, "/api/setup-keys", payload)
	if err != nil {
		return SetupKey{}, fmt.Errorf("netbird: setup-key create: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return SetupKey{}, fmt.Errorf("netbird: setup-key create: status %d", resp.StatusCode)
	}
	var raw struct {
		ID  string `json:"id"`
		Key string `json:"key"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&raw); err != nil {
		return SetupKey{}, fmt.Errorf("netbird: setup-key create decode: %w", err)
	}
	return SetupKey{ID: raw.ID, Key: raw.Key}, nil
}

func (c *Client) RevokeSetupKey(ctx context.Context, keyID string) error {
	resp, err := c.do(ctx, http.MethodDelete, "/api/setup-keys/"+keyID, nil)
	if err != nil {
		return fmt.Errorf("netbird: setup-key revoke %s: %w", keyID, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("netbird: setup-key revoke %s: status %d", keyID, resp.StatusCode)
	}
	return nil
}

func (c *Client) FindPeerByHostname(ctx context.Context, hostname string) (string, error) {
	resp, err := c.do(ctx, http.MethodGet, "/api/peers", nil)
	if err != nil {
		return "", fmt.Errorf("netbird: peers list: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", fmt.Errorf("netbird: peers list: status %d", resp.StatusCode)
	}
	var peers []struct {
		ID       string `json:"id"`
		Hostname string `json:"hostname"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&peers); err != nil {
		return "", fmt.Errorf("netbird: peers list decode: %w", err)
	}
	for _, p := range peers {
		if p.Hostname == hostname {
			return p.ID, nil
		}
	}
	return "", nil
}

func (c *Client) FindSetupKeyByName(ctx context.Context, name string) (SetupKey, error) {
	resp, err := c.do(ctx, http.MethodGet, "/api/setup-keys", nil)
	if err != nil {
		return SetupKey{}, fmt.Errorf("netbird: setup-keys list: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return SetupKey{}, fmt.Errorf("netbird: setup-keys list: status %d", resp.StatusCode)
	}
	var keys []struct {
		ID   string `json:"id"`
		Key  string `json:"key"`
		Name string `json:"name"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&keys); err != nil {
		return SetupKey{}, fmt.Errorf("netbird: setup-keys list decode: %w", err)
	}
	for _, k := range keys {
		if k.Name == name {
			return SetupKey{ID: k.ID, Key: k.Key}, nil
		}
	}
	return SetupKey{}, nil
}

func (c *Client) DeletePeer(ctx context.Context, peerID string) error {
	resp, err := c.do(ctx, http.MethodDelete, "/api/peers/"+peerID, nil)
	if err != nil {
		return fmt.Errorf("netbird: peer delete %s: %w", peerID, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("netbird: peer delete %s: status %d", peerID, resp.StatusCode)
	}
	return nil
}
