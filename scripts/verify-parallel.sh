#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
AIW="$ROOT/bin/aiw"
WORK="$(mktemp -d /tmp/aiw-verify.XXXXXX)"
cleanup() {
  if [ "${AIW_VERIFY_KEEP:-0}" = "1" ]; then
    echo "workdir preservado: $WORK"
  else
    rm -rf "$WORK"
  fi
}
trap cleanup EXIT

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
  intake)
    printf '%s\n' \
      '# Intake' \
      '## Resumo' \
      'Tarefa pronta.' \
      '## Critérios de aceite' \
      '- O pipeline conclui a tarefa.' \
      '## Perguntas abertas' \
      '## Decisão' \
      'DECISION: READY' \
      'RISK: LOW'
    ;;
  spec-validator|plan-validator)
    echo "DECISION: APPROVED"
    ;;
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
  mkdir -p "$dir/tasks/doing" "$dir/tasks/done" "$dir/tasks/failed" "$dir/.aiw"
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
    doing_dir: ./tasks/doing
    done_dir: ./tasks/done
    failed_dir: ./tasks/failed
  alias:
    driver: folder
    path: ./tasks
    doing_dir: ./tasks/doing
    done_dir: ./tasks/done
    failed_dir: ./tasks/failed
    labels: [aiw]
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

Implementar a tarefa "$title" com validação automática do pipeline.
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
if [ "$E1" -ne 0 ]; then
  tail -5 "$DIR/out1.jsonl" >&2 || true
  tail -10 "$DIR/err1.log" >&2 || true
fi
if [ "$E2" -ne 0 ]; then
  tail -5 "$DIR/out2.jsonl" >&2 || true
  tail -10 "$DIR/err2.log" >&2 || true
fi
assert_eq "$E1" 0 "primeiro pool"
assert_eq "$E2" 0 "segundo pool"

SUCCEEDED=$( { grep -h -c '"status":"succeeded"' "$DIR/out1.jsonl" "$DIR/out2.jsonl" || true; } | awk '{s+=$1} END{print s+0}' )
FAILED=$( { grep -h -c '"status":"failed"' "$DIR/out1.jsonl" "$DIR/out2.jsonl" || true; } | awk '{s+=$1} END{print s+0}' )
assert_eq "$SUCCEEDED" 8 "tarefas concluídas nos dois pools"
assert_eq "$FAILED" 0 "tarefas falhas nos dois pools"

