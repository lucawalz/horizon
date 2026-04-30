package headscale

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

type PreAuthKey struct {
	ID   string
	Key  string
	User string
}

type Node struct {
	ID       string
	Hostname string
}

type Client struct {
	apiURL string
	apiKey string
}

func NewClient(apiURL, apiKey string) *Client {
	return &Client{apiURL: apiURL, apiKey: apiKey}
}

func (c *Client) do(ctx context.Context, method, path string, body []byte) (*http.Response, error) {
	var bodyReader io.Reader
	if body != nil {
		bodyReader = bytes.NewReader(body)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.apiURL+path, bodyReader)
	if err != nil {
		return nil, fmt.Errorf("headscale: build request %s %s: %w", method, path, err)
	}
	req.Header.Set("Authorization", "Bearer "+c.apiKey)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("headscale: %s %s: %w", method, path, err)
	}
	return resp, nil
}

func (c *Client) getUserID(ctx context.Context, name string) (string, error) {
	resp, err := c.do(ctx, http.MethodGet, "/api/v1/user", nil)
	if err != nil {
		return "", fmt.Errorf("headscale: user list: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("headscale: user list: status %d: %s", resp.StatusCode, bytes.TrimSpace(body))
	}
	var raw struct {
		Users []struct {
			ID   string `json:"id"`
			Name string `json:"name"`
		} `json:"users"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&raw); err != nil {
		return "", fmt.Errorf("headscale: user list decode: %w", err)
	}
	for _, u := range raw.Users {
		if u.Name == name {
			return u.ID, nil
		}
	}
	return "", fmt.Errorf("headscale: user %s not found", name)
}

func (c *Client) CreatePreAuthKey(ctx context.Context, user string) (PreAuthKey, error) {
	userID, err := c.getUserID(ctx, user)
	if err != nil {
		return PreAuthKey{}, fmt.Errorf("headscale: pre-auth-key create: %w", err)
	}
	expiration := time.Now().Add(time.Hour).UTC().Format(time.RFC3339)
	payload, err := json.Marshal(map[string]interface{}{
		"user":       userID,
		"reusable":   false,
		"ephemeral":  true,
		"expiration": expiration,
	})
	if err != nil {
		return PreAuthKey{}, fmt.Errorf("headscale: pre-auth-key create encode: %w", err)
	}
	resp, err := c.do(ctx, http.MethodPost, "/api/v1/preauthkey", payload)
	if err != nil {
		return PreAuthKey{}, fmt.Errorf("headscale: pre-auth-key create: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(resp.Body)
		return PreAuthKey{}, fmt.Errorf("headscale: pre-auth-key create: status %d: %s", resp.StatusCode, bytes.TrimSpace(body))
	}
	var raw struct {
		PreAuthKey struct {
			ID   string `json:"id"`
			Key  string `json:"key"`
			User struct {
				Name string `json:"name"`
			} `json:"user"`
		} `json:"preAuthKey"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&raw); err != nil {
		return PreAuthKey{}, fmt.Errorf("headscale: pre-auth-key create decode: %w", err)
	}
	return PreAuthKey{ID: raw.PreAuthKey.ID, Key: raw.PreAuthKey.Key, User: raw.PreAuthKey.User.Name}, nil
}

func (c *Client) RevokePreAuthKey(ctx context.Context, user, key string) error {
	userID, err := c.getUserID(ctx, user)
	if err != nil {
		return fmt.Errorf("headscale: pre-auth-key revoke: %w", err)
	}
	payload, err := json.Marshal(map[string]string{
		"user": userID,
		"key":  key,
	})
	if err != nil {
		return fmt.Errorf("headscale: pre-auth-key revoke encode: %w", err)
	}
	resp, err := c.do(ctx, http.MethodDelete, "/api/v1/preauthkey", payload)
	if err != nil {
		return fmt.Errorf("headscale: pre-auth-key revoke: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("headscale: pre-auth-key revoke: status %d", resp.StatusCode)
	}
	return nil
}

func (c *Client) ListPreAuthKeys(ctx context.Context, user string) ([]PreAuthKey, error) {
	resp, err := c.do(ctx, http.MethodGet, "/api/v1/preauthkey?user="+user, nil)
	if err != nil {
		return nil, fmt.Errorf("headscale: pre-auth-keys list: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("headscale: pre-auth-keys list: status %d", resp.StatusCode)
	}
	var raw struct {
		PreAuthKeys []struct {
			ID   string `json:"id"`
			Key  string `json:"key"`
			User string `json:"user"`
		} `json:"preAuthKeys"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&raw); err != nil {
		return nil, fmt.Errorf("headscale: pre-auth-keys list decode: %w", err)
	}
	keys := make([]PreAuthKey, len(raw.PreAuthKeys))
	for i, k := range raw.PreAuthKeys {
		keys[i] = PreAuthKey{ID: k.ID, Key: k.Key, User: k.User}
	}
	return keys, nil
}

func (c *Client) FindNodeByHostname(ctx context.Context, hostname string) (string, error) {
	resp, err := c.do(ctx, http.MethodGet, "/api/v1/machine", nil)
	if err != nil {
		return "", fmt.Errorf("headscale: machines list: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", fmt.Errorf("headscale: machines list: status %d", resp.StatusCode)
	}
	var raw struct {
		Machines []struct {
			ID       string `json:"id"`
			Hostname string `json:"hostname"`
		} `json:"machines"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&raw); err != nil {
		return "", fmt.Errorf("headscale: machines list decode: %w", err)
	}
	for _, m := range raw.Machines {
		if m.Hostname == hostname {
			return m.ID, nil
		}
	}
	return "", nil
}

func (c *Client) DeleteNode(ctx context.Context, nodeID string) error {
	resp, err := c.do(ctx, http.MethodDelete, "/api/v1/machine/"+nodeID, nil)
	if err != nil {
		return fmt.Errorf("headscale: node delete %s: %w", nodeID, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("headscale: node delete %s: status %d", nodeID, resp.StatusCode)
	}
	return nil
}
