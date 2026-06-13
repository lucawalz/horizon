package wireguard

import (
	"net"
	"testing"
)

func TestAllocateIPDeterministic(t *testing.T) {
	first, err := AllocateIP("10.100.0.0/24", "deadbeef")
	if err != nil {
		t.Fatalf("AllocateIP: %v", err)
	}
	second, err := AllocateIP("10.100.0.0/24", "deadbeef")
	if err != nil {
		t.Fatalf("AllocateIP: %v", err)
	}
	if first != second {
		t.Errorf("AllocateIP not deterministic: %q vs %q", first, second)
	}
}

func TestAllocateIPBounds(t *testing.T) {
	cases := []string{"00000000", "ffffffff", "00000001", "000000fc", "aabb1122", "3"}
	for _, id := range cases {
		ip, err := AllocateIP("10.100.0.0/24", id)
		if err != nil {
			t.Fatalf("AllocateIP(%q): %v", id, err)
		}
		parsed := net.ParseIP(ip).To4()
		if parsed == nil {
			t.Fatalf("AllocateIP(%q) = %q not IPv4", id, ip)
		}
		host := int(parsed[3])
		if host < hostMin || host > hostMax {
			t.Errorf("AllocateIP(%q) host octet %d out of [%d,%d]", id, host, hostMin, hostMax)
		}
		if parsed[0] != 10 || parsed[1] != 100 || parsed[2] != 0 {
			t.Errorf("AllocateIP(%q) = %q, want 10.100.0.x", id, ip)
		}
	}
}

func TestAllocateIPRejectsNonHex(t *testing.T) {
	if _, err := AllocateIP("10.100.0.0/24", "zzzz"); err == nil {
		t.Error("expected error for non-hex burst id")
	}
}

func TestAllocateIPRejectsBadSubnet(t *testing.T) {
	if _, err := AllocateIP("not-a-cidr", "deadbeef"); err == nil {
		t.Error("expected error for invalid subnet")
	}
}
