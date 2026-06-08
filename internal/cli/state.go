package cli

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
)

type BurstState struct {
	BurstID          string `json:"burst_id"`
	Hostname         string `json:"hostname"`
	ZeroTierMemberID string `json:"zerotier_member_id"`
	HetznerServerID  string `json:"hetzner_server_id"`
}

var burstIDPattern = regexp.MustCompile(`^[a-f0-9]{4,16}$`)

func DefaultStateDir() (string, error) {
	if x := os.Getenv("XDG_STATE_HOME"); x != "" {
		return filepath.Join(x, "horizon"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("state dir: home: %w", err)
	}
	return filepath.Join(home, ".local", "state", "horizon"), nil
}

func WriteState(dir string, st BurstState) error {
	if !burstIDPattern.MatchString(st.BurstID) {
		return fmt.Errorf("state: invalid burst_id %q", st.BurstID)
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("state: mkdir %s: %w", dir, err)
	}
	buf, err := json.MarshalIndent(st, "", "  ")
	if err != nil {
		return fmt.Errorf("state: marshal: %w", err)
	}
	path := filepath.Join(dir, st.BurstID+".json")
	if err := os.WriteFile(path, buf, 0o600); err != nil {
		return fmt.Errorf("state: write %s: %w", path, err)
	}
	return nil
}

func ReadState(dir, burstID string) (BurstState, error) {
	if !burstIDPattern.MatchString(burstID) {
		return BurstState{}, fmt.Errorf("state: invalid burst_id %q", burstID)
	}
	path := filepath.Join(dir, burstID+".json")
	buf, err := os.ReadFile(path)
	if err != nil {
		return BurstState{}, fmt.Errorf("state: read %s: %w", path, err)
	}
	var st BurstState
	if err := json.Unmarshal(buf, &st); err != nil {
		return BurstState{}, fmt.Errorf("state: parse %s: %w", path, err)
	}
	return st, nil
}

func ListStates(dir string) ([]string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("state: list %s: %w", dir, err)
	}
	ids := make([]string, 0, len(entries))
	for _, e := range entries {
		name := e.Name()
		if filepath.Ext(name) != ".json" {
			continue
		}
		id := name[:len(name)-len(".json")]
		if !burstIDPattern.MatchString(id) {
			continue
		}
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids, nil
}

func DeleteState(dir, burstID string) error {
	if !burstIDPattern.MatchString(burstID) {
		return fmt.Errorf("state: invalid burst_id %q", burstID)
	}
	path := filepath.Join(dir, burstID+".json")
	if err := os.Remove(path); err != nil {
		return fmt.Errorf("state: delete %s: %w", path, err)
	}
	return nil
}

func PidFilePath(burstID string) (string, error) {
	if !burstIDPattern.MatchString(burstID) {
		return "", fmt.Errorf("state: invalid burst_id %q", burstID)
	}
	dir, err := DefaultStateDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, burstID+".pid"), nil
}
