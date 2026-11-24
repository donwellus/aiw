# ADR 028 — Review/correção interno e merge após CI

- Status: aceito
- Data: 2026-11-24

## Contexto

O `reviewLoop` já fazia review → correção → testes, mas:
- o reviewer lia o resumo do implementador;
- o corretor usava o mesmo prompt de implementação, sem direito formal de contestar;
- ao exceder o limite, a run falhava;
- o PR era aberto e a run terminava sem esperar CI ou merge;
- não havia política de merge.

## Decisão

1. **Reviewer independente**:
   - lê intake/requisitos, spec, plano, diff/código e test log;
   - não lê o resumo do implementador;
   - emite `APROVADO` ou `BLOCKING: [CRITICAL|HIGH|MEDIUM] ...`.
2. **Corretor separado**:
   - novo prompt/estágio `correct` e engine configurável;
   - responde por blocker: `FIXED`, `DISPUTED` ou `NEEDS_CLARIFICATION`;
   - pode editar código/testes, sem commit/push;
   - se o blocker exigir mudar requisito/spec/plano, não altera: escala.
3. **Loop**:
   - correção → testes → novo diff → re-review;
   - re-review lê a resposta do corretor;
   - limite `max_review_rounds` → run `paused` com `review-blockers.md`;
   - dispute não resolvido não é aceito automaticamente.
4. **Entrega**:
   - PR só depois de aprovação interna;
   - `delivery.merge: auto_after_approval` aguarda checks e mergeabilidade;
   - `required_checks` é explícito; sem branch protection, o aiw valida CI;
   - merge serializado por repo (`merge_concurrency`);
   - falha/pendência/conflito → `ErrPaused` no estágio `deliver`;
   - nunca usa `--admin`.
5. **Só depois do merge**: a run conclui, `MarkDone` é aplicado, a issue fecha via
   `Closes #N` e os artefatos ficam disponíveis para clean.
6. **Hooks de grupo**: `external_review`/`external_reply` são previstos na
   configuração, mas a ingestão de reviews externos fica para fase posterior.

## Consequências

Positivas:

- Reviewer e corretor têm contextos e responsabilidades separadas.
- Disputas técnicas são registradas, não silenciadas.
- PR não fica “aberto e esquecido”: CI e merge fazem parte do fluxo.
- Issue só fecha quando o PR é mergeado.

Negativas e riscos:

- Worker fica ocupado durante CI/merge; um delivery worker assíncrono fica para
  depois.
- CI failure pausa para intervenção; correção pós-CI ainda não é automática.
- Conflito/rebase automático e merge queue avançada ainda não existem.
- Review externo humano ainda não é ingerido.
