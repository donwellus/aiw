package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"github.com/donwellus/aiw/internal/pipeline"
)

func newIntakeCommand(opts *rootOptions) *cobra.Command {
	return &cobra.Command{
		Use:   "intake <run-id>",
		Short: "Mostra o intake e as perguntas de uma execução",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := opts.load()
			if err != nil {
				return err
			}
			run, err := pipeline.LoadRun(cfg, args[0])
			if err != nil {
				return err
			}
			runDir := filepath.Join(cfg.RunsDir(), run.ID)
			intakePath := filepath.Join(runDir, "intake.md")
			questionsPath := filepath.Join(runDir, "questions.md")
			intake, intakeErr := os.ReadFile(intakePath)
			questions, questionsErr := os.ReadFile(questionsPath)
			if intakeErr != nil && questionsErr != nil {
				return fmt.Errorf("run %s não tem intake nem perguntas", run.ID)
			}
			out := cmd.OutOrStdout()
			fmt.Fprintf(out, "run:     %s\n", run.ID)
			fmt.Fprintf(out, "status:  %s\n", run.Status)
			fmt.Fprintf(out, "intake:  %s\n", intakePath)
			fmt.Fprintf(out, "perguntas: %s\n\n", questionsPath)
			if questionsErr == nil && strings.TrimSpace(string(questions)) != "" {
				fmt.Fprintln(out, "== Perguntas ==")
				fmt.Fprintln(out, strings.TrimSpace(string(questions)))
				fmt.Fprintln(out)
			}
			if intakeErr == nil {
				fmt.Fprintln(out, "== Intake ==")
				fmt.Fprintln(out, strings.TrimSpace(string(intake)))
				fmt.Fprintln(out)
			}
			if run.Status == "needs_input" {
				fmt.Fprintln(out, "edite a tarefa na fonte e execute novamente")
			}
			return nil
		},
	}
}
