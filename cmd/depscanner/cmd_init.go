package main

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
)

const scaffoldConfig = `# depscanner configuration
#
# Environment variables are expanded inline — use ${VAR_NAME} syntax.
# Example: token: "${GITEA_TOKEN}"
#
# ── HOW TO SET THIS UP ──────────────────────────────────────────
# Before running depscanner, fill in every field with your real values.
# Each section below explains what to put.
#
# 1. Get a Gitea API token: Settings → Applications → Manage Access Tokens
# 2. Export as env var: export GITEA_TOKEN="your_token_here"
# 3. Find your org name (the Gitea organization that owns the repos)
# 4. Find your source repo name (the shared library you want to track)
#
# Then run: depscanner scan
# ────────────────────────────────────────────────────────────────

# ── SOURCES ─────────────────────────────────────────────────────
# The shared library ("source of truth") you want to track.
# Depscanner will diff this repo's API and scan consumers for usage.
sources:
  # url:    Your Gitea instance URL (e.g. "https://gitea.example.com")
  # token:  Gitea API token — use ${GITEA_TOKEN} env var, don't paste raw
  # org:    Gitea organization that owns this repo
  # repo:   Exact repo name inside that org (not URL, just name like "my-lib")
  - gitea: { url: "https://gitea.example.com", token: "${GITEA_TOKEN}", org: "my-org", repo: "my-lib" }

    # module: Optional. Full Go module path (e.g. "gitea.example.com/my-org/my-lib").
    #         Depscanner auto-reads this from the repo's go.mod when omitted.
    #         Set it only if the go.mod path differs from the default.
    # module: "gitea.example.com/my-org/my-lib"

  # Add more sources if you track multiple independent libraries.
  # Each source = one shared library in one repo.
  # - gitea: { url: ..., token: ${GITEA_TOKEN}, org: another-org, repo: lib-two }

# ── CONSUMERS ───────────────────────────────────────────────────
# The services/applications that might import your source library.
# Depscanner scans every repo in these orgs for dependency usage.
consumers:
  # Same fields as source (url, token, org).
  # exclude_repos: Optional. Repos to skip (docs, config repos, etc.).
  #                Supports glob patterns: *, ?, [...]
  - gitea: { url: "https://gitea.example.com", token: "${GITEA_TOKEN}", org: "my-org", exclude_repos: [docs] }

  # Consumers can span different orgs or be local paths:
  # - gitea: { url: ..., token: ${GITEA_TOKEN}, org: partner-org, exclude_repos: [docs] }
  # - path: ~/Workspace/my-service          # scan a local checkout instead

# ── CACHE DIRECTORY ────────────────────────────────────────────
# Where depscanner clones repos for scanning.
# Default: ~/.depscanner/repos (throwaway cache, you never touch these).
# If your workspace already has <org>/<repo> layout, point this at your
# workspace root to avoid duplicating clones on disk.
cache_dir: "~/.depscanner/repos"

# ── BRANCH TRACKING ────────────────────────────────────────────
# Maps consumer branches → source branches for staleness checks.
# Used when you pass --branch <key> to scan.
# Example: --branch dev checks consumers' "dev" branch against source's "dev" branch.
# Format: consumer_branch_name: source_branch_name
branch_tracking:
  dev: dev
  staging: staging
  main: main

`

func newInitCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "init",
		Short: "Write scaffold config to ./.depscanner.yaml",
		Long: `Creates .depscanner.yaml in the current directory with common defaults.
Edit the file with your Gitea URL, token, and org before running scan.`,
		Args: cobra.NoArgs,
		RunE: func(_ *cobra.Command, _ []string) error {
			if _, err := os.Stat(".depscanner.yaml"); err == nil {
				return fmt.Errorf(".depscanner.yaml already exists in current directory")
			}
			if err := os.WriteFile(".depscanner.yaml", []byte(scaffoldConfig), 0644); err != nil {
				return fmt.Errorf("write config: %w", err)
			}
			fmt.Println("Wrote .depscanner.yaml — edit it with your Gitea URL, token, and org.")
			return nil
		},
	}
}
