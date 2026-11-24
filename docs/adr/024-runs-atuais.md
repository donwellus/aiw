# ADR 024 — Runs atuais versus histórico

- Status: aceito
- Data: 2026-11-22

## Contexto

O `aiw_runs` listava todas as tentativas. Um projeto com retries/necessidades de
input antigas mostrava dezenas de runs `needs_input`/`paused` mesmo com todas as
tarefas atuais concluídas, confundindo o coordenador sobre o que ainda estava
ativo.

## Decisão

1. O comando `aiw runs` e a ferramenta MCP `aiw_runs` mostram por padrão apenas
   a **run atual de cada tarefa**.
2. `aiw runs --all` / `aiw_runs {all:true}` mostra o histórico completo.
3. A run atual é determinada pelo estado canônico mais recente da tarefa
   (claim key/base folder, via `internal/taskkey`). Se não houver estado, usa a
   run mais recente por data.
4. O histórico marca `current` e `superseded_by`; a tabela mostra
   `SUPERSEDIDA POR`.
5. `aiw clean` também passou a usar o estado canônico mais recente para decidir
   se uma tarefa está ativa, em vez de um registro antigo de alias. Runs
   superadas com estado terminal deixam de ser puladas por engano.
6. `report` continua contabilizando custo/todas as runs; a visão de tarefas já
   era deduplicada.

## Consequências

Positivas:

- O coordenador vê 49 runs atuais `succeeded` em vez de 32 tentativas
  históricas não-terminais.
- Histórico continua disponível com `--all`.
- `clean` consegue remover worktrees antigas de aliases sem confundir estado
  atual com estado histórico.
- Nenhum status de run.json é reescrito; a mudança é de visão.

Negativas e riscos:

- Mudança de comportamento de `aiw runs`: scripts que queriam todo o histórico
  precisam usar `--all`.
- `--all` continua mostrando runs antigas `needs_input`/`paused`; elas são
  históricas, não ativas.
- Custo acumulado continua no `report`, que soma todas as runs.
