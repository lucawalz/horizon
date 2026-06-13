package wireguard

import (
	"context"
	"os/exec"
	"strconv"
)

type Peer struct {
	PublicKey     string
	Endpoint      string
	AllowedIP     string
	KeepaliveSecs int
}

type PeerManager interface {
	AddPeer(ctx context.Context, peer Peer) error
	RemovePeer(ctx context.Context, publicKey string) error
}

type commandRunner interface {
	Run(ctx context.Context, name string, args ...string) error
}

type execRunner struct{}

func (execRunner) Run(ctx context.Context, name string, args ...string) error {
	return exec.CommandContext(ctx, name, args...).Run()
}

type SSHPeerManager struct {
	Host       string
	User       string
	Iface      string
	ListenPort int
	runner     commandRunner
}

func NewSSHPeerManager(host, user, iface string, port int) *SSHPeerManager {
	return &SSHPeerManager{Host: host, User: user, Iface: iface, ListenPort: port, runner: execRunner{}}
}

func (m *SSHPeerManager) sshArgs(wgArgs ...string) []string {
	args := []string{
		"-o", "StrictHostKeyChecking=accept-new",
		"-o", "BatchMode=yes",
		m.User + "@" + m.Host,
		"wg", "set", m.Iface,
	}
	return append(args, wgArgs...)
}

func (m *SSHPeerManager) AddPeer(ctx context.Context, peer Peer) error {
	args := m.sshArgs(
		"peer", peer.PublicKey,
		"endpoint", peer.Endpoint+":"+strconv.Itoa(m.ListenPort),
		"persistent-keepalive", strconv.Itoa(peer.KeepaliveSecs),
		"allowed-ips", peer.AllowedIP,
	)
	return m.runner.Run(ctx, "ssh", args...)
}

func (m *SSHPeerManager) RemovePeer(ctx context.Context, publicKey string) error {
	args := m.sshArgs("peer", publicKey, "remove")
	return m.runner.Run(ctx, "ssh", args...)
}
