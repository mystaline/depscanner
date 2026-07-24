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

func TestSelectSourceByRepo(t *testing.T) {
	multi := &config.Config{Sources: []config.Source{
		{Provider: config.Provider{Gitea: &config.GiteaProvider{Org: "org-a", Repo: "my-lib"}}},
		{Provider: config.Provider{Gitea: &config.GiteaProvider{Org: "org-b", Repo: "lib-two"}}},
	}}
	if s, err := selectSource(multi, "my-lib"); err != nil {
		t.Fatalf("select by repo: %v", err)
	} else if s.Gitea.Repo != "my-lib" {
		t.Fatalf("selected wrong source: %+v", s)
	}
	if _, err := selectSource(multi, "missing"); err == nil {
		t.Fatal("expected error for unknown repo")
	}
}
