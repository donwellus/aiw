# ADR 007 — Evals com tarefas golden

- Status: aceito
- Data: 2026-11-17

## Contexto

Os testes provam a mecânica do orquestrador, mas não a qualidade do resultado dos agentes em tarefas reais. Precisamos de uma forma repetível de medir sucesso, custo e duração por tarefa, sem tocar no repositório original nem poluir traces de produção.

## Decisão

1. Comando `aiw eval --suite <yaml>` com casos golden declarativos:
   - `task`, `repo`, `base_ref` opcional, `verify`, engine/timeout por caso.
2. Cada caso roda em diretório temporário isolado:
   - repo é clonado (ou copiado e inicializado como git) e o pipeline cria worktree/estado próprios;
   - o repositório original não é alterado.
3. Verificação explícita: comando `verify` roda no worktree resultante; sucesso = pipeline concluído + verificação com exit 0.
4. Métricas por caso: status do pipeline, verificação, custo, duração, run/branch; artefatos copiados para `ArtifactsDir`.
5. Resultados em `.aiw/evals/<timestamp>/results.json` (ou `--out`); exit code 1 se qualquer caso falhar.
6. Telemetria `noop` nos evals para não poluir Langfuse; a análise vem dos artefatos/resultados.
7. `evals/smoke` valida o harness com engine `fake` e entra no `make verify`/CI.
8. Evals sequenciais nesta versão.

## Consequências

Positivas:

- Mede qualidade de forma repetível e comparável por engine/tarefa.
- Isolamento total do repositório original.
- Base para regressões de prompt, intake, validadores e custo.

Negativas e riscos:

- Sem LLM-as-judge nem diff de referência; a qualidade depende do comando `verify`.
- Casos golden exigem curadoria; suites pequenas podem superestimar qualidade.
- Custo/tempo reais dependem do engine e da API.
- Evals sequenciais podem demorar; paralelismo fica para depois.

## Alternativas

- **Benchmarks públicos (SWE-bench)**: úteis para calibrar modelos, mas não refletem o repositório/domínio do usuário.
- **LLM-as-judge**: mais flexível, porém mais caro e ruidoso; pode ser adicionado como métrica complementar.
- **Rodar eval direto no repositório**: mais simples, porém arriscado e não reprodutível.