RUNS=$( { find "$DIR/.aiw/runs" -name run.json 2>/dev/null || true; } | wc -l | tr -d ' ')
DONE=$(find "$DIR/tasks/done" -name '*.md' | wc -l | tr -d ' ')
CLAIMS=$(find "$DIR/.aiw/state/claims" -type f -name '*.lock' | wc -l | tr -d ' ')
RUNNING=$( { grep -l '"status": "running"' "$DIR"/.aiw/state/tasks/*.json 2>/dev/null || true; } | wc -l | tr -d ' ')
BRANCHES=$(git -C "$DIR" branch --list 'aiw/*' | wc -l | tr -d ' ')
WORKTREES=$(find "$DIR/.aiw/worktrees" -mindepth 1 -maxdepth 1 -type d | wc -l | tr -d ' ')
DOING=$(find "$DIR/tasks/doing" -name '*.md' | wc -l | tr -d ' ')
INBOX=$(find "$DIR/tasks" -maxdepth 1 -name '*.md' | wc -l | tr -d ' ')

assert_eq "$RUNS" 8 "runs criadas (exactly-once)"
assert_eq "$DONE" 8 "tarefas em done"
assert_eq "$CLAIMS" 0 "claims remanescentes"
assert_eq "$RUNNING" 0 "estados running remanescentes"
assert_eq "$BRANCHES" 8 "branches aiw/*"
assert_eq "$WORKTREES" 8 "worktrees"
assert_eq "$DOING" 0 "pasta doing vazia"
assert_eq "$INBOX" 0 "inbox vazio"

for name in intake.md spec.md spec-review.md plan.md plan-review.md implement.md test.log diff.patch review.md; do
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

# 2) Aliases folder: claims canônicos entre sources com a mesma pasta.
DIR="$WORK/aliases"
new_project "$DIR"
for i in 1 2 3 4; do
  write_task "$DIR" "alias-$i" "Tarefa alias $i"
done
export AIW_SLOW_SECONDS=0.2
"$AIW" --root "$DIR" work --source folder --workers 2 --auto --json >"$DIR/folder.jsonl" 2>"$DIR/folder.err" &
A1=$!
"$AIW" --root "$DIR" work --source alias --workers 2 --auto --json >"$DIR/alias.jsonl" 2>"$DIR/alias.err" &
A2=$!
set +e
wait "$A1"
AE1=$?
wait "$A2"
AE2=$?
set -e
assert_eq "$AE1" 0 "pool folder"
assert_eq "$AE2" 0 "pool alias"
SUCCEEDED=$( { grep -h -c '"status":"succeeded"' "$DIR/folder.jsonl" "$DIR/alias.jsonl" || true; } | awk '{s+=$1} END{print s+0}' )
RUNS=$( { find "$DIR/.aiw/runs" -name run.json 2>/dev/null || true; } | wc -l | tr -d ' ')
DONE=$(find "$DIR/tasks/done" -name '*.md' | wc -l | tr -d ' ')
DOING=$(find "$DIR/tasks/doing" -name '*.md' | wc -l | tr -d ' ')
CLAIMS=$(find "$DIR/.aiw/state/claims" -type f -name '*.lock' | wc -l | tr -d ' ')
assert_eq "$SUCCEEDED" 4 "sucessos com aliases"
assert_eq "$RUNS" 4 "runs com aliases (exactly-once)"
assert_eq "$DONE" 4 "tarefas em done com aliases"
assert_eq "$DOING" 0 "doing vazio com aliases"
assert_eq "$CLAIMS" 0 "claims após aliases"
pass "aliases folder: claim canônico exactly-once"

# 3) Falha isolada: uma tarefa com repo inválido não derruba as outras.
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
FAILED_FILES=$(find "$DIR/tasks/failed" -name '*.md' | wc -l | tr -d ' ')
DOING=$(find "$DIR/tasks/doing" -name '*.md' | wc -l | tr -d ' ')
CLAIMS=$(find "$DIR/.aiw/state/claims" -type f -name '*.lock' | wc -l | tr -d ' ')
FAILED_STATE=$( { grep -l '"status": "failed"' "$DIR"/.aiw/state/tasks/*.json 2>/dev/null || true; } | wc -l | tr -d ' ')
assert_eq "$DONE" 2 "tarefas boas em done"
assert_eq "$FAILED_FILES" 1 "tarefa ruim em failed"
assert_eq "$DOING" 0 "doing vazio após falha"
assert_eq "$CLAIMS" 0 "claims após falha"
assert_eq "$FAILED_STATE" 1 "estado failed registrado"
pass "falha isolada: 2 concluídas, 1 falhou, claims liberados"

# 3) Cancelamento: SIGINT libera claims, não move para failed e permite rerun.
DIR="$WORK/cancel"
new_project "$DIR"
write_task "$DIR" "cancel-a" "Tarefa cancelável A"
write_task "$DIR" "cancel-b" "Tarefa cancelável B"

export AIW_SLOW_SECONDS=5
"$AIW" --root "$DIR" work --workers 2 --auto --drain-timeout 1s --json >"$DIR/out-cancel.jsonl" 2>"$DIR/err-cancel.log" &
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
DOING=$(find "$DIR/tasks/doing" -name '*.md' | wc -l | tr -d ' ')
INBOX=$(find "$DIR/tasks" -maxdepth 1 -name '*.md' | wc -l | tr -d ' ')
assert_eq "$CLAIMS" 0 "claims após cancelamento"
assert_eq "$RUNNING" 0 "estados running após cancelamento"
assert_eq "$DONE" 0 "tarefas em done após cancelamento"
assert_eq "$DOING" 0 "doing vazio após cancelamento"
assert_eq "$INBOX" 2 "tarefas devolvidas ao inbox após cancelamento"

export AIW_SLOW_SECONDS=0.1
"$AIW" --root "$DIR" work --workers 2 --auto --json >"$DIR/out-retry.jsonl" 2>"$DIR/err-retry.log"
SUCCEEDED=$(grep -c '"status":"succeeded"' "$DIR/out-retry.jsonl" || true)
assert_eq "$SUCCEEDED" 2 "sucessos após cancelamento e rerun"
DONE=$(find "$DIR/tasks/done" -name '*.md' | wc -l | tr -d ' ')
CLAIMS=$(find "$DIR/.aiw/state/claims" -type f -name '*.lock' | wc -l | tr -d ' ')
assert_eq "$DONE" 2 "tarefas concluídas após rerun"
assert_eq "$CLAIMS" 0 "claims após rerun"
pass "cancelamento: claims liberados, tarefas reexecutadas"

# 4) Intake: needs_input, perguntas e reprocessamento após editar a tarefa.
DIR="$WORK/needs-input"
new_project "$DIR"
cat > "$DIR/tasks/needs.md" <<'TASK'
---
id: needs
title: Tarefa sem contexto
repo: .
labels: [aiw]
acceptance:
  - O pipeline conclui a tarefa.
---
curta
TASK

export AIW_SLOW_SECONDS=0.1
"$AIW" --root "$DIR" work --workers 1 --auto --json >"$DIR/out.jsonl" 2>"$DIR/err.log"
NEEDS=$(grep -c '"status":"needs_input"' "$DIR/out.jsonl" || true)
SUCCEEDED=$(grep -c '"status":"succeeded"' "$DIR/out.jsonl" || true)
DONE=$(find "$DIR/tasks/done" -name '*.md' | wc -l | tr -d ' ')
CLAIMS=$(find "$DIR/.aiw/state/claims" -type f -name '*.lock' | wc -l | tr -d ' ')
RUN_DIR=$(find "$DIR/.aiw/runs" -mindepth 1 -maxdepth 1 -type d | head -1)
DOING=$(find "$DIR/tasks/doing" -name '*.md' | wc -l | tr -d ' ')
INBOX=$(find "$DIR/tasks" -maxdepth 1 -name '*.md' | wc -l | tr -d ' ')
assert_eq "$NEEDS" 1 "tarefa marcada needs_input"
assert_eq "$SUCCEEDED" 0 "nenhuma tarefa concluída com input faltando"
assert_eq "$DONE" 0 "nada em done com input faltando"
assert_eq "$DOING" 0 "doing vazio com needs_input"
assert_eq "$INBOX" 1 "tarefa devolvida ao inbox com needs_input"
assert_eq "$CLAIMS" 0 "claims após needs_input"
[ -f "$RUN_DIR/questions.md" ] || fail "questions.md não foi criado"

cat > "$DIR/tasks/needs.md" <<'TASK'
---
id: needs
title: Tarefa sem contexto
repo: .
labels: [aiw]
acceptance:
  - O pipeline conclui a tarefa.
---
Descrição agora suficientemente detalhada para executar a tarefa.
TASK

"$AIW" --root "$DIR" work --workers 1 --auto --json >"$DIR/out2.jsonl" 2>"$DIR/err2.log"
SUCCEEDED=$(grep -c '"status":"succeeded"' "$DIR/out2.jsonl" || true)
DONE=$(find "$DIR/tasks/done" -name '*.md' | wc -l | tr -d ' ')
CLAIMS=$(find "$DIR/.aiw/state/claims" -type f -name '*.lock' | wc -l | tr -d ' ')
assert_eq "$SUCCEEDED" 1 "tarefa reprocessada após editar"
assert_eq "$DONE" 1 "tarefa em done após editar"
assert_eq "$CLAIMS" 0 "claims após reprocessamento"
pass "intake: needs_input, perguntas e reprocessamento após edição"

# 5) Pause/resume: pausa no boundary e retoma.
DIR="$WORK/pause"
new_project "$DIR"
write_task "$DIR" "pause-a" "Tarefa pausável"

export AIW_SLOW_SECONDS=0.5
"$AIW" --root "$DIR" work --workers 1 --auto --json >"$DIR/out.jsonl" 2>"$DIR/err.log" &
PID=$!
RUN_ID=""
for i in $(seq 1 100); do
  RUN_ID=$(python3 - "$DIR" <<'PYRUN'
import json
import pathlib
import sys
runs = pathlib.Path(sys.argv[1]) / ".aiw/runs"
for path in runs.glob("*/run.json"):
    data = json.loads(path.read_text())
    if data.get("status") == "running":
        print(data["id"])
        break
PYRUN
)
  [ -n "$RUN_ID" ] && break
  sleep 0.1
done
[ -n "$RUN_ID" ] || fail "run não iniciou para o teste de pause"
"$AIW" --root "$DIR" pause "$RUN_ID" >"$DIR/pause.log" 2>&1
set +e
wait "$PID"
PAUSE_CODE=$?
set -e
assert_eq "$PAUSE_CODE" 0 "work após pausa"
PAUSED=$(grep -c '"status":"paused"' "$DIR/out.jsonl" || true)
DOING=$(find "$DIR/tasks/doing" -name '*.md' | wc -l | tr -d ' ')
CLAIMS=$(find "$DIR/.aiw/state/claims" -type f -name '*.lock' | wc -l | tr -d ' ')
assert_eq "$PAUSED" 1 "resultado paused"
assert_eq "$DOING" 1 "tarefa pausada permanece em doing"
assert_eq "$CLAIMS" 0 "claims após pausa"

export AIW_SLOW_SECONDS=0.05
"$AIW" --root "$DIR" resume "$RUN_ID" --auto >"$DIR/resume.log" 2>&1
DONE=$(find "$DIR/tasks/done" -name '*.md' | wc -l | tr -d ' ')
CLAIMS=$(find "$DIR/.aiw/state/claims" -type f -name '*.lock' | wc -l | tr -d ' ')
assert_eq "$DONE" 1 "tarefa concluída após resume"
assert_eq "$CLAIMS" 0 "claims após resume"
pass "pause: boundary, doing e resume"

# 5b) Pausa global: work pausa, run bloqueado, daemon idle e resume --all.
DIR="$WORK/global-pause"
new_project "$DIR"
write_task "$DIR" "global-a" "Tarefa pausa global A"
write_task "$DIR" "global-b" "Tarefa pausa global B"

export AIW_SLOW_SECONDS=0.5
"$AIW" --root "$DIR" work --workers 1 --auto --pause-poll 50ms --json >"$DIR/out.jsonl" 2>"$DIR/err.log" &
PID=$!
RUN_ID=""
for i in $(seq 1 100); do
  RUN_ID=$(python3 - "$DIR" <<'PYRUN'
import json
import pathlib
import sys
runs = pathlib.Path(sys.argv[1]) / ".aiw/runs"
for path in runs.glob("*/run.json"):
    data = json.loads(path.read_text())
    if data.get("status") == "running":
        print(data["id"])
        break
PYRUN
)
  [ -n "$RUN_ID" ] && break
  sleep 0.1
done
[ -n "$RUN_ID" ] || fail "run não iniciou para o teste de pausa global"
"$AIW" --root "$DIR" pause >"$DIR/pause-global.log" 2>&1
[ -f "$DIR/.aiw/PAUSE" ] || fail "marker de pausa global não criado"
set +e
wait "$PID"
GLOBAL_WORK_CODE=$?
set -e
assert_eq "$GLOBAL_WORK_CODE" 0 "work após pausa global"
PAUSED=$(grep -c '"status":"paused"' "$DIR/out.jsonl" || true)
INBOX=$(find "$DIR/tasks" -maxdepth 1 -name '*.md' | wc -l | tr -d ' ')
DOING=$(find "$DIR/tasks/doing" -name '*.md' | wc -l | tr -d ' ')
assert_eq "$PAUSED" 1 "resultado paused na pausa global"
assert_eq "$INBOX" 1 "tarefa não iniciada permanece no inbox"
assert_eq "$DOING" 1 "tarefa pausada permanece em doing"

set +e
"$AIW" --root "$DIR" run --task global-b >"$DIR/blocked.log" 2>&1
BLOCKED_CODE=$?
set -e
assert_eq "$BLOCKED_CODE" 1 "run bloqueado pela pausa global"
BLOCKED=$(grep -c 'pausa global ativa' "$DIR/blocked.log" || true)
assert_eq "$BLOCKED" 1 "mensagem de bloqueio do run"

"$AIW" --root "$DIR" daemon --interval 100ms --pause-poll 100ms --status-addr 127.0.0.1:0 --auto >"$DIR/daemon.log" 2>"$DIR/daemon.err" &
DAEMON_PID=$!
ADDR=""
for i in $(seq 1 50); do
  ADDR=$(grep -o 'http://[^/]*' "$DIR/daemon.log" 2>/dev/null | head -1 || true)
  [ -n "$ADDR" ] && break
  sleep 0.1
done
[ -n "$ADDR" ] || fail "daemon não publicou status na pausa global"
for i in $(seq 1 50); do
  grep -q 'pausa global ativa; idle' "$DIR/daemon.log" 2>/dev/null && break
  sleep 0.1
done
grep -q 'pausa global ativa; idle' "$DIR/daemon.log" || fail "daemon não entrou em idle"
curl -fsS "$ADDR/status" >"$DIR/status-paused.json"
python3 - "$DIR/status-paused.json" <<'PYSTATUS'
import json
import sys
status = json.load(open(sys.argv[1]))
assert status["paused"] is True, status
PYSTATUS
sleep 0.4
INBOX=$(find "$DIR/tasks" -maxdepth 1 -name '*.md' | wc -l | tr -d ' ')
assert_eq "$INBOX" 1 "daemon idle não coleta tarefas"

export AIW_SLOW_SECONDS=0.05
"$AIW" --root "$DIR" resume --all --auto >"$DIR/resume-all.log" 2>&1
grep -q 'pausa global removida' "$DIR/resume-all.log" || fail "resume --all não removeu a pausa global"
[ ! -f "$DIR/.aiw/PAUSE" ] || fail "marker de pausa global permaneceu"
for i in $(seq 1 100); do
  DONE=$(find "$DIR/tasks/done" -name '*.md' | wc -l | tr -d ' ')
  [ "$DONE" = "2" ] && break
  sleep 0.1
done
DONE=$(find "$DIR/tasks/done" -name '*.md' | wc -l | tr -d ' ')
INBOX=$(find "$DIR/tasks" -maxdepth 1 -name '*.md' | wc -l | tr -d ' ')
DOING=$(find "$DIR/tasks/doing" -name '*.md' | wc -l | tr -d ' ')
CLAIMS=$(find "$DIR/.aiw/state/claims" -type f -name '*.lock' | wc -l | tr -d ' ')
assert_eq "$DONE" 2 "tarefas concluídas após resume --all"
assert_eq "$INBOX" 0 "inbox vazio após resume --all"
assert_eq "$DOING" 0 "doing vazio após resume --all"
assert_eq "$CLAIMS" 0 "claims após resume --all"
kill -INT "$DAEMON_PID"
set +e
wait "$DAEMON_PID"
DAEMON_CODE=$?
set -e
assert_eq "$DAEMON_CODE" 0 "daemon encerrado após resume --all"
pass "pausa global: work pausa, run bloqueado, daemon idle e resume --all"

# 6) Drain: SIGINT espera o run em andamento e deixa o próximo no inbox.
DIR="$WORK/drain"
new_project "$DIR"
write_task "$DIR" "drain-a" "Tarefa drain A"
write_task "$DIR" "drain-b" "Tarefa drain B"

export AIW_SLOW_SECONDS=0.15
"$AIW" --root "$DIR" work --workers 1 --auto --json >"$DIR/out.jsonl" 2>"$DIR/err.log" &
PID=$!
for i in $(seq 1 100); do
  RUNS=$( { find "$DIR/.aiw/runs" -name run.json 2>/dev/null || true; } | wc -l | tr -d ' ')
  [ "$RUNS" -ge 1 ] && break
  sleep 0.1
done
kill -INT "$PID"
set +e
wait "$PID"
DRAIN_CODE=$?
set -e
assert_eq "$DRAIN_CODE" 0 "drain gracioso"
SUCCEEDED=$(grep -c '"status":"succeeded"' "$DIR/out.jsonl" || true)
DONE=$(find "$DIR/tasks/done" -name '*.md' | wc -l | tr -d ' ')
INBOX=$(find "$DIR/tasks" -maxdepth 1 -name '*.md' | wc -l | tr -d ' ')
DOING=$(find "$DIR/tasks/doing" -name '*.md' | wc -l | tr -d ' ')
CLAIMS=$(find "$DIR/.aiw/state/claims" -type f -name '*.lock' | wc -l | tr -d ' ')
assert_eq "$SUCCEEDED" 1 "run em andamento concluída no drain"
assert_eq "$DONE" 1 "uma tarefa concluída"
assert_eq "$INBOX" 1 "uma tarefa pendente no inbox"
assert_eq "$DOING" 0 "doing vazio após drain"
assert_eq "$CLAIMS" 0 "claims após drain"
pass "drain: SIGINT conclui o run atual e preserva a fila"

# 7) Daemon: um ciclo batch contínuo com o mesmo pool.
DIR="$WORK/daemon"
new_project "$DIR"
write_task "$DIR" "daemon-a" "Tarefa do daemon A"
write_task "$DIR" "daemon-b" "Tarefa do daemon B"

export AIW_SLOW_SECONDS=0.1
"$AIW" --root "$DIR" daemon --once --workers 2 --auto --json >"$DIR/out.jsonl" 2>"$DIR/err.log"
SUCCEEDED=$(grep -c '"status":"succeeded"' "$DIR/out.jsonl" || true)
DONE=$(find "$DIR/tasks/done" -name '*.md' | wc -l | tr -d ' ')
DOING=$(find "$DIR/tasks/doing" -name '*.md' | wc -l | tr -d ' ')
CLAIMS=$(find "$DIR/.aiw/state/claims" -type f -name '*.lock' | wc -l | tr -d ' ')
assert_eq "$SUCCEEDED" 2 "sucessos no daemon"
assert_eq "$DONE" 2 "tarefas em done pelo daemon"
assert_eq "$DOING" 0 "doing vazio após daemon"
assert_eq "$CLAIMS" 0 "claims após daemon"
pass "daemon: ciclo batch, pool e write-back"

# 6) Daemon: endpoints /healthz e /status.
DIR="$WORK/status"
new_project "$DIR"
"$AIW" --root "$DIR" daemon --interval 1h --status-addr 127.0.0.1:0 >"$DIR/out.log" 2>"$DIR/err.log" &
PID=$!
ADDR=""
for i in $(seq 1 50); do
  ADDR=$(grep -o 'http://[^/]*' "$DIR/out.log" 2>/dev/null | head -1 || true)
  [ -n "$ADDR" ] && break
  sleep 0.1
done
[ -n "$ADDR" ] || fail "daemon não publicou o endereço de status"
HEALTH=$(curl -fsS "$ADDR/healthz" || true)
assert_eq "$HEALTH" "ok" "healthz do daemon"
curl -fsS "$ADDR/status" >"$DIR/status.json"
python3 - "$DIR/status.json" <<'PYSTATUS'
import json
import sys
status = json.load(open(sys.argv[1]))
assert status["workers"] >= 1, status
assert status["interval"] == "1h0m0s", status
assert "cycles" in status and "claims" in status, status
PYSTATUS
kill -INT "$PID"
wait "$PID"
pass "daemon: /healthz e /status"

# 7) Release: devolve tarefa pausada em doing para o inbox.
DIR="$WORK/release"
new_project "$DIR"
write_task "$DIR" "release-a" "Tarefa pausada"

python3 - "$DIR" <<'PYCONF'
import sys
from pathlib import Path
path = Path(sys.argv[1]) / ".aiw/config.yaml"
text = path.read_text().replace("    spec: false", "    spec: human")
path.write_text(text)
PYCONF

export AIW_SLOW_SECONDS=0.1
"$AIW" --root "$DIR" work --workers 1 --json >"$DIR/out.jsonl" 2>"$DIR/err.log"
PAUSED=$(grep -c '"status":"paused"' "$DIR/out.jsonl" || true)
assert_eq "$PAUSED" 1 "tarefa pausada no gate spec"
DOING=$(find "$DIR/tasks/doing" -name '*.md' | wc -l | tr -d ' ')
assert_eq "$DOING" 1 "tarefa pausada permanece em doing"
RUN_ID=$(python3 - "$DIR" <<'PYRUN'
import json
import pathlib
import sys
runs = pathlib.Path(sys.argv[1]) / ".aiw/runs"
for path in runs.glob("*/run.json"):
    data = json.loads(path.read_text())
    if data["status"] == "paused":
        print(data["id"])
        break
PYRUN
)
[ -n "$RUN_ID" ] || fail "run pausada não encontrada"
"$AIW" --root "$DIR" release "$RUN_ID" >"$DIR/release.log" 2>&1
INBOX=$(find "$DIR/tasks" -maxdepth 1 -name '*.md' | wc -l | tr -d ' ')
DOING=$(find "$DIR/tasks/doing" -name '*.md' | wc -l | tr -d ' ')
CLAIMS=$(find "$DIR/.aiw/state/claims" -type f -name '*.lock' | wc -l | tr -d ' ')
TODO=$(grep -l '"status": "todo"' "$DIR"/.aiw/state/tasks/*.json 2>/dev/null | wc -l | tr -d ' ')
assert_eq "$INBOX" 1 "tarefa devolvida ao inbox"
assert_eq "$DOING" 0 "doing vazio após release"
assert_eq "$CLAIMS" 0 "claims após release"
assert_eq "$TODO" 1 "estado todo após release"
pass "release: pausada em doing devolvida ao inbox"

echo "verify ok"
