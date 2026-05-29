package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/mystaline/depscanner/internal/config"
	"github.com/mystaline/depscanner/internal/repo"
	"github.com/spf13/cobra"
)

func newUnshallowCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "unshallow [repo-name]",
		Short: "Fetch full git history for cached repos",
		Long: `Converts shallow-cloned repos in the local cache to full clones.

Without arguments, unshallows all repos in the org cache.
With a repo name, unshallows only that repo.

Use --no-fetch on subsequent commands to preserve full history.
Next sync without --no-fetch will re-shallow automatically.`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := config.Load(cfgPath)
			if err != nil {
				return fmt.Errorf("load config: %w", err)
			}
			if cacheDir != "" {
				cfg.CacheDir = cacheDir
			}

			mgr := repo.NewManager(cfg.CacheDir, cfg.Gitea.Org)
			orgPath := mgr.GetOrgPath()

			if len(args) == 1 {
				return unshallowOne(orgPath, args[0])
			}
			return unshallowAll(orgPath)
		},
	}
}

func unshallowOne(orgPath, name string) error {
	repoPath := filepath.Join(orgPath, name)
	if _, err := os.Stat(filepath.Join(repoPath, ".git")); os.IsNotExist(err) {
		return fmt.Errorf("repo %q not found in cache", name)
	}
	fmt.Printf("unshallowing %s... ", name)
	unshallowTargetRepo(repoPath)
	fmt.Println("done")
	return nil
}

func unshallowAll(orgPath string) error {
	entries, err := os.ReadDir(orgPath)
	if err != nil {
		return fmt.Errorf("read cache dir: %w", err)
	}

	var repos []string
	for _, e := range entries {
		if e.IsDir() && !strings.HasPrefix(e.Name(), ".") {
			repos = append(repos, e.Name())
		}
	}

	if len(repos) == 0 {
		fmt.Println("no repos in cache")
		return nil
	}

	for _, name := range repos {
		repoPath := filepath.Join(orgPath, name)
		fmt.Printf("unshallowing %-40s ", name)
		unshallowTargetRepo(repoPath)
		fmt.Println("done")
	}
	return nil
}
