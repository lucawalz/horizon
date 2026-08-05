package cloudinit

import (
	"fmt"
	"sort"
	"strings"

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

const (
	ArchitectureAMD64 = "amd64"
	ArchitectureARM64 = "arm64"
)

var knownArchitectures = []string{ArchitectureAMD64, ArchitectureARM64}

type File struct {
	Path        string
	Permissions string
	Owner       string
	Content     string
}

type Options struct {
	Flavor              string
	Server              string
	Architecture        string
	Labels              []string
	Taints              []string
	Files               []File
	PreCommands         []string
	PostCommands        []string
	InstallWatchdogUnit *bool
	BinaryBaseURL       string
	Passthrough         bool
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
	architecture, err := normalizedArchitecture(opts.Architecture)
	if err != nil {
		return "", err
	}
	opts.Architecture = architecture

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
		flavorCommands, err := flavor.Commands(opts)
		if err != nil {
			return "", err
		}
		files = append(flavorFiles, files...)
		commands = append(commands, flavorCommands...)
		commands = append(commands, watchdogCommands(opts)...)
		files = append(files, watchdogFiles(opts)...)
	}

	commands = append(commands, opts.PostCommands...)
	return document(files, commands), nil
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

func normalizedArchitecture(architecture string) (string, error) {
	if architecture == "" {
		return ArchitectureAMD64, nil
	}
	for _, known := range knownArchitectures {
		if architecture == known {
			return architecture, nil
		}
	}
	return "", fmt.Errorf("cloudinit: Options.Architecture %q is invalid, want one of %s", architecture, strings.Join(knownArchitectures, ", "))
}
