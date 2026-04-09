package main

import (
	"encoding/json"
	"fmt"
	"os"
	"text/tabwriter"

	"github.com/mystaline/depscanner/internal/analysis"
	"github.com/mystaline/depscanner/internal/config"
	"github.com/mystaline/depscanner/internal/gitea"
	"github.com/mystaline/depscanner/internal/repo"
	"github.com/spf13/cobra"
)

var (
	breakingOnly bool
)

func newDiffCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "diff <from> <to>",
		Short: "Detect structural and behavioral changes between two commits of the target module",
		Args:  cobra.ExactArgs(2),
		RunE:  runDiff,
	}
	cmd.Flags().BoolVar(&breakingOnly, "breaking-only", false, "show only breaking changes")
	return cmd
}

func runDiff(_ *cobra.Command, args []string) error {
	from, to := args[0], args[1]

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

	_, targetRepo := gitea.ParseModuleOwnerRepo(cfg.TargetModule)
	mgr := repo.NewManager(cfg.CacheDir, cfg.Gitea.Org)
	repoPath := mgr.GetRepoPath(targetRepo)

	// Ensure the target repo exists locally.
	if _, statErr := os.Stat(repoPath); statErr != nil {
		targetOwner, _ := gitea.ParseModuleOwnerRepo(cfg.TargetModule)
		fmt.Printf("Cloning target module %s...\n", cfg.TargetModule)
		cloneURL := fmt.Sprintf("%s/%s/%s.git", cfg.Gitea.URL, targetOwner, targetRepo)
		repos := []gitea.Repository{{Name: targetRepo, CloneURL: cloneURL}}
		if err := mgr.SyncRepos(repos, false); err != nil {
			return fmt.Errorf("sync target module: %w", err)
		}
	}

	// Unshallow to access full history.
	fmt.Printf("Fetching full history for %s...\n", targetRepo)
	unshallowTargetRepo(repoPath)

	// Phase A
	fmt.Printf("Building symbol index at %s...\n", from)
	if err := mgr.CheckoutCommit(targetRepo, from); err != nil {
		return fmt.Errorf("checkout %s: %w", from, err)
	}
	oldIndex, err := analysis.BuildSymbolIndex(repoPath)
	if err != nil {
		return fmt.Errorf("build index at %s: %w", from, err)
	}

	// Phase B
	fmt.Printf("Building symbol index at %s...\n", to)
	if err := mgr.CheckoutCommit(targetRepo, to); err != nil {
		return fmt.Errorf("checkout %s: %w", to, err)
	}
	newIndex, err := analysis.BuildSymbolIndex(repoPath)
	if err != nil {
		return fmt.Errorf("build index at %s: %w", to, err)
	}

	changes := analysis.DiffSymbols(oldIndex, newIndex)

	if breakingOnly {
		var filtered []analysis.SymbolChange
		for _, c := range changes {
			if c.Breaking {
				filtered = append(filtered, c)
			}
		}
		changes = filtered
	}

	if format == "json" {
		return json.NewEncoder(os.Stdout).Encode(diffOutput{
			From:         from,
			To:           to,
			TargetModule: cfg.TargetModule,
			Changes:      changes,
			Total:        len(changes),
			Breaking:     countBreaking(changes),
			Additive:     len(changes) - countBreaking(changes),
		})
	}

	return printDiffTable(from, to, changes)
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

func printDiffTable(from, to string, changes []analysis.SymbolChange) error {
	if len(changes) == 0 {
		fmt.Printf("\nNo API changes detected between %s and %s.\n", from, to)
		return nil
	}

	fmt.Printf("\nChanges in target module (%s → %s):\n\n", shortenHash(from), shortenHash(to))

	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	for _, c := range changes {
		icon := colorGreen + "+" + colorReset
		label := "additive"
		if c.Breaking {
			icon = colorRed + "✗" + colorReset
			label = "BREAKING"
		} else if c.Kind == analysis.ChangeLogic {
			icon = colorYellow + "~" + colorReset
			label = "LOGIC"
		} else if c.Kind == analysis.ChangeAffected {
			icon = "\033[34m" + "·" + colorReset
			label = "IMPACTED"
		}

		detail := formatChangeDetail(c)
		fmt.Fprintf(w, "  %s  %-20s\t%-30s\t%s\t[%s]\n",
			icon, string(c.Kind), c.Symbol, detail, label)
	}
	w.Flush()

	breaking := countBreaking(changes)
	logic := countKind(changes, analysis.ChangeLogic)
	affected := countKind(changes, analysis.ChangeAffected)
	additive := len(changes) - breaking - logic - affected
	fmt.Printf("\nSummary: %d breaking, %d logic, %d impacted, %d additive changes\n", breaking, logic, affected, additive)
	return nil
}

func countKind(changes []analysis.SymbolChange, kind analysis.ChangeKind) int {
	n := 0
	for _, c := range changes {
		if c.Kind == kind {
			n++
		}
	}
	return n
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

