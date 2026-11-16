# Segurança

## Versões suportadas

Apenas a branch `main` recebe correções de segurança no momento.

## Como reportar

Não abra issue pública para vulnerabilidades. Use o canal privado de Security Advisories:

https://github.com/donwellus/aiw/security/advisories/new

Inclua:

- versão/commit afetado;
- passos de reprodução;
- impacto esperado;
- sugestão de correção, se houver.

Você receberá uma resposta assim que possível. Créditos são dados no advisory, se desejado.

## Modelo de ameaça

O `aiw` orquestra agentes que editam arquivos e executam comandos em um repositório. Pontos de atenção:

- Execute sempre em `git worktree` isolado, com permissões mínimas; nunca use bypass de permissões no workspace principal.
- Conteúdo de tarefas/issues é dado não confiável: trate como contexto e mantenha gates humanos.
- Traces no Langfuse podem conter prompts e código. Use `capture: metadata` ou self-host conforme a sensibilidade; `redact_secrets` remove padrões comuns de token.
- Segredos devem vir de variáveis de ambiente. `infra/langfuse/.env` é ignorado pelo git e criado com permissão 600.
- Pull requests e pushes só acontecem com `--pr`; o merge é sempre humano.

## Escopo

Falhas em dependências devem ser reportadas aos projetos upstream, mas avise-nos se afetarem o `aiw`.
