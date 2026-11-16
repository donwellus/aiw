# ADR 001 — Arquitetura do aiw

- Status: aceito
- Data: 2026-11-15

## Contexto

Precisamos de um fluxo de desenvolvimento com agentes que leia tarefas de uma fonte (pasta, GitHub, Linear, Jira, Trello), passe por análise, arquitetura, implementação, testes e revisão, e entregue uma branch/PR. O objetivo é dogfooding: usar o próprio fluxo para construir a aplicação, com CLI em Go e sem SaaS.

Restrições: rodar localmente, ser auditável, permitir troca de engine, evitar custo descontrolado e não depender de orquestração Python/TypeScript.

## Decisão

1. **CLI Go** (`aiw`) com máquina de estados determinística: `requirements → architect → implement → test → review → deliver`.
2. **Delegar o loop de agente** (tool use, edição, execução) aos CLIs `claude` e `opencode`, atrás de uma interface `Engine`. Um driver `exec` genérico cobre futuras engines.
3. **Handoff por artefatos**: `spec.md`, `plan.md`, `implement.md`, `diff.patch`, `test.log`, `review.md`, `deliver.md`, espelhados em `.aiw/runs/<id>/`.
4. **Isolamento por git worktree** e branch `aiw/<run-id>`. Commit feito pelo pipeline; push/PR apenas com `--pr`.
5. **Gates humanos** em spec e plano, persistidos em `run.json`; `aiw approve` + `aiw resume`.
6. **Budget por execução** e custo/tokens agregados a partir do JSON dos engines.
7. **Fonte plugável** (`Source`) com snapshots em `.aiw/queue` e deduplicação/lock em `.aiw/state`. v0 implementa `folder`.
8. **Telemetria plugável**, com Langfuse self-hosted e fail-open (ADR 003).
9. **Sem LangChain/LangGraph** (ADR 002).

## Consequências

Positivas:

- Pipeline reproduzível e depurável; artefatos explicam cada decisão.
- Troca de engine/modelo sem mudar o orquestrador.
- Custo controlável por run e por estágio.
- Base de código pequena, sem runtime extra.

Negativas:

- Qualidade depende dos CLIs de agente e dos prompts.
- Multi-repo exige resolver o repositório por tarefa, já suportado por `repo:`/`repo:*`.
- Execução paralela e durabilidade avançada (retomada com fila) ficam para depois; `resume` atual é por estágio.

## Alternativas descartadas

- **LangGraph/LangChain**: exigiria sidecar Python ou reescrita; o loop de agente já é resolvido pelos CLIs.
- **Loop de agente próprio**: aumentaria escopo e risco sem diferencial no curto prazo.
- **Temporal desde o início**: durabilidade real, mas custo operacional alto para a v0; pode ser adotado se a retomada por arquivo se mostrar insuficiente.
