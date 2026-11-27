package pipeline

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/donwellus/aiw/internal/source"
)

func (p *Pipeline) prepareRequirementsAttachments(ctx context.Context, run *RunState) error {
	refs := source.MediaReferences(&run.Task)
	if len(refs) == 0 && !source.MentionsVisualRequirement(&run.Task) {
		return nil
	}
	if len(run.Task.Attachments) > 0 && attachmentsReadable(run.Task.Attachments) {
		return nil
	}
	if len(refs) == 0 {
		return p.requirementsAttachmentBlocked(run, "referência visual mencionada, mas nenhum anexo acessível foi encontrado", nil)
	}
	src, err := p.Sources.Open(run.Source)
	if err != nil {
		return p.requirementsAttachmentBlocked(run, "fonte indisponível para baixar anexos: "+err.Error(), refs)
	}
	fetcher, ok := src.(source.AttachmentFetcher)
	if !ok {
		return p.requirementsAttachmentBlocked(run, "a fonte não suporta download de imagens; anexe as referências novamente", refs)
	}
	destDir := p.workArtifact(run, "attachments")
	attachments, err := fetcher.FetchAttachments(ctx, &run.Task, destDir)
	if err != nil {
		return p.requirementsAttachmentBlocked(run, "falha ao baixar anexos: "+err.Error(), refs)
	}
	if len(attachments) == 0 {
		return p.requirementsAttachmentBlocked(run, "referências visuais presentes, mas nenhuma imagem foi baixada", refs)
	}
	paths := make([]string, 0, len(attachments))
	for _, attachment := range attachments {
		paths = append(paths, attachment.Path)
	}
	run.Task.Attachments = paths
	p.save(run, p.runDir(run))
	appendEvent(p.runDir(run), "requirements.attachments", map[string]any{"count": len(attachments), "refs": refs})
	p.mirror(run)
	return nil
}

func (p *Pipeline) requirementsAttachmentBlocked(run *RunState, reason string, refs []string) error {
	var builder strings.Builder
	builder.WriteString("# Requisitos visuais bloqueados\n\n")
	builder.WriteString(reason)
	builder.WriteString("\n\n")
	if len(refs) > 0 {
		builder.WriteString("## Referências encontradas\n\n")
		for _, ref := range refs {
			fmt.Fprintf(&builder, "- %s\n", ref)
		}
	}
	p.writeWork(run, "requirements-blockers.md", builder.String())
	p.writeQuestions(run, []string{"Adicione uma imagem acessível à fonte ou corrija a referência visual: " + reason}, nil)
	p.mirror(run)
	return fmt.Errorf("%w: %s", ErrNeedsInput, reason)
}

func attachmentsReadable(paths []string) bool {
	for _, path := range paths {
		info, err := os.Stat(path)
		if err != nil || !info.Mode().IsRegular() {
			return false
		}
	}
	return len(paths) > 0
}
