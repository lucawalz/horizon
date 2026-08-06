package cloudinit

import (
	"fmt"
	"maps"
	"regexp"
	"slices"
	"strings"
)

var k3sGeneratedConfigKeys = []string{"server", "token", "node-label", "node-taint"}

const (
	k3sReleaseTagExample = "v1.35.6+k3s1"
	k3sVersionMatchRule  = "it has to match the control plane the node joins, which reports its own on the server version line of kubectl version"
)

var k3sReleaseTag = regexp.MustCompile(`^v[0-9]+\.[0-9]+\.[0-9]+(-[0-9A-Za-z.]+)?\+k3s[0-9]+$`)

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
	if err := validateK3sVersion(opts); err != nil {
		return err
	}
	for _, key := range k3sGeneratedConfigKeys {
		if _, owned := opts.FlavorConfig[key]; owned {
			return fmt.Errorf("flavor k3s generates the %q config key from its own flags, so it cannot also be set as extra flavour configuration", key)
		}
	}
	return nil
}

func validateK3sVersion(opts Options) error {
	if !opts.installsKubernetes() {
		if opts.KubernetesVersion != "" {
			return fmt.Errorf("--install-kubernetes=false emits no install command, so --kubernetes-version %q would be discarded",
				opts.KubernetesVersion)
		}
		return nil
	}
	if opts.KubernetesVersion == "" {
		return fmt.Errorf("flavor k3s needs --kubernetes-version, such as %s, and %s",
			k3sReleaseTagExample, k3sVersionMatchRule)
	}
	if !k3sReleaseTag.MatchString(opts.KubernetesVersion) {
		return fmt.Errorf("--kubernetes-version %q is not a k3s release tag, which looks like %s, and %s",
			opts.KubernetesVersion, k3sReleaseTagExample, k3sVersionMatchRule)
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
	for _, key := range slices.Sorted(maps.Keys(opts.FlavorConfig)) {
		b.WriteString(key + ": " + opts.FlavorConfig[key] + "\n")
	}
	return []File{{
		Path:        "/etc/rancher/k3s/config.yaml",
		Permissions: secretFilePermissions,
		Content:     b.String(),
	}}, nil
}

func (k3sFlavor) Commands(opts Options) ([]string, error) {
	return []string{"curl -sfL https://get.k3s.io | INSTALL_K3S_VERSION=" + opts.KubernetesVersion + " sh -s - agent"}, nil
}
