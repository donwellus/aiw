package engine

import (
	"context"
	"os"
	"path/filepath"
)

type Fake struct {
	name string
}

func NewFake(name string) *Fake {
	return &Fake{name: name}
}

func (f *Fake) Name() string {
	return f.name
}

func (f *Fake) Run(ctx context.Context, req Request) (Result, error) {
	if err := ctx.Err(); err != nil {
		return Result{}, err
	}
	switch req.Stage {
	case "requirements":
		return Result{Output: "# Spec (fake)\n\n## Contexto\n\nGerado pelo engine fake para validar o pipeline.\n\n## Critérios de aceite\n\n- Pipeline executa todos os estágios.\n"}, nil
	case "architect":
		return Result{Output: "# Plano (fake)\n\n1. Criar arquivo de demonstração.\n2. Validar pipeline.\n"}, nil
	case "implement":
		path := filepath.Join(req.Workdir, "AIW_FAKE_CHANGE.md")
		content := "# Alteração simulada\n\nO engine fake executou o estágio de implementação.\n"
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			return Result{}, err
		}
		return Result{Output: "implementação simulada criada em AIW_FAKE_CHANGE.md\n"}, nil
	case "review":
		return Result{Output: "APROVADO\n\nRevisão simulada pelo engine fake.\n"}, nil
	default:
		return Result{Output: "ok (fake)\n"}, nil
	}
}
