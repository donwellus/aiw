#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
AIW="$ROOT/bin/aiw"
WORK="$(mktemp -d /tmp/aiw-verify.XXXXXX)"
trap 'rm -rf "$WORK"' EXIT

if [ ! -x "$AIW" ]; then
  make -C "$ROOT" build >/dev/null
fi

pass() {
  echo "ok   $*"
}

fail() {
  echo "FAIL $*" >&2
  exit 1
}

assert_eq() {
  local got="$1" want="$2" label="$3"
  if [ "$got" != "$want" ]; then
    fail "$label: obtido $got, esperado $want"
  fi
}

assert_ge() {
  local got="$1" want="$2" label="$3"
  if [ "$got" -lt "$want" ]; then
    fail "$label: obtido $got, esperado >= $want"
  fi
}

SLOW="$WORK/slow-agent.sh"
cat > "$SLOW" <<'SH'
#!/bin/sh
set -eu
stage="${1:-unknown}"
sleep "${AIW_SLOW_SECONDS:-0.3}"
case "$stage" in
  implement)
    printf 'change %s\n' "$(basename "$PWD")" >> AIW_CHANGE.md
    echo "implemented"
    ;;
  review)
    echo "APROVADO"
    ;;
  *)
    echo "ok $stage"
    ;;
esac
SH
chmod +x "$SLOW"

new_project() {
  local dir="$1"
  mkdir -p "$dir/tasks/done" "$dir/tasks/failed" "$dir/.aiw"
  git -C "$dir" init -q -b main
  git -C "$dir" config user.email verify@example.com
  git -C "$dir" config user.name verify
  printf '# app\n' > "$dir/README.md"
  printf '.aiw/\n' > "$dir/.gitignore"
  git -C "$dir" add -A
  git -C "$dir" commit -qm initial
  cat > "$dir/.aiw/config.yaml" <<EOF
engines:
  slow:
    driver: exec
    command: ["/bin/sh", "$SLOW", "{{stage}}"]
    prompt_via: stdin
defaults:
  engine: slow
  timeout: 5m
  budget_usd: 1
  max_test_retries: 1
  max_review_rounds: 1
  workers: 2
  claim_ttl: 1h
telemetry:
  provider: noop
sources:
  folder:
    driver: folder
    path: ./tasks
    done_dir: ./tasks/done
    failed_dir: ./tasks/failed
default_source: folder
workspace:
  default_repo: .
  test_commands: []
  max_test_concurrency: 1
  gates:
    spec: false
    plan: false
EOF
}

write_task() {
  local dir="$1" id="$2" title="$3" repo="${4:-.}"
  cat > "$dir/tasks/$id.md" <<EOF
---
id: $id
title: $title
repo: $repo
labels: [aiw]
acceptance:
  - O pipeline conclui a tarefa.
---

$title
EOF
}

# 1) Dois pools concorrentes, 8 tarefas, exactly-once.
DIR="$WORK/concurrent"
new_project "$DIR"
for i in $(seq 1 8); do
  write_task "$DIR" "task-$i" "Tarefa $i"
done

export AIW_SLOW_SECONDS=0.25
"$AIW" --root "$DIR" work --workers 2 --auto --json >"$DIR/out1.jsonl" 2>"$DIR/err1.log" &
P1=$!
"$AIW" --root "$DIR" work --workers 2 --auto --json >"$DIR/out2.jsonl" 2>"$DIR/err2.log" &
P2=$!
set +e
wait "$P1"
E1=$?
wait "$P2"
E2=$?
set -e
assert_eq "$E1" 0 "primeiro pool"
assert_eq "$E2" 0 "segundo pool"

SUCCEEDED=$( { grep -h -c '"status":"succeeded"' "$DIR/out1.jsonl" "$DIR/out2.jsonl" || true; } | awk '{s+=$1} END{print s+0}' )
FAILED=$( { grep -h -c '"status":"failed"' "$DIR/out1.jsonl" "$DIR/out2.jsonl" || true; } | awk '{s+=$1} END{print s+0}' )
assert_eq "$SUCCEEDED" 8 "tarefas concluídas nos dois pools"
assert_eq "$FAILED" 0 "tarefas falhas nos dois pools"

