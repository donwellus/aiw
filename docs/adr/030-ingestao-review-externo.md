# ADR 030 — Ingestão de review externo

- Status: aceito
- Data: 2026-11-24

## Contexto

O PR era mergeado após CI verde sem ler reviews externos. Em projetos de grupo,
`CHANGES_REQUESTED`, comentários e threads podem exigir correções antes do
merge. O hook `delivery.external_review` existia, mas não tinha implementação.

## Decisão

1. **Interface opcional** `source.PullRequestReviewer`:
   - `ReadPullRequestReview` retorna `reviewDecision` e feedback acionável;
   - `ReplyPullRequestFeedback` responde threads via GraphQL ou comentário geral;
   - apenas GitHub implementa; `folder` continua sem chamadas de PR.
2. **Modos**:
   - `ignore`: comportamento anterior;
   - `consume`: processa reviews e espera liberação quando há
     `CHANGES_REQUESTED`/`REVIEW_REQUIRED`;
   - `require`: processa reviews e só mergeia com `APPROVED`.
3. **Feedback**:
   - reviews formais com corpo, threads não resolvidas/não outdated e
     comentários gerais;
   - respostas do aiw usam `<!-- aiw:external-reply -->` e são filtradas;
   - threads resolvidas/outdated são ignoradas;
   - IDs processados ficam em `run.external_review.processed`.
4. **Correção**:
   - prompt `external-correct.md` lê spec/plano/diff/test log e o feedback;
   - decide `FIXED`, `NO_ACTION`, `CONFLICT` ou `NEEDS_CLARIFICATION`;
   - só corrige código/testes; nunca altera spec/plano;
   - `FIXED` roda testes, commit local novo, push e aguarda o CI do novo HEAD;
   - `CONFLICT`/limite de rodadas pausa com `external-review-conflict.md`/`external-review-limit.md`.
5. **Respostas**:
   - `auto`: responde sempre que o corretor autorizar;
   - `auto_unless_conflict` (padrão): responde `FIXED`/`NO_ACTION`, nunca conflito;
   - `never`: não responde;
   - o aiw não aprova formalmente o próprio PR nem usa `--admin`.
6. **Merge**:
   - valida CI, mergeabilidade, `headRefOid` do novo commit e política externa;
   - `require` aguarda aprovação humana real;
   - nunca falsifica `reviewDecision`.

## Consequências

Positivas:

- Review externo aciona correção/reply sem sair do mesmo run.
- Conflitos de escopo são interrompidos para decisão humana.
- Respostas automáticas são rastreáveis e não geram loop de ingestão.
- Folder permanece inalterado.

Negativas e riscos:

- O worker fica ocupado esperando review/aprovação até `ci_timeout`.
- Não há resolução automática de threads; o revisor precisa resolver/aprovar.
- `require` depende de um revisor humano real; não funciona sem equipe.
- Feedback externo muito amplo pode consumir `max_fix_rounds`; depois pausa.
