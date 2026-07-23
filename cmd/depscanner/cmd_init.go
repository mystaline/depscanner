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

# Track N shared libraries against a consumer pool.
sources:
  - name: my-lib
    gitea: { url: "https://gitea.example.com", token: "${YOUR_GITEA_TOKEN}", org: "my-org" }
    module: "gitea.example.com/my-org/my-lib"

  # More sources (or just one). Each tracks independent libs.
  # - name: lib-two
  #   gitea: { url: ..., token: ${GITEA_TOKEN}, org: another-org }

# Services to scan for usages of any source.
consumers:
  - gitea: { url: "https://gitea.example.com", token: "${YOUR_GITEA_TOKEN}", org: "my-org" }

  # Consumers can span orgs:
  # - gitea: { url: ..., token: ${GITEA_TOKEN}, org: partner-org, exclude_repos: [docs] }
  # - path: ~/Workspace/my-service          # local checkout

# Local directory where repos are cloned/cached under <org>/<repo>.
cache_dir: "~/.depscanner/repos"

# Branch tracking: consumer branch → source branch.
# Used with --branch for staleness checks.
branch_tracking:
  dev: dev
  staging: staging
  main: main

# Only scan repos matching these glob patterns (empty = all).
# include_repos:
#   - service-a
#   - service-b

# Skip repos that are definitely not relevant Go services.
# Supports glob patterns: *, ?, [...]
exclude_repos:
  - docs

# --- Legacy single-source mode ---
# Replace sources/consumers above with these two if you prefer:
# gitea:
#   url: "https://gitea.example.com"
#   token: "${YOUR_GITEA_TOKEN}"
#   org: "my-org"
# target_module: "gitea.example.com/my-org/my-lib"
`

func newInitCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "init",
		Short: "Write scaffold config to ./depscanner.yaml",
		Long: `Creates depscanner.yaml in the current directory with common defaults.
Edit the file with your Gitea URL, token, and org before running scan.`,
		Args: cobra.NoArgs,
		RunE: func(_ *cobra.Command, _ []string) error {
			if _, err := os.Stat("depscanner.yaml"); err == nil {
				return fmt.Errorf("depscanner.yaml already exists in current directory")
			}
			if err := os.WriteFile("depscanner.yaml", []byte(scaffoldConfig), 0644); err != nil {
				return fmt.Errorf("write config: %w", err)
			}
			fmt.Println("Wrote depscanner.yaml — edit it with your Gitea URL, token, and org.")
			return nil
		},
	}
}
