package cloudinit

import (
	"fmt"
	"strings"
)

type k3sFlavor struct{}

func init() { register(k3sFlavor{}) }

func (k3sFlavor) Name() string { return "k3s" }

func (k3sFlavor) Validate(opts Options) error {
	if opts.Server == "" {
		return fmt.Errorf("flavor k3s needs a server URL")
	}
	if !strings.HasPrefix(opts.Server, "https://") {
		return fmt.Errorf("server URL must start with https://, got %q", opts.Server)
	}
	return nil
}

func (k3sFlavor) Files(opts Options) ([]File, error) {
	var b strings.Builder
	b.WriteString("server: " + opts.Server + "\n")
	b.WriteString("token: " + joinTokenSentinel + "\n")
	if len(opts.Labels) > 0 {
		b.WriteString("node-label:\n")
		for _, l := range opts.Labels {
			b.WriteString("  - " + l + "\n")
		}
	}
	if len(opts.Taints) > 0 {
		b.WriteString("node-taint:\n")
		for _, t := range opts.Taints {
			b.WriteString("  - " + t + "\n")
		}
	}
	return []File{{
		Path:        "/etc/rancher/k3s/config.yaml",
		Permissions: "0600",
		Content:     b.String(),
	}}, nil
}

func (k3sFlavor) Commands(Options) ([]string, error) {
	return []string{"curl -sfL https://get.k3s.io | sh -s - agent"}, nil
}
