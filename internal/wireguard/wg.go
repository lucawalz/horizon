package wireguard

import (
	"context"
	"fmt"
	"os/exec"
)

func HubReachable(ctx context.Context, host, user string) error {
	cmd := exec.CommandContext(ctx,
		"ssh",
		"-o", "StrictHostKeyChecking=accept-new",
		"-o", "BatchMode=yes",
		user+"@"+host,
		"true",
	)
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("wireguard: hub %s@%s unreachable: %w", user, host, err)
	}
	return nil
}
