package cli_test

import (
	"context"

	"github.com/lucawalz/horizon/internal/wireguard"
)

type mockPeerManager struct {
	addErr      error
	removeErr   error
	addCalls    []string
	removeCalls []string
}

func (m *mockPeerManager) AddPeer(_ context.Context, peer wireguard.Peer) error {
	m.addCalls = append(m.addCalls, peer.PublicKey)
	return m.addErr
}

func (m *mockPeerManager) RemovePeer(_ context.Context, publicKey string) error {
	m.removeCalls = append(m.removeCalls, publicKey)
	return m.removeErr
}

type mockHetznerProvider struct {
	burstID      string
	hostname     string
	serverID     string
	serverIP     string
	pubKey       string
	wgIP         string
	applyErr     error
	destroyCalls int
	destroyErr   error
	generateErr  error
}

func (m *mockHetznerProvider) SetRuntimeSecrets(hubPublicKey, wgIP, sshPublicKey, k3sURL, k3sToken string) {
}

func (m *mockHetznerProvider) GenerateTFVars() (map[string]string, error) {
	if m.generateErr != nil {
		return nil, m.generateErr
	}
	return map[string]string{"burst_id": m.burstID}, nil
}

func (m *mockHetznerProvider) Apply(_ context.Context, _ map[string]string) error {
	return m.applyErr
}

func (m *mockHetznerProvider) Destroy(_ context.Context) error {
	m.destroyCalls++
	return m.destroyErr
}

func (m *mockHetznerProvider) Hostname() string           { return m.hostname }
func (m *mockHetznerProvider) BurstID() string            { return m.burstID }
func (m *mockHetznerProvider) ServerID() string           { return m.serverID }
func (m *mockHetznerProvider) ServerIP() string           { return m.serverIP }
func (m *mockHetznerProvider) WireGuardPublicKey() string { return m.pubKey }
func (m *mockHetznerProvider) WireGuardIP() string        { return m.wgIP }
