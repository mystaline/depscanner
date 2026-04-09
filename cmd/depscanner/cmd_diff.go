package main

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"text/tabwriter"

	"github.com/mystaline/depscanner/internal/analysis"
	"github.com/mystaline/depscanner/internal/config"
	"github.com/mystaline/depscanner/internal/gitea"
	"github.com/mystaline/depscanner/internal/repo"
	"github.com/spf13/cobra"
)

var (
	diffFrom     string
	diffTo       string
	breakingOnly bool
)

func newDiffCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "diff",
		Short: "Compare target module API between two commits/branches and detect breaking changes",
		Long: `Builds a symbol index of the target module at two different refs (commits, branches, or tags)
and reports all changes: added, removed, or modified symbols.

Symbols tracked: functions, methods, structs, interfaces, constants, variables, and type aliases.`,
		RunE: runDiff,
	}
	cmd.Flags().StringVar(&diffFrom, "from", "", "starting ref (commit hash, branch, or tag)")
	cmd.Flags().StringVar(&diffTo, "to", "", "ending ref (commit hash, branch, or tag)")
	cmd.Flags().BoolVar(&breakingOnly, "breaking-only", false, "show only breaking changes")
	_ = cmd.MarkFlagRequired("from")
	_ = cmd.MarkFlagRequired("to")
	return cmd
}

func runDiff(_ *cobra.Command, _ []string) error {
	cfg, err := config.Load(cfgPath)
	if err != nil {
		return err
	}
	if cacheDir != "" {
		cfg.CacheDir = cacheDir
	}
	if err := cfg.Validate(); err != nil {
		return fmt.Errorf("config: %w", err)
	}

	targetOwner, targetRepo := gitea.ParseModuleOwnerRepo(cfg.TargetModule)
	if targetOwner == "" {
		return fmt.Errorf("target_module %q does not look like a full module path (expected host/owner/repo)", cfg.TargetModule)
	}

	mgr := repo.NewManager(cfg.CacheDir, cfg.Gitea.Org)
	repoPath := mgr.GetRepoPath(targetRepo)

	// Ensure the target repo exists locally.
	if _, statErr := os.Stat(repoPath); statErr != nil {
		fmt.Printf("Cloning target module %s...\n", cfg.TargetModule)
		cloneURL := fmt.Sprintf("%s/%s/%s.git", cfg.Gitea.URL, targetOwner, targetRepo)
		repos := []gitea.Repository{{Name: targetRepo, CloneURL: cloneURL}}
		if err := mgr.SyncRepos(repos, false); err != nil {
			return fmt.Errorf("sync target module: %w", err)
		}
	}

	// Unshallow the repo to access full history for commit checkout.
	fmt.Printf("Fetching full history for %s...\n", targetRepo)
	if err := unshallowRepo(repoPath); err != nil {
		fmt.Fprintf(os.Stderr, "  warn: unshallow failed (may work with branch refs): %v\n", err)
	}

	// Phase A: checkout --from ref, build symbol index.
	fmt.Printf("Building symbol index at %s...\n", diffFrom)
	if err := mgr.CheckoutCommit(targetRepo, diffFrom); err != nil {
		return fmt.Errorf("checkout --from %s: %w", diffFrom, err)
	}
	oldIndex, err := analysis.BuildSymbolIndex(repoPath)
	if err != nil {
		return fmt.Errorf("build index at %s: %w", diffFrom, err)
	}

	// Phase B: checkout --to ref, build symbol index.
	fmt.Printf("Building symbol index at %s...\n", diffTo)
	if err := mgr.CheckoutCommit(targetRepo, diffTo); err != nil {
		return fmt.Errorf("checkout --to %s: %w", diffTo, err)
	}
	newIndex, err := analysis.BuildSymbolIndex(repoPath)
	if err != nil {
		return fmt.Errorf("build index at %s: %w", diffTo, err)
	}

	// Diff.
	changes := analysis.DiffSymbols(oldIndex, newIndex)

	// Filter if --breaking-only.
	if breakingOnly {
		var filtered []analysis.SymbolChange
		for _, c := range changes {
			if c.Breaking {
				filtered = append(filtered, c)
			}
		}
		changes = filtered
	}

	// Output.
	if format == "json" {
		return json.NewEncoder(os.Stdout).Encode(diffOutput{
			From:         diffFrom,
			To:           diffTo,
			TargetModule: cfg.TargetModule,
			Changes:      changes,
			Total:        len(changes),
			Breaking:     countBreaking(changes),
			Additive:     len(changes) - countBreaking(changes),
		})
	}

	return printDiffTable(changes)
}

type diffOutput struct {
	From         string                  `json:"from"`
	To           string                  `json:"to"`
	TargetModule string                  `json:"target_module"`
	Changes      []analysis.SymbolChange `json:"changes"`
	Total        int                     `json:"total"`
	Breaking     int                     `json:"breaking"`
	Additive     int                     `json:"additive"`
}

func printDiffTable(changes []analysis.SymbolChange) error {
	if len(changes) == 0 {
		fmt.Printf("\nNo API changes detected between %s and %s.\n", diffFrom, diffTo)
		return nil
	}

	fmt.Printf("\nChanges in target module (%s → %s):\n\n", shortenHash(diffFrom), shortenHash(diffTo))

	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	for _, c := range changes {
		icon := colorGreen + "+" + colorReset
		label := "additive"
		if c.Breaking {
			icon = colorRed + "✗" + colorReset
			label = "BREAKING"
		}

		detail := formatChangeDetail(c)
		fmt.Fprintf(w, "  %s  %-20s\t%-30s\t%s\t[%s]\n",
			icon,
			string(c.Kind),
			c.Symbol,
			detail,
			label,
		)
	}
	w.Flush()

	breaking := countBreaking(changes)
	additive := len(changes) - breaking
	fmt.Printf("\nSummary: %d breaking, %d additive changes\n", breaking, additive)
	return nil
}

func formatChangeDetail(c analysis.SymbolChange) string {
	switch {
	case c.OldValue != "" && c.NewValue != "":
		return c.OldValue + " → " + c.NewValue
	case c.NewValue != "":
		return c.NewValue
	case c.OldValue != "":
		return c.OldValue
	default:
		return ""
	}
}

func countBreaking(changes []analysis.SymbolChange) int {
	n := 0
	for _, c := range changes {
		if c.Breaking {
			n++
		}
	}
	return n
}

func unshallowRepo(repoPath string) error {
	cmd := exec.Command("git", "-C", repoPath, "fetch", "--unshallow", "--quiet")
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("%s", firstLine(out))
	}
	return nil
}

func firstLine(out []byte) string {
	s := string(out)
	if idx := len(s); idx > 0 {
		for i, c := range s {
			if c == '\n' {
				return s[:i]
			}
		}
	}
	return s
}
