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

cat > "$TMP/tasks/002-smoke.md" <<'TASK'
---
id: 002-smoke
title: Segunda tarefa do smoke
repo: .
labels: [aiw]
acceptance:
  - Pipeline fake conclui.
---
Tarefa paralela 2.
TASK

cat > "$TMP/tasks/003-smoke.md" <<'TASK'
---
id: 003-smoke
title: Terceira tarefa do smoke
repo: .
labels: [aiw]
acceptance:
  - Pipeline fake conclui.
---
Tarefa paralela 3.
TASK

WORK_JSON="$("$AIW" --root "$TMP" work --workers 2 --engine fake --auto --no-write-back --json)"
printf '%s\n' "$WORK_JSON"
COUNT="$(printf '%s\n' "$WORK_JSON" | grep -c '"status":"succeeded"' || true)"
if [ "$COUNT" -ne 2 ]; then
  echo "esperava 2 tarefas paralelas concluídas, obtive $COUNT" >&2
  exit 1
fi
BRANCHES="$(git -C "$TMP" branch --list 'aiw/*' | wc -l)"
if [ "$BRANCHES" -lt 3 ]; then
  echo "esperava ao menos 3 branches aiw/*, obtive $BRANCHES" >&2
  exit 1
fi
echo "smoke ok"
