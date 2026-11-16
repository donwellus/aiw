# ADR 003 — Langfuse self-hosted para observabilidade

- Status: aceito
- Data: 2026-11-15

## Contexto

Precisamos inspecionar execuções: prompts enviados, saídas, custo, tokens, latência, gates, retries e resultados de evals. A solução deve funcionar com Go, preferencialmente self-hosted, e nunca derrubar o pipeline.

## Decisão

- **Langfuse v4 self-hosted via Docker Compose**, compose oficial vendorizado em `infra/langfuse/compose.yaml`, imagem fixada (`LANGFUSE_IMAGE_TAG`, atualmente `4.80.0`).
- Serviços: web, worker, Postgres, ClickHouse, Redis, MinIO (e AI gateway do compose oficial, ocioso sem chaves).
- **Headless init** (`LANGFUSE_INIT_*`) cria organização, projeto, usuário e chaves na primeira subida. `infra/langfuse/init-env.sh` gera segredos aleatórios.
- **Cliente Go via OpenTelemetry SDK** (OTLP HTTP) para `/api/public/otel/v1/traces`, Basic auth e header `x-langfuse-ingestion-version: 4`; batching/retry do exporter.
- **Scores** via `POST /api/public/scores`, pois o endpoint de traces legado (`/api/public/ingestion`) foi descontinuado no modo `events_only` do Langfuse v4.
- Modelo de trace: 1 trace por run, 1 span por estágio, 1 generation por chamada de engine, events para gates/retries, scores para sucesso/custo.
- **Captura completa** (prompt/output/código), com redaction de padrões de segredo e truncamento de campos em 256 KiB. Atributos usam o namespace `langfuse.*` mapeado pelo Langfuse (input/output, tipo, modelo, usage e custo em `gen_ai.*`).
- **Fail-open por padrão**: falha de telemetria não altera o resultado do run.
- Ambiente de dev via Compose; kind + Helm documentado como evolução futura. O cliente HTTP não muda entre os modos.

## Consequências

- Stack local pesada (ClickHouse/Redis/MinIO); subir sob demanda (`make telemetry-up/down`).
- Dados de trace ficam na máquina, o que viabiliza `capture: full`.
- Crescimento do ClickHouse exige retenção/limpeza futura.
- Se Langfuse estiver fora, traces são perdidos (fail-open) e `aiw doctor` sinaliza.
- Migrações de versão do Langfuse exigem atualizar o pin e testar o mapeamento de atributos OTLP.
- O modo `events_only` do v4 desabilita APIs legadas de consulta; validação automatizada usa `/api/public/v2/observations` e, quando necessário, consultas diretas ao ClickHouse.

## Alternativas

- **Langfuse Cloud**: mais rápido de começar, mas prompts/código sairiam da máquina; mantido como opção via `LANGFUSE_HOST`.
- **OTel + outro backend**: o modelo OTel já é usado; trocar o backend exige apenas mudar o exporter (Langfuse, Jaeger, otel-collector).
- **Solução caseira (JSONL)**: já temos `events.jsonl` local como fallback de auditoria, mas sem UI, comparação e agregação.
