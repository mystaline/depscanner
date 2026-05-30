package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/mystaline/depscanner/internal/config"
	"github.com/spf13/cobra"
)

const defaultUnshallowTimeout = 30 * time.Minute

func newUnshallowCmd() *cobra.Command {
	var timeoutMin int
	cmd := &cobra.Command{
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

			timeout := time.Duration(timeoutMin) * time.Minute
			if len(args) == 1 {
				return unshallowFind(cfg.CacheDir, args[0], timeout, cfg.UnshallowBranches)
			}
			return unshallowAllOrgs(cfg.CacheDir, timeout, cfg.UnshallowBranches)
		},
	}
	cmd.Flags().IntVar(&timeoutMin, "timeout", 30, "per-repo timeout in minutes")
	return cmd
}

// unshallowFind searches all org subdirs under cacheDir for a repo by name.
func unshallowFind(cacheDir, name string, timeout time.Duration, branches []string) error {
	orgs, err := os.ReadDir(cacheDir)
	if err != nil {
		return fmt.Errorf("read cache dir: %w", err)
	}
	for _, org := range orgs {
		if !org.IsDir() || strings.HasPrefix(org.Name(), ".") {
			continue
		}
		repoPath := filepath.Join(cacheDir, org.Name(), name)
		if _, err := os.Stat(filepath.Join(repoPath, ".git")); err == nil {
			fmt.Printf("unshallowing %s/%s... ", org.Name(), name)
			if err := unshallowTargetRepo(repoPath, timeout, branches); err != nil {
				fmt.Printf("error: %v\n", err)
				return err
			}
			fmt.Println("done")
			return nil
		}
	}
	return fmt.Errorf("repo %q not found in cache under %s", name, cacheDir)
}

// unshallowAllOrgs unshallows every repo across all org subdirs under cacheDir.
func unshallowAllOrgs(cacheDir string, timeout time.Duration, branches []string) error {
	orgs, err := os.ReadDir(cacheDir)
	if err != nil {
		return fmt.Errorf("read cache dir: %w", err)
	}

	found := false
	for _, org := range orgs {
		if !org.IsDir() || strings.HasPrefix(org.Name(), ".") {
			continue
		}
		orgPath := filepath.Join(cacheDir, org.Name())
		repos, err := os.ReadDir(orgPath)
		if err != nil {
			continue
		}
		for _, r := range repos {
			if !r.IsDir() || strings.HasPrefix(r.Name(), ".") {
				continue
			}
			repoPath := filepath.Join(orgPath, r.Name())
			if _, err := os.Stat(filepath.Join(repoPath, ".git")); err != nil {
				continue
			}
			found = true
			fmt.Printf("unshallowing %-50s ", org.Name()+"/"+r.Name())
			if err := unshallowTargetRepo(repoPath, timeout, branches); err != nil {
				fmt.Printf("error: %v\n", err)
				continue
			}
			fmt.Println("done")
		}
	}

	if !found {
		fmt.Println("no repos in cache")
	}
	return nil
}
