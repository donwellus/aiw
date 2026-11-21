# ADR 018 — Relatório read-only multi-formato

- Status: aceito
- Data: 2026-11-21

## Contexto

`aiw status` mostra apenas running/paused/needs_input; `aiw list --all` mostra o
histórico do folder sem custo; `aiw runs` lista execuções com custo, mas não a
situação consolidada por tarefa. Faltava uma visão única, estável e exportável de
tudo que já passou pelo aiw, incluindo concluídas.

## Decisão

1. **Comando separado**: `aiw report` é somente leitura e não altera `status`,
   estado, runs, claims ou pausa. O `status` permanece curto e operacional.
2. **Fontes de dados**: `.aiw/state/tasks` para a situação por tarefa e
   `.aiw/runs/*/run.json` para estágio, custo, tokens, PR e erro. Tarefas são
   deduplicadas pela claim key canônica (fallback `source:id`).
3. **Semântica de custo**:
   - resumo = soma de **todas as runs** (custo real, incluindo retries);
   - coluna da tarefa = custo da **última run**;
   - ambos aparecem explicitamente no relatório.
4. **Conteúdo**: resumo por status, runs, custo/tokens, claims ativos/vencidos,
   tarefas com estágio/run/local, alertas de failed/needs_input/paused/pausa
   global/claim vencido/run ausente.
5. **Snapshot opcional**: `--source <nome>` consulta a fonte e inclui tarefas
   ainda sem estado. Folder usa `AllLister` (inbox+doing+done+failed); fontes
   remotas usam `List` (inbox). O estado local vence o snapshot. Sem a flag, o
   comando é offline e determinístico.
6. **Formatos**: tabela (default), `--json`, `--yaml`, `--md` e `--html`
   autocontido, mutuamente exclusivos. JSON/YAML usam o mesmo schema; Markdown
   escapa HTML/pipes; HTML usa `html/template` com autoescape.
7. **Ordem**: ativos e pendências primeiro (`running`, `paused`, `needs_input`,
   `failed`, `todo`, `canceled`, `succeeded`); mais recentes primeiro dentro do
   grupo. Alertas priorizam erros.

## Consequências

Positivas:

- Uma visão completa e exportável sem inflar `status`.
- Custo real acumulado e custo por tarefa ficam explícitos e comparáveis.
- Formatos legíveis e de automação compartilham a mesma agregação.
- `--source` permite enxergar a fila bruta sem tornar o comando dependente de
  rede por padrão.

Negativas e riscos:

- O resumo acumulado pode divergir da soma das colunas quando há retries/re-runs;
  o relatório documenta a diferença, mas exige leitura atenta.
- `--source` remoto pode fazer muitas chamadas e inclui apenas o inbox atual.
- Claims vencidos são detectados por idade (`claim_ttl`), não por liveness do PID.
- HTML/Markdown não substituem o `status` para acompanhamento em tempo real.
