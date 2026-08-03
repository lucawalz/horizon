package agent

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

const (
	hostnamePath     = "/hostname"
	instanceIDPath   = "/instance-id"
	metadataAttempts = 30
	metadataBodyCap  = 4 << 10
)

var metadataRetryDelay = 2 * time.Second

type Identity struct {
	Name       string
	InstanceID string
}

func resolveIdentity(ctx context.Context, baseURL string, client *http.Client) (Identity, error) {
	name, err := fetchMetadata(ctx, baseURL+hostnamePath, client)
	if err != nil {
		return Identity{}, err
	}
	instanceID, err := fetchMetadata(ctx, baseURL+instanceIDPath, client)
	if err != nil {
		return Identity{}, err
	}
	return Identity{Name: name, InstanceID: instanceID}, nil
}

func fetchMetadata(ctx context.Context, url string, client *http.Client) (string, error) {
	var lastErr error
	for attempt := range metadataAttempts {
		if attempt > 0 {
			if err := waitBeforeRetry(ctx, metadataRetryDelay); err != nil {
				return "", fmt.Errorf("agent: read metadata %q: %w", url, err)
			}
		}
		value, err := readMetadata(ctx, url, client)
		if err == nil {
			return value, nil
		}
		lastErr = err
	}
	return "", fmt.Errorf("agent: read metadata %q after %d attempts: %w", url, metadataAttempts, lastErr)
}

func readMetadata(ctx context.Context, url string, client *http.Client) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", err
	}
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("metadata service answered %s", resp.Status)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, metadataBodyCap))
	if err != nil {
		return "", err
	}
	value := strings.TrimSpace(string(body))
	if value == "" {
		return "", errors.New("metadata service answered with an empty body")
	}
	return value, nil
}
