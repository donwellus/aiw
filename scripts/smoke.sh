#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
AIW="$ROOT/bin/aiw"
TMP="$(mktemp -d /tmp/aiw-smoke.XXXXXX)"
trap 'rm -rf "$TMP"' EXIT

git -C "$TMP" init -q -b main
git -C "$TMP" config user.email smoke@example.com
git -C "$TMP" config user.name smoke
printf '# app\n' > "$TMP/README.md"
git -C "$TMP" add -A
git -C "$TMP" commit -qm initial

"$AIW" --root "$TMP" init --force >/dev/null
"$AIW" --root "$TMP" run --task tasks/001-exemplo.md --engine fake --auto --no-write-back
RUN_ID="$(ls "$TMP/.aiw/runs" | tail -1)"
"$AIW" --root "$TMP" status "$RUN_ID" | sed -n '1,25p'
echo "smoke ok"
