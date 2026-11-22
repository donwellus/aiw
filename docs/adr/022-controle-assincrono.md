# ADR 022 — Controle assíncrono do coordenador

- Status: aceito
- Data: 2026-11-22

## Contexto

A fase 1 do coordenador era read-only. Para operar o projeto, ele precisa de
pause, resume, approve, release e clean sem bloquear a conversa. `resume` pode
durar horas; executá-lo dentro do MCP impediria o coordenador de responder.

## Decisão

1. **MCP com escrita opt-in**: `aiw mcp --allow-write`. Sem a flag, as
   ferramentas de escrita não aparecem em `tools/list`.
2. **Ações síncronas rápidas**: `pause`, `approve`, `release`,
   `clean_preview` e `clean_apply`. `clean_apply` exige `confirm=true`.
3. **Operações assíncronas**: `resume` e `resume all` são persistidos em
   `.aiw/control/operations/<id>.json` com status
   `queued/running/succeeded/failed/canceled`.
4. **Executor no daemon**: um worker de controle independente processa a fila a
   cada ~1s, inclusive quando o daemon está em pausa global/idle. O daemon
   publica heartbeat em `.aiw/daemon.json`; sem heartbeat ativo, `resume`
   retorna erro.
5. **Não bloqueante**: o MCP retorna `operation_id` imediatamente. O
   coordenador acompanha com `operations` e `operation_show`.
6. **Cancelamento**: `operation_cancel` grava um marcador; o executor cancela o
   contexto da operação em andamento e registra `canceled`.
7. **Shared core**: `internal/control` centraliza pause, approve, release,
   candidatos de resume e a fila. CLI e MCP usam o mesmo código.
8. **Permissões**: o agente permite pause/resume/approve/operation_cancel; pede
   confirmação para release e clean_apply; continua proibido de editar, usar
   shell ou fazer merge/deploy.
9. **Fora da fase**: cancelar run em andamento, responder needs_input,
   prioridade/hold, notificações proativas e merge automático.

## Consequências

Positivas:

- Coordenador continua disponível durante resumes longos.
- Operações sobrevivem à sessão do MCP e são auditáveis.
- Daemon permanece o único executor de workers e operações longas.
- Mesmo core para CLI e MCP reduz divergência.

Negativas e riscos:

- `resume` exige daemon ativo e heartbeat; sem ele, fica explícito que não roda.
- Latência de até ~1s para iniciar uma operação.
- Operações `running` órfãs após crash do daemon exigem recuperação futura.
- Cancelamento de run em andamento e needs_input ainda são manuais.
