# ADR 023 — Cancelamento cooperativo e resposta inline

- Status: aceito
- Data: 2026-11-22

## Contexto

O coordenador já controlava pause, resume, clean, approve e release, mas não
conseguia cancelar uma run nem responder perguntas de `needs_input`. O único
caminho era usar a CLI ou editar a tarefa na fonte manualmente.

## Decisão

1. **Cancelamento cooperativo**:
   - marker `.aiw/runs/<id>/CANCEL`;
   - `Runner.Execute`/`Resume` observam o marker e cancelam o contexto da run;
   - o engine é interrompido via `exec.CommandContext`;
   - o pipeline também verifica o marker entre estágios;
   - run pausada/needs_input sem processo ativo é finalizada na hora.
2. **Sem PID/força**:
   - não há registry de PID nem sinal direto; o cancelamento é por run e
     cooperativo.
3. **Comando/ferramenta**:
   - `aiw cancel <run-id>` e `aiw_cancel`;
   - running com claim → `canceling`; o watcher finaliza;
   - paused/needs_input/sem claim → `canceled` imediato.
4. **Resposta inline**:
   - `aiw_questions` lê `questions.md`/`intake.md`;
   - `aiw_answer` valida `needs_input`, grava `answer.md`, publica um comentário
     via `source.Commenter` e enfileira uma operação `rerun`;
   - folder ganhou `Comment` por append no arquivo; fontes remotas já incluem
     comentários no `ContentHash`;
   - o daemon refaz o `Fetch` da tarefa atualizada e executa uma nova run.
5. **Concorrência**:
   - o rerun pode competir com o polling do daemon; o claim garante que apenas
     uma execução roda. O operation_id pode terminar como `busy` se o daemon
     vencer a corrida.
6. **Auditoria**: resposta fica na fonte e em `.aiw/runs/<id>/answer.md`; a
   operação de rerun fica em `.aiw/control/operations/`.
7. **Fora**: cancelar por PID/sinal, prioridade/hold, dependências,
   notificações e merge/deploy.

## Consequências

Positivas:

- Coordenador fecha o ciclo operacional sem shell nem edição manual.
- Cancelamento isolado não mata o daemon nem outras runs.
- Respostas ficam registradas na fonte e em arquivo local.
- Rerun é assíncrono e acompanhável.

Negativas e riscos:

- Cancelamento depende do boundary/tool call; um processo externo travado pode
  atrasar.
- Se o daemon não estiver ativo, o rerun fica `queued`.
- Polling do daemon pode vencer a corrida do rerun; claim evita duplicação.
- A run antiga permanece no histórico como `needs_input`.
