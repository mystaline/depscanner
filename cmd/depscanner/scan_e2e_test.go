// cmd/depscanner/scan_e2e_test.go
package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/mystaline/depscanner/internal/analysis"
	"github.com/mystaline/depscanner/internal/config"
	"github.com/mystaline/depscanner/internal/gitea"
	"github.com/mystaline/depscanner/internal/repo"
)

// gitInit creates a repo dir with the given files and one commit.
func gitInit(t *testing.T, dir string, files map[string]string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	for name, body := range files {
		p := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	for _, args := range [][]string{
		{"init", "-q"},
		{"config", "user.email", "t@t"},
		{"config", "user.name", "t"},
		{"add", "-A"},
		{"commit", "-q", "-m", "init"},
	} {
		cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
}

type fakeLister struct{ repos []gitea.Repository }

func (f fakeLister) ListOrgRepos(string) ([]gitea.Repository, error) { return f.repos, nil }

func TestScanE2E_MultiSource(t *testing.T) {
	root := t.TempDir()
	cache := filepath.Join(root, "cache")

	// Consumer org "BETS-V2" served from a bare repo the manager can clone.
	consumerSrc := filepath.Join(root, "svc-a-src")
	gitInit(t, consumerSrc, map[string]string{
		"go.mod": "module svc/a\n\ngo 1.22\n\nrequire example.com/org/ts-utils v0.1.0\n",
		"main.go": `package main
import "example.com/org/ts-utils/helper"
func main() { helper.Must(nil) }
`,
	})
	bare := filepath.Join(root, "svc-a.git")
	if out, err := exec.Command("git", "clone", "--bare", "-q", consumerSrc, bare).CombinedOutput(); err != nil {
		t.Fatalf("bare clone: %v\n%s", err, out)
	}

	// Source module as a local path provider (module read from its go.mod).
	sourceDir := filepath.Join(root, "ts-utils")
	if err := os.MkdirAll(filepath.Join(sourceDir, "helper"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sourceDir, "go.mod"), []byte("module example.com/org/ts-utils\n\ngo 1.22\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	fake := fakeLister{repos: []gitea.Repository{{Name: "svc-a", CloneURL: bare}}}
	factory := func(string, string) repo.Lister { return fake }

	consumer := config.Provider{Gitea: &config.GiteaProvider{URL: "x", Token: "x", Org: "BETS-V2"}}
	cset, err := repo.ResolveProvider(consumer, cache, false, factory)
	if err != nil {
		t.Fatal(err)
	}
	if err := cset.Mgr.SyncRepos(cset.Repos, false); err != nil {
		t.Fatal(err)
	}

	src := config.Source{Provider: config.Provider{Name: "ts-utils", Path: sourceDir}}
	sres, err := repo.ResolveProvider(src.Provider, cache, false, factory)
	if err != nil {
		t.Fatal(err)
	}
	module, err := readModuleForTest(t, sres, sourceDir)
	_ = module

	funcNames = []string{"helper.Must"}
	t.Cleanup(func() { funcNames = nil })

	repoPath := cset.Mgr.GetRepoPath("svc-a")
	res, hasGoMod, usesTarget := scanRepoForSource(repoPath, "example.com/org/ts-utils", "ts-utils", cset.Group, "svc-a", "main", "", cset.Repos[0], analysis.ReturnTypeRegistry{})
	if !hasGoMod || !usesTarget {
		t.Fatalf("expected svc-a to use ts-utils: hasGoMod=%v usesTarget=%v", hasGoMod, usesTarget)
	}
	if res.SourceName != "ts-utils" || res.Group != "BETS-V2" {
		t.Fatalf("tagging wrong: source=%q group=%q", res.SourceName, res.Group)
	}
	if len(res.CallSites) != 1 {
		t.Fatalf("expected 1 call site for helper.Must, got %d", len(res.CallSites))
	}
}

// readModuleForTest reads the source module path; asserts the path resolver
// points at the real dir.
func readModuleForTest(t *testing.T, sres repo.Resolved, sourceDir string) (string, error) {
	t.Helper()
	if sres.Mgr.GetRepoPath(sres.Repos[0].Name) != sourceDir {
		t.Fatalf("path provider resolved to %q, want %q", sres.Mgr.GetRepoPath(sres.Repos[0].Name), sourceDir)
	}
	return "example.com/org/ts-utils", nil
}
