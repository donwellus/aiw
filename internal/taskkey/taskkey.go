package taskkey

import (
	"path/filepath"

	"github.com/donwellus/aiw/internal/config"
)

type Keyer struct {
	cfg   *config.Config
	bases map[string]string
}

func New(cfg *config.Config) *Keyer {
	keyer := &Keyer{cfg: cfg, bases: map[string]string{}}
	for name, sourceCfg := range cfg.Sources {
		if sourceCfg.Driver != "folder" || sourceCfg.Path == "" {
			continue
		}
		base := sourceCfg.Path
		if !filepath.IsAbs(base) {
			base = filepath.Join(cfg.Root, base)
		}
		keyer.bases[name] = filepath.Clean(base)
	}
	return keyer
}

func (k *Keyer) Key(sourceName, id, claimKey string) string {
	if claimKey != "" {
		return claimKey
	}
	if sourceName == "" {
		sourceName = k.cfg.DefaultSource
	}
	if base, ok := k.bases[sourceName]; ok {
		return "folder:" + base + ":" + id
	}
	return sourceName + ":" + id
}