RUNS=$(find "$DIR/.aiw/runs" -name run.json | wc -l | tr -d ' ')
DONE=$(find "$DIR/tasks/done" -name '*.md' | wc -l | tr -d ' ')
CLAIMS=$(find "$DIR/.aiw/state/claims" -type f -name '*.lock' | wc -l | tr -d ' ')
RUNNING=$( { grep -l '"status": "running"' "$DIR"/.aiw/state/tasks/*.json 2>/dev/null || true; } | wc -l | tr -d ' ')
BRANCHES=$(git -C "$DIR" branch --list 'aiw/*' | wc -l | tr -d ' ')
WORKTREES=$(find "$DIR/.aiw/worktrees" -mindepth 1 -maxdepth 1 -type d | wc -l | tr -d ' ')

assert_eq "$RUNS" 8 "runs criadas (exactly-once)"
assert_eq "$DONE" 8 "tarefas em done"
assert_eq "$CLAIMS" 0 "claims remanescentes"
assert_eq "$RUNNING" 0 "estados running remanescentes"
assert_eq "$BRANCHES" 8 "branches aiw/*"
assert_eq "$WORKTREES" 8 "worktrees"

for name in spec.md plan.md implement.md test.log diff.patch review.md; do
  COUNT=$(find "$DIR/.aiw/runs" -name "$name" | wc -l | tr -d ' ')
  assert_eq "$COUNT" 8 "artefato $name por run"
done

python3 - "$DIR" <<'PY'
import json
import pathlib
import subprocess
import sys

root = pathlib.Path(sys.argv[1])
runs = [json.loads(p.read_text()) for p in (root / ".aiw/runs").glob("*/run.json")]
assert len(runs) == 8, f"runs={len(runs)}"
ids = [run["task"]["external_id"] for run in runs]
assert len(set(ids)) == 8, f"ids duplicados: {ids}"
for run in runs:
    assert run["status"] == "succeeded", (run["id"], run["status"])
    worktree = pathlib.Path(run["worktree"])
    branch = run["branch"]
    content = subprocess.check_output(
        ["git", "-C", str(worktree), "show", "HEAD:AIW_CHANGE.md"], text=True
    ).strip()
    run_suffix = branch.rsplit("/", 1)[-1]
    assert run_suffix in content, (branch, content)
    log = subprocess.check_output(
        ["git", "-C", str(worktree), "log", "--oneline", "-1"], text=True
    )
    assert "aiw:" in log, log
    status = subprocess.check_output(
        ["git", "-C", str(worktree), "status", "--porcelain"], text=True
    )
    assert status.strip() == "", f"worktree sujo em {run['id']}: {status!r}"

states = [json.loads(p.read_text()) for p in (root / ".aiw/state/tasks").glob("*.json")]
assert len(states) == 8, f"states={len(states)}"
for state in states:
    assert state["status"] == "succeeded", state
print("ok   isolamento de artefatos, commits e estado por tarefa")
PY
pass "dois pools concorrentes: 8 tarefas, exactly-once, claims liberados"

# 2) Falha isolada: uma tarefa com repo inválido não derruba as outras.
DIR="$WORK/failure"
new_project "$DIR"
write_task "$DIR" "good-a" "Tarefa boa A"
write_task "$DIR" "bad-repo" "Tarefa com repo inválido" "./missing"
write_task "$DIR" "good-b" "Tarefa boa B"

export AIW_SLOW_SECONDS=0.1
set +e
"$AIW" --root "$DIR" work --workers 3 --auto --json >"$DIR/out.jsonl" 2>"$DIR/err.log"
FAIL_CODE=$?
set -e
if [ "$FAIL_CODE" -eq 0 ]; then
  fail "work deveria sair com erro quando uma tarefa falha"
fi
SUCCEEDED=$(grep -c '"status":"succeeded"' "$DIR/out.jsonl" || true)
FAILED=$(grep -c '"status":"failed"' "$DIR/out.jsonl" || true)
assert_eq "$SUCCEEDED" 2 "sucessos com falha isolada"
assert_eq "$FAILED" 1 "falhas com falha isolada"
DONE=$(find "$DIR/tasks/done" -name '*.md' | wc -l | tr -d ' ')
CLAIMS=$(find "$DIR/.aiw/state/claims" -type f -name '*.lock' | wc -l | tr -d ' ')
FAILED_STATE=$( { grep -l '"status": "failed"' "$DIR"/.aiw/state/tasks/*.json 2>/dev/null || true; } | wc -l | tr -d ' ')
assert_eq "$DONE" 2 "tarefas boas em done"
assert_eq "$CLAIMS" 0 "claims após falha"
assert_eq "$FAILED_STATE" 1 "estado failed registrado"
pass "falha isolada: 2 concluídas, 1 falhou, claims liberados"

# 3) Cancelamento: SIGINT libera claims, não move para failed e permite rerun.
DIR="$WORK/cancel"
new_project "$DIR"
write_task "$DIR" "cancel-a" "Tarefa cancelável A"
write_task "$DIR" "cancel-b" "Tarefa cancelável B"

export AIW_SLOW_SECONDS=5
"$AIW" --root "$DIR" work --workers 2 --auto --json >"$DIR/out-cancel.jsonl" 2>"$DIR/err-cancel.log" &
PID=$!
sleep 1
kill -INT "$PID"
set +e
wait "$PID"
CANCEL_CODE=$?
set -e
if [ "$CANCEL_CODE" -eq 0 ]; then
  fail "work cancelado deveria sair com erro"
fi
CLAIMS=$(find "$DIR/.aiw/state/claims" -type f -name '*.lock' | wc -l | tr -d ' ')
RUNNING=$( { grep -l '"status": "running"' "$DIR"/.aiw/state/tasks/*.json 2>/dev/null || true; } | wc -l | tr -d ' ')
DONE=$(find "$DIR/tasks/done" -name '*.md' | wc -l | tr -d ' ')
assert_eq "$CLAIMS" 0 "claims após cancelamento"
assert_eq "$RUNNING" 0 "estados running após cancelamento"
assert_eq "$DONE" 0 "tarefas em done após cancelamento"

export AIW_SLOW_SECONDS=0.1
"$AIW" --root "$DIR" work --workers 2 --auto --json >"$DIR/out-retry.jsonl" 2>"$DIR/err-retry.log"
SUCCEEDED=$(grep -c '"status":"succeeded"' "$DIR/out-retry.jsonl" || true)
assert_eq "$SUCCEEDED" 2 "sucessos após cancelamento e rerun"
DONE=$(find "$DIR/tasks/done" -name '*.md' | wc -l | tr -d ' ')
CLAIMS=$(find "$DIR/.aiw/state/claims" -type f -name '*.lock' | wc -l | tr -d ' ')
assert_eq "$DONE" 2 "tarefas concluídas após rerun"
assert_eq "$CLAIMS" 0 "claims após rerun"
pass "cancelamento: claims liberados, tarefas reexecutadas"

echo "verify ok"
