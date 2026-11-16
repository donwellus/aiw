package sources

import (
	"fmt"
	"sort"

	"github.com/donwellus/aiw/internal/config"
	"github.com/donwellus/aiw/internal/source"
	"github.com/donwellus/aiw/internal/source/folder"
)

type Registry struct {
	cfg *config.Config
}

func NewRegistry(cfg *config.Config) *Registry {
	return &Registry{cfg: cfg}
}

func (r *Registry) Open(name string) (source.Source, error) {
	if name == "" {
		name = r.cfg.DefaultSource
	}
	sc, err := r.cfg.SourceConfig(name)
	if err != nil {
		return nil, err
	}
	switch sc.Driver {
	case "folder":
		return folder.New(name, r.cfg.Root, sc)
	default:
		return nil, fmt.Errorf("driver de fonte %q ainda não implementado (disponível: folder)", sc.Driver)
	}
}

func (r *Registry) Names() []string {
	names := make([]string, 0, len(r.cfg.Sources))
	for name := range r.cfg.Sources {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}
