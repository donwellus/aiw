# ADR 016 — Pause, drain e resume robusto

- Status: aceito
- Data: 2026-11-21

## Contexto

O pipeline só pausava em gates. Ctrl+C cancelava o tool call atual; `resume` retomava por estágio, mas `work`/daemon não tinham drain e o resume não reaplicava o estado na fonte.

## Decisão

1. **Pause request**:
   - `aiw pause <run-id>` grava `.aiw/runs/<id>/PAUSE`;
   - o pipeline verifica o marker no boundary entre estágios;
   - retorna `ErrPauseRequested`, run/estado `paused`, claim liberado; `doing`/labels permanecem.
2. **Resume**:
   - `Runner.Resume` centraliza claim canônico, estado `running`, `MarkRunning` na fonte, remoção do marker e `pipeline.Resume`;
   - mapeia `succeeded/paused/needs_input/canceled/failed` e faz `MarkPending`/`MarkFailed`;
   - estágio interrompido recomeça do início.
3. **Drain no work/daemon**:
   - primeiro SIGINT/SIGTERM fecha um canal de drain: workers não pegam novos jobs;
   - runs em andamento recebem o mesmo sinal no `pipeline.Options`;
   - cada run termina apenas o estágio atual e para no boundary seguinte como `paused`;
   - `--drain-timeout` (default 5m) limita a espera;
   - segundo SIGINT ou timeout cancela o contexto;
   - tarefas não iniciadas ficam na fila.
4. `Runner.Work`/pool recebe um canal de drain e o propaga ao pipeline; o estágio
   atual não é cancelado no drain normal.
5. Testes: unit de drain, cenário `verify` pause→resume e drain; página no README.

## Consequências

Positivas:

- Pausa sem matar a run e sem perder o trabalho já concluído.
- `work`/daemon encerram sem cancelar o estágio atual e preservam a run `paused`.
- Resume restaura visibilidade na fonte (doing/labels).

Negativas e riscos:

- Pausa só em boundary de estágio; tool calls longos atrasam a pausa.
- Claim fica liberado enquanto paused; `resume` precisa reivindicar.
- Drain espera o estágio atual, não a run inteira; `drain-timeout` controla
  estágios muito longos.
- Sem auto-resume de canceladas ao reiniciar daemon; retomada explícita.
