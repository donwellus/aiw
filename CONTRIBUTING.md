# Contribuindo com o aiw

Obrigado pelo interesse. O projeto é novo e feedback com casos reais é especialmente útil.

## Ambiente

- Go 1.21+
- `git`
- Opcional: Docker + Docker Compose (Langfuse), `claude`/`opencode` (execuções reais), `gh` (`--pr`)

```bash
git clone https://github.com/donwellus/aiw.git
cd aiw
make check
make smoke
```

## Fluxo de contribuição

1. Abra uma issue descrevendo o problema ou proposta antes de mudanças grandes.
2. Crie uma branch curta (`feat/...`, `fix/...`, `docs/...`).
3. Mantenha o escopo pequeno e inclua testes para comportamento novo.
4. Rode `make check` (e `make lint` se tiver golangci-lint instalado).
5. Abra o PR explicando motivação, comportamento e como validar.
6. Mudanças de arquitetura devem vir com um ADR em `docs/adr/`.

## Estilo

- `gofmt`/`goimports` obrigatórios; o CI verifica.
- Mensagens de commit no formato Conventional Commits (`feat:`, `fix:`, `docs:`, `refactor:`, `test:`, `chore:`).
- Evite dependências novas sem justificativa no PR.
- Não adicione comentários redundantes; prefira nomes claros e testes.

## Segurança

- Nunca commite segredos, `.env`, chaves de API ou traces com dados sensíveis.
- Use `infra/langfuse/.env.example` como referência e mantenha `.env` local.
- O CI roda gitleaks no histórico, govulncheck e dependency review.
- Para vulnerabilidades, siga [SECURITY.md](../SECURITY.md).

## Licença

Ao contribuir, você concorda que sua contribuição é licenciada sob a Apache License 2.0 (inbound = outbound).
