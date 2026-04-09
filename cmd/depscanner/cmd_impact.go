package main

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"text/tabwriter"

	"github.com/mystaline/depscanner/internal/analysis"
	"github.com/mystaline/depscanner/internal/config"
	"github.com/mystaline/depscanner/internal/gitea"
	"github.com/mystaline/depscanner/internal/repo"
	"github.com/spf13/cobra"
)

func newImpactCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "impact <from> <to>",
		Short: "Analyze per-repo impact of changes between two versions of the target module",
		Args:  cobra.ExactArgs(2),
		RunE:  runImpact,
	}
	return cmd
}

func runImpact(cmd *cobra.Command, args []string) error {
	from, to := args[0], args[1]

	cfg, err := config.Load(cfgPath)
	if err != nil {
		return err
	}
	if cacheDir != "" {
		cfg.CacheDir = cacheDir
	}
	
	giteaClient := gitea.NewClient(cfg.Gitea.URL, cfg.Gitea.Token)
	mgr := repo.NewManager(cfg.CacheDir, cfg.Gitea.Org)

	_, targetRepo := gitea.ParseModuleOwnerRepo(cfg.TargetModule)
	targetRepoPath := mgr.GetRepoPath(targetRepo)

	// Phase 1: Diff
	fmt.Printf("Phase 1: Diffing target module %s (%s → %s)...\n", cfg.TargetModule, shortenHash(from), shortenHash(to))
	
	if err := mgr.CheckoutCommit(targetRepo, from); err != nil {
		return err
	}
	oldIndex, _ := analysis.BuildSymbolIndex(targetRepoPath)

	if err := mgr.CheckoutCommit(targetRepo, to); err != nil {
		return err
	}
	newIndex, _ := analysis.BuildSymbolIndex(targetRepoPath)

	changes := analysis.DiffSymbols(oldIndex, newIndex)
	var interesting []analysis.SymbolChange
	var funcTargets []string

	for _, c := range changes {
		if c.Breaking || c.Kind == analysis.ChangeLogic || c.Kind == analysis.ChangeAffected {
			if c.Category == analysis.KindFunc || c.Category == analysis.KindMethod {
				interesting = append(interesting, c)
				
				// Extract "PackageBaseName.FuncName"
				dotIdx := strings.LastIndex(c.Symbol, ".")
				if dotIdx != -1 {
					fullPkg := c.Symbol[:dotIdx]
					funcName := c.Symbol[dotIdx+1:]
					pkgParts := strings.Split(fullPkg, "/")
					pkgBaseName := pkgParts[len(pkgParts)-1]
					funcTargets = append(funcTargets, pkgBaseName+"."+funcName)
				}
			}
		}
	}

	if len(interesting) == 0 {
		fmt.Printf("\nNo impactful changes detected.\n")
		return nil
	}
	fmt.Printf("  Found %d impactful changes (affecting %d functions)\n\n", len(interesting), len(funcTargets))

	// Phase 2: Concurrent Scan
	repos, _ := giteaClient.ListOrgRepos(cfg.Gitea.Org)
	repos = filterRepos(repos, cfg.IncludeRepos, cfg.ExcludeRepos)

	fmt.Printf("Phase 2: Syncing and scanning %d repos (concurrent)...\n", len(repos))

	var mu sync.Mutex
	repoCallSites := make(map[string][]analysis.CallSite)
	repoVersions := make(map[string]string)
	scannedCount := 0

	processFn := func(r gitea.Repository, synced bool) {
		if !synced { return }
		repoPath := mgr.GetRepoPath(r.Name)
		
		goModPath := filepath.Join(repoPath, "go.mod")
		if _, err := os.Stat(goModPath); err != nil { return }
		info, _ := analysis.ParseGoMod(goModPath, cfg.TargetModule)
		if !info.Found { return }

		var allSites []analysis.CallSite
		for _, target := range funcTargets {
			sites, _, _ := analysis.ScanCallSites(repoPath, cfg.TargetModule, target)
			allSites = append(allSites, sites...)
		}

		mu.Lock()
		scannedCount++
		if len(allSites) > 0 {
			repoCallSites[r.Name] = allSites
			repoVersions[r.Name] = info.Version
		}
		mu.Unlock()
	}

	if branch != "" {
		mgr.PipelineSyncBranchAndProcess(repos, branch, noFetch, 4, processFn)
	} else {
		mgr.PipelineSyncAndProcess(repos, noFetch, 4, processFn)
	}
	fmt.Println()

	// Phase 3: Report
	fmt.Printf("Phase 3: Analyzing impact for %d matched repositories...\n", scannedCount)
	impacts := analysis.AnalyzeImpact(changes, repoCallSites)
	// Inject versions into impacts
	for i := range impacts {
		impacts[i].CurrentVersion = repoVersions[impacts[i].RepoName]
	}
	return printImpactReport(impacts, interesting, scannedCount, from, to)
}

func printImpactReport(impacts []analysis.RepoImpact, changes []analysis.SymbolChange, scannedRepos int, from, to string) error {
	fmt.Printf("\nImpact Report (%s → %s):\n", shortenHash(from), shortenHash(to))
	fmt.Printf("Impacting changes (breaking/logic): %d\n\n", len(changes))

	if len(impacts) == 0 {
		fmt.Printf("  \033[32m✓\033[0m No consumers are affected by these changes.\n")
		return nil
	}

	sort.Slice(impacts, func(i, j int) bool { return impacts[i].RepoName < impacts[j].RepoName })
	for _, ri := range impacts {
		status := "\033[31m⚠ UPDATE REQUIRED\033[0m"
		if strings.HasSuffix(ri.CurrentVersion, shortenHash(to)) {
			status = "\033[32m✓ UP TO DATE\033[0m"
		}
		
		fmt.Printf("%s \033[1m%s\033[0m (current: %s)\n", status, ri.RepoName, ri.CurrentVersion)
		for _, imp := range ri.Impacts {
			icon := "\033[31m✗\033[0m"
			if imp.Change.Kind == analysis.ChangeLogic { icon = "\033[33m~\033[0m" }
			if imp.Change.Kind == analysis.ChangeAffected { icon = "\033[34m·\033[0m" }

			fmt.Printf("  %s %s — %d call sites:\n", icon, imp.Change.Symbol, len(imp.Sites))
			w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
			for _, site := range imp.Sites {
				fmt.Fprintf(w, "      %s:%d\t%s\n", site.File, site.Line, site.FuncName)
			}
			w.Flush()
		}
		fmt.Println()
	}
	return nil
}
