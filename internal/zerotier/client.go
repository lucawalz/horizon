package zerotier

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

const (
	apiBaseURL        = "https://api.zerotier.com"
	requestTimeout    = 12 * time.Second
	maxRetries        = 3
	retryBackoffBase  = 500 * time.Millisecond
	statusTooManyReqs = 429
)

type Member struct {
	ID              string
	NodeID          string
	Name            string
	Authorized      bool
	IPAssignments   []string
	PhysicalAddress string
}

type Client struct {
	apiURL string
	token  string
	http   *http.Client
}

func NewClient(apiURL, token string) *Client {
	base := apiURL
	if base == "" {
		base = apiBaseURL
	}
	return &Client{apiURL: base, token: token, http: http.DefaultClient}
}

func (c *Client) doOnce(ctx context.Context, method, path string, body []byte) (*http.Response, error) {
	attemptCtx, cancel := context.WithTimeout(ctx, requestTimeout)
	var br io.Reader
	if body != nil {
		br = bytes.NewReader(body)
	}
	req, err := http.NewRequestWithContext(attemptCtx, method, c.apiURL+path, br)
	if err != nil {
		cancel()
		return nil, fmt.Errorf("zerotier: build %s %s: %w", method, path, err)
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.http.Do(req)
	if err != nil {
		cancel()
		return nil, fmt.Errorf("zerotier: %s %s: %w", method, path, err)
	}
	resp.Body = &cancelOnCloseBody{ReadCloser: resp.Body, cancel: cancel}
	return resp, nil
}

func (c *Client) do(ctx context.Context, method, path string, body []byte) (*http.Response, error) {
	var lastErr error
	for attempt := 0; attempt < maxRetries; attempt++ {
		if attempt > 0 {
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(retryBackoffBase << (attempt - 1)):
			}
		}
		resp, err := c.doOnce(ctx, method, path, body)
		if err != nil {
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			lastErr = err
			continue
		}
		if isRetryableStatus(resp.StatusCode) {
			resp.Body.Close()
			lastErr = fmt.Errorf("zerotier: %s %s: status %d", method, path, resp.StatusCode)
			continue
		}
		return resp, nil
	}
	return nil, lastErr
}

func isRetryableStatus(code int) bool {
	return code == statusTooManyReqs || code >= 500
}

type cancelOnCloseBody struct {
	io.ReadCloser
	cancel context.CancelFunc
}

func (b *cancelOnCloseBody) Close() error {
	err := b.ReadCloser.Close()
	b.cancel()
	return err
}

func (c *Client) setAuthorized(ctx context.Context, networkID, memberID, name string, authorized bool) error {
	member := map[string]any{"config": map[string]any{"authorized": authorized}}
	if name != "" {
		member["name"] = name
	}
	payload, err := json.Marshal(member)
	if err != nil {
		return fmt.Errorf("zerotier: encode authorize payload: %w", err)
	}
	resp, err := c.do(ctx, http.MethodPost, "/api/v1/network/"+networkID+"/member/"+memberID, payload)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("zerotier: set-authorized %s/%s: status %d: %s", networkID, memberID, resp.StatusCode, bytes.TrimSpace(body))
	}
	return nil
}

func (c *Client) Authorize(ctx context.Context, networkID, memberID, name string) error {
	return c.setAuthorized(ctx, networkID, memberID, name, true)
}

func (c *Client) Deauthorize(ctx context.Context, networkID, memberID string) error {
	return c.setAuthorized(ctx, networkID, memberID, "", false)
}

func (c *Client) ListMembers(ctx context.Context, networkID string) ([]Member, error) {
	resp, err := c.do(ctx, http.MethodGet, "/api/v1/network/"+networkID+"/member", nil)
	if err != nil {
		return nil, fmt.Errorf("zerotier: members list: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("zerotier: members list: status %d", resp.StatusCode)
	}
	var raw []struct {
		ID              string `json:"id"`
		NodeID          string `json:"nodeId"`
		Name            string `json:"name"`
		PhysicalAddress string `json:"physicalAddress"`
		Config          struct {
			Authorized    bool     `json:"authorized"`
			IPAssignments []string `json:"ipAssignments"`
		} `json:"config"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&raw); err != nil {
		return nil, fmt.Errorf("zerotier: members decode: %w", err)
	}
	out := make([]Member, len(raw))
	for i, m := range raw {
		out[i] = Member{ID: m.ID, NodeID: m.NodeID, Name: m.Name, Authorized: m.Config.Authorized, IPAssignments: m.Config.IPAssignments, PhysicalAddress: m.PhysicalAddress}
	}
	return out, nil
}

func (c *Client) DeleteMember(ctx context.Context, networkID, memberID string) error {
	resp, err := c.do(ctx, http.MethodDelete, "/api/v1/network/"+networkID+"/member/"+memberID, nil)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("zerotier: delete-member %s/%s: status %d: %s", networkID, memberID, resp.StatusCode, bytes.TrimSpace(body))
	}
	return nil
}

func (c *Client) FindMemberByName(ctx context.Context, networkID, name string) (string, error) {
	members, err := c.ListMembers(ctx, networkID)
	if err != nil {
		return "", err
	}
	for _, m := range members {
		if m.Name == name {
			return m.NodeID, nil
		}
	}
	return "", nil
}

func (c *Client) WaitForMemberByName(ctx context.Context, networkID, name string, timeout, poll time.Duration) (string, error) {
	if poll <= 0 {
		poll = 2 * time.Second
	}
	if timeout <= 0 {
		timeout = 2 * time.Minute
	}
	deadlineCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	ticker := time.NewTicker(poll)
	defer ticker.Stop()
	var lastErr error
	for {
		id, err := c.FindMemberByName(deadlineCtx, networkID, name)
		if err == nil && id != "" {
			return id, nil
		}
		if err != nil {
			lastErr = err
		}
		select {
		case <-deadlineCtx.Done():
			if lastErr != nil {
				return "", fmt.Errorf("zerotier: wait member %s: timeout: %w", name, lastErr)
			}
			return "", fmt.Errorf("zerotier: wait member %s: timeout after %s", name, timeout)
		case <-ticker.C:
		}
	}
}

func (c *Client) FindMemberByIP(ctx context.Context, networkID, ip string) (string, error) {
	members, err := c.ListMembers(ctx, networkID)
	if err != nil {
		return "", err
	}
	for _, m := range members {
		host, _, _ := strings.Cut(m.PhysicalAddress, "/")
		if host == "" {
			host = m.PhysicalAddress
		}
		if host == ip {
			return m.NodeID, nil
		}
	}
	return "", nil
}

func (c *Client) WaitForMemberByIP(ctx context.Context, networkID, ip string, timeout, poll time.Duration) (string, error) {
	if poll <= 0 {
		poll = 2 * time.Second
	}
	if timeout <= 0 {
		timeout = 2 * time.Minute
	}
	deadlineCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	ticker := time.NewTicker(poll)
	defer ticker.Stop()
	var lastErr error
	for {
		id, err := c.FindMemberByIP(deadlineCtx, networkID, ip)
		if err == nil && id != "" {
			return id, nil
		}
		if err != nil {
			lastErr = err
		}
		select {
		case <-deadlineCtx.Done():
			if lastErr != nil {
				return "", fmt.Errorf("zerotier: wait member ip %s: timeout: %w", ip, lastErr)
			}
			return "", fmt.Errorf("zerotier: wait member ip %s: timeout after %s", ip, timeout)
		case <-ticker.C:
		}
	}
}
