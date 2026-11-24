# Coordenador via OpenCode + MCP

O coordenador é um agente primário do OpenCode que conversa com o humano e
opera o aiw por MCP. Ele nunca executa tarefas: workers rodam no daemon em
worktrees isolados.

```text
OpenCode coordinator
        │ MCP (aiw mcp --allow-write)
        ▼
aiw control (pause/approve/release/clean + fila)
        │ operações persistentes
        ▼
aiw daemon (executor assíncrono + workers)
```

## Requisitos

- `aiw` no `PATH` (`make install` ou `go install ./cmd/aiw`).
- OpenCode instalado.
- Projeto aiw configurado (`.aiw/config.yaml`).
- Daemon ativo para `resume`.

## 1. Suba o daemon

```bash
aiw daemon --workers 2 --interval 60s
```

O daemon:

- executa workers;
- publica heartbeat em `.aiw/daemon.json`;
- processa a fila de controle a cada ~1s, inclusive em pausa global;
- é obrigatório para `resume` assíncrono.

## 2. Configure o MCP

```bash
mkdir -p .opencode/agents
cp /caminho/para/aiw/examples/opencode/opencode.jsonc ./opencode.jsonc
cp /caminho/para/aiw/examples/opencode/agents/aiw-coordinator.md .opencode/agents/
```

O exemplo registra:

```jsonc
{
  "$schema": "https://opencode.ai/config.json",
  "mcp": {
    "servers": {
      "aiw": {
        "type": "local",
        "command": ["aiw", "mcp", "--allow-write"],
        "cwd": "."
      }
    }
  }
}
```

Sem `--allow-write`, o servidor continua read-only e as ferramentas de escrita
não aparecem em `tools/list`.

## 3. Abra o coordenador

```bash
opencode
```

Selecione `aiw-coordinator`. Para outro terminal:

```bash
opencode serve
opencode attach <url>
```

## Ferramentas

Leitura:

| Ferramenta | Função |
|---|---|
| `aiw_status` | resumo, ativos, claims, alertas e pausa |
| `aiw_report` | relatório completo ou `summary_only` |
| `aiw_runs` | runs recentes/filtro |
| `aiw_run_show` | run + eventos |
| `aiw_events` | eventos recentes |
| `aiw_daemon_status` | heartbeat/atividade do daemon |
| `aiw_operations` | operações assíncronas |
| `aiw_operation_show` | detalhe/resultado de operação |
| `aiw_clean_preview` | prévia do clean |
| `aiw_questions` | perguntas/intake de uma run needs_input |

Controle:

| Ferramenta | Comportamento |
|---|---|
| `aiw_pause` | pausa global ou por run |
| `aiw_resume` | enfileira resume; retorna `operation_id` |
| `aiw_approve` | aprova gate intake/spec/plan |
| `aiw_release` | libera claim/estado — confirmação no OpenCode |
| `aiw_clean_apply` | exige `confirm=true` + confirmação no OpenCode |
| `aiw_operation_cancel` | cancela operação pendente/em execução |
| `aiw_cancel` | cancela run em andamento, pausada ou needs_input |
| `aiw_answer` | publica resposta na fonte e enfileira rerun |

`resume` não bloqueia: o daemon executa e o coordenador acompanha:

```text
aiw_resume {run_id: "..."}
→ operation_id: op-...

aiw_operation_show {operation_id: "op-..."}
→ status: running
→ ...
→ status: succeeded
→ result.status: succeeded
```

## Exemplo de conversa

```text
Você: como está o projeto?

Coordenador:
  [aiw_status]
  Nenhuma tarefa ativa. 49 succeeded, 82 runs, US$ 2,73.

Você: a run X está pausada no gate spec. Aprova e retoma.

Coordenador:
  [aiw_approve {run_id: "X", gate: "spec"}]
  [aiw_resume {run_id: "X", auto: true}]
  Operação op-... enfileirada.

Você: e o que falhou?

Coordenador:
  [aiw_runs {status: "failed"}]
  ...

Você: a tarefa X está needs_input. Responda: "use SQLite com OPFS".

Coordenador:
  [aiw_questions {run_id: "X"}]
  A pergunta é sobre a persistência. Vou publicar sua resposta.
  [aiw_answer {run_id: "X", text: "use SQLite com OPFS"}]
  Resposta publicada; rerun op-... enfileirado.

Você: cancela a run Y.

Coordenador:
  [aiw_cancel {run_id: "Y"}]
  Cancelamento solicitado; a tarefa será devolvida ao inbox.
```

## Segurança

- O agente nega `edit`, `shell` e `subagent`.
- `release` e `clean_apply` pedem confirmação no OpenCode.
- `clean_apply` também exige `confirm=true` no MCP.
- O coordenador não faz merge, push ou deploy.
- Workers continuam sujeitos a claims, worktrees, gates e permissões do aiw.
- Operações ficam auditáveis em `.aiw/control/operations/`.

## Limitações atuais

- Sem prioridade/hold por issue e sem dependências (issue #6).
- Sem notificações proativas.
- Operações `running` órfãs após crash do daemon exigem recuperação manual.
- É necessário manter OpenCode e o daemon vivos.
