# ADR 037 — Correção automática de falhas de CI no delivery

- Status: aceito
- Data: 2026-11-28

## Contexto

Quando o CI de um PR falhava, o delivery pausava e não distinguia código
quebrado de teste desatualizado. O coordenador não pode editar nem executar git,
então o humano precisava intervir fora do fluxo.

## Decisão

1. O delivery coleta o check que falhou e o log do GitHub Actions.
2. Um agente de qualidade no mesmo worktree/PR decide:
   - `FIX_CODE`: corrigir implementação/testes afetados;
   - `FIX_TEST`: ajustar teste quando a expectativa ficou incompatível com a spec;
   - `RETRY_CI`: reexecutar falha aparentemente flaky;
   - `NEEDS_CLARIFICATION`: pausar para decisão humana.
3. A correção passa por guardrails:
   - somente checks configurados em `delivery.ci_fix_checks` (default `verify`);
   - `security` e checks não configurados pausam;
   - `FIX_TEST` só altera testes/fixtures;
   - workflows e artefatos de requisitos/spec/plano são proibidos;
   - testes locais precisam passar;
   - limite `delivery.max_ci_fix_rounds` (default 3) e budget.
4. O pipeline valida o diff, commita e faz push na mesma branch, aguardando novo
   CI e merge.
5. Em falha ambígua, limite atingido ou check não elegível, grava
   `ci-blockers.md` e pausa. O coordenador expõe o blocker via `aiw_ci_status`.
6. Uma decisão humana pode ser registrada por `aiw_ci_decision`
   (`FIX_CODE`, `FIX_TEST`, `RETRY_CI`); o feedback é anexado ao prompt e o
   resume é enfileirado. O coordenador não edita nem executa git.

## Consequências

Positivas:

- Falhas de teste/código podem ser resolvidas dentro do fluxo, sem edição manual.
- Testes não podem ser simplesmente removidos ou silenciados.
- O humano continua no controle de casos ambíguos e de segurança.
- O mesmo PR/branch é mantido, sem criar uma task concorrente.

Negativas e riscos:

- Exige leitura de logs do provedor e um engine de correção com permissão no
  worktree.
- A correção automática de testes ainda depende de evidência de spec; mudanças
  amplas pausam.
- Cada rodada consome budget e aumenta o tempo até o merge.
