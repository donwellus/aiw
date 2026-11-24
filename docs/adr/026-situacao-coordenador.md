# ADR 026 — Situação determinística para o coordenador

- Status: aceito
- Data: 2026-11-24

## Contexto

O coordenador errou ao inferir slots e estado de pausa a partir de tabelas
parciais: `aiw_status` não mostrava queued, `runs` podia exibir run cancelada
antiga como atual e `workers` era confundido com número de runs. O modelo
precisava adivinhar.

## Decisão

1. Nova ferramenta MCP read-only `aiw_situation` retorna:
   - daemon: ativo, pid, worker_slots, intervalo, heartbeat;
   - capacity: worker_slots, active_runs, queued_tasks,
     active/queued control operations, over_worker_slots e explicação;
   - tarefas agrupadas em queued, running, paused, needs_input e failed;
   - pausas com gate, motivo e últimos blockers de `validation.rejected`;
   - operações de controle, claims e alertas.
2. `queued` vem do estado persistido; não da memória do daemon.
3. O prompt do coordenador exige começar por `aiw_situation`, consultar
   `run_show`/`events` para pausas e nunca inferir slots de `workers`.
4. O coordinator deve dizer explicitamente quando não há evidência.
5. `worker_slots` é apenas o pool de workers; operações de controle podem
   iniciar runs adicionais e aparecem separadas em capacity.

## Consequências

Positivas:

- Respostas do coordenador passam a citar fatos determinísticos.
- Fila, pausa, gate, blockers e capacidade ficam explícitos.
- Menos conclusões erradas sobre “esperando slot” ou “workers não aplicado”.
- O mesmo snapshot serve para CLI futura e automação.

Negativas e riscos:

- Mais um payload para o modelo; mitigado pelo resumo e agrupamento.
- Heartbeat/daemon ausente deve ser reportado como ausência de evidência.
- Operações de controle ainda não entram no limite de workers; o snapshot
  apenas explicita a diferença.
