package repo

import (
	"path/filepath"
	"testing"

	"github.com/mystaline/depscanner/internal/config"
	"github.com/mystaline/depscanner/internal/gitea"
)

type fakeLister struct{ repos []gitea.Repository }

func (f fakeLister) ListOrgRepos(string) ([]gitea.Repository, error) { return f.repos, nil }

func TestResolveGiteaProvider(t *testing.T) {
	fake := fakeLister{repos: []gitea.Repository{{Name: "svc-a"}, {Name: "docs"}}}
	p := config.Provider{Gitea: &config.GiteaProvider{URL: "https://h", Token: "t", Org: "org-a", ExcludeRepos: []string{"docs"}}}
	got, err := ResolveProvider(p, "/cache", false, func(string, string) Lister { return fake })
	if err != nil {
		t.Fatal(err)
	}
	if got.Group != "org-a" || got.Local {
		t.Fatalf("group=%q local=%v", got.Group, got.Local)
	}
	if len(got.Repos) != 1 || got.Repos[0].Name != "svc-a" {
		t.Fatalf("repos = %+v", got.Repos)
	}
	if got.Mgr.GetOrgPath() != filepath.Join("/cache", "org-a") {
		t.Fatalf("org path = %q", got.Mgr.GetOrgPath())
	}
}

func TestResolveGitProvider(t *testing.T) {
	p := config.Provider{Git: "https://gitea.example.com/org-a/acme-lib.git"}
	got, err := ResolveProvider(p, "/cache", false, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got.Group != "gitea.example.com-org-a" {
		t.Fatalf("group = %q", got.Group)
	}
	if len(got.Repos) != 1 || got.Repos[0].Name != "acme-lib" || got.Repos[0].CloneURL != p.Git {
		t.Fatalf("repos = %+v", got.Repos)
	}
}

func TestResolvePathProvider(t *testing.T) {
	dir := t.TempDir() // .../base
	p := config.Provider{Path: dir}
	got, err := ResolveProvider(p, "/cache", false, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !got.Local {
		t.Fatal("expected Local=true")
	}
	if len(got.Repos) != 1 || got.Repos[0].Name != filepath.Base(dir) {
		t.Fatalf("repos = %+v", got.Repos)
	}
	if got.Mgr.GetRepoPath(filepath.Base(dir)) != dir {
		t.Fatalf("repo path = %q, want %q", got.Mgr.GetRepoPath(filepath.Base(dir)), dir)
	}
}
