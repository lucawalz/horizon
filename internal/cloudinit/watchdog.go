package cloudinit

import (
	"path/filepath"
	"strings"
)

const (
	watchdogBinaryPath         = "/var/lib/horizon/bin/horizon"
	watchdogTokenPath          = "/etc/horizon/token"
	watchdogUnitName           = "horizon-watchdog.service"
	persistentWatchdogUnitPath = "/etc/systemd/system/" + watchdogUnitName
	// A read-only /etc/systemd/system holds neither the unit nor the symlink systemctl enable creates, so /run takes both.
	transientWatchdogUnitPath = "/run/systemd/system/" + watchdogUnitName
	perBootWatchdogScriptPath = "/var/lib/cloud/scripts/per-boot/horizon-watchdog"
	perBootScriptPermissions  = "0755"
	checksumFileName          = "checksums.txt"
)

var (
	watchdogStateDir            = filepath.Dir(filepath.Dir(watchdogBinaryPath))
	installIncompleteMarkerPath = filepath.Join(watchdogStateDir, "install-incomplete")
)

func watchdogFiles(opts Options) []File {
	files := []File{{
		Path:        watchdogTokenPath,
		Permissions: secretFilePermissions,
		Content:     nodeTokenSentinel,
	}}
	if opts.TransientWatchdogUnit {
		files = append(files, File{
			Path:        perBootWatchdogScriptPath,
			Permissions: perBootScriptPermissions,
			Content:     perBootWatchdogScript(),
		})
	}
	return files
}

func watchdogCommands(opts Options) []string {
	install := []string{
		"set -eu",
		"mkdir -p " + watchdogStateDir,
		"touch " + installIncompleteMarkerPath,
		"V=" + versionSentinel,
		"NUM=${V#v}",
		"BASE=" + opts.BinaryBaseURL + "/${V}",
		"ARCH=$(uname -m)",
		`case "$ARCH" in x86_64) ARCH=amd64 ;; aarch64) ARCH=arm64 ;; *) echo "horizon: unsupported architecture $ARCH" >&2; exit 1 ;; esac`,
		"TARBALL=horizon_${NUM}_linux_${ARCH}.tar.gz",
		"TMP=$(mktemp -d)",
		"curl -fsSL --max-time 120 -o \"$TMP/$TARBALL\" \"$BASE/$TARBALL\"",
		"curl -fsSL --max-time 60 -o \"$TMP/" + checksumFileName + "\" \"$BASE/horizon_${NUM}_checksums.txt\"",
	}
	install = append(install, verifyChecksum()...)
	install = append(install,
		"tar -xzf \"$TMP/$TARBALL\" -C \"$TMP\" horizon",
		"install -D -m0755 \"$TMP/horizon\" "+watchdogBinaryPath,
		watchdogBinaryPath+" version >/dev/null",
		"rm -rf \"$TMP\"",
		"rm -f "+installIncompleteMarkerPath,
	)

	commands := []string{strings.Join(install, "\n")}
	if !*opts.InstallWatchdogUnit {
		return commands
	}
	if opts.TransientWatchdogUnit {
		return append(commands, strings.Join([]string{"set -eu", perBootWatchdogScriptPath}, "\n"))
	}

	enabled := append(watchdogUnit(), "[Install]", "WantedBy=multi-user.target")
	lines := []string{"set -eu"}
	lines = append(lines, armWatchdog(persistentWatchdogUnitPath, enabled, "enable --now")...)
	return append(commands, strings.Join(lines, "\n"))
}

func verifyChecksum() []string {
	return []string{
		`EXPECTED=$(awk -v want="$TARBALL" 'NF == 2 && $2 == want { hash = $1; found++ } END { if (found != 1) exit 1; print hash }' "$TMP/` + checksumFileName + `")`,
		`ACTUAL=$(sha256sum "$TMP/$TARBALL")`,
		`[ "$EXPECTED" = "${ACTUAL%% *}" ] || { echo "horizon: checksum mismatch for $TARBALL" >&2; exit 1; }`,
	}
}

func perBootWatchdogScript() string {
	incompleteInstallMessage := "horizon: watchdog install did not complete, marker " + installIncompleteMarkerPath +
		"; this node lost the layer that survives an unreachable control plane, the orphan reconciler still sweeps this instance at expiry"
	lines := []string{
		"#!/bin/sh",
		"set -eu",
		"if [ -x " + watchdogBinaryPath + " ]; then",
		"  :",
		"elif [ -e " + installIncompleteMarkerPath + " ]; then",
		"  echo \"" + incompleteInstallMessage + "\" >&2",
		"  exit 1",
		"else",
		"  exit 0",
		"fi",
	}
	lines = append(lines, armWatchdog(transientWatchdogUnitPath, watchdogUnit(), "start")...)
	return strings.Join(lines, "\n")
}

func armWatchdog(path string, unit []string, activation string) []string {
	lines := []string{"cat > " + path + " <<'UNIT'"}
	lines = append(lines, unit...)
	return append(lines, "UNIT", "systemctl daemon-reload", "systemctl "+activation+" "+watchdogUnitName)
}

func watchdogUnit() []string {
	return []string{
		"[Unit]",
		"Description=horizon node watchdog",
		"After=network-online.target",
		"Wants=network-online.target",
		"[Service]",
		"Type=simple",
		"Restart=always",
		"RestartSec=5",
		"ExecStart=" + watchdogBinaryPath + " watchdog --max-lifetime=" + maxLifetimeSentinel +
			" --token-file=" + watchdogTokenPath + " --state-dir=/run/horizon",
	}
}
