package zerotier

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"
)

const FallbackIDToolPath = "/usr/local/bin/zerotier-idtool"

type Identity struct {
	Secret   string
	Public   string
	MemberID string
}

func ParseIdentity(identity string) (Identity, error) {
	trimmed := strings.TrimSpace(identity)
	parts := strings.Split(trimmed, ":")
	if len(parts) < 4 || parts[0] == "" {
		return Identity{}, fmt.Errorf("zerotier: malformed identity %q", trimmed)
	}
	public := strings.Join(parts[:3], ":")
	return Identity{Secret: trimmed, Public: public, MemberID: parts[0]}, nil
}

func GenerateIdentity(ctx context.Context) (Identity, error) {
	tool := idtoolPath()
	cctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	out, err := exec.CommandContext(cctx, tool, "generate").Output()
	if err != nil {
		return Identity{}, fmt.Errorf("zerotier: generate identity: %w", err)
	}
	return ParseIdentity(string(out))
}

func idtoolPath() string {
	if path, err := exec.LookPath("zerotier-idtool"); err == nil {
		return path
	}
	return FallbackIDToolPath
}

func IDToolAvailable() bool {
	if _, err := exec.LookPath("zerotier-idtool"); err == nil {
		return true
	}
	if info, err := os.Stat(FallbackIDToolPath); err == nil && !info.IsDir() {
		return true
	}
	return false
}
