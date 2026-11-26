# ADR 031 — Preview obrigatório para refine

- Status: aceito
- Data: 2026-11-24

## Contexto

O `aiw_refine` recebia o feedback como string livre e o coordenador podia
enfileirá-lo diretamente. Em runs pausadas, o texto era frequentemente redigido
pelo próprio agente, sem uma etapa explícita de revisão humana. O gate pedia
uma decisão, mas o conteúdo operacional acabava sendo gerado e aplicado na
mesma ação.

## Decisão

1. **Ferramenta read-only `refine_preview`**:
   - recebe `run_id`, `target` e `feedback`;
   - valida run pausada, target, worktree e estágios posteriores;
   - retorna `preview_id`, feedback normalizado, arquivo de feedback, gates,
     estágios e artefatos afetados;
   - não cria operação, não grava `feedback/*.md` e não altera a run.
2. **`refine` exige `preview_id`**:
   - o ID é um hash do run, target, feedback e estado relevante;
   - feedback ou estado alterados invalidam o preview;
   - o MCP valida antes de enfileirar;
   - o executor revalida imediatamente antes de `PrepareRefine`.
3. **Confirmação humana**:
   - o coordenador mostra o preview integral e espera confirmação;
   - `aiw_refine` entra na política `ask` do OpenCode;
   - o agente não deve apresentar feedback redigido por ele como decisão humana.
4. **Compatibilidade**:
   - operações de refine antigas sem `preview_id` continuam por uma release,
     com warning; novas chamadas MCP sempre exigem preview.

## Consequências

Positivas:

- O humano vê o texto exato e o impacto antes de gastar uma rodada.
- Feedback do coordenador deixa de ser aplicado silenciosamente.
- Mudanças concorrentes no run invalidam o preview.
- Preview é read-only e pode ser usado com daemon parado.

Negativas e riscos:

- Todo refine passa por duas chamadas MCP.
- O `preview_id` protege contra mudança acidental, não contra um agente que
  ignore o fluxo; a confirmação `ask` cobre esse caso.
- Operações legadas precisam ser aceitas temporariamente sem token.
