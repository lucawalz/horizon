package cli

import (
	"os"
)

type BurstState struct {
	BurstID             string `json:"burst_id"`
	Hostname            string `json:"hostname"`
	HeadscaleNodeID     string `json:"headscale_node_id"`
	HeadscalePreAuthKey string `json:"headscale_preauth_key"`
	HetznerServerID     string `json:"hetzner_server_id"`
}

func DefaultStateDir() (string, error) { return "", nil }
func WriteState(dir string, st BurstState) error { return nil }
func ReadState(dir, burstID string) (BurstState, error) { return BurstState{}, nil }
func ListStates(dir string) ([]string, error) { return nil, nil }
func DeleteState(dir, burstID string) error { return nil }

var _ = os.ErrNotExist
