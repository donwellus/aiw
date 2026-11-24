# ADR 005 — Intake com DoR e gates policy-based

- Status: aceito
- Data: 2026-11-17

## Contexto

O pipeline começava em requirements e dependia de gates humanos em spec/plan. Isso não escala para o pool/daemon e não tratava tarefas incompletas ou ambíguas. A decisão foi mover a validação para o início: intake vira o gate principal; spec/plan podem ser automáticos se o intake e validadores concordarem.

## Decisão

1. **Nova etapa `intake`** antes de requirements:
   - explora o repositório apenas para leitura;
   - grava `intake.md` com resumo, escopo, fora de escopo, decisões assumidas,
     critérios de aceite, perguntas bloqueantes, risco e decisão;
   - grava `questions.md` somente quando uma decisão humana é indispensável;
   - registra decisões não bloqueantes em `assumptions.md`.
2. **Contrato simples** no fim do intake: `DECISION: READY|NEEDS_INPUT` e `RISK: LOW|MEDIUM|HIGH`.
3. **DoR determinístico complementar**:
   - descrição mínima (`intake.min_description_chars`, default 10);
   - critérios de aceite no source ou propostos no intake;
   - decisão READY; perguntas não bloqueantes não invalidam a tarefa;
   - perguntas só bloqueiam quando o intake marca `DECISION: NEEDS_INPUT`.
4. **Política** (`gates.intake: policy`):
   - falha de DoR ou `DECISION: NEEDS_INPUT` → status `needs_input` (não é falha);
   - risco HIGH ou labels configurados (`risk_labels`) → gate humano;
   - caso contrário, segue automaticamente.
5. **`needs_input`**:
   - claim é liberado, tarefa permanece no inbox, estado `needs_input`;
   - o pool ignora enquanto o hash não mudar;
   - o humano edita/responde e o rerun continua; decisões que o agente consegue
     tomar sozinho vão para “Decisões assumidas” e não param o pipeline.
6. **Validadores de spec/plan**:
   - chamadas com contexto limpo, rubrica e saída `DECISION: APPROVED|REJECTED` + `BLOCKING:`;
   - até `max_gate_rounds` correções;
   - se continuar reprovando, o gate escala para humano (`Gates[name] = escalated`).
7. **GateMode** compatível com a configuração antiga:
   - `true` → `human`, `false` → `auto`;
   - novos valores: `auto`, `human`, `policy`, `off`;
   - default: `intake: policy`, `spec: auto`, `plan: auto`.

## Consequências

Positivas:

- Tarefas incompletas param antes de consumir spec/implementação.
- O pool pode rodar sem `--auto` na maioria dos casos.
- Spec/plan ganham revisão automática independente do agente que os produziu.
- O caminho de escalonamento para humano continua existindo.

Negativas e riscos:

- Custo adicional de intake + validadores.
- Parsing de decisões em Markdown depende do contrato de prompt; prompts e testes precisam evoluir juntos.
- Bullets quebrados em várias linhas precisam ser unidos pelo parser; coberto por testes.
- `needs_input` agora tem resposta inline via coordenador/MCP, mas a CLI dedicada
  ainda não existe.
- Validador pode rejeitar por estilo; `max_gate_rounds` e prompts precisam ser calibrados com tasks reais.

## Alternativas

- **Manter apenas gates humanos em spec/plan**: não escala no pool/daemon.
- **Intake sempre humano**: mais seguro, porém lento; pode ser configurado com `gates.intake: human`.
- **Intake totalmente automático sem DoR**: arriscado; tarefas vagas avançariam.
- **JSON estruturado para intake/validadores**: mais robusto de parsear, porém mais rígido para prompts e modelos; pode ser adotado se o parsing Markdown se mostrar frágil.
