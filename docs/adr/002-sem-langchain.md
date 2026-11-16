# ADR 002 — Não usar LangChain/LangGraph no core

- Status: aceito
- Data: 2026-11-15

## Contexto

LangChain e LangGraph oferecem abstrações de LLM, tools, memória, checkpoints e interrupções. A pergunta era se ajudariam a construir o workflow.

## Decisão

Não adicionar LangChain/LangGraph como dependência. Usar apenas as ideias do LangGraph: estado explícito, checkpoints, interrupções para aprovação e artefatos como contrato.

## Motivos

- SDKs oficiais em Python/TypeScript; não há suporte Go oficial.
- Integrar exigiria sidecar + IPC, dois runtimes e mais superfície de falha numa CLI.
- O loop de tool use já é delegado aos CLIs `claude`/`opencode`.
- Templates, parsing e máquina de estados são simples em Go.
- Observabilidade (Langfuse) e durabilidade (Temporal Go) têm alternativas agnósticas.
- Os problemas difíceis (context engineering, evals, sandbox, gates) não são resolvidos pelo framework.

## Consequências

- Precisamos manter prompts, contratos de artefato e retomada por estado próprio.
- Evals e tracing não vêm prontos; usamos Langfuse + testes de integração.

## Revisitar quando

- O core virar serviço Python/TypeScript.
- Houver requisito pesado de RAG multi-fonte.
- A equipe padronizar Python e a integração com Go deixar de ser custo.
