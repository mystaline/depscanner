package repo

import (
	"fmt"
	"path/filepath"

	"github.com/mystaline/depscanner/internal/config"
	"github.com/mystaline/depscanner/internal/gitea"
)

// Lister lists the repositories in a Gitea org. *gitea.Client satisfies it.
type Lister interface {
	ListOrgRepos(org string) ([]gitea.Repository, error)
}

// ListerFactory builds a Lister for a given Gitea URL and token.
type ListerFactory func(url, token string) Lister

// Resolved is a provider turned into a concrete cache manager + repo list.
type Resolved struct {
	Group string
	Mgr   *Manager
	Repos []gitea.Repository
	Local bool // path provider: repo is used in place, never cloned/fetched
}

// ResolveProvider turns a config.Provider into a Resolved.
func ResolveProvider(p config.Provider, cacheDir string, offline bool, newLister ListerFactory) (Resolved, error) {
	kind, err := p.Location()
	if err != nil {
		return Resolved{}, err
	}
	baseDir := cacheDir
	if p.FlatCache != "" {
		baseDir = p.FlatCache
	}
	orgDir := ""
	switch kind {
	case "gitea":
		g := p.Gitea
		if p.FlatCache == "" {
			orgDir = g.Org
		}
		mgr := NewManager(baseDir, orgDir)
		var repos []gitea.Repository
		if offline {
			repos, err = mgr.ListLocalRepos()
			if err != nil {
				return Resolved{}, fmt.Errorf("list local repos for %s: %w", g.Org, err)
			}
		} else {
			if newLister == nil {
				return Resolved{}, fmt.Errorf("gitea provider %s requires a lister", g.Org)
			}
			repos, err = newLister(g.URL, g.Token).ListOrgRepos(g.Org)
			if err != nil {
				return Resolved{}, fmt.Errorf("list repos for %s: %w", g.Org, err)
			}
		}
		if g.Repo != "" {
			var found bool
			for _, r := range repos {
				if r.Name == g.Repo {
					repos = []gitea.Repository{r}
					found = true
					break
				}
			}
			if !found {
				return Resolved{}, fmt.Errorf("repo %q not found in org %q (%d repos total)", g.Repo, g.Org, len(repos))
			}
		} else {
			repos = filterReposByName(repos, g.IncludeRepos, g.ExcludeRepos)
		}
		return Resolved{Group: g.Org, Mgr: mgr, Repos: repos}, nil

	case "git":
		host, owner, name, perr := config.ParseGitURL(p.Git)
		if perr != nil {
			return Resolved{}, perr
		}
		group := host + "-" + owner
		if p.FlatCache != "" {
			orgDir = ""
		} else {
			orgDir = group
		}
		return Resolved{
			Group: group,
			Mgr:   NewManager(baseDir, orgDir),
			Repos: []gitea.Repository{{Name: name, CloneURL: p.Git}},
		}, nil

	default: // path
		abs, aerr := filepath.Abs(p.Path)
		if aerr != nil {
			return Resolved{}, aerr
		}
		group, gerr := p.Group()
		if gerr != nil {
			return Resolved{}, gerr
		}
		return Resolved{
			Group: group,
			Mgr:   NewManager(filepath.Dir(abs), ""),
			Repos: []gitea.Repository{{Name: filepath.Base(abs)}},
			Local: true,
		}, nil
	}
}

// filterReposByName applies include/exclude glob lists to a repo slice.
func filterReposByName(repos []gitea.Repository, include, exclude []string) []gitea.Repository {
	var out []gitea.Repository
	for _, r := range repos {
		if matchesAnyPattern(r.Name, exclude) {
			continue
		}
		if len(include) > 0 && !matchesAnyPattern(r.Name, include) {
			continue
		}
		out = append(out, r)
	}
	return out
}

// matchesAnyPattern reports whether name equals or glob-matches any pattern.
func matchesAnyPattern(name string, patterns []string) bool {
	for _, p := range patterns {
		if p == name {
			return true
		}
		if ok, _ := filepath.Match(p, name); ok {
			return true
		}
	}
	return false
}
