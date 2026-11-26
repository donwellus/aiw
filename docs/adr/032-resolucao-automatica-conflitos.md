# ADR 032 — Resolução automática de conflitos de merge

- Status: aceito
- Data: 2026-11-25

## Contexto

PRs do aiw podiam ficar bloqueados quando a `main` avançava e o GitHub reportava
`mergeable: CONFLICTING`. O `deliver` pausava com `PR com conflito de merge`, mas
não havia caminho automático para integrar a base, resolver o conflito e retomar
CI/merge. Isso obrigava um agente externo a fazer rebase/merge manual.

## Decisão

1. O conflito é tratado no mesmo loop de delivery que processa review externo.
   Não depende de `external_review != ignore`.
2. Quando o PR está `CONFLICTING`/`DIRTY`, o aiw:
   - busca a branch base informada pelo PR;
   - faz merge da base na branch da run (sem rebase/force-push automático);
   - se houver conflito, escreve `conflict.md` e roda `conflict-correct`;
   - exige `DECISION: RESOLVED` e ausência de marcadores;
   - roda testes, commita o merge, faz push normal e volta a aguardar CI.
3. Conflito em `intake.md`, `spec.md` ou `plan.md` não é resolvido
   automaticamente: pausa para decisão humana.
4. Limite `conflict_max_rounds`; ao exceder, pausa com artefato.
5. Estado `ConflictState` registra tentativas, base, arquivos e resultado.
6. `Runner.Resume` de uma run com PR existente passa a reentrar no delivery com
   `CreatePR`, permitindo que `aiw resume`/daemon reavaliem CI, conflito e merge.
7. `aiw merge <run-id>` também usa o mesmo resolvedor para runs já pausadas.

## Consequências

Positivas:

- PRs do aiw não dependem de intervenção manual em git.
- Conflito e feedback externo compartilham o mesmo contexto de delivery.
- Merge da base evita force-push e preserva histórico.

Negativas e riscos:

- O agente pode escolher uma resolução semanticamente errada; testes e o limite
  de rodadas reduzem o risco.
- Conflitos de lockfile/spec/plano ainda pausam ou exigem cuidado.
- O worktree da run precisa estar limpo antes do merge.
