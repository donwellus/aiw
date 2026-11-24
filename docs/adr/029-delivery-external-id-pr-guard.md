# ADR 029 — Delivery tipado e PR guard

- Status: aceito
- Data: 2026-11-24

## Contexto

O run já guardava `task.external_id` (a issue), mas não o identificador do PR.
Após restart/cancelamento, o estado podia voltar para `todo/queued` e o daemon
iniciava uma nova implementação mesmo com um PR aberto. O PR não era um claim
nem uma fonte de verdade para elegibilidade.

## Decisão

1. **Delivery tipado no run**:
   ```json
   "delivery": {
     "provider": "github",
     "kind": "pull_request",
     "external_id": "10",
     "url": "...",
     "branch": "aiw/...",
     "status": "open|merged"
   }
   ```
   - `task.external_id` continua sendo a issue;
   - `delivery.external_id` identifica o PR;
   - `PRURL`/`PRMerged` são mantidos para compatibilidade e espelhados.
2. **PR guard opt-in**:
   - interface `source.PullRequestLister`;
   - apenas GitHub implementa (`gh pr list --state open`);
   - `Runner.Collect` mapeia PR por `Closes #N`, `delivery.external_id` ou
     branch `aiw/<run-id>`;
   - candidato com PR aberto não é reivindicado e vira `pr_open`;
   - `folder` e conectores sem suporte não fazem chamadas de PR.
3. **Persistência antes do CI**: `createPR` grava `delivery` e chama
   `OnDelivery`, que grava `pr_open`, antes de esperar CI/merge.
4. **Entrega manual/CI bloqueada**: `ErrDeliveryPending` pausa sem marcar
   `succeeded`/`MarkDone`; o estágio `deliver` fica pendente e pode ser
   reexecutado.
5. **`aiw merge <run-id>`**: adota PR existente, valida CI/mergeável, executa
   merge e reconcilia o estado para `succeeded`.
6. **Folder intacto**: sem `PullRequestLister`, sem `pr_open`, sem gh.

## Consequências

Positivas:

- Restart/cancelamento não duplica PRs para a mesma issue.
- PRs antigos são detectados e podem ser adotados.
- `delivery.external_id` evita parsear URL no merge/watch.
- Folder mantém o comportamento atual sem regressões.

Negativas e riscos:

- Listar PRs abertos adiciona uma chamada `gh pr list` por ciclo GitHub.
- PRs antigos sem `Closes` no corpo dependem do fallback de branch/run.
- Runs antigas já em andamento não são desduplicadas retroativamente; precisam
  de cancelamento/adoção manual.
- Ainda não há watcher de delivery após restart para merge automático; `aiw
  merge` cobre a adoção.
