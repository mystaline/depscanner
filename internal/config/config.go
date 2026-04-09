// Package config loads depscanner configuration from a YAML file,
// with environment variable expansion for sensitive values like tokens.
package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

// GiteaConfig holds connection details for the Gitea instance.
type GiteaConfig struct {
	URL   string `yaml:"url"`
	Token string `yaml:"token"`
	Org   string `yaml:"org"`
}

// Config holds all runtime configuration for depscanner.
type Config struct {
	Gitea           GiteaConfig       `yaml:"gitea"`
	TargetModule    string            `yaml:"target_module"`
	CacheDir        string            `yaml:"cache_dir"`
	IncludeRepos    []string          `yaml:"include_repos"`
	ExcludeRepos    []string          `yaml:"exclude_repos"`
	DefaultBranch   string            `yaml:"default_branch"`
	BranchTracking  map[string]string `yaml:"branch_tracking"`
}

// Validate checks that all required fields are present.
func (c *Config) Validate() error {
	if c.Gitea.URL == "" {
		return fmt.Errorf("gitea.url is required")
	}
	if c.Gitea.Token == "" {
		return fmt.Errorf("gitea.token is required")
	}
	if c.Gitea.Org == "" {
		return fmt.Errorf("gitea.org is required")
	}
	if c.TargetModule == "" {
		return fmt.Errorf("target_module is required")
	}
	return nil
}

// Load reads the config file at path (default: ~/.depscanner.yaml if empty).
// ${ENV_VAR} placeholders are expanded before YAML parsing.
func Load(path string) (*Config, error) {
	if path == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return nil, fmt.Errorf("get home dir: %w", err)
		}
		path = filepath.Join(home, ".depscanner.yaml")
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
	return &cfg, nil
}

func defaults() *Config {
	cacheDir, _ := expandHome("~/.depscanner/repos")
	if cacheDir == "" {
		cacheDir = ".depscanner/repos" // fallback to relative path
	}
	return &Config{
		CacheDir:      cacheDir,
		DefaultBranch: "main",
		BranchTracking: map[string]string{
			"dev":     "dev",
			"staging": "staging",
			"main":    "main",
		},
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
