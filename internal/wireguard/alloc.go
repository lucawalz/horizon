package wireguard

import (
	"fmt"
	"net"
	"strconv"
)

const (
	hostMin   = 3
	hostMax   = 254
	hostRange = hostMax - hostMin + 1
)

func AllocateIP(subnetCIDR, burstID string) (string, error) {
	ip, _, err := net.ParseCIDR(subnetCIDR)
	if err != nil {
		return "", fmt.Errorf("wireguard: parse subnet %q: %w", subnetCIDR, err)
	}
	ip4 := ip.To4()
	if ip4 == nil {
		return "", fmt.Errorf("wireguard: subnet %q is not IPv4", subnetCIDR)
	}
	prefix := burstID
	if len(prefix) > 8 {
		prefix = prefix[:8]
	}
	seed, err := strconv.ParseUint(prefix, 16, 64)
	if err != nil {
		return "", fmt.Errorf("wireguard: burst id %q must be hex: %w", burstID, err)
	}
	host := hostMin + int(seed%hostRange)
	return fmt.Sprintf("%d.%d.%d.%d", ip4[0], ip4[1], ip4[2], host), nil
}
