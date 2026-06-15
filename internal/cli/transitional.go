package cli

import "context"

type hetznerProvider interface {
	SetRuntimeSecrets(hubPublicKey, wgIP, sshPublicKey, k3sURL, k3sToken string)
	GenerateTFVars() (map[string]string, error)
	Apply(ctx context.Context, vars map[string]string) error
	Destroy(ctx context.Context) error
	Hostname() string
	BurstID() string
	ServerID() string
	ServerIP() string
	WireGuardPublicKey() string
	WireGuardIP() string
}
