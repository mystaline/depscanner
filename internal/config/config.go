// Package config loads depscanner configuration from a YAML file,
// with environment variable expansion for sensitive values like tokens.
package config

import (
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

// OrgConfig holds per-org settings for multi-org mode.
type OrgConfig struct {
	Name         string   `yaml:"name"`
	IncludeRepos []string `yaml:"include_repos"`
	ExcludeRepos []string `yaml:"exclude_repos"`
}

// GiteaConfig holds connection details for the Gitea instance.
type GiteaConfig struct {
	URL   string      `yaml:"url"`
	Token string      `yaml:"token"`
	Org   string      `yaml:"org"`
	Orgs  []OrgConfig `yaml:"orgs"`
}

// GiteaProvider is a Gitea-org location: repos are auto-discovered via the API.
type GiteaProvider struct {
	URL          string   `yaml:"url"`
	Token        string   `yaml:"token"`
	Org          string   `yaml:"org"`
	IncludeRepos []string `yaml:"include_repos"`
	ExcludeRepos []string `yaml:"exclude_repos"`
}

// Provider is one location for a source or consumer: exactly one of
// Gitea (org auto-discovery), Git (clone URL), or Path (local checkout).
type Provider struct {
	Name  string         `yaml:"name"`
	Gitea *GiteaProvider `yaml:"gitea"`
	Git   string         `yaml:"git"`
	Path  string         `yaml:"path"`
}

// Source is a source-of-truth module: a Provider plus an optional module path
// (read from the repo's go.mod when empty).
type Source struct {
	Provider `yaml:",inline"`
	Module   string `yaml:"module"`
}

// Location returns "gitea", "git", or "path", or an error if not exactly one
// location field is set.
func (p Provider) Location() (string, error) {
	n := 0
	kind := ""
	if p.Gitea != nil {
		n++
		kind = "gitea"
	}
	if p.Git != "" {
		n++
		kind = "git"
	}
	if p.Path != "" {
		n++
		kind = "path"
	}
	if n != 1 {
		return "", fmt.Errorf("provider %q must set exactly one of gitea/git/path (found %d)", p.Name, n)
	}
	return kind, nil
}

// ParseGitURL extracts host, owner, and repo (without .git) from an https or
// scp-style git URL.
func ParseGitURL(raw string) (host, owner, repo string, err error) {
	s := strings.TrimSuffix(raw, ".git")
	// scp-style: git@host:owner/repo
	if strings.HasPrefix(s, "git@") {
		s = strings.TrimPrefix(s, "git@")
		hostPart, rest, ok := strings.Cut(s, ":")
		if !ok {
			return "", "", "", fmt.Errorf("invalid git url: %s", raw)
		}
		host = hostPart
		owner, repo = splitLastTwo(rest)
	} else {
		u, perr := url.Parse(s)
		if perr != nil || u.Host == "" {
			return "", "", "", fmt.Errorf("invalid git url: %s", raw)
		}
		host = u.Host
		owner, repo = splitLastTwo(strings.Trim(u.Path, "/"))
	}
	if owner == "" || repo == "" {
		return "", "", "", fmt.Errorf("git url missing owner/repo: %s", raw)
	}
	return host, owner, repo, nil
}

// splitLastTwo returns the last two path segments of p (owner, repo).
func splitLastTwo(p string) (owner, repo string) {
	parts := strings.Split(strings.Trim(p, "/"), "/")
	if len(parts) < 2 {
		return "", ""
	}
	return parts[len(parts)-2], parts[len(parts)-1]
}

// Group returns the classification label for a provider.
func (p Provider) Group() (string, error) {
	if p.Name != "" {
		return p.Name, nil
	}
	kind, err := p.Location()
	if err != nil {
		return "", err
	}
	switch kind {
	case "gitea":
		return p.Gitea.Org, nil
	case "git":
		host, owner, _, perr := ParseGitURL(p.Git)
		if perr != nil {
			return "", perr
		}
		return host + "-" + owner, nil
	default: // path
		return filepath.Base(strings.TrimRight(p.Path, "/")), nil
	}
}

// Config holds all runtime configuration for depscanner.
type Config struct {
	Gitea             GiteaConfig       `yaml:"gitea"`
	TargetModule      string            `yaml:"target_module"`
	CacheDir          string            `yaml:"cache_dir"`
	IncludeRepos      []string          `yaml:"include_repos"`
	ExcludeRepos      []string          `yaml:"exclude_repos"`
	DefaultBranch     string            `yaml:"default_branch"`
	BranchTracking    map[string]string `yaml:"branch_tracking"`
	UnshallowBranches []string          `yaml:"unshallow_branches"`
	Offline           bool              `yaml:"offline"`
	Sources           []Source          `yaml:"sources"`
	Consumers         []Provider        `yaml:"consumers"`
}

// Validate checks that all required fields are present.
func (c *Config) Validate() error {
	if len(c.Sources) == 0 {
		return fmt.Errorf("at least one source is required (set `sources` or legacy `target_module`)")
	}
	if len(c.Consumers) == 0 {
		return fmt.Errorf("at least one consumer is required (set `consumers` or legacy `gitea.org`/`gitea.orgs`)")
	}
	for _, s := range c.Sources {
		if _, err := s.Location(); err != nil {
			return fmt.Errorf("source: %w", err)
		}
	}
	for _, p := range c.Consumers {
		if _, err := p.Location(); err != nil {
			return fmt.Errorf("consumer: %w", err)
		}
	}
	return nil
}

// normalize back-fills Sources/Consumers from the legacy single-target config
// when the new-style lists are empty, so old config files keep working.
func (c *Config) normalize() {
	legacyGitea := func() *GiteaProvider {
		if c.Gitea.URL == "" && c.Gitea.Org == "" && len(c.Gitea.Orgs) == 0 {
			return nil
		}
		return &GiteaProvider{URL: c.Gitea.URL, Token: c.Gitea.Token}
	}

	if len(c.Sources) == 0 && c.TargetModule != "" {
		src := Source{Module: c.TargetModule}
		if g := legacyGitea(); g != nil {
			owner, repoName := parseModuleOwner(c.TargetModule)
			gp := *g
			gp.Org = owner
			gp.IncludeRepos = []string{repoName}
			src.Provider = Provider{Gitea: &gp}
		}
		c.Sources = []Source{src}
	}

	if len(c.Consumers) == 0 {
		for _, org := range c.ActiveOrgs() {
			c.Consumers = append(c.Consumers, Provider{
				Gitea: &GiteaProvider{
					URL:          c.Gitea.URL,
					Token:        c.Gitea.Token,
					Org:          org.Name,
					IncludeRepos: org.IncludeRepos,
					ExcludeRepos: org.ExcludeRepos,
				},
			})
		}
	}
}

// parseModuleOwner extracts the owner segment (host/owner/repo → owner) from a
// module path. Returns "" if the path is not host/owner/repo shaped.
func parseModuleOwner(modulePath string) (owner, repo string) {
	parts := strings.Split(modulePath, "/")
	if len(parts) < 3 {
		return "", ""
	}
	return parts[len(parts)-2], parts[len(parts)-1]
}

// Load reads the config file at path. Resolution order:
//  1. Explicit --config path (if non-empty)
//  2. ./depscanner.yaml (cwd)
//  3. $HOME/.depscanner.yaml
//  4. Defaults (no file at all)
// ${ENV_VAR} placeholders are expanded before YAML parsing.
func Load(path string) (*Config, error) {
	if path == "" {
		// try cwd first
		if _, err := os.Stat("depscanner.yaml"); err == nil {
			path = "depscanner.yaml"
		} else {
			home, err := os.UserHomeDir()
			if err != nil {
				return nil, fmt.Errorf("get home dir: %w", err)
			}
			path = filepath.Join(home, ".depscanner.yaml")
		}
	}

	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return defaults(), nil
		}
		return nil, fmt.Errorf("read config %s: %w", path, err)
	}

	expanded := os.ExpandEnv(string(data))

	var cfg Config
	if err := yaml.Unmarshal([]byte(expanded), &cfg); err != nil {
		return nil, fmt.Errorf("parse config: %w", err)
	}

	expandedCache, expandErr := expandHome(cfg.CacheDir)
	if expandErr != nil {
		return nil, expandErr
	}
	cfg.CacheDir = expandedCache
	if cfg.DefaultBranch == "" {
		cfg.DefaultBranch = "main"
	}
	if len(cfg.BranchTracking) == 0 {
		cfg.BranchTracking = map[string]string{
			"dev":     "dev",
			"staging": "staging",
			"main":    "main",
		}
	}
	if len(cfg.UnshallowBranches) == 0 {
		cfg.UnshallowBranches = defaultUnshallowBranches()
	}
	cfg.normalize()
	return &cfg, nil
}

