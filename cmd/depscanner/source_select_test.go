package main

import (
	"testing"

	"github.com/mystaline/depscanner/internal/config"
)

func TestSelectSource(t *testing.T) {
	one := &config.Config{Sources: []config.Source{{Provider: config.Provider{Name: "a", Path: "/a"}}}}
	if s, err := selectSource(one, ""); err != nil || s.Name != "a" {
		t.Fatalf("single default: %+v %v", s, err)
	}

	multi := &config.Config{Sources: []config.Source{
		{Provider: config.Provider{Name: "a", Path: "/a"}},
		{Provider: config.Provider{Name: "b", Path: "/b"}},
	}}
	if _, err := selectSource(multi, ""); err == nil {
		t.Fatal("expected error when multiple sources and no --source")
	}
	if s, err := selectSource(multi, "b"); err != nil || s.Name != "b" {
		t.Fatalf("by name: %+v %v", s, err)
	}
	if _, err := selectSource(multi, "zzz"); err == nil {
		t.Fatal("expected error for unknown source")
	}
}
