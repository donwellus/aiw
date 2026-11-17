# ADR 004 — Paralelismo com claim atômico

- Status: aceito
- Data: 2026-11-16

## Contexto

O `aiw` executava uma tarefa por vez. Precisamos processar várias tarefas em paralelo sem duplicar runs, sem misturar artefatos e sem perder trabalho quando um processo cai. O estado anterior (`tasks.json` único + checagem em memória) não era atômico entre processos.

## Decisão

1. **Pool batch `aiw work`**: N workers em processo, fila snapshot das tarefas elegíveis, uma linha de resultado por tarefa. Gates exigem `--auto` nesta fase.
2. **Claim atômico por tarefa**: arquivo em `.aiw/state/claims/<source>-<id>-<hash>.lock` criado com `O_CREATE|O_EXCL`, contendo `run_id`, `pid` e timestamp. TTL configurável + heartbeat enquanto a run está viva. `aiw release <run-id>` libera runs mortas.
3. **Estado por tarefa**: `.aiw/state/tasks/<source>-<id>-<hash>.json` com escrita atômica. Migração automática do antigo `tasks.json` para um backup `.migrated`.
4. **Isolamento**: cada worker mantém worktree/branch próprios. `git worktree add` é serializado por repositório em processo e tem retry para contenção de lock do git.
5. **Limites**: `--workers` global, `max_concurrency` por engine, `max_test_concurrency` por repositório.
6. **Cancelamento**: SIGINT/SIGTERM interrompe a fila, cancela runs em andamento, libera claims e devolve tarefas canceladas para `todo` (sem write-back para `failed`).
7. **Uma tarefa falhando não derruba o pool**; o exit code é 1 se alguma falhar.

## Consequências

Positivas:

- Várias tarefas avançam ao mesmo tempo com isolamento e custo por tarefa.
- Claim por arquivo funciona entre processos e após restart, sem banco.
- Estado por tarefa elimina clobber de um arquivo compartilhado.

Negativas e riscos:

- `tasks/` local: claim por arquivo resolve a mesma máquina, não múltiplas máquinas compartilhando pasta.
- Runs muito longas dependem do heartbeat para não serem consideradas stale.
- Testes no mesmo repositório são serializados por padrão para evitar conflito de portas/recursos.
- Gates interativos não são suportados no pool; intake/gates policy-based virão na fase seguinte.
- Conflitos de merge entre branches paralelas são esperados e resolvidos na revisão humana.

## Alternativas

- **`aiw run --next` + `xargs -P`**: menos código, mas UX precária e ainda exigiria claim atômico.
- **Daemon (`aiw daemon`) primeiro**: desejável a médio prazo, mas o pool batch permite validar concorrência/claim antes do processo contínuo.
- **Pasta `doing` como claim**: resolve visibilidade e máquinas compartilhadas, mas adiciona movimentos no git e casos de borda (paused, crash, stale). Fica opcional na fase do daemon.
