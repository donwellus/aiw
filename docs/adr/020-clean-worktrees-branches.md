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
   restringe a lista. Runs `running` nunca são selecionadas.
3. **Runs superadas**: `needs_input`/`paused` entram por padrão quando existe um
   estado mais recente da mesma tarefa canônica (`succeeded`/`failed`) com
   `run_id` diferente e atualizado depois da criação da run antiga.
   `--skip-superseded` desativa. A identidade canônica usa claim key ou base do
   folder (aliases incluídos) via `internal/taskkey`.
4. **Proteção contra corrida**:
   - rejeita claim existente por run ID ou estado ativo da tarefa;
   - na execução real adquire o claim canônico da tarefa, relê `run.json` e
     revalida status/estado antes de remover;
   - libera o claim ao final, inclusive em erro.
5. **Worktrees**:
   - só aceita caminhos dentro de `.aiw/worktrees/`;
   - worktree sujo é preservado, exceto com `--force`;
   - `Worktree.Remove(ctx, force)` usa `git worktree remove` sem/sim `--force`;
   - worktree ausente é tratado como removido e entra no `git worktree prune`.
6. **Branches**:
   - só aceita prefixo `aiw/`;
   - apaga com `git branch -d` somente se merged na branch padrão detectada
     localmente; `--force` usa `-D`;
   - `--keep-branches` remove apenas worktrees;
   - a branch só é removida depois do worktree.
7. **Preservação**: `.aiw/runs` nunca é apagado; custo/histórico do `report`
   continua disponível.
8. **Saída**: tabela com ações/motivos, `--dry-run` e `--json`
   (`dry_run`, `summary`, `actions`, `errors`). Cada ação superada traz
   `superseded_by`; o resumo expõe `superseded`. Erros de remoção retornam
   exit code não-zero.
9. **Sem rede**: não faz `fetch` nem consulta PR. Merge é local; branches
   squash-merged ou remotas exigem `--force` consciente. Branches remotas/PRs
   não são apagadas.

## Consequências

Positivas:

- Controle explícito do acúmulo de worktrees/branches.
- Runs antigas de tentativas já vencidas não ficam presas para sempre.
- Defaults seguros: não perde trabalho não commitado nem branch não merged.
- Claim + releitura reduzem corrida com `work`/`resume`/daemon.
- Custo e histórico permanecem no projeto.

Negativas e riscos:

- Runs sem estado canônico localizável não são reconhecidas como superadas.
- Branches squash-merged não são reconhecidas como merged localmente.
- Worktrees sujos de runs failed/canceled exigem `--force`.
- `--force` remove diffs não commitados de forma irreversível.
- Branches remotas/PRs continuam no servidor e exigem limpeza manual.
- Clean não roda automaticamente; o usuário decide quando executar.
