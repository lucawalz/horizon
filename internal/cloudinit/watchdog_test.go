package cloudinit

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

const (
	abortOnAnyFailure  = "set -eu"
	surviveAbsentStubs = "set -u"
	architectureStub   = "#!/bin/sh\necho x86_64\n"
	harnessPermissions = 0o755
	harnessVersion     = "v0.0.0"
)

var directoryChange = regexp.MustCompile(`(^|[;&|(]\s*)cd\s`)

func watchdogOptions() Options {
	return Options{
		Flavor:              "k3s",
		Server:              "https://10.20.0.10:6443",
		KubernetesVersion:   pinnedKubernetesVersion,
		InstallWatchdogUnit: boolPtr(true),
	}
}

func TestWatchdogCommandsNeverChangeTheWorkingDirectory(t *testing.T) {
	transient := watchdogOptions()
	transient.TransientWatchdogUnit = true
	suppressed := watchdogOptions()
	suppressed.InstallWatchdogUnit = boolPtr(false)

	tests := []struct {
		name string
		opts Options
	}{
		{name: "persistent unit", opts: watchdogOptions()},
		{name: "transient unit", opts: transient},
		{name: "no unit", opts: suppressed},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			for _, command := range watchdogCommands(tc.opts) {
				for _, line := range strings.Split(command, "\n") {
					if directoryChange.MatchString(line) {
						t.Errorf("%q changes the working directory, which every later runcmd entry inherits", line)
					}
				}
			}
		})
	}
}

func TestWatchdogCommandsLeaveTheWorkingDirectoryResolvable(t *testing.T) {
	shell, err := exec.LookPath("sh")
	if err != nil {
		t.Skipf("no POSIX shell to run the emitted commands: %v", err)
	}
	getcwd, err := exec.LookPath("pwd")
	if err != nil {
		t.Skipf("no pwd to resolve the working directory the commands leave behind: %v", err)
	}

	opts := watchdogOptions()
	opts.TransientWatchdogUnit = true
	shellified := strings.Join(watchdogCommands(opts), "\n")
	script := strings.ReplaceAll(shellified, abortOnAnyFailure, surviveAbsentStubs)

	path := filepath.Join(t.TempDir(), "runcmd")
	content := script + "\n" + getcwd + " >/dev/null\n"
	if err := os.WriteFile(path, []byte(content), harnessPermissions); err != nil {
		t.Fatalf("write the shellified commands: %v", err)
	}

	cmd := exec.Command(shell, path)
	cmd.Dir = t.TempDir()
	cmd.Env = []string{
		"PATH=" + stubbedPath(t),
		strings.Trim(versionSentinel, "${}") + "=" + harnessVersion,
	}
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("the emitted commands left no resolvable working directory: %v\n%s", err, out)
	}
}

func stubbedPath(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "uname"), []byte(architectureStub), harnessPermissions); err != nil {
		t.Fatalf("write the architecture stub: %v", err)
	}
	for _, name := range []string{"mktemp", "rm"} {
		resolved, err := exec.LookPath(name)
		if err != nil {
			t.Skipf("no %s to reproduce the temporary directory lifecycle: %v", name, err)
		}
		if err := os.Symlink(resolved, filepath.Join(dir, name)); err != nil {
			t.Fatalf("link %s into the stubbed path: %v", name, err)
		}
	}
	return dir
}
