# ADR 038 — Write-back não reimporta tarefas e resume preserva CreatePR

- Status: aceito
- Data: 2026-11-28

## Contexto

O write-back do AIW adiciona um comentário de conclusão na issue. O driver
GitHub incluía todos os comentários no `ContentHash`, então o próprio
comentário mudava o hash e o daemon reimportava a issue concluída. Runs
retomadas por `refine` também perdiam a opção `CreatePR`, podendo terminar sem
abrir PR.

## Decisão

1. Comentários automáticos de conclusão/falha recebem o marcador
   `<!-- aiw:writeback -->`.
2. Respostas externas já marcadas com `<!-- aiw:external-reply -->` também são
   ignoradas no hash.
3. Comentários antigos no formato `aiw: run ... concluída.`/`falhou.` são
   reconhecidos como legado.
4. O hash continua incluindo body, acceptance e comentários humanos.
5. `RunState.CreatePR` persiste a intenção de abrir PR.
6. `Runner.Resume` e `Pipeline.Resume` restauram `CreatePR` de runs anteriores;
   runs com PR existente também mantêm a intenção.

## Consequências

Positivas:

- Write-back não cria runs duplicadas.
- Comentários humanos ainda podem disparar uma nova execução.
- Resume/refine não perde a entrega por PR.

Negativas e riscos:

- Runs antigas criadas antes do campo `CreatePR` não recuperam a intenção
  automaticamente; é necessário refazer/abrir o PR explicitamente.
- O filtro de legado depende do formato dos comentários antigos.
