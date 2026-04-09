package main

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"text/tabwriter"

	"github.com/mystaline/depscanner/internal/analysis"
	"github.com/mystaline/depscanner/internal/config"
	"github.com/mystaline/depscanner/internal/gitea"
	"github.com/mystaline/depscanner/internal/repo"
	"github.com/spf13/cobra"
)

var (
	impactFrom string
	impactTo   string
)

func newImpactCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "impact",
		Short: "Analyze per-repo impact of breaking changes between two versions of the target module",
		Long: `Combines the diff engine (Phase 5) with call-site scanning (Phase 4) to produce
a per-repo upgrade checklist showing which repos are affected by breaking changes
and exactly where the affected calls are located.`,
		RunE: runImpact,
	}
	cmd.Flags().StringVar(&impactFrom, "from", "", "starting ref (commit hash, branch, or tag)")
	cmd.Flags().StringVar(&impactTo, "to", "", "ending ref (commit hash, branch, or tag)")
	_ = cmd.MarkFlagRequired("from")
	_ = cmd.MarkFlagRequired("to")
	return cmd
}

func runImpact(_ *cobra.Command, _ []string) error {
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
		return fmt.Errorf("target_module %q does not look like a full module path", cfg.TargetModule)
	}

	giteaClient := gitea.NewClient(cfg.Gitea.URL, cfg.Gitea.Token)
	mgr := repo.NewManager(cfg.CacheDir, cfg.Gitea.Org)

	// ── Step 1: Diff the target module ──────────────────────────────────

	fmt.Println("Phase 1: Diffing target module...")

	targetRepoPath := mgr.GetRepoPath(targetRepo)

	// Ensure the target repo exists locally.
	if _, statErr := os.Stat(targetRepoPath); statErr != nil {
		fmt.Printf("  Cloning target module %s...\n", cfg.TargetModule)
		cloneURL := fmt.Sprintf("%s/%s/%s.git", cfg.Gitea.URL, targetOwner, targetRepo)
		repos := []gitea.Repository{{Name: targetRepo, CloneURL: cloneURL}}
		if err := mgr.SyncRepos(repos, false); err != nil {
			return fmt.Errorf("sync target module: %w", err)
		}
	}

	// Unshallow for commit access.
	impactUnshallow(targetRepoPath)

	// Build symbol index at --from.
	fmt.Printf("  Building symbol index at %s...\n", impactFrom)
	if err := mgr.CheckoutCommit(targetRepo, impactFrom); err != nil {
		return fmt.Errorf("checkout --from %s: %w", impactFrom, err)
	}
	oldIndex, err := analysis.BuildSymbolIndex(targetRepoPath)
	if err != nil {
		return fmt.Errorf("build index at %s: %w", impactFrom, err)
	}

	// Build symbol index at --to.
	fmt.Printf("  Building symbol index at %s...\n", impactTo)
	if err := mgr.CheckoutCommit(targetRepo, impactTo); err != nil {
		return fmt.Errorf("checkout --to %s: %w", impactTo, err)
	}
	newIndex, err := analysis.BuildSymbolIndex(targetRepoPath)
	if err != nil {
		return fmt.Errorf("build index at %s: %w", impactTo, err)
	}

	changes := analysis.DiffSymbols(oldIndex, newIndex)

	// Filter to breaking only for impact analysis.
	var breakingChanges []analysis.SymbolChange
	for _, c := range changes {
		if c.Breaking {
			breakingChanges = append(breakingChanges, c)
		}
	}

	if len(breakingChanges) == 0 {
		fmt.Printf("\nNo breaking changes detected between %s and %s. All repos are safe.\n", impactFrom, impactTo)
		return nil
	}

	fmt.Printf("  Found %d breaking changes\n\n", len(breakingChanges))

	// ── Step 2: Scan consumer repos for call sites ──────────────────────

	fmt.Println("Phase 2: Scanning consumer repos for affected call sites...")

	repos, err := giteaClient.ListOrgRepos(cfg.Gitea.Org)
	if err != nil {
		return fmt.Errorf("list repos: %w", err)
	}
	repos = filterRepos(repos, cfg.IncludeRepos, cfg.ExcludeRepos)

	// Sync repos if needed.
	if !noFetch {
		fmt.Printf("  Syncing %d repositories...\n", len(repos))
		for _, r := range repos {
			branchToSync := "main"
			if branch != "" {
				branchToSync = branch
			}
			mgr.SyncBranch(r.Name, r.CloneURL, branchToSync)
		}
		fmt.Println()
	}

	// For each breaking change, extract the function names we need to scan for.
	// Then scan all repos for call sites of those functions.
	funcNames := extractFuncNames(breakingChanges)

	repoCallSites := make(map[string][]analysis.CallSite)
	scannedRepos := 0

	for _, r := range repos {
		repoPath := mgr.GetRepoPath(r.Name)
		goModPath := filepath.Join(repoPath, "go.mod")
		if _, statErr := os.Stat(goModPath); statErr != nil {
			continue // no go.mod
		}

		info, parseErr := analysis.ParseGoMod(goModPath, cfg.TargetModule)
		if parseErr != nil || !info.Found {
			continue // doesn't use target module
		}

		scannedRepos++
		var allSites []analysis.CallSite

		for _, fn := range funcNames {
			sites, _, scanErr := analysis.ScanCallSites(repoPath, cfg.TargetModule, fn)
			if scanErr != nil {
				fmt.Fprintf(os.Stderr, "  warn: scan %s for %s: %v\n", r.Name, fn, scanErr)
				continue
			}
			allSites = append(allSites, sites...)
		}

		if len(allSites) > 0 {
			repoCallSites[r.Name] = allSites
		}
	}

	fmt.Printf("  Scanned %d repos that use target module\n\n", scannedRepos)

	// ── Step 3: Cross-reference and produce impact report ───────────────

	fmt.Println("Phase 3: Analyzing impact...")

	impacts := analysis.AnalyzeImpact(changes, repoCallSites)

	// Output.
	if format == "json" {
		return json.NewEncoder(os.Stdout).Encode(impactOutput{
			From:         impactFrom,
			To:           impactTo,
			TargetModule: cfg.TargetModule,
			Changes:      breakingChanges,
			Impacts:      impacts,
			Summary:      analysis.FormatImpactSummary(impacts, scannedRepos),
		})
	}

	return printImpactReport(impacts, breakingChanges, scannedRepos)
}

