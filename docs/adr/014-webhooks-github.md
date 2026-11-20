# ADR 014 — Webhooks no daemon (GitHub)

- Status: aceito
- Data: 2026-11-17

## Contexto

O daemon faz polling por intervalo. Queremos reduzir a latência para novas issues/edições sem tornar o payload do provedor a fonte de verdade e sem rodar o pipeline dentro do handler HTTP.

## Decisão

1. `aiw daemon --webhook-addr` sobe um servidor HTTP separado:
   - `POST /webhooks/github` (alias `/webhooks`);
   - `GET /healthz`.
2. GitHub:
   - valida `X-Hub-Signature-256` (HMAC-SHA256 do corpo) com `webhook_secret_env` (default `GITHUB_WEBHOOK_SECRET`);
   - deduplica por `X-GitHub-Delivery` (24h, limite em memória);
   - trata `ping` (200) e apenas `issues` com ações opened/edited/reopened/labeled/unlabeled;
   - confere `repository.full_name` do repositório configurado;
   - responde 202 e não executa o pipeline no handler.
3. Wake: o handler sinaliza um canal `wake` (buffer 1); o loop do daemon interrompe a espera e roda `Collect` imediatamente. O payload não é usado como task: Fetch/Check/Claim continuam sendo a fonte de verdade.
4. Polling permanece como fallback (eventos perdidos/restart/rate limit).
5. Webhooks exigem endpoint público (HTTPS/reverse proxy) ou túnel em desenvolvimento; segredo só via env.

## Consequências

Positivas:

- Latência de segundos em vez de até um intervalo.
- Nenhum caminho novo para criação de task: claim/estado/dedupe continuam valendo.
- Segurança explícita (HMAC + dedupe + filtro de repo/evento).

Negativas e riscos:

- Requer exposição de rede e gestão de segredo.
- Fila/wake em memória; reinício perde eventos (polling cobre).
- Ainda faz listagem completa no próximo ciclo; otimização por ref direto fica para depois.
- Apenas GitHub nesta versão.

## Alternativas

- **Executar o pipeline no handler**: simples, porém bloqueia a resposta e perde o controle de concorrência/claims.
- **Webhook como fonte oficial**: arriscado; payload pode estar atrasado/incompleto.
- **Só polling**: mais simples, com latência maior e mais chamadas à API.
