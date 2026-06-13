package cli_test

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lucawalz/horizon/internal/cli"
)

func TestStateRoundTrip(t *testing.T) {
	dir := t.TempDir()
	st := cli.BurstState{
		BurstID:         "abc1234",
		Hostname:        "horizon-burst-abc1234",
		WireGuardIP:     "10.100.0.7",
		WireGuardPubKey: "pub7",
		HetznerServerID: "42",
	}
	if err := cli.WriteState(dir, st); err != nil {
		t.Fatalf("WriteState: %v", err)
	}
	got, err := cli.ReadState(dir, "abc1234")
	if err != nil {
		t.Fatalf("ReadState: %v", err)
	}
	if got != st {
		t.Errorf("ReadState = %+v, want %+v", got, st)
	}
}

func TestStateJSONFieldNames(t *testing.T) {
	dir := t.TempDir()
	st := cli.BurstState{BurstID: "aabb1122", Hostname: "h", WireGuardIP: "10.100.0.9", WireGuardPubKey: "pub99", HetznerServerID: "42"}
	if err := cli.WriteState(dir, st); err != nil {
		t.Fatalf("WriteState: %v", err)
	}
	buf, err := os.ReadFile(filepath.Join(dir, "aabb1122.json"))
	if err != nil {
		t.Fatalf("read file: %v", err)
	}
	got := string(buf)
	if !strings.Contains(got, `"wireguard_pubkey": "pub99"`) {
		t.Errorf("missing wireguard_pubkey: %s", got)
	}
	if !strings.Contains(got, `"wireguard_ip": "10.100.0.9"`) {
		t.Errorf("missing wireguard_ip: %s", got)
	}
	if strings.Contains(got, "zerotier_member_id") {
		t.Errorf("legacy zerotier field present: %s", got)
	}
}

func TestStateMode600(t *testing.T) {
	dir := t.TempDir()
	st := cli.BurstState{BurstID: "abc1234"}
	if err := cli.WriteState(dir, st); err != nil {
		t.Fatalf("WriteState: %v", err)
	}
	info, err := os.Stat(filepath.Join(dir, "abc1234.json"))
	if err != nil {
		t.Fatalf("Stat: %v", err)
	}
	if mode := info.Mode().Perm(); mode != 0o600 {
		t.Errorf("file mode = %04o, want 0600", mode)
	}
}

func TestListStatesEmpty(t *testing.T) {
	dir := t.TempDir()
	ids, err := cli.ListStates(dir)
	if err != nil {
		t.Fatalf("ListStates: %v", err)
	}
	if len(ids) != 0 {
		t.Errorf("ListStates = %v, want empty", ids)
	}
}

func TestListStatesMultiple(t *testing.T) {
	dir := t.TempDir()
	for _, id := range []string{"bbbb1111", "aaaa2222"} {
		if err := cli.WriteState(dir, cli.BurstState{BurstID: id}); err != nil {
			t.Fatalf("WriteState(%s): %v", id, err)
		}
	}
	ids, err := cli.ListStates(dir)
	if err != nil {
		t.Fatalf("ListStates: %v", err)
	}
	if len(ids) != 2 {
		t.Fatalf("ListStates len = %d, want 2", len(ids))
	}
	if ids[0] != "aaaa2222" || ids[1] != "bbbb1111" {
		t.Errorf("ListStates order = %v, want [aaaa2222 bbbb1111]", ids)
	}
}

func TestReadStateNotFound(t *testing.T) {
	dir := t.TempDir()
	_, err := cli.ReadState(dir, "dead1234")
	if err == nil {
		t.Fatal("ReadState: expected error, got nil")
	}
	if !errors.Is(err, os.ErrNotExist) {
		t.Errorf("ReadState error = %v, want os.ErrNotExist in chain", err)
	}
}

func TestDeleteState(t *testing.T) {
	dir := t.TempDir()
	st := cli.BurstState{BurstID: "cafe1234"}
	if err := cli.WriteState(dir, st); err != nil {
		t.Fatalf("WriteState: %v", err)
	}
	if err := cli.DeleteState(dir, "cafe1234"); err != nil {
		t.Fatalf("DeleteState: %v", err)
	}
	_, err := cli.ReadState(dir, "cafe1234")
	if !errors.Is(err, os.ErrNotExist) {
		t.Errorf("after DeleteState: ReadState error = %v, want os.ErrNotExist", err)
	}
}

func TestDefaultStateDir(t *testing.T) {
	xdgDir := t.TempDir()
	t.Setenv("XDG_STATE_HOME", xdgDir)
	got, err := cli.DefaultStateDir()
	if err != nil {
		t.Fatalf("DefaultStateDir (XDG): %v", err)
	}
	want := xdgDir + "/horizon"
	if got != want {
		t.Errorf("DefaultStateDir (XDG) = %q, want %q", got, want)
	}

	t.Setenv("XDG_STATE_HOME", "")
	homeDir := t.TempDir()
	t.Setenv("HOME", homeDir)
	got2, err := cli.DefaultStateDir()
	if err != nil {
		t.Fatalf("DefaultStateDir (HOME): %v", err)
	}
	want2 := homeDir + "/.local/state/horizon"
	if got2 != want2 {
		t.Errorf("DefaultStateDir (HOME) = %q, want %q", got2, want2)
	}
}
