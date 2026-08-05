package cloudinit

import "strings"

func watchdogFiles(Options) []File {
	return []File{{
		Path:        "/etc/horizon/token",
		Permissions: "0600",
		Content:     nodeTokenSentinel,
	}}
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
		"cd \"$TMP\"",
		"curl -fsSL --max-time 120 -O \"$BASE/$TARBALL\"",
		"curl -fsSL --max-time 60 -O \"$BASE/horizon_${NUM}_checksums.txt\"",
		"grep \" ${TARBALL}$\" \"horizon_${NUM}_checksums.txt\" > expected.txt",
		"sha256sum -c expected.txt",
		"tar -xzf \"$TARBALL\" horizon",
		"install -D -m0755 horizon /var/lib/horizon/bin/horizon",
		"/var/lib/horizon/bin/horizon version >/dev/null",
		"rm -rf \"$TMP\"",
	}, "\n")

	commands := []string{install}
	if *opts.InstallWatchdogUnit {
		commands = append(commands, strings.Join([]string{
			"set -eu",
			"cat > /etc/systemd/system/horizon-watchdog.service <<'UNIT'",
			"[Unit]",
			"Description=horizon node watchdog",
			"After=network-online.target",
			"Wants=network-online.target",
			"[Service]",
			"Type=simple",
			"Restart=always",
			"RestartSec=5",
			"ExecStart=/var/lib/horizon/bin/horizon watchdog --max-lifetime=" + maxLifetimeSentinel + " --token-file=/etc/horizon/token --state-dir=/run/horizon",
			"[Install]",
			"WantedBy=multi-user.target",
			"UNIT",
			"systemctl daemon-reload",
			"systemctl enable --now horizon-watchdog.service",
		}, "\n"))
	}
	return commands
}
