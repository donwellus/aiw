# ADR 025 — Fila persistida com estado `queued`

- Status: aceito
- Data: 2026-11-24

## Contexto

O `work`/daemon podia coletar vários candidatos e deixá-los em memória aguardando
um worker. O estado da tarefa continuava com o último run (por exemplo, uma run
cancelada) e `aiw status` não mostrava a fila. O coordenador interpretava tarefas
aguardando como canceladas ou sem retry.

## Decisão

1. Antes de iniciar o pool, `Runner.MarkQueued` grava cada candidato em
   `.aiw/state/tasks` com:
   - `status: "queued"`;
   - `run_id: ""`;
   - `content_hash` atual.
2. Não marca queued em `--dry-run` nem quando já existe run `running` com claim
   ativo.
3. `Runner.Check` considera `queued` elegível; ao iniciar, o estado vira
   `running`; se o claim estiver ocupado, permanece `queued`.
4. A fila sobrevive a restart: o próximo ciclo coleta as tarefas queued e as
   processa.
5. `aiw status` e MCP `status` incluem `queued`; `report` mostra no
   `by_status`.
6. `runview` não marca a run cancelada antiga como atual quando o estado da
   tarefa é `queued` ou `todo`; `runs --all` continua mostrando o histórico.
7. Operações de controle (`resume`, `resume_all`, `rerun`) continuam em fila
   separada e não são contabilizadas no limite de workers nesta fase.

## Consequências

Positivas:

- O coordenador enxerga tarefas aguardando worker.
- Restart do daemon não esconde nem perde a fila.
- Run cancelada histórica não aparece como execução atual.
- `queued` é compatível com claims atômicos existentes.

Negativas e riscos:

- Estado queued sem daemon ativo pode ficar visível até um novo ciclo.
- Se a fonte remover a tarefa, o estado queued pode ficar órfão até uma limpeza
  futura.
- Operações de controle ainda podem adicionar concorrência fora do limite de
  workers; um limitador global fica para outra fase.
