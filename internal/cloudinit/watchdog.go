package cloudinit

import "strings"

const (
	watchdogBinaryPath         = "/var/lib/horizon/bin/horizon"
	watchdogTokenPath          = "/etc/horizon/token"
	watchdogUnitName           = "horizon-watchdog.service"
	persistentWatchdogUnitPath = "/etc/systemd/system/" + watchdogUnitName
	// A read-only /etc/systemd/system holds neither the unit nor the symlink systemctl enable creates, so /run takes both.
	transientWatchdogUnitPath = "/run/systemd/system/" + watchdogUnitName
	perBootWatchdogScriptPath = "/var/lib/cloud/scripts/per-boot/horizon-watchdog"
	perBootScriptPermissions  = "0755"
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
	install := strings.Join([]string{
		"set -eu",
		"V=" + versionSentinel,
		"NUM=${V#v}",
		"BASE=" + opts.BinaryBaseURL + "/${V}",
		"ARCH=$(uname -m)",
		`case "$ARCH" in x86_64) ARCH=amd64 ;; aarch64) ARCH=arm64 ;; *) echo "horizon: unsupported architecture $ARCH" >&2; exit 1 ;; esac`,
		"TARBALL=horizon_${NUM}_linux_${ARCH}.tar.gz",
		"TMP=$(mktemp -d)",
		"curl -fsSL --max-time 120 -o \"$TMP/$TARBALL\" \"$BASE/$TARBALL\"",
		"curl -fsSL --max-time 60 -o \"$TMP/checksums.txt\" \"$BASE/horizon_${NUM}_checksums.txt\"",
		"EXPECTED=$(grep \" ${TARBALL}$\" \"$TMP/checksums.txt\")",
		"ACTUAL=$(sha256sum \"$TMP/$TARBALL\")",
		"[ \"${EXPECTED%% *}\" = \"${ACTUAL%% *}\" ] || { echo \"horizon: checksum mismatch for $TARBALL\" >&2; exit 1; }",
		"tar -xzf \"$TMP/$TARBALL\" -C \"$TMP\" horizon",
		"install -D -m0755 \"$TMP/horizon\" " + watchdogBinaryPath,
		watchdogBinaryPath + " version >/dev/null",
		"rm -rf \"$TMP\"",
	}, "\n")

	commands := []string{install}
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

func perBootWatchdogScript() string {
	lines := []string{
		"#!/bin/sh",
		"set -eu",
		"[ -x " + watchdogBinaryPath + " ] || exit 0",
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
