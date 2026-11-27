# ADR 034 — Upload e ingestão de imagens

- Status: aceito
- Data: 2026-11-27

## Contexto

Issues com wireframes e screenshots chegavam ao texto, mas as imagens não eram
baixadas. O intake podia seguir sem elas e a implementação divergia do desenho.
Também não havia como criar uma issue com imagens pelo coordinator.

## Decisão

1. **Upload na criação**:
   - `TaskDraft.Attachments` com paths locais;
   - `attachments_authorized=true` obrigatório;
   - `validator` verifica arquivo regular, extensão de imagem, tamanho e
     autorização;
   - GitHub usa `gh issue create --attach`;
   - folder copia para `tasks/attachments/<task-id>/` e usa links relativos.
2. **Download na execução**:
   - `source.AttachmentFetcher` recebe `destDir` e devolve paths locais;
   - GitHub baixa `user-attachments` com token do `gh` e hosts permitidos;
   - redirects para `github-production-{user,release}-asset-*.s3.amazonaws.com`
     são aceitos, mas o token do GitHub não é enviado ao S3;
   - folder resolve paths locais;
   - arquivos vão para `.aiw-run/attachments/`.
3. **Gate em requirements**:
   - referências `![...]`, HTML `<img>`, URLs de imagem e menções a
     wireframe/desenho/screenshot são verificadas;
   - falha de download ou ausência de imagem → `ErrNeedsInput` em
     `requirements`, com `requirements-blockers.md`/`questions.md`;
   - spec, plano e implementação não são executados nesse caso.
4. **Prompt**:
   - paths de anexos entram no `prompts.Data`;
   - intake, requirements, architect, implement e review devem ler as imagens.

## Consequências

Positivas:

- O coordinator cria issues com imagens sem shell.
- O worker recebe as imagens antes de definir requisitos e implementar.
- Falhas de referência visual param o fluxo deterministicamente.

Negativas e riscos:

- Upload para GitHub depende de `gh` com suporte a `--attach`.
- Anexos de repositórios privados exigem token válido.
- Referências muito genéricas podem criar falsos positivos no gate; por isso o
  bloqueio inclui o motivo e permite corrigir a issue.
