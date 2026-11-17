# ADR 012 — Linear MCP para agentes

- Status: aceito
- Data: 2026-11-17

## Contexto

O source Linear usa GraphQL para leitura e write-back determinístico. Queremos que os agentes consultem issues/projetos relacionados durante intake/spec sem tornar o MCP a fonte oficial nem permitir mudanças fora do pipeline.

## Decisão

1. MCP configurável **por engine** (`engines.<name>.mcp.<server>`), apenas servidores remotos com URL/headers nesta versão.
2. O `aiw` gera a configuração em `.aiw/mcp/` com permissão 0600 (gitignored):
   - Claude Code: `--mcp-config <arquivo>`, tipo `http`, e auto-allow `mcp__<server>`;
   - opencode: `OPENCODE_CONFIG=<arquivo>`, `mcp.servers.<name>` tipo `remote`, `oauth:false`, `codemode:false` e permissão `<server>_*`.
3. Variáveis `${VAR}` na config são expandidas; se o header de Authorization ficar vazio, o servidor é ignorado com aviso.
4. Recomendação: usar `https://mcp.linear.app/mcp/readonly`; write-back continua no source GraphQL.
5. Sem suporte a OAuth interativo nesta fase; autenticação por API key/Bearer.

## Consequências

Positivas:

- Agentes ganham contexto de Linear/projetos sem substituir o source determinístico.
- Configuração isolada por engine e por servidor.
- Segredos ficam em `.aiw` 0600 e não vão para prompts/traces.

Negativas e riscos:

- Permissões de tools MCP variam entre engines; o auto-allow pode expor mais do que o necessário se a URL for read-write.
- Sem OAuth, requer API key válida; token expirado gera falhas de tool no agente.
- O servidor MCP pode mudar nomes/contratos de tools.
- MCP consome contexto do modelo; habilitar só quando necessário.

## Alternativas

- **MCP como source oficial**: descartado nesta fase por ser menos determinístico que GraphQL.
- **Bridge próprio de tools**: mais controle, porém mais manutenção e menos compatível com os agentes.
- **OAuth do MCP**: melhor UX futura, mas exige fluxo interativo/armazenamento de tokens.
