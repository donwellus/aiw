# Coordenador via OpenCode + MCP

Esta é a fase 1 do coordenador do aiw: **read-only**.

A arquitetura separa:

- **Coordenador**: agente primário do OpenCode, sempre disponível para conversa.
- **Control plane**: `aiw mcp`, servidor MCP local com ferramentas de leitura.
- **Workers**: runs do pipeline aiw, executadas pelo `aiw daemon` ou `aiw work`.

O coordenador não edita código, não abre worktree e não executa tarefas. Ele
consulta o estado e responde; quem trabalha são os workers.

## Requisitos

- `aiw` no `PATH` (`make install` ou `go install ./cmd/aiw`).
- OpenCode instalado.
- Um projeto aiw configurado (`.aiw/config.yaml`).

## 1. Suba os workers separadamente

```bash
aiw daemon --workers 2 --interval 60s
```

Sem daemon, o coordenador ainda consegue ler o estado, mas nenhum worker novo
vai executar.

## 2. Configure o MCP do aiw

Copie o exemplo para o projeto (se já existir `opencode.jsonc`, mescle apenas a
entrada `mcp.servers.aiw`):

```bash
mkdir -p .opencode/agents
cp /caminho/para/aiw/examples/opencode/opencode.jsonc ./opencode.jsonc
cp /caminho/para/aiw/examples/opencode/agents/aiw-coordinator.md .opencode/agents/
```

O `opencode.jsonc` registra o servidor local:

```jsonc
{
  "$schema": "https://opencode.ai/config.json",
  "mcp": {
    "servers": {
      "aiw": {
        "type": "local",
        "command": ["aiw", "mcp"],
        "cwd": "."
      }
    }
  }
}
```

Ajuste `command` se o binário não estiver no `PATH`.

## 3. Abra o coordenador

```bash
opencode
```

Selecione o agente `aiw-coordinator` e converse normalmente.

Para manter o coordenador disponível em outro terminal:

```bash
opencode serve
opencode attach <url>
```

O MCP inicia sob demanda e não escreve nada em stdout que não seja protocolo
MCP.

## Ferramentas disponíveis

O cliente OpenCode expõe os nomes prefixados pelo servidor `aiw`:

| Ferramenta | Função |
|---|---|
| `aiw_status` | resumo, tarefas ativas, claims, alertas e pausa global |
| `aiw_report` | relatório completo ou `summary_only` |
| `aiw_runs` | runs recentes, filtro por status e limite |
| `aiw_run_show` | detalhe da run + últimos eventos |
| `aiw_events` | eventos recentes de uma run ou de todas |

Todas são somente leitura. As ferramentas de controle (`pause`, `resume`,
`approve`, `release`, `clean`, `cancel`) entram na fase 2, junto com um modelo de
operações assíncronas para não bloquear a conversa.

## Exemplo de conversa

```text
Você: como está o projeto?

Coordenador:
  [aiw_status]
  Nenhuma tarefa ativa. 49 tarefas succeeded, 82 runs, custo acumulado US$ 2,73.

Você: alguma falhou?

Coordenador:
  [aiw_runs {status: "failed"}]
  A run X falhou no estágio test; erro Y. Quer que eu detalhe os eventos?
```

## Segurança

- O agente nega `edit`, `shell` e `subagent`; só usa `aiw_*` e leitura.
- O servidor não expõe escrita nesta versão.
- Workers continuam sujeitos a claims, worktrees, gates e permissões do aiw.
- O coordenador nunca faz merge, push ou deploy.

## Limitações da fase 1

- Sem pause/resume/approve/cancel pelo coordenador.
- Sem ferramenta de listagem direta da fila da fonte; use `aiw list` na CLI.
- Sem notificações proativas; o coordenador responde quando você fala.
- Dependências entre tarefas ainda dependem da issue #6.
- Requer manter `opencode`/`opencode serve` e o daemon vivos.

## Próximas fases

1. Controle assíncrono: pause/resume/approve/release/clean/cancel.
2. Lista/inspeção de issues e prioridade/hold.
3. Dependências e blocked (issue #6).
4. Notificações proativas via desktop/Telegram/Slack.
5. Interface própria `aiw coordinator` apenas se necessário.
