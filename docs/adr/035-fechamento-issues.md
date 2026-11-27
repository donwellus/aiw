# ADR 035 — Fechamento manual de issues pelo coordinator

- Status: aceito
- Data: 2026-11-27

## Contexto

O AIW fechava issues automaticamente no merge/`close_on_done`, mas o coordinator
não tinha uma operação explícita. Fechar sem cancelar uma run poderia deixar um
worker ativo, claim preso e PR órfão.

## Decisão

1. Interface opcional `source.IssueCloser`.
2. Ferramenta `aiw_close_issue` com `issue_id`, `reason` e `cancel_run`.
3. A operação:
   - exige motivo;
   - recusa run ativa sem `cancel_run=true`;
   - recusa issue com PR aberto;
   - cancela primeiro;
   - se o cancelamento for assíncrono, retorna `closed=false` e não fecha;
   - só fecha após a run ficar terminal/claim liberado;
   - marca o registro local como `canceled`.
4. Fechamento é idempotente:
   - GitHub consulta o estado antes; issue já `CLOSED` retorna sucesso sem novo
     comentário;
   - folder procura a task por ID em `done`; se já estiver lá, retorna sucesso
     sem mover de novo;
   - o comentário de fechamento no folder é gravado no máximo uma vez para o
     mesmo motivo.
5. Folder grava `aiw: fechada: <motivo>` no `.md` e move a task para `done_dir`.
   Os anexos em `tasks/attachments/<task-id>/` permanecem onde estão.
6. `aiw_close_issue` entra como ferramenta de escrita e permissão `ask` no
   coordinator.

## Consequências

Positivas:

- Coordinator consegue encerrar issues sem shell/gh.
- Nenhum fechamento silencioso com run ativa.
- Motivo fica auditado na fonte.

Negativas e riscos:

- Cancelamento de run `running` é cooperativo e exige nova chamada.
- GitHub não fornece edição de corpo nesta operação; fechar não é editar.
- Folder não move anexos junto com a task; eles continuam em
  `tasks/attachments/<task-id>/` para preservar referências e evitar perda de
  arquivos.
