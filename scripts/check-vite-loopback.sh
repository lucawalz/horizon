#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
site_dir="$repo_root/internal/web/site"
config="$site_dir/vite.config.ts"
manifest="$site_dir/package.json"

if ! grep -qF "host: '127.0.0.1'" "$config"; then
  echo "$config no longer pins the dev server to host: '127.0.0.1'; the dashboard it proxies is unauthenticated and must not be reachable off the machine" >&2
  exit 1
fi

widened="$(grep -oE "host[[:space:]]*:[[:space:]]*[^,}[:space:]]+" "$config" | grep -vE "^host[[:space:]]*:[[:space:]]*'127\.0\.0\.1'$" || true)"
if [ -n "$widened" ]; then
  echo "$config carries a host binding that is not 127.0.0.1:" >&2
  echo "$widened" >&2
  echo "every server this file configures, dev and preview alike, proxies the unauthenticated dashboard and must not be reachable off the machine" >&2
  exit 1
fi

scripts="$(jq -r '.scripts[]?' "$manifest")"
if grep -q -- '--host' <<<"$scripts"; then
  echo "an npm script passes --host, which overrides the loopback binding in vite.config.ts and exposes the unauthenticated dashboard to the network" >&2
  exit 1
fi

echo "every vite server stays bound to loopback."
