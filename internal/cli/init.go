package cli

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/donwellus/aiw/internal/config"
)

const exampleTask = `---
id: 001-exemplo
title: Documentar como executar o projeto
repo: .
labels: [aiw, documentacao]
priority: medium
acceptance:
  - Existe um arquivo WELCOME.md com uma seção "Como executar".
  - O comando de execução do projeto está documentado e correto.
---

## Contexto

Tarefa de exemplo criada pelo aiw init.

## O que fazer

Crie ou atualize o arquivo WELCOME.md na raiz do repositório com uma seção
"Como executar" descrevendo o comando principal do projeto, os pré-requisitos
e como rodar os testes.
`

func newInitCommand(opts *rootOptions) *cobra.Command {
	var force bool
	cmd := &cobra.Command{
		Use:   "init",
		Short: "Cria a configuração inicial e a pasta de tarefas",
		RunE: func(cmd *cobra.Command, args []string) error {
			root, err := filepath.Abs(opts.root)
			if err != nil {
				return err
			}
			path, err := config.WriteDefault(root, force)
			if err != nil {
				return err
			}
			for _, dir := range []string{"tasks", "tasks/done", "tasks/failed"} {
				if err := os.MkdirAll(filepath.Join(root, dir), 0o755); err != nil {
					return err
				}
			}
			examplePath := filepath.Join(root, "tasks", "001-exemplo.md")
			if _, err := os.Stat(examplePath); os.IsNotExist(err) {
				if err := os.WriteFile(examplePath, []byte(exampleTask), 0o644); err != nil {
					return err
				}
			}
			if err := ensureGitignore(filepath.Join(root, ".gitignore"), ".aiw/"); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "config criada em %s\n", path)
			fmt.Fprintf(cmd.OutOrStdout(), "tarefa de exemplo em %s\n\n", examplePath)
			fmt.Fprintln(cmd.OutOrStdout(), "Próximos passos:")
			fmt.Fprintln(cmd.OutOrStdout(), "  1. suba o Langfuse: make telemetry-up (no repo do aiw) ou defina LANGFUSE_*")
			fmt.Fprintln(cmd.OutOrStdout(), "  2. valide o ambiente: aiw doctor")
			fmt.Fprintln(cmd.OutOrStdout(), "  3. execute a tarefa de exemplo: aiw run --task tasks/001-exemplo.md --auto --engine fake")
			return nil
		},
	}
	cmd.Flags().BoolVar(&force, "force", false, "sobrescreve a config existente")
	return cmd
}

func ensureGitignore(path, entry string) error {
	data, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	for _, line := range splitLines(string(data)) {
		if line == entry {
			return nil
		}
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer file.Close()
	if len(data) > 0 && data[len(data)-1] != '\n' {
		if _, err := file.WriteString("\n"); err != nil {
			return err
		}
	}
	_, err = file.WriteString(entry + "\n")
	return err
}

func splitLines(value string) []string {
	var lines []string
	start := 0
	for i := 0; i < len(value); i++ {
		if value[i] == '\n' {
			lines = append(lines, value[start:i])
			start = i + 1
		}
	}
	if start < len(value) {
		lines = append(lines, value[start:])
	}
	return lines
}
