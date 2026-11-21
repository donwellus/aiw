# ADR 017 — Stop global com daemon ocioso

- Status: aceito
- Data: 2026-11-21

## Contexto

O pause por run (ADR-016) exige conhecer o run-id e não impede que `work`/daemon
comecem novas tarefas. Para parar o projeto inteiro — manutenção, incidente ou
custo — faltava um interruptor único, idempotente e fácil de retomar.

## Decisão

1. **Marker global**: `aiw pause` sem argumento grava `.aiw/PAUSE` via
   `internal/pause` (`Set`, `Clear`, `Active`, `Watch`). O marker é a única
   fonte de verdade e pode ser criado por scripts (`touch .aiw/PAUSE`).
2. **Pipeline**: além do marker por run, cada boundary de estágio consulta o
   marker global; se ativo, retorna `ErrPauseRequested`, registra `paused`
   e libera o claim.
3. **Bloqueio de start**: `aiw run` e `aiw work` falham com mensagem explícita
   enquanto o marker existir; `aiw resume <run-id>` também é bloqueado para
   forçar uma retomada consciente.
4. **Work**: um watcher (`--pause-poll`, default 10s) fecha o canal de drain
   quando o marker aparece. Workers não pegam novos jobs; o run atual termina o
   estágio e pausa no boundary. As demais tarefas ficam no inbox.
5. **Daemon idle**: com o marker ativo o daemon não chama `Collect`; registra
   `[daemon] pausa global ativa; idle`, checa o marker a cada `--pause-poll`
   (default 10s) e mantém `/healthz`, `/status` e webhooks respondendo. SIGINT
   continua drenando e encerrando o processo; a pausa nunca mata a run.
6. **Retomada**: `aiw resume --all` remove o marker e retoma, sequencialmente,
   runs `paused` e `canceled` (estado `todo` com run-id). `needs_input` e
   `failed` ficam de fora; falhas aparecem no resumo e retornam erro no final.
7. **Observabilidade**: `/status` expõe `"paused": true/false`; a saída do
   `work` reporta quantas tarefas não foram iniciadas por causa da pausa.

## Consequências

Positivas:

- Um comando para parar tudo sem matar runs nem perder trabalho concluído.
- Daemon permanece observável e volta automaticamente ao limpar o marker.
- `resume --all` fecha o ciclo de para/retoma com uma ação.
- O marker em arquivo é auditável e idempotente.

Negativas e riscos:

- Polling de até 10s: uma tarefa pode começar logo antes do marker ser visto;
  o boundary ainda a pausa sem cancelar.
- `resume --all` é sequencial e não substitui um pool para muitas runs.
- Runs `failed`/`needs_input` exigem ação explícita e não são retomadas pelo
  `--all`.
- O marker global não substitui pausa cirúrgica por run.
