# ADR 006 — Daemon contínuo e pasta doing opcional

- Status: aceito
- Data: 2026-11-17

## Contexto

O pool `aiw work` processa um lote e encerra. Para uso contínuo, precisamos de polling, estado vivo e desligamento gracioso. Também avaliamos uma pasta `doing` para visibilidade e claim por rename, além do claim atômico em `.aiw/state/claims`.

## Decisão

### Daemon (`aiw daemon`)

1. Loop de polling com `--interval` (default 60s), reutilizando `runner.Collect` e `runner.Work`.
2. Sem resumo global fixo: uma linha por tarefa concluída, uma linha por ciclo com trabalho, e `/status` JSON opcional (`--status-addr`) com ciclos, totais por status, runs em andamento e claims.
3. `--once` executa um único ciclo (cron/testes).
4. Nunca abre prompt; gates humanos pausam a tarefa, liberam o slot e o daemon segue.
5. SIGINT/SIGTERM cancela runs em andamento, libera claims e devolve tarefas canceladas ao inbox.
6. `paused` e `needs_input` ficam fora da fila até `resume` ou mudança de hash do conteúdo.

### Pasta doing (opt-in)

1. `doing_dir` configurável no source `folder`; default vazio (desligado).
2. No claim, o arquivo é movido atomicamente para `doing/` (rename), dando visibilidade e um claim por rename além do arquivo de claim.
3. `paused` permanece em `doing/`.
4. `needs_input` e cancelamento devolvem o arquivo ao inbox (`PendingWriter`).
5. `done`/`failed` movem a partir de `doing/` (o driver localiza por caminho, basename ou ID).
6. `aiw release` libera claim/estado e devolve ao inbox (`Releaser`).
7. Sem `doing_dir`, o comportamento anterior permanece (claim file apenas).

## Consequências

Positivas:

- Operação contínua com recuperação de claims e status observável.
- Pasta `doing` dá semântica visual ao que está em execução e reduz dependência de `.aiw` em pastas compartilhadas.
- Interfaces opcionais (`PendingWriter`, `Releaser`) permitem que conectores remotos adotem semântica equivalente.

Negativas e riscos:

- Movimentos extras de arquivo geram mudanças no git quando `tasks/` é versionado.
- Logs do daemon crescem com o tempo; retenção/rotação ficam a cargo do operador.
- `/status` é somente leitura e em memória; não substitui métricas/telemetria (Langfuse).
- Múltiplas máquinas exigem pasta compartilhada com rename confiável ou conector remoto; o claim local não coordena entre máquinas.

## Alternativas

- **Só `aiw work` + cron**: mais simples, porém sem status vivo nem shutdown coordenado.
- **Servidor com fila persistente (DB)**: mais robusto, mas complexo para esta fase; o estado/claims em arquivo cobrem o uso atual.
- **Doing como único claim**: funciona em pasta compartilhada, mas não cobre conectores remotos; mantido como camada opcional.
