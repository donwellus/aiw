package engine

import (
	"fmt"
	"sort"
	"sync"

	"github.com/donwellus/aiw/internal/config"
)

type Registry struct {
	cfg   *config.Config
	mu    sync.Mutex
	cache map[string]Engine
}

func NewRegistry(cfg *config.Config) *Registry {
	return &Registry{cfg: cfg, cache: map[string]Engine{}}
}

func (r *Registry) Get(name string) (Engine, error) {
	if name == "" {
		name = r.cfg.Defaults.Engine
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if cached, ok := r.cache[name]; ok {
		return cached, nil
	}
	ec, err := r.cfg.EngineConfig(name)
	if err != nil {
		return nil, err
	}
	built, err := build(name, ec)
	if err != nil {
		return nil, err
	}
	r.cache[name] = built
	return built, nil
}

func (r *Registry) Names() []string {
	names := make([]string, 0, len(r.cfg.Engines))
	for name := range r.cfg.Engines {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func build(name string, ec config.EngineConfig) (Engine, error) {
	driver := ec.Driver
	if driver == "" {
		driver = name
	}
	switch driver {
	case "fake":
		return NewFake(name), nil
	case "claude":
		return NewClaude(name, ec)
	case "opencode":
		return NewOpencode(name, ec)
	case "exec":
		return NewExec(name, ec)
	default:
		return nil, fmt.Errorf("driver de engine %q desconhecido", driver)
	}
}
