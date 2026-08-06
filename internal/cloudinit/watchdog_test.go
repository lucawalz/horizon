package cloudinit

import (
	"crypto/sha256"
	"encoding/hex"
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
	harnessTarball     = "horizon_0.0.0_linux_amd64.tar.gz"
	unpublishedHash    = "0000000000000000000000000000000000000000000000000000000000000000"
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

type watchdogShape struct {
	name string
	opts Options
}

func watchdogShapes() []watchdogShape {
	transient := watchdogOptions()
	transient.TransientWatchdogUnit = true
	suppressed := watchdogOptions()
	suppressed.InstallWatchdogUnit = boolPtr(false)
	return []watchdogShape{
		{name: "persistent unit", opts: watchdogOptions()},
		{name: "transient unit", opts: transient},
		{name: "no unit", opts: suppressed},
	}
}

func TestWatchdogCommandsNeverChangeTheWorkingDirectory(t *testing.T) {
	for _, tc := range watchdogShapes() {
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
	if os.Geteuid() == 0 {
		t.Skip("as root the emitted unit redirection would reach the real /etc/systemd/system")
	}

	for _, tc := range watchdogShapes() {
		t.Run(tc.name, func(t *testing.T) {
			shellified := strings.Join(watchdogCommands(tc.opts), "\n")
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
				"TMPDIR=" + t.TempDir(),
				strings.Trim(versionSentinel, "${}") + "=" + harnessVersion,
			}
			out, err := cmd.CombinedOutput()
			if err != nil {
				t.Fatalf("the emitted commands left no resolvable working directory: %v\n%s", err, out)
			}
		})
	}
}

func TestVerifyChecksumAcceptsOnlyThePublishedLineForTheTarball(t *testing.T) {
	shell, err := exec.LookPath("sh")
	if err != nil {
		t.Skipf("no POSIX shell to run the emitted verification: %v", err)
	}
	if _, err := exec.LookPath("sha256sum"); err != nil {
		t.Skipf("no sha256sum to hash the downloaded tarball: %v", err)
	}

	tests := []struct {
		name      string
		checksums func(published string) string
		accepted  bool
	}{
		{
			name:      "the published line",
			checksums: func(published string) string { return published + "  " + harnessTarball + "\n" },
			accepted:  true,
		},
		{
			name: "the published line among the other artifacts",
			checksums: func(published string) string {
				return unpublishedHash + "  horizon_0.0.0_linux_arm64.tar.gz\n" +
					published + "  " + harnessTarball + "\n" +
					unpublishedHash + "  horizon_0.0.0_darwin_amd64.tar.gz\n"
			},
			accepted: true,
		},
		{
			name:      "a tampered tarball",
			checksums: func(string) string { return unpublishedHash + "  " + harnessTarball + "\n" },
		},
		{
			name:      "no line for the tarball",
			checksums: func(published string) string { return published + "  horizon_0.0.0_linux_arm64.tar.gz\n" },
		},
		{
			name:      "an empty file",
			checksums: func(string) string { return "" },
		},
		{
			name: "the tarball listed twice with the published line first",
			checksums: func(published string) string {
				return published + "  " + harnessTarball + "\n" + unpublishedHash + "  " + harnessTarball + "\n"
			},
		},
		{
			name: "the tarball listed twice with the published line second",
			checksums: func(published string) string {
				return unpublishedHash + "  " + harnessTarball + "\n" + published + "  " + harnessTarball + "\n"
			},
		},
		{
			name: "a line naming another file whose name ends with the tarball",
			checksums: func(published string) string {
				return published + "  evil " + harnessTarball + "\n"
			},
		},
		{
			name:      "a line carrying a hash and no name",
			checksums: func(published string) string { return published + "\n" },
		},
		{
			name: "a line naming the tarball and trailing a further field",
			checksums: func(published string) string {
				return published + "  " + harnessTarball + " extra\n"
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			payload := []byte("horizon release payload")
			if err := os.WriteFile(filepath.Join(dir, harnessTarball), payload, harnessPermissions); err != nil {
				t.Fatalf("write the downloaded tarball: %v", err)
			}
			digest := sha256.Sum256(payload)
			content := tc.checksums(hex.EncodeToString(digest[:]))
			if err := os.WriteFile(filepath.Join(dir, checksumFileName), []byte(content), harnessPermissions); err != nil {
				t.Fatalf("write the published checksums: %v", err)
			}

			script := strings.Join(append([]string{
				abortOnAnyFailure,
				"TMP=" + dir,
				"TARBALL=" + harnessTarball,
			}, verifyChecksum()...), "\n") + "\n"
			path := filepath.Join(dir, "verify")
			if err := os.WriteFile(path, []byte(script), harnessPermissions); err != nil {
				t.Fatalf("write the verification script: %v", err)
			}

			out, err := exec.Command(shell, path).CombinedOutput()
			if tc.accepted && err != nil {
				t.Fatalf("the published tarball was rejected: %v\n%s", err, out)
			}
			if !tc.accepted && err == nil {
				t.Fatalf("a tarball the checksums do not vouch for was accepted:\n%s", content)
			}
		})
	}
}

func TestPerBootWatchdogScriptDistinguishesPendingFromFailedInstalls(t *testing.T) {
	shell, err := exec.LookPath("sh")
	if err != nil {
		t.Skipf("no POSIX shell to run the emitted per-boot script: %v", err)
	}
	cat, err := exec.LookPath("cat")
	if err != nil {
		t.Skipf("no cat to write the unit file the emitted script embeds: %v", err)
	}
	if os.Geteuid() == 0 {
		t.Skip("as root the emitted unit redirection would reach the real /run/systemd/system")
	}

	tests := []struct {
		name          string
		writeBinary   bool
		writeMarker   bool
		wantExit      int
		wantUnitFile  bool
		wantSystemctl bool
		wantStderr    bool
	}{
		{name: "install not yet run", wantExit: 0},
		{name: "install did not complete", writeMarker: true, wantExit: 1, wantStderr: true},
		{name: "install completed", writeBinary: true, writeMarker: true, wantExit: 0, wantUnitFile: true, wantSystemctl: true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			binary := filepath.Join(root, "horizon")
			marker := filepath.Join(root, "install-incomplete")
			unit := filepath.Join(root, "horizon-watchdog.service")
			systemctlLog := filepath.Join(root, "systemctl.log")

			replacer := strings.NewReplacer(
				watchdogBinaryPath, binary,
				installIncompleteMarkerPath, marker,
				transientWatchdogUnitPath, unit,
			)
			script := replacer.Replace(perBootWatchdogScript())

			if tc.writeBinary {
				if err := os.WriteFile(binary, nil, harnessPermissions); err != nil {
					t.Fatalf("write the installed binary: %v", err)
				}
			}
			if tc.writeMarker {
				if err := os.WriteFile(marker, nil, harnessPermissions); err != nil {
					t.Fatalf("write the in-progress marker: %v", err)
				}
			}

			path := filepath.Join(root, "per-boot")
			if err := os.WriteFile(path, []byte(script), harnessPermissions); err != nil {
				t.Fatalf("write the per-boot script: %v", err)
			}

			cmd := exec.Command(shell, path)
			cmd.Env = []string{"PATH=" + stubbedSystemctlPath(t, cat, systemctlLog)}
			out, err := cmd.CombinedOutput()

			exitCode := 0
			if err != nil {
				exitErr, ok := err.(*exec.ExitError)
				if !ok {
					t.Fatalf("the emitted script could not run: %v\n%s", err, out)
				}
				exitCode = exitErr.ExitCode()
			}
			if exitCode != tc.wantExit {
				t.Fatalf("exit code = %d, want %d\n%s", exitCode, tc.wantExit, out)
			}
			if _, err := os.Stat(unit); (err == nil) != tc.wantUnitFile {
				t.Fatalf("unit file present = %v, want %v", err == nil, tc.wantUnitFile)
			}
			if _, err := os.Stat(systemctlLog); (err == nil) != tc.wantSystemctl {
				t.Fatalf("systemctl invoked = %v, want %v", err == nil, tc.wantSystemctl)
			}
			if tc.wantStderr && !strings.Contains(string(out), marker) {
				t.Fatalf("expected the marker path in stderr, got:\n%s", out)
			}
		})
	}
}

func stubbedSystemctlPath(t *testing.T, cat, log string) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.Symlink(cat, filepath.Join(dir, "cat")); err != nil {
		t.Fatalf("link cat into the stubbed path: %v", err)
	}
	script := "#!/bin/sh\necho \"$@\" >> " + log + "\n"
	if err := os.WriteFile(filepath.Join(dir, "systemctl"), []byte(script), harnessPermissions); err != nil {
		t.Fatalf("write the systemctl stub: %v", err)
	}
	return dir
}

func stubbedPath(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "uname"), []byte(architectureStub), harnessPermissions); err != nil {
		t.Fatalf("write the architecture stub: %v", err)
	}
	for _, name := range []string{"mkdir", "mktemp", "rm"} {
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
