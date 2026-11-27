# ADR 033 — Criação de issues pelo coordenador

- Status: aceito
- Data: 2026-11-27

## Contexto

O coordinator não podia criar issues; o humano precisava trocar de agente, o que
tornava o fluxo lento e fragmentado. Criação por texto livre também permitia
issues sem critérios de aceite ou outros campos necessários.

## Decisão

1. **Draft estruturado**: `source.TaskDraft` com título, descrição, critérios de
   aceite, labels, prioridade, assignee e repo.
2. **Criadores opcionais**: `source.TaskCreator` e `Caps.Create`; folder e
   GitHub implementados nesta fase. Linear/Jira/Trello ficam para depois.
3. **Fonte automática**: o coordinator não escolhe source. Usa a source ativa do
   daemon; se o daemon estiver inativo, usa `default_source`. Sem fonte válida,
   erro.
4. **Preview obrigatório**: `prepare_issue` read-only normaliza o draft, aponta
   `missing`, formata o corpo e retorna `preview_id`.
5. **Criação intencionalmente incompleta**: `create_issue` exige `preview_id`;
   se houver `missing`, exige `allow_incomplete=true` e
   `acknowledged_missing` exatamente igual. O corpo recebe
   `## Campos pendentes`.
6. **Coordinator**: aceita texto livre na conversa, mas converte para os campos
   estruturados, mostra o preview e nunca usa edit/shell.
7. **Folder**: labels são frontmatter estático; não há add/remove dinâmico. O
   corpo carrega a seção de campos pendentes.

## Consequências

Positivas:

- O coordinator cria issues sem trocar de agente.
- Issues incompletas não são criadas por acidente.
- A criação respeita a source que o daemon realmente processa.
- Folder e GitHub compartilham o mesmo fluxo.

Negativas e riscos:

- Duas chamadas MCP no fluxo normal (prepare + create).
- Linear/Jira/Trello dependem de implementações futuras de `TaskCreator`.
- Criação intencionalmente incompleta ainda exige acompanhamento humano no
  intake.