type impactOutput struct {
	From         string                  `json:"from"`
	To           string                  `json:"to"`
	TargetModule string                  `json:"target_module"`
	Changes      []analysis.SymbolChange `json:"breaking_changes"`
	Impacts      []analysis.RepoImpact   `json:"impacts"`
	Summary      string                  `json:"summary"`
}

func printImpactReport(impacts []analysis.RepoImpact, changes []analysis.SymbolChange, scannedRepos int) error {
	fmt.Printf("\nImpact Report (%s → %s):\n", shortenHash(impactFrom), shortenHash(impactTo))
	fmt.Printf("Breaking changes: %d\n\n", len(changes))

	if len(impacts) == 0 {
		fmt.Printf("  %s✓%s No repos are affected by these breaking changes.\n", colorGreen, colorReset)
		fmt.Printf("\nSummary: %s\n", analysis.FormatImpactSummary(impacts, scannedRepos))
		return nil
	}

	for _, ri := range impacts {
		fmt.Printf("%s%s%s (%d breaking changes, %d call sites):\n",
			colorRed, ri.RepoName, colorReset, ri.BreakingCount, ri.TotalSites)

		for _, imp := range ri.Impacts {
			detail := formatImpactChangeDetail(imp.Change)
			fmt.Printf("  %s✗%s %s %s — %d call sites:\n",
				colorRed, colorReset,
				imp.Change.Symbol,
				detail,
				len(imp.Sites))

			w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
			for _, site := range imp.Sites {
				fmt.Fprintf(w, "      %s:%d\t%s\n", site.File, site.Line, site.FuncName)
			}
			w.Flush()
		}
		fmt.Println()
	}

	// Unaffected repos summary.
	unaffected := scannedRepos - len(impacts)
	if unaffected > 0 {
		fmt.Printf("%s✓%s %d repos have no impact\n\n", colorGreen, colorReset, unaffected)
	}

	fmt.Printf("Summary: %s\n", analysis.FormatImpactSummary(impacts, scannedRepos))
	return nil
}

func formatImpactChangeDetail(c analysis.SymbolChange) string {
	parts := []string{string(c.Kind)}
	if c.OldValue != "" && c.NewValue != "" {
		parts = append(parts, c.OldValue+" → "+c.NewValue)
	} else if c.OldValue != "" {
		parts = append(parts, c.OldValue)
	}
	return strings.Join(parts, " ")
}

// extractFuncNames returns deduplicated function names from breaking changes
// for use with ScanCallSites.
func extractFuncNames(changes []analysis.SymbolChange) []string {
	seen := make(map[string]struct{})
	var names []string

	for _, c := range changes {
		// For functions/methods, use the symbol key directly.
		// For struct field changes, use the struct name.
		parts := strings.Split(c.Symbol, ".")
		funcName := parts[len(parts)-1]

		// For qualified names, reconstruct as "pkg.FuncName".
		var key string
		if len(parts) >= 2 {
			key = parts[len(parts)-2] + "." + funcName
		} else {
			key = funcName
		}

		if _, exists := seen[key]; !exists {
			seen[key] = struct{}{}
			names = append(names, key)
		}
	}

	return names
}

func impactUnshallow(repoPath string) {
	cmd := exec.Command("git", "-C", repoPath, "fetch", "--unshallow", "--quiet")
	_ = cmd.Run() // best-effort, may already be unshallowed
}
