# ADR 011 — Conector Trello

- Status: aceito
- Data: 2026-11-17

## Contexto

Fechamos a lista inicial de provedores com Trello, cujo modelo é board → listas → cards, diferente de issue trackers. Precisamos mapear esse modelo para o mesmo contrato de source e lifecycle.

## Decisão

1. Driver `trello` via REST v1, autenticação por `key`/`token` (`TRELLO_KEY`/`TRELLO_TOKEN`).
2. Config: `board`, `list` (fila de entrada), `running_list`/`done_list`/`failed_list`/`pending_list`, `repo_path`, labels/exclude e `comment`.
3. Leitura:
   - lista cards do board e filtra pela `list` configurada;
   - `Fetch` busca card, ações `commentCard` e checklists;
   - checklist e `- [ ]` viram critérios de aceite;
   - `Ref.ProviderID` guarda o card ID; `Ref.ID` usa shortLink.
4. Write-back:
   - `PUT /cards/{id}` com `idList` para running/done/failed/pending;
   - comentário via `POST /cards/{id}/actions/comments`;
   - `release` move para `pending_list`.
5. Listas são resolvidas por nome e cacheadas; se uma lista configurada não existir, o driver falha explicitamente.
6. `aiw doctor` valida credenciais, board e listas.

## Consequências

Positivas:

- Cobre o fluxo de quem organiza trabalho em boards.
- Modelo de lista mapeia bem para o lifecycle do aiw.
- Sem dependência de SDK; API simples.

Negativas e riscos:

- Trello não tem estados/tipos ricos; a lista é o estado.
- Check-ins/checklists têm formato próprio; descrição pode ser texto livre.
- Rate limits da API e paginação de cards/comentários limitadas nesta versão.
- Escrita concorrente em cards pode gerar conflito se outros usuários moverem o card durante a run.

## Alternativas

- **Webhooks do Trello**: melhor para daemon reativo, mas exige endpoint público.
- **Cards como subtarefas de um board por projeto**: mais estrutura, mais complexidade de configuração.
