package wireguard

import (
	"context"
	"errors"
	"strings"
	"testing"
)

type fakeRunner struct {
	calls [][]string
	err   error
}

func (f *fakeRunner) Run(_ context.Context, name string, args ...string) error {
	f.calls = append(f.calls, append([]string{name}, args...))
	return f.err
}

func newTestManager(r commandRunner) *SSHPeerManager {
	return &SSHPeerManager{Host: "192.168.20.1", User: "root", Iface: "wg0", ListenPort: 51820, runner: r}
}

func TestAddPeerExactArgv(t *testing.T) {
	r := &fakeRunner{}
	m := newTestManager(r)
	err := m.AddPeer(context.Background(), Peer{
		PublicKey:     "PUBKEY==",
		Endpoint:      "203.0.113.7",
		AllowedIP:     "10.100.0.5/32",
		KeepaliveSecs: 25,
	})
	if err != nil {
		t.Fatalf("AddPeer: %v", err)
	}
	want := []string{
		"ssh",
		"-o", "StrictHostKeyChecking=accept-new",
		"-o", "BatchMode=yes",
		"root@192.168.20.1",
		"wg", "set", "wg0",
		"peer", "PUBKEY==",
		"endpoint", "203.0.113.7:51820",
		"persistent-keepalive", "25",
		"allowed-ips", "10.100.0.5/32",
	}
	if len(r.calls) != 1 {
		t.Fatalf("expected 1 call, got %d", len(r.calls))
	}
	assertArgv(t, r.calls[0], want)
}

func TestRemovePeerExactArgv(t *testing.T) {
	r := &fakeRunner{}
	m := newTestManager(r)
	if err := m.RemovePeer(context.Background(), "PUBKEY=="); err != nil {
		t.Fatalf("RemovePeer: %v", err)
	}
	want := []string{
		"ssh",
		"-o", "StrictHostKeyChecking=accept-new",
		"-o", "BatchMode=yes",
		"root@192.168.20.1",
		"wg", "set", "wg0",
		"peer", "PUBKEY==", "remove",
	}
	if len(r.calls) != 1 {
		t.Fatalf("expected 1 call, got %d", len(r.calls))
	}
	assertArgv(t, r.calls[0], want)
}

func TestAddPeerPropagatesError(t *testing.T) {
	r := &fakeRunner{err: errors.New("ssh failed")}
	m := newTestManager(r)
	if err := m.AddPeer(context.Background(), Peer{PublicKey: "X"}); err == nil {
		t.Error("expected error from runner")
	}
}

func assertArgv(t *testing.T, got, want []string) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("argv length = %d, want %d\ngot:  %s\nwant: %s",
			len(got), len(want), strings.Join(got, " "), strings.Join(want, " "))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("argv[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}
