# ADR 013 — Judge e diff de referência nos evals

- Status: aceito
- Data: 2026-11-17

## Contexto

O `aiw eval` media apenas pipeline concluído + comando `verify`. Isso não captura qualidade subjetiva nem quão próxima a solução ficou de uma referência conhecida.

## Decisão

1. **Judge opcional** na suite:
   - `judge.engine`, `judge.model`, `judge.rubric`, `judge.threshold`;
   - prompt `eval-judge.md` recebe tarefa, rubrica, spec e diff;
   - saída obrigatória `SCORE: 0..10`; última ocorrência vence;
   - `threshold > 0` bloqueia; `0` é informativo; custo do judge entra no caso.
2. **Similaridade de referência** opcional por caso (`reference_ref`):
   - calculada entre `diff.patch` gerado e `git diff base reference_ref`;
   - Jaccard das linhas adicionadas/removidas;
   - informativa (não bloqueia).
3. Resultados JSON incluem `judge_score`, `judge_threshold`, `judge_output`, `reference_similarity` e erros associados.
4. Fake engine responde `SCORE: 9` para permitir smoke do harness.

## Consequências

Positivas:

- Evals medem qualidade, não só execução.
- Threshold permite quality gate em CI/local.
- Similaridade dá sinal objetivo contra uma solução de referência.

Negativas e riscos:

- LLM-as-judge é ruidoso e pode ter viés; rubrica precisa ser calibrada.
- Similaridade por linhas é sensível a formatação/refatorações diferentes.
- Custo/latência adicionais quando o judge está ativo.
- Judge com engine fake serve só para testar o harness, não qualidade real.

## Alternativas

- **Só diff de referência**: determinístico, mas pune soluções válidas diferentes.
- **Só judge**: flexível, mas subjetivo/caro.
- **Testes ocultos**: ótimos como sinal principal; continuam sendo o `verify`, com judge/diff como complementos.
