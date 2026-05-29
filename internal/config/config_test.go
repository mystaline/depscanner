package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadDefault(t *testing.T) {
	// Load with empty path should use ~/.depscanner.yaml (which likely doesn't exist in test)
	// This tests the fallback behavior
	cfg, err := Load("")
	if err != nil {
		t.Fatalf("Load(\"\") failed: %v", err)
	}
	if cfg == nil {
		t.Error("Load returned nil config")
	}
	// Should have defaults
	if cfg.DefaultBranch != "main" {
		t.Errorf("DefaultBranch = %q, want \"main\"", cfg.DefaultBranch)
	}
	if len(cfg.BranchTracking) == 0 {
		t.Error("BranchTracking should have defaults")
	}
}

func TestLoadValidConfig(t *testing.T) {
	configContent := `gitea:
  url: "https://gitea.example.com"
  token: "test-token-123"
  org: "my-org"

target_module: "github.com/example/lib"
cache_dir: "~/.depscanner/repos"

include_repos:
  - service-*

exclude_repos:
  - test-*

branch_tracking:
  dev: develop
  main: main
`

	tmpdir := t.TempDir()
	configPath := filepath.Join(tmpdir, "depscanner.yaml")
	err := os.WriteFile(configPath, []byte(configContent), 0644)
	if err != nil {
		t.Fatalf("failed to create temp config: %v", err)
	}

	cfg, err := Load(configPath)
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}

	if cfg.Gitea.URL != "https://gitea.example.com" {
		t.Errorf("Gitea.URL = %q, want \"https://gitea.example.com\"", cfg.Gitea.URL)
	}
	if cfg.Gitea.Token != "test-token-123" {
		t.Errorf("Gitea.Token = %q, want \"test-token-123\"", cfg.Gitea.Token)
	}
	if cfg.Gitea.Org != "my-org" {
		t.Errorf("Gitea.Org = %q, want \"my-org\"", cfg.Gitea.Org)
	}
	if cfg.TargetModule != "github.com/example/lib" {
		t.Errorf("TargetModule = %q, want \"github.com/example/lib\"", cfg.TargetModule)
	}
	if len(cfg.IncludeRepos) != 1 || cfg.IncludeRepos[0] != "service-*" {
		t.Errorf("IncludeRepos = %v, want [\"service-*\"]", cfg.IncludeRepos)
	}
	if len(cfg.ExcludeRepos) != 1 || cfg.ExcludeRepos[0] != "test-*" {
		t.Errorf("ExcludeRepos = %v, want [\"test-*\"]", cfg.ExcludeRepos)
	}
	if cfg.BranchTracking["dev"] != "develop" {
		t.Errorf("BranchTracking[dev] = %q, want \"develop\"", cfg.BranchTracking["dev"])
	}
}

func TestLoadWithEnvVarExpansion(t *testing.T) {
	configContent := `gitea:
  url: "https://gitea.example.com"
  token: "${GITEA_TOKEN}"
  org: "my-org"

target_module: "github.com/example/lib"
cache_dir: "~/.depscanner/repos"
`

	os.Setenv("GITEA_TOKEN", "secret-token-from-env")
	defer os.Unsetenv("GITEA_TOKEN")

	tmpdir := t.TempDir()
	configPath := filepath.Join(tmpdir, "depscanner.yaml")
	err := os.WriteFile(configPath, []byte(configContent), 0644)
	if err != nil {
		t.Fatalf("failed to create temp config: %v", err)
	}

	cfg, err := Load(configPath)
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}

	if cfg.Gitea.Token != "secret-token-from-env" {
		t.Errorf("Gitea.Token = %q, want \"secret-token-from-env\" (env var not expanded)", cfg.Gitea.Token)
	}
}

func TestValidate(t *testing.T) {
	tests := []struct {
		name      string
		config    Config
		expectErr bool
		errMsg    string
	}{
		{
			name: "valid config",
			config: Config{
				Gitea: GiteaConfig{
					URL:   "https://gitea.example.com",
					Token: "token",
					Org:   "org",
				},
				TargetModule: "github.com/example/lib",
			},
			expectErr: false,
		},
		{
			name: "missing gitea.url",
			config: Config{
				Gitea: GiteaConfig{
					Token: "token",
					Org:   "org",
				},
				TargetModule: "github.com/example/lib",
			},
			expectErr: true,
			errMsg:    "gitea.url is required when not in offline mode",
		},
		{
			name: "missing gitea.token",
			config: Config{
				Gitea: GiteaConfig{
					URL: "https://gitea.example.com",
					Org: "org",
				},
				TargetModule: "github.com/example/lib",
			},
			expectErr: true,
			errMsg:    "gitea.token is required when not in offline mode",
		},
		{
			name: "missing gitea.org",
			config: Config{
				Gitea: GiteaConfig{
					URL:   "https://gitea.example.com",
					Token: "token",
				},
				TargetModule: "github.com/example/lib",
			},
			expectErr: true,
			errMsg:    "gitea.org or gitea.orgs is required when not in offline mode",
		},
		{
			name: "missing target_module",
			config: Config{
				Gitea: GiteaConfig{
					URL:   "https://gitea.example.com",
					Token: "token",
					Org:   "org",
				},
			},
			expectErr: true,
			errMsg:    "target_module is required",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.config.Validate()
			if tt.expectErr && err == nil {
				t.Errorf("Validate() expected error, got nil")
			}
			if !tt.expectErr && err != nil {
				t.Errorf("Validate() failed: %v", err)
			}
			if tt.expectErr && err != nil && err.Error() != tt.errMsg {
				t.Errorf("Validate() error = %q, want %q", err.Error(), tt.errMsg)
			}
		})
	}
}

func TestGetBranchForRepo(t *testing.T) {
	cfg := &Config{
		DefaultBranch: "main",
		BranchTracking: map[string]string{
			"dev":        "develop",
			"staging":    "staging",
			"service-a:custom": "service-a-custom",
		},
	}

	tests := []struct {
		repoName   string
		branchFlag string
		want       string
		desc       string
	}{
		{
			repoName:   "any-repo",
			branchFlag: "",
			want:       "main",
			desc:       "empty flag returns default",
		},
		{
			repoName:   "any-repo",
			branchFlag: "dev",
			want:       "develop",
			desc:       "alias from global tracking",
		},
		{
			repoName:   "service-a",
			branchFlag: "custom",
			want:       "service-a-custom",
			desc:       "repo-specific tracking for unknown global alias",
		},
		{
			repoName:   "any-repo",
			branchFlag: "feature/xyz",
			want:       "feature/xyz",
			desc:       "unknown branch uses literal name",
		},
		{
			repoName:   "service-a",
			branchFlag: "staging",
			want:       "staging",
			desc:       "global alias when repo-specific not defined",
		},
	}

	for _, tt := range tests {
		t.Run(tt.desc, func(t *testing.T) {
			got := cfg.GetBranchForRepo(tt.repoName, tt.branchFlag)
			if got != tt.want {
				t.Errorf("GetBranchForRepo(%q, %q) = %q, want %q", tt.repoName, tt.branchFlag, got, tt.want)
			}
		})
	}
}
