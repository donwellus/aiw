# ADR 036 — Scheduler contínuo e drain por estágio

- Status: aceito
- Data: 2026-11-27

## Contexto

O daemon coletava um lote e chamava `runner.Work`, que bloqueava até todos os
jobs do lote terminarem. Uma tarefa longa mantinha o slot ocupado e impedia
novas issues de serem coletadas, mesmo com outros slots livres. O drain também
esperava a run inteira, embora pause já parasse no boundary de estágio.

## Decisão

1. O daemon passa a ter um **coletor independente** e um **pool fixo de
   workers**:
   - o coletor roda a cada `--interval` e por wake de webhook;
   - workers iniciam uma vez e consomem uma fila em memória;
   - conclusão de job acorda o coletor para preencher o slot.
2. `Runner.Pool` centraliza fila, pause, drain e outcomes; `Runner.Work`
   continua como wrapper batch para `aiw work` e `--once`.
3. O coletor mantém um mapa `scheduled` por task para não duplicar jobs;
   `MarkQueued` é gravado antes de o job ficar visível ao worker.
4. Tasks não iniciadas permanecem `queued` após drain/pausa e são recuperadas
   pelo estado persistido.
5. Pausa global e drain param no boundary entre estágios:
   - o estágio atual termina;
   - o próximo não inicia;
   - a run fica `paused` e o claim é liberado.
6. O primeiro SIGINT/SIGTERM ativa o drain; segundo SIGINT ou
   `--drain-timeout` cancela o estágio atual. O drain não espera a run inteira.
7. `--once` continua processando um único lote e encerrando.

## Consequências

Positivas:

- Slots livres processam tasks rápidas enquanto tasks longas continuam.
- Novas issues entram na fila sem esperar o lote atual terminar.
- Drain preserva progresso e não descarta estágios concluídos.
- `queued` continua visível no status/report e sobrevive a restart.

Negativas e riscos:

- Aumenta a complexidade de concorrência; exige race tests e deduplicação local.
- Tasks rápidas ainda esperam se todos os workers estiverem em estágios longos;
  não há preempção.
- O estágio atual sempre termina antes da pausa; estágios muito longos atrasam
  o drain até o timeout.
