package config

import (
	"os"
	"path/filepath"
	"testing"
)

func writeCfg(t *testing.T, body string) string {
	t.Helper()
	dir := t.TempDir()
	p := filepath.Join(dir, "c.yaml")
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestLegacyConfigNormalizes(t *testing.T) {
	p := writeCfg(t, `
gitea:
  url: https://gitea.example.com
  token: t
  org: org-a
target_module: gitea.example.com/org-a/acme-lib
`)
	cfg, err := Load(p)
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.Sources) != 1 || cfg.Sources[0].Module != "gitea.example.com/org-a/acme-lib" {
		t.Fatalf("sources = %+v", cfg.Sources)
	}
	g := cfg.Sources[0].Gitea
	if g == nil {
		t.Fatal("source has no gitea provider")
	}
	if g.Repo != "acme-lib" {
		t.Errorf("Repo = %q, want %q", g.Repo, "acme-lib")
	}
	if len(g.IncludeRepos) != 0 {
		t.Errorf("IncludeRepos should be empty, got %v", g.IncludeRepos)
	}
	if len(cfg.Consumers) != 1 || cfg.Consumers[0].Gitea == nil || cfg.Consumers[0].Gitea.Org != "org-a" {
		t.Fatalf("consumers = %+v", cfg.Consumers)
	}
	if err := cfg.Validate(); err != nil {
		t.Fatalf("validate: %v", err)
	}
}

func TestNewStyleConfigValidates(t *testing.T) {
	p := writeCfg(t, `
sources:
  - name: core
    path: /tmp/core
consumers:
  - path: /tmp/svc
`)
	cfg, err := Load(p)
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.Sources) != 1 || len(cfg.Consumers) != 1 {
		t.Fatalf("got sources=%d consumers=%d", len(cfg.Sources), len(cfg.Consumers))
	}
	if err := cfg.Validate(); err != nil {
		t.Fatalf("validate: %v", err)
	}
}

func TestValidateRejectsEmpty(t *testing.T) {
	cfg := &Config{}
	if err := cfg.Validate(); err == nil {
		t.Fatal("expected error for empty config")
	}
}
