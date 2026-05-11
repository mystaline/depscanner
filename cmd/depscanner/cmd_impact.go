package main

import (
	"fmt"
	"os"
	"os/exec"
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
		Short: "Analyze per-repo impact with surgical resolution tracking",
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

	mgr := repo.NewManager(cfg.CacheDir, cfg.Gitea.Org)

	if cfg.Offline {
		noFetch = true
	}

	targetOwner, targetRepo := gitea.ParseModuleOwnerRepo(cfg.TargetModule)
	targetRepoPath := mgr.GetRepoPath(targetRepo)

	// Clone target repo if not present locally.
	if _, statErr := os.Stat(targetRepoPath); statErr != nil {
		if cfg.Offline {
			return fmt.Errorf("target module %q not found in cache (%s) and offline mode is enabled", cfg.TargetModule, targetRepoPath)
		}
		fmt.Printf("Cloning target module %s...\n", cfg.TargetModule)
		cloneURL := fmt.Sprintf("%s/%s/%s.git", cfg.Gitea.URL, targetOwner, targetRepo)
		if err := mgr.SyncRepos([]gitea.Repository{{Name: targetRepo, CloneURL: cloneURL}}, false); err != nil {
			return fmt.Errorf("clone target module: %w", err)
		}
	}

	// Ensure target repo is updated and unshallowed for ancestry checks (if online)
	if !noFetch {
		unshallowTargetRepo(targetRepoPath)
		if branch != "" {
			// Ensure the specific branch we are interested in is fetched
			_ = exec.Command("git", "-C", targetRepoPath, "fetch", "origin", branch+":"+branch, "--quiet").Run()
		}
		_ = exec.Command("git", "-C", targetRepoPath, "fetch", "--all", "--tags", "--quiet").Run()
	} else if _, statErr := os.Stat(targetRepoPath); statErr != nil {
		return fmt.Errorf("target module %q not found in cache (%s) and offline mode is enabled", cfg.TargetModule, targetRepoPath)
	}

	// Phase 1: Diff
	fmt.Printf("Phase 1: Diffing target module %s (%s → %s)...\n", cfg.TargetModule, shortenHash(from), shortenHash(to))

	if err := mgr.CheckoutCommit(targetRepo, from); err != nil {
		return err
	}
	oldIndex, _ := analysis.BuildSymbolIndex(targetRepoPath, cfg.TargetModule)

	if err := mgr.CheckoutCommit(targetRepo, to); err != nil {
		return err
	}
	newIndex, _ := analysis.BuildSymbolIndex(targetRepoPath, cfg.TargetModule)

	changes := analysis.DiffSymbols(oldIndex, newIndex)
	var interesting []analysis.SymbolChange
	var funcTargets []string
	symbolIntroCommits := make(map[string]string)

	for _, c := range changes {
		if c.Breaking || c.Kind == analysis.ChangeLogic || c.Kind == analysis.ChangeAffected {
			if c.Category == analysis.KindFunc || c.Category == analysis.KindMethod {
				interesting = append(interesting, c)

				dotIdx := strings.LastIndex(c.Symbol, ".")
				if dotIdx != -1 {
					fullPkg := c.Symbol[:dotIdx]
					funcName := c.Symbol[dotIdx+1:]
					relPkg := ""
					if fullPkg != cfg.TargetModule {
						relPkg = strings.TrimPrefix(fullPkg, cfg.TargetModule+"/")
					}
					funcTargets = append(funcTargets, relPkg+"."+funcName)

					// Track resolution using file-based history
					// Find the symbol in newIndex to get its file path
					for _, sym := range newIndex {
						if sym.QualifiedName() == c.Symbol {
							intro := findSymbolIntroCommit(targetRepoPath, from, to, sym.File, sym.StartLine, sym.EndLine)
							symbolIntroCommits[c.Symbol] = intro
							break
						}
					}
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
	var repos []gitea.Repository
	if cfg.Offline {
		noFetch = true
		fmt.Printf("Listing repositories from local cache: %s\n", mgr.GetOrgPath())
		var lerr error
		repos, lerr = mgr.ListLocalRepos()
		if lerr != nil {
			return fmt.Errorf("list local repos: %w", lerr)
		}
	} else {
		giteaClient := gitea.NewClient(cfg.Gitea.URL, cfg.Gitea.Token)
		var lerr error
		repos, lerr = giteaClient.ListOrgRepos(cfg.Gitea.Org)
		if lerr != nil {
			return fmt.Errorf("list repos: %w", lerr)
		}
	}
	repos = filterRepos(repos, cfg.IncludeRepos, cfg.ExcludeRepos)

	fmt.Printf("Phase 2: Syncing and scanning %d repos (concurrent)...\n", len(repos))

	var mu sync.Mutex
	repoCallSites := make(map[string][]analysis.CallSite)
	repoVersions := make(map[string]string)
	scannedCount := 0

	processFn := func(r gitea.Repository, synced bool) {
		if !synced {
			return
		}
		repoPath := mgr.GetRepoPath(r.Name)

		goModPath := filepath.Join(repoPath, "go.mod")
		if _, err := os.Stat(goModPath); err != nil {
			return
		}
		info, _ := analysis.ParseGoMod(goModPath, cfg.TargetModule)
		if !info.Found {
			return
		}

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

	if branch != "" && !cfg.Offline {
		mgr.PipelineSyncBranchAndProcess(repos, branch, noFetch, 4, processFn)
	} else {
		mgr.PipelineSyncAndProcess(repos, noFetch, 4, processFn)
	}
	fmt.Println()

	// Phase 3: Analyzing impact
	fmt.Printf("Phase 3: Analyzing impact for %d matched repositories...\n", scannedCount)
	impacts := analysis.AnalyzeImpact(changes, repoCallSites)
	for i := range impacts {
		impacts[i].CurrentVersion = repoVersions[impacts[i].RepoName]
	}

	return printImpactReport(targetRepoPath, impacts, interesting, scannedCount, from, to, symbolIntroCommits)
}

func printImpactReport(targetRepoPath string, impacts []analysis.RepoImpact, changes []analysis.SymbolChange, scannedRepos int, from, to string, introCommits map[string]string) error {
	fmt.Printf("\nImpact Report (%s → %s):\n", shortenHash(from), shortenHash(to))
	fmt.Printf("Impacting changes (breaking/logic): %d\n\n", len(changes))

	if len(impacts) == 0 {
		fmt.Printf("Checked %d impactful symbols across all reservoirs:\n", len(changes))
		for _, c := range changes {
			icon := "·"
			if c.Breaking {
				icon = formatter.ColorRed() + "✗" + formatter.ColorReset()
			} else if c.Kind == analysis.ChangeLogic {
				icon = formatter.ColorYellow() + "~" + formatter.ColorReset()
			}
			fmt.Printf("  %s %s: 0 call sites\n", icon, c.Symbol)
		}
		formatter.Printf("\n  %s✓ No consumers are affected by these changes.%s\n\n", formatter.ColorGreen(), formatter.ColorReset())
		return nil
	}

	sort.Slice(impacts, func(i, j int) bool { return impacts[i].RepoName < impacts[j].RepoName })
	for _, ri := range impacts {
		repoIsResolved := true
		for _, imp := range ri.Impacts {
			intro := introCommits[imp.Change.Symbol]
			repoVer := extractHash(ri.CurrentVersion)
			if intro != "" && !isAncestor(targetRepoPath, intro, repoVer) {
				repoIsResolved = false
				break
			}
		}

		status := formatter.ColorRed() + "⚠ ACTION REQUIRED" + formatter.ColorReset()
		if repoIsResolved {
			status = formatter.ColorGreen() + "✓ RESOLVED" + formatter.ColorReset()
		}

		formatter.Printf("%s %s%s%s (current: %s)\n", status, formatter.ColorBold(), ri.RepoName, formatter.ColorReset(), ri.CurrentVersion)
		for _, imp := range ri.Impacts {
			intro := introCommits[imp.Change.Symbol]
			repoVer := extractHash(ri.CurrentVersion)
			symbolIsResolved := intro != "" && isAncestor(targetRepoPath, intro, repoVer)

			icon := formatter.ColorRed() + "✗" + formatter.ColorReset()
			if imp.Change.Kind == analysis.ChangeLogic {
				icon = formatter.ColorYellow() + "~" + formatter.ColorReset()
			}
			if imp.Change.Kind == analysis.ChangeAffected {
				icon = formatter.ColorBlue() + "·" + formatter.ColorReset()
			}
			if symbolIsResolved {
				icon = formatter.ColorGreen() + "✓" + formatter.ColorReset()
			}

			formatter.Printf("  %s %s — %d call sites:", icon, imp.Change.Symbol, len(imp.Sites))
			if symbolIsResolved {
				formatter.Printf(" %s(resolved in %s)%s", formatter.ColorGreen(), shortenHash(intro), formatter.ColorReset())
			} else if intro != "" {
				formatter.Printf(" %s(needs commit %s)%s", formatter.ColorYellow(), shortenHash(intro), formatter.ColorReset())
			}
			formatter.Println()

			w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
			for _, site := range imp.Sites {
				fmt.Fprintf(w, "      %s:%d\t%s\n", site.File, site.Line, site.RawName)
			}
			w.Flush()
		}
		fmt.Println()
	}
	return nil
}

func findSymbolIntroCommit(repoPath, from, to, filePath string, startLine, endLine int) string {
	if filePath == "" || startLine == 0 {
		return to
	}
	// Relativize path if needed (internal symbols might have absolute paths)
	if strings.Contains(filePath, repoPath) {
		filePath, _ = filepath.Rel(repoPath, filePath)
	}
	filePath = filepath.ToSlash(filePath)

	// Surgical tracking: find the first commit in the range that touched THESE lines.
	// We use the line numbers from the 'to' commit.
	rangeArg := fmt.Sprintf("%d,%d:%s", startLine, endLine, filePath)
	cmd := exec.Command("git", "-C", repoPath, "log", "-L", rangeArg, "--reverse", "--pretty=format:%H", from+".."+to)
	out, err := cmd.Output()
	if err != nil || len(out) == 0 {
		// Fallback to file-based if -L fails (e.g. file renamed or line tracking issues)
		cmd = exec.Command("git", "-C", repoPath, "log", "--reverse", "--pretty=format:%H", from+".."+to, "--", filePath)
		out, _ = cmd.Output()
	}

	if len(out) == 0 {
		return to
	}

	commits := strings.Split(strings.TrimSpace(string(out)), "\n")
	// The output of 'git log -L' might contain headers if not handled carefully,
	// but with --pretty=format:%H we should get just hashes.
	// However, git log -L is known to have some verbose output even with --pretty.
	// We filter to ensure we only get valid hashes.
	for _, line := range commits {
		line = strings.TrimSpace(line)
		if len(line) >= 7 { // Hash length
			return line
		}
	}
	return to
}

func extractHash(version string) string {
	parts := strings.Split(version, "-")
	if len(parts) > 1 {
		return parts[len(parts)-1]
	}
	// Handle raw hashes too
	return version
}
