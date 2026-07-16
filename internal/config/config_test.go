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

func TestGetBranchForRepo(t *testing.T) {
	cfg := &Config{
		DefaultBranch: "main",
		BranchTracking: map[string]string{
			"dev":              "develop",
			"staging":          "staging",
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

func TestActiveOrgs_MultiOrg(t *testing.T) {
	cfg := &Config{
		Gitea: GiteaConfig{
			URL:   "https://gitea.example.com",
			Token: "tok",
			Orgs: []OrgConfig{
				{Name: "org-a", IncludeRepos: []string{"svc-1"}},
				{Name: "org-b", ExcludeRepos: []string{"old"}},
			},
		},
	}
	orgs := cfg.ActiveOrgs()
	if len(orgs) != 2 {
		t.Fatalf("ActiveOrgs() len = %d, want 2", len(orgs))
	}
	if orgs[0].Name != "org-a" {
		t.Errorf("orgs[0].Name = %q, want \"org-a\"", orgs[0].Name)
	}
	if len(orgs[0].IncludeRepos) != 1 || orgs[0].IncludeRepos[0] != "svc-1" {
		t.Errorf("orgs[0].IncludeRepos = %v, want [svc-1]", orgs[0].IncludeRepos)
	}
	if orgs[1].Name != "org-b" {
		t.Errorf("orgs[1].Name = %q, want \"org-b\"", orgs[1].Name)
	}
	if len(orgs[1].ExcludeRepos) != 1 || orgs[1].ExcludeRepos[0] != "old" {
		t.Errorf("orgs[1].ExcludeRepos = %v, want [old]", orgs[1].ExcludeRepos)
	}
}

func TestActiveOrgs_SingleOrgFallback(t *testing.T) {
	cfg := &Config{
		Gitea:        GiteaConfig{Org: "legacy-org"},
		IncludeRepos: []string{"svc-a"},
		ExcludeRepos: []string{"old"},
	}
	orgs := cfg.ActiveOrgs()
	if len(orgs) != 1 {
		t.Fatalf("ActiveOrgs() len = %d, want 1", len(orgs))
	}
	if orgs[0].Name != "legacy-org" {
		t.Errorf("orgs[0].Name = %q, want \"legacy-org\"", orgs[0].Name)
	}
	if len(orgs[0].IncludeRepos) != 1 || orgs[0].IncludeRepos[0] != "svc-a" {
		t.Errorf("orgs[0].IncludeRepos = %v, want [svc-a]", orgs[0].IncludeRepos)
	}
	if len(orgs[0].ExcludeRepos) != 1 || orgs[0].ExcludeRepos[0] != "old" {
		t.Errorf("orgs[0].ExcludeRepos = %v, want [old]", orgs[0].ExcludeRepos)
	}
}

func TestActiveOrgs_EmptyReturnsNil(t *testing.T) {
	cfg := &Config{}
	orgs := cfg.ActiveOrgs()
	if orgs != nil {
		t.Errorf("ActiveOrgs() with no org set = %v, want nil", orgs)
	}
}

func TestLoadMultiOrgConfig(t *testing.T) {
	configContent := `gitea:
  url: "https://gitea.example.com"
  token: "tok"
  orgs:
    - name: org-a
      include_repos:
        - svc-1
    - name: org-b
      exclude_repos:
        - old

target_module: "gitea.example.com/lib/utils"
`
	tmpdir := t.TempDir()
	configPath := filepath.Join(tmpdir, "depscanner.yaml")
	if err := os.WriteFile(configPath, []byte(configContent), 0644); err != nil {
		t.Fatalf("write config: %v", err)
	}
	cfg, err := Load(configPath)
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}
	if err := cfg.Validate(); err != nil {
		t.Errorf("Validate() failed on loaded multi-org config: %v", err)
	}
	if len(cfg.Gitea.Orgs) != 2 {
		t.Fatalf("Gitea.Orgs len = %d, want 2", len(cfg.Gitea.Orgs))
	}
	if cfg.Gitea.Orgs[0].Name != "org-a" {
		t.Errorf("Orgs[0].Name = %q, want \"org-a\"", cfg.Gitea.Orgs[0].Name)
	}
	if len(cfg.Gitea.Orgs[0].IncludeRepos) != 1 || cfg.Gitea.Orgs[0].IncludeRepos[0] != "svc-1" {
		t.Errorf("Orgs[0].IncludeRepos = %v, want [svc-1]", cfg.Gitea.Orgs[0].IncludeRepos)
	}
	if cfg.Gitea.Orgs[1].Name != "org-b" {
		t.Errorf("Orgs[1].Name = %q, want \"org-b\"", cfg.Gitea.Orgs[1].Name)
	}
	if len(cfg.Gitea.Orgs[1].ExcludeRepos) != 1 || cfg.Gitea.Orgs[1].ExcludeRepos[0] != "old" {
		t.Errorf("Orgs[1].ExcludeRepos = %v, want [old]", cfg.Gitea.Orgs[1].ExcludeRepos)
	}
}

func TestActiveOrgs_OrgsPreferenceOverOrg(t *testing.T) {
	cfg := &Config{
		Gitea: GiteaConfig{
			Org:  "legacy-org",
			Orgs: []OrgConfig{{Name: "priority-org"}},
		},
		IncludeRepos: []string{"svc-a"},
	}
	orgs := cfg.ActiveOrgs()
	if len(orgs) != 1 {
		t.Fatalf("ActiveOrgs() len = %d, want 1", len(orgs))
	}
	if orgs[0].Name != "priority-org" {
		t.Errorf("orgs[0].Name = %q, want \"priority-org\"", orgs[0].Name)
	}
}
