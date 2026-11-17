# ADR 010 — Conector Jira

- Status: aceito
- Data: 2026-11-17

## Contexto

Parte dos times usa Jira Cloud. Precisamos consumir issues por JQL e refletir o andamento no workflow, mantendo o mesmo contrato de source.

## Decisão

1. Driver `jira` via REST API v3, autenticação Basic (`JIRA_EMAIL` + `JIRA_API_TOKEN`) ou Bearer PAT se email vazio.
2. Config: `base_url`, `jql` ou `project`, `repo_path`, labels/exclude, nomes de transição para write-back e `comment`.
3. Leitura:
   - `GET /rest/api/3/search/jql` com JQL/maxResults/fields;
   - `GET /rest/api/3/issue/{key}` com comentários;
   - descrição e comentários em ADF são convertidos para texto; `taskList`/checklists viram critérios de aceite.
4. Write-back:
   - `GET /issue/{key}/transitions` e `POST` para `running_transition`/`done_transition`/`failed_transition`/`pending_transition`;
   - comentário em ADF com run/branch/PR/custo.
5. `aiw doctor` valida `/rest/api/3/myself`.
6. `release` usa `pending_transition`.

## Consequências

Positivas:

- Jira Cloud coberto com transições e comentários.
- JQL dá controle direto da fila.
- Sem SDK extra; HTTP/JSON transparente.

Negativas e riscos:

- ADF exige conversão; formatação complexa perde estrutura.
- Nomes de transição variam por workflow e precisam ser configurados.
- `search/jql` não é suportado por instalações antigas de Jira Server/DC.
- Paginação completa fica para depois; `maxResults` limita a fila.

## Alternativas

- **API v2**: descrição em texto, porém endpoints antigos/depreciados.
- **SDK oficial**: não há SDK Go oficial; REST direto é suficiente.
- **Webhooks**: melhor para daemon em escala, mas exige endpoint público.
