# ADR 027 — Refinamento de gates com target explícito

- Status: aceito (prévia obrigatória adicionada pelo ADR 031)
- Data: 2026-11-24

## Contexto

`aiw_answer` atende `needs_input`. Runs pausadas em gates (`spec`, `plan`) não
podem usar answer: o artefato é derivado e precisa ser regenerado. Editar
`plan.md` manualmente também é frágil, porque o plano deriva de `spec.md`.

## Decisão

> Atualização: desde o ADR 031, `aiw_refine` exige `preview_id` gerado por
> `aiw_refine_preview` e confirmação humana.

1. Nova operação assíncrona `refine` e ferramenta MCP `aiw_refine`:
   - `run_id`;
   - `target`: `intake`, `spec` ou `plan`;
   - `feedback` humano;
   - `auto`/`budget` como no resume.
2. O target é o artefato mais antigo invalidado, não necessariamente o gate
   atual:
   - `plan`: regenera apenas architect/plan;
   - `spec`: regenera requirements/spec e architect/plan;
   - `intake`: reinicia intake, spec e plan.
3. Feedback fica append-only em:
   - `.aiw/runs/<id>/feedback/intake.md`;
   - `.aiw/runs/<id>/feedback/spec.md`;
   - `.aiw/runs/<id>/feedback/plan.md`.
4. O pipeline injeta o feedback do estágio no prompt via `HumanFeedback`.
5. Antes de regenerar:
   - exige run `paused`;
   - bloqueia se implement/test/review/deliver já não estiverem pendentes;
   - remove artefatos derivados do worktree para evitar leitura stale;
   - reseta os estágios a partir do target e os gates correspondentes.
6. Depois chama `Runner.Resume`; a operação é acompanhada por `operation_id`.
7. Quando target é `spec`, feedback antigo de plan é removido; quando é
   `intake`, spec/plan feedback também são removidos.
8. `aiw_answer` continua exclusivo de `needs_input` e agora orienta usar
   `aiw_refine` quando a run está pausada.

## Consequências

Positivas:

- Spec e plan permanecem consistentes e versionados por feedback.
- Coordinator não tenta usar answer em gate nem editar artefatos por conta
  própria.
- Refinamento é assíncrono, auditável e revalida os estágios.
- Falha cedo se implementação já começou.

Negativas e riscos:

- Refinamento depois de implementação iniciada exige nova run nesta fase.
- O custo de regenerar spec+plan pode ser alto.
- Feedback antigo de estágios invalidados é descartado do prompt (permanece no
  histórico apenas quando não é do target).
