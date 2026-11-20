# ADR 015 — Claims canônicos para aliases folder

- Status: aceito
- Data: 2026-11-20

## Contexto

O `poema` usa várias sources `folder`/`wave-*` apontando para a mesma pasta `tasks/`, com filtros de labels diferentes. O claim era keyed por `source:id`, então a mesma tarefa podia ser reivindicada por aliases diferentes. O estado também acumulava registros por source e o `aiw status` mostrava registros antigos (`needs_input` de uma source, `succeeded` de outra).

## Decisão

1. **ClaimKey canônico** em `source.Ref`/`source.Task`:
   - folder: `folder:<abs(clean(base))>:<externalID>`;
   - remoto: fallback `source:externalID`;
   - `Runner` e `resume` usam `Ref.ClaimKey` → `Task.ClaimKey` → fallback.
2. O arquivo de claim é nomeado por hash da **ClaimKey**; `ClaimInfo` inclui `key`.
3. Compatibilidade: `ClaimByKey` ainda enxerga o lock no formato legado (`<source>-<id>-<hash>.lock`) enquanto ele estiver vivo, evitando duplicação durante upgrade.
4. Registros de estado ganham `key`; dedupe de status (`LatestByID`) e `list --all` escolhem o registro mais recente por ID.
5. `aiw status` sem id agrupa por ID e só mostra o estado mais recente; registros históricos continuam no disco.
6. `make verify` ganhou cenário com dois aliases da mesma pasta provando exactly-once.

## Consequências

Positivas:

- Aliases da mesma pasta não rodam a mesma tarefa em paralelo.
- Status/list não mostram mais `needs_input` antigo quando há `succeeded` posterior.
- Compatível com runs/claims antigos durante a transição.

Negativas e riscos:

- ClaimKey por caminho não cobre symlinks/NFS/múltiplas máquinas; `doing/` compartilhado continua sendo o mecanismo para esse caso.
- Registros antigos sem `key` dependem do fallback por ID/localização.
- Estado continua auditando por source; a visão ativa deduplica.
