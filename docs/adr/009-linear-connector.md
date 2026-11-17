# ADR 009 — Conector Linear

- Status: aceito
- Data: 2026-11-17

## Contexto

Além de GitHub, o time usa Linear. Precisamos consumir issues por time/projeto, hidratar o intake/spec com descrição/comentários e refletir o andamento de volta no workflow.

## Decisão

1. Driver `linear` via GraphQL (`https://api.linear.app/graphql`), API key em `LINEAR_API_KEY` (configurável via `api_key_env`).
2. Config: `team` (obrigatório), `project` opcional, `states` (filtro de entrada), `repo_path`, labels/exclude e nomes de estado para write-back.
3. Leitura:
   - `issues` por time/projeto, normalizando título, descrição, labels, assignee, estado e comentários;
   - checklists `- [ ]` viram critérios de aceite;
   - `Ref.ProviderID` guarda o UUID da issue; `Ref.ID` usa o identificador (`ENG-123`).
4. Write-back:
   - `running_state` no claim; `done_state`/`failed_state` no terminal;
   - comentário com run/branch/PR/custo;
   - `needs_input`/cancelado vão para `pending_state`;
   - estados são resolvidos por nome via `workflowStates` e cacheados.
5. `aiw doctor` valida API key, viewer e time via `source.Checker`.
6. `release` devolve a issue para `pending_state`.

## Consequências

Positivas:

- Fluxo bidirecional com Linear sem servidor adicional.
- Estado do workflow reflete a run; comentários dão rastreabilidade.

Negativas e riscos:

- Depende de API key e permissões de escrita.
- Nome de estados precisa existir no time; falha explícita se não existir.
- Lista limitada a `limit` (default 100); paginação completa fica para depois.
- Labels no write-back não são alteradas (estado/comentário são a fonte).

## Alternativas

- **Webhooks do Linear**: melhor para daemon em escala, mas exige endpoint público.
- **SDK oficial**: não há SDK Go oficial; GraphQL direto é simples e transparente.
- **Somente leitura**: menos risco, mas perde o feedback de andamento no Linear.
