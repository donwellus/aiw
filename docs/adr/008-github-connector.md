# ADR 008 — Conector GitHub

- Status: aceito
- Data: 2026-11-17

## Contexto

O source `folder` cobre uso local, mas o fluxo real de desenvolvimento usa issues. O GitHub é o primeiro conector remoto: precisa listar/ler issues, alimentar intake/spec com contexto e fazer write-back de estado.

## Decisão

1. Driver `github` usando o CLI `gh` (autenticação existente do usuário), com `repo`, `repo_path`, `state`, `labels`, `exclude_labels`.
2. Leitura:
   - `gh issue list` para refs; `gh issue view` para detalhe com comentários.
   - Título, corpo, labels, assignee, comentários e checklist (`- [ ]`) são normalizados para `source.Task`.
   - `ContentHash` inclui corpo e comentários; novo comentário reabre a tarefa para execução.
3. Write-back:
   - claim: adiciona `aiw:running`;
   - sucesso: `aiw:done`, remove running, comentário com run/branch/PR/custo, fecha issue se `close_on_done`;
   - com `--pr`, o corpo do PR inclui `Closes #N`; a issue fecha no merge para a
     branch padrão, não na abertura. Para esse fluxo, `close_on_done` deve ficar
     `false`;
   - falha: `aiw:failed`, remove running, comentário com erro;
   - `needs_input`/cancelado: remove running;
   - labels configuráveis; labels são criadas com `gh label create --force` se necessário.
4. `repo_path` mapeia a issue para o checkout local; sem ele, vale `workspace.default_repo`/label `repo:*`.
5. `aiw doctor` chama `gh auth status` e `gh repo view` via interface opcional `source.Checker`.
6. `aiw release` remove `aiw:running` via `Releaser`.

## Consequências

Positivas:

- Issues reais entram no fluxo com contexto e critérios de aceite.
- Write-back visível no GitHub sem servidor/daemon adicional.
- Fechamento da issue acontece no merge do PR, alinhado ao fluxo de revisão.
- Claim local (`slides`) + label remota dá visibilidade e reduz duplicidade.

Negativas e riscos:

- Depende do `gh` autenticado e de permissão de escrita para labels/comentários.
- Comentários de write-back podem duplicar em retries extremos; labels são idempotentes.
- Rate limit do GitHub em listas grandes; `--limit`/filtros locais por enquanto.
- Sincronização de estado é best-effort; a fonte de verdade do claim continua em `.aiw/state`.

## Alternativas

- **API REST direta com token**: mais controle, mais código e gestão de auth.
- **GitHub App/webhooks**: ideal para daemon em escala, mas exige servidor público; fica para depois.
- **Só issues como fonte, sem write-back**: menos ruído, porém perde feedback no provedor.
