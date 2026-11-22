# ADR 021 — Coordenador via OpenCode + MCP read-only

- Status: aceito; fase de controle ampliada pelo ADR-022
- Data: 2026-11-22

## Contexto

O aiw executa workers em worktrees isolados, mas não tem um agente persistente
para conversar com o humano, triar issues e supervisionar o andamento sem
ocupar um slot de trabalho. A opção escolhida foi separar claramente controle e
execução:

- coordenador sempre disponível;
- workers efêmeros no pipeline atual;
- scheduler determinístico no aiw.

## Decisão

1. **Runtime**: coordenador como agente primário do OpenCode, em sessão longa
   (TUI, `opencode serve` ou attach). O aiw não implementa chat próprio nesta
   fase.
2. **Control plane**: novo `aiw mcp`, servidor MCP stdio read-only com:
   - `status`, `report`, `runs`, `run_show`, `events`.
   Os nomes expostos ao cliente são prefixados: `aiw_status` etc.
3. **Permissões**: o agente nega `edit`, `shell` e `subagent`; pode ler arquivos
   e chamar ferramentas `aiw_*`. Assim ele não executa tarefas nem abre
   worktrees.
4. **Workers**: continuam sendo runs do pipeline atual, via daemon/work, com
   claims, worktrees, gates, telemetria e PR.
5. **Comunicação**: workers não conversam com o coordenador. O contrato é
   `run.json`, eventos, artefatos, estado e `report`; o coordenador consulta
   esses dados via MCP.
6. **Sem bloqueio**: ferramentas são de leitura e rápidas. Ações de execução
   longas (`work`, `resume`) não são expostas; controle assíncrono fica para a
   fase 2.
7. **Configuração**: exemplos em `examples/opencode/`, documentação em
   `docs/coordinator.md`; o usuário copia para `.opencode/agents` e
   `opencode.jsonc` do projeto.
8. **Segurança**: prompts e permissões limitam o agente; o servidor MCP não
   aceita escrita nesta versão; merge/deploy continuam humanos.

## Consequências

Positivas:

- Coordenador sempre disponível sem criar uma UI própria.
- Reaproveita sessões, permissões e TUI do OpenCode.
- Separação clara entre intenção (LLM) e execução determinística (aiw).
- O mesmo MCP pode ser usado por outros clientes no futuro.
- Superfície de escrita mínima na fase 1.

Negativas e riscos:

- Dependência do OpenCode e da configuração local.
- Requer daemon separado para workers; coordenador não inicia execuções.
- Sem notificações proativas e sem controle remoto nesta fase.
- Ferramentas de controle precisarão de operações assíncronas para não bloquear
  a conversa.
- Dependências de tarefas (#6) ainda são pré-requisito para triagem completa.