func defaultUnshallowBranches() []string {
	return []string{"main"}
}

func defaults() *Config {
	cacheDir, _ := expandHome("~/.depscanner/repos")
	if cacheDir == "" {
		cacheDir = ".depscanner/repos"
	}
	return &Config{
		CacheDir:      cacheDir,
		DefaultBranch: "main",
		BranchTracking: map[string]string{
			"dev":     "dev",
			"staging": "staging",
			"main":    "main",
		},
		UnshallowBranches: defaultUnshallowBranches(),
	}
}

// expandHome replaces a leading ~ with the actual home directory path.
// Returns the path unchanged if home directory cannot be resolved.
func expandHome(path string) (string, error) {
	if strings.HasPrefix(path, "~/") {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("expand ~: %w", err)
		}
		return filepath.Join(home, path[2:]), nil
	}
	return path, nil
}

// GetBranchForRepo determines the correct branch to sync for a given repository
// based on the global branch flag, branch tracking configuration, and defaults.
func (c *Config) GetBranchForRepo(repoName string, branchFlag string) string {
	if branchFlag == "" {
		return c.DefaultBranch
	}

	// 1. Check if the flag value matches a global tracking alias (e.g. --branch dev)
	if targetBranch, ok := c.BranchTracking[branchFlag]; ok {
		return targetBranch
	}

	// 2. Check if there's a repo-specific tracking rule (e.g. "repo-name:dev": "development")
	repoKey := repoName + ":" + branchFlag
	if targetBranch, ok := c.BranchTracking[repoKey]; ok {
		return targetBranch
	}

	// 3. Fallback to literal branch name
	return branchFlag
}

// ActiveOrgs returns the list of orgs to scan.
// If gitea.orgs is set, it is used directly.
// Otherwise falls back to the legacy gitea.org + top-level include/exclude.
func (c *Config) ActiveOrgs() []OrgConfig {
	if len(c.Gitea.Orgs) > 0 {
		return c.Gitea.Orgs
	}
	if c.Gitea.Org == "" {
		return nil
	}
	return []OrgConfig{{
		Name:         c.Gitea.Org,
		IncludeRepos: c.IncludeRepos,
		ExcludeRepos: c.ExcludeRepos,
	}}
}
