package cloudinit

import (
	"fmt"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/lucawalz/horizon/internal/provider"
)

const DefaultBinaryBaseURL = "https://github.com/lucawalz/horizon/releases/download"

const (
	nodeTokenSentinel   = provider.NodeTokenSentinel
	versionSentinel     = provider.VersionSentinel
	maxLifetimeSentinel = provider.MaxLifetimeSentinel
	joinTokenSentinel   = provider.JoinTokenSentinel
)

const defaultFilePermissions = "0644"

const defaultFileOwner = "root:root"

type File struct {
	Path        string
	Permissions string
	Owner       string
	Content     string
}

type Options struct {
	Flavor              string
	Server              string
	Labels              []string
	Taints              []string
	Files               []File
	PreCommands         []string
	PostCommands        []string
	InstallKubernetes   *bool
	InstallWatchdogUnit *bool
	// The unit lives in /run because a read-only /etc/systemd/system cannot hold it or the symlink enabling it creates.
	TransientWatchdogUnit bool
	BinaryBaseURL         string
	Passthrough           bool
}

func (o Options) installsKubernetes() bool {
	return o.InstallKubernetes == nil || *o.InstallKubernetes
}

type Flavor interface {
	Name() string
	Validate(opts Options) error
	Files(opts Options) ([]File, error)
	Commands(opts Options) ([]string, error)
}

var flavors = map[string]Flavor{}

func register(f Flavor) { flavors[f.Name()] = f }

func Flavors() []string {
	names := make([]string, 0, len(flavors))
	for name := range flavors {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func Render(opts Options) (string, error) {
	if opts.InstallWatchdogUnit == nil {
		return "", fmt.Errorf("cloudinit: Options.InstallWatchdogUnit must be set explicitly to true or false")
	}
	if opts.TransientWatchdogUnit && !*opts.InstallWatchdogUnit {
		return "", fmt.Errorf("--transient-watchdog-unit writes the watchdog unit to /run, so it needs --install-watchdog-unit to stay true")
	}
	if opts.BinaryBaseURL == "" {
		opts.BinaryBaseURL = DefaultBinaryBaseURL
	}
	files := append([]File{}, opts.Files...)
	commands := append([]string{}, opts.PreCommands...)

	if !opts.Passthrough {
		flavor, ok := flavors[opts.Flavor]
		if !ok {
			return "", fmt.Errorf("unknown flavor %q, known flavors are %s", opts.Flavor, strings.Join(Flavors(), ", "))
		}
		if err := flavor.Validate(opts); err != nil {
			return "", err
		}
		flavorFiles, err := flavor.Files(opts)
		if err != nil {
			return "", err
		}
		files = append(flavorFiles, files...)
		if opts.installsKubernetes() {
			flavorCommands, err := flavor.Commands(opts)
			if err != nil {
				return "", err
			}
			commands = append(commands, flavorCommands...)
		}
		commands = append(commands, watchdogCommands(opts)...)
		files = append(files, watchdogFiles(opts)...)
	}

	commands = append(commands, opts.PostCommands...)
	rendered := document(files, commands)
	if err := parses(rendered); err != nil {
		return "", err
	}
	return rendered, nil
}

func parses(rendered string) error {
	var parsed any
	if err := yaml.Unmarshal([]byte(rendered), &parsed); err != nil {
		return fmt.Errorf("cloudinit: the generated document is not valid YAML, a file content or a command most likely starts with an indent or a tab: %w", err)
	}
	return nil
}

func document(files []File, commands []string) string {
	var b strings.Builder
	b.WriteString("#cloud-config\n")
	if len(files) > 0 {
		b.WriteString("write_files:\n")
		for _, f := range files {
			b.WriteString("  - path: " + f.Path + "\n")
			b.WriteString("    permissions: '" + defaulted(f.Permissions, defaultFilePermissions) + "'\n")
			b.WriteString("    owner: " + defaulted(f.Owner, defaultFileOwner) + "\n")
			b.WriteString("    content: |\n")
			for _, line := range strings.Split(strings.TrimRight(f.Content, "\n"), "\n") {
				b.WriteString("      " + line + "\n")
			}
		}
	}
	if len(commands) > 0 {
		b.WriteString("runcmd:\n")
		for _, c := range commands {
			b.WriteString("  - |\n")
			for _, line := range strings.Split(strings.TrimRight(c, "\n"), "\n") {
				b.WriteString("    " + line + "\n")
			}
		}
	}
	return b.String()
}

func defaulted(value, fallback string) string {
	if value == "" {
		return fallback
	}
	return value
}
