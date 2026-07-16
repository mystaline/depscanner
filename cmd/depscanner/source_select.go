package main

import (
	"fmt"

	"github.com/mystaline/depscanner/internal/config"
)

func selectSource(cfg *config.Config, name string) (config.Source, error) {
	if name == "" {
		if len(cfg.Sources) == 1 {
			return cfg.Sources[0], nil
		}
		return config.Source{}, fmt.Errorf("--source is required (%d sources configured)", len(cfg.Sources))
	}
	for _, s := range cfg.Sources {
		label := s.Name
		if label == "" {
			label, _ = s.Group()
		}
		if label == name {
			return s, nil
		}
	}
	return config.Source{}, fmt.Errorf("source %q not found", name)
}
