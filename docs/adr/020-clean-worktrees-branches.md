# ADR 020 — Limpeza segura de worktrees e branches

- Status: aceito
- Data: 2026-11-21

## Contexto

Cada run cria `.aiw/worktrees/<run-id>` e `aiw/<run-id>`. Nada era removido
automaticamente: `Worktree.Remove` existia só para testes, não apagava branches
e não havia comando público. Worktrees e branches se acumulam indefinidamente,
mesmo após a entrega.

## Decisão

1. **Comando**: `aiw clean`, separado de `release` (claims) e `report`
   (histórico read-only).
2. **Seleção padrão**: runs `succeeded`, `failed` e `canceled`; `--status`
   restringe a lista. Runs `running`, `paused` e `needs_input` nunca são
   selecionadas.
3. **Proteção contra corrida**:
   - rejeita claim existente por run ID ou estado ativo da tarefa;
   - na execução real adquire o claim canônico da tarefa, relê `run.json` e
     revalida status/estado antes de remover;
   - libera o claim ao final, inclusive em erro.
4. **Worktrees**:
   - só aceita caminhos dentro de `.aiw/worktrees/`;
   - worktree sujo é preservado, exceto com `--force`;
   - `Worktree.Remove(ctx, force)` usa `git worktree remove` sem/sim `--force`;
   - worktree ausente é tratado como removido e entra no `git worktree prune`.
5. **Branches**:
   - só aceita prefixo `aiw/`;
   - apaga com `git branch -d` somente se merged na branch padrão detectada
     localmente; `--force` usa `-D`;
   - `--keep-branches` remove apenas worktrees;
   - a branch só é removida depois do worktree.
6. **Preservação**: `.aiw/runs` nunca é apagado; custo/histórico do `report`
   continua disponível.
7. **Saída**: tabela com ações/motivos, `--dry-run` e `--json`
   (`dry_run`, `summary`, `actions`, `errors`). Erros de remoção retornam
   exit code não-zero.
8. **Sem rede**: não faz `fetch` nem consulta PR. Merge é local; branches
   squash-merged ou remotas exigem `--force` consciente. Branches remotas/PRs
   não são apagadas.

## Consequências

Positivas:

- Controle explícito do acúmulo de worktrees/branches.
- Defaults seguros: não perde trabalho não commitado nem branch não merged.
- Claim + releitura reduzem corrida com `work`/`resume`/daemon.
- Custo e histórico permanecem no projeto.

Negativas e riscos:

- Branches squash-merged não são reconhecidas como merged localmente.
- Worktrees sujos de runs failed/canceled exigem `--force`.
- `--force` remove diffs não commitados de forma irreversível.
- Branches remotas/PRs continuam no servidor e exigem limpeza manual.
- Clean não roda automaticamente; o usuário decide quando executar.
