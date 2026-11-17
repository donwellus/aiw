package eval

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/donwellus/aiw/internal/config"
)

type Suite struct {
	Name    string          `yaml:"name"`
	Engine  string          `yaml:"engine"`
	Timeout config.Duration `yaml:"timeout"`
	Cases   []Case          `yaml:"cases"`
	Path    string          `yaml:"-"`
	Dir     string          `yaml:"-"`
}

type Case struct {
	ID      string          `yaml:"id"`
	Task    string          `yaml:"task"`
	Repo    string          `yaml:"repo"`
	BaseRef string          `yaml:"base_ref"`
	Verify  string          `yaml:"verify"`
	Engine  string          `yaml:"engine"`
	Timeout config.Duration `yaml:"timeout"`
}

func Load(path string) (*Suite, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(abs)
	if err != nil {
		return nil, err
	}
	var suite Suite
	if err := yaml.Unmarshal(data, &suite); err != nil {
		return nil, fmt.Errorf("suite inválida: %w", err)
	}
	suite.Path = abs
	suite.Dir = filepath.Dir(abs)
	if strings.TrimSpace(suite.Name) == "" {
		suite.Name = filepath.Base(suite.Dir)
	}
	if suite.Timeout == 0 {
		suite.Timeout = config.Duration(30 * time.Minute)
	}
	if len(suite.Cases) == 0 {
		return nil, fmt.Errorf("suite %s sem casos", suite.Name)
	}
	ids := map[string]bool{}
	for index := range suite.Cases {
		item := &suite.Cases[index]
		item.ID = strings.TrimSpace(item.ID)
		if item.ID == "" {
			return nil, fmt.Errorf("caso #%d sem id", index+1)
		}
		if ids[item.ID] {
			return nil, fmt.Errorf("id de caso duplicado: %s", item.ID)
		}
		ids[item.ID] = true
		item.Task = resolve(suite.Dir, item.Task)
		item.Repo = resolve(suite.Dir, item.Repo)
		if item.Task == "" || item.Repo == "" {
			return nil, fmt.Errorf("caso %s precisa de task e repo", item.ID)
		}
		if info, err := os.Stat(item.Task); err != nil || info.IsDir() {
			return nil, fmt.Errorf("caso %s: task inválida %s", item.ID, item.Task)
		}
		if info, err := os.Stat(item.Repo); err != nil || !info.IsDir() {
			return nil, fmt.Errorf("caso %s: repo inválido %s", item.ID, item.Repo)
		}
		if item.Timeout == 0 {
			item.Timeout = suite.Timeout
		}
	}
	return &suite, nil
}

func resolve(base, value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	if filepath.IsAbs(value) {
		return filepath.Clean(value)
	}
	return filepath.Join(base, value)
}
