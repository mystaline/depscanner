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

func TestResolveGiteaRepo(t *testing.T) {
	fake := fakeLister{repos: []gitea.Repository{
		{Name: "svc-a", CloneURL: "https://h/org/svc-a"},
		{Name: "svc-b", CloneURL: "https://h/org/svc-b"},
		{Name: "docs", CloneURL: "https://h/org/docs"},
	}}

	// Repo exact match
	p := config.Provider{Gitea: &config.GiteaProvider{URL: "https://h", Token: "t", Org: "org-a", Repo: "svc-a"}}
	got, err := ResolveProvider(p, "/cache", false, func(string, string) Lister { return fake })
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Repos) != 1 || got.Repos[0].Name != "svc-a" {
		t.Fatalf("repos = %+v, want [svc-a]", got.Repos)
	}

	// Repo not found → error
	p2 := config.Provider{Gitea: &config.GiteaProvider{URL: "https://h", Token: "t", Org: "org-a", Repo: "nonexistent"}}
	_, err = ResolveProvider(p2, "/cache", false, func(string, string) Lister { return fake })
	if err == nil {
		t.Fatal("expected error for missing repo")
	}

	// IncludeRepos still works (consumer path) when Repo is empty
	p3 := config.Provider{Gitea: &config.GiteaProvider{URL: "https://h", Token: "t", Org: "org-a", IncludeRepos: []string{"svc-*"}}}
	got3, err := ResolveProvider(p3, "/cache", false, func(string, string) Lister { return fake })
	if err != nil {
		t.Fatal(err)
	}
	if len(got3.Repos) != 2 {
		t.Fatalf("repos = %+v, want 2 (svc-a, svc-b)", got3.Repos)
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

func TestResolveGiteaFlatCache(t *testing.T) {
	fake := fakeLister{repos: []gitea.Repository{{Name: "svc-a"}, {Name: "svc-b"}}}
	p := config.Provider{
		FlatCache: "/flat",
		Gitea:     &config.GiteaProvider{URL: "https://h", Token: "t", Org: "org-a"},
	}
	got, err := ResolveProvider(p, "/cache", false, func(string, string) Lister { return fake })
	if err != nil {
		t.Fatal(err)
	}
	if got.Group != "org-a" {
		t.Fatalf("group = %q, want org-a", got.Group)
	}
	if got.Local {
		t.Fatal("expected Local=false")
	}
	if len(got.Repos) != 2 {
		t.Fatalf("repos = %+v, want 2 (svc-a, svc-b)", got.Repos)
	}
	if got.Mgr.GetOrgPath() != "/flat" {
		t.Fatalf("GetOrgPath = %q, want /flat", got.Mgr.GetOrgPath())
	}
	if got.Mgr.GetRepoPath("svc-a") != "/flat/svc-a" {
		t.Fatalf("GetRepoPath(svc-a) = %q, want /flat/svc-a", got.Mgr.GetRepoPath("svc-a"))
	}
}
