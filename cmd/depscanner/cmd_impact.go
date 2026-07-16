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

	if cfg.Offline {
		noFetch = true
	}

	factory := giteaListerFactory()

	src, err := selectSource(cfg, sourceFlag)
	if err != nil {
		return err
	}
	res, err := repo.ResolveProvider(src.Provider, cfg.CacheDir, cfg.Offline, factory)
	if err != nil {
		return fmt.Errorf("resolve source: %w", err)
	}
	targetRepo := res.Repos[0].Name
	mgr := res.Mgr
	targetRepoPath := mgr.GetRepoPath(targetRepo)

	// Clone target repo if not present locally.
	if !res.Local {
		if _, statErr := os.Stat(filepath.Join(targetRepoPath, ".git")); os.IsNotExist(statErr) {
			if cfg.Offline {
				return fmt.Errorf("source %q not found in cache (%s) and offline mode is enabled", targetRepo, targetRepoPath)
			}
			fmt.Printf("Cloning target module %s...\n", targetRepo)
			if err := mgr.SyncRepos(res.Repos, false); err != nil {
				return fmt.Errorf("clone target module: %w", err)
			}
		}
	}

	targetModule := src.Module
	if targetModule == "" {
		targetModule, err = analysis.ReadModulePath(filepath.Join(targetRepoPath, "go.mod"))
		if err != nil {
			return fmt.Errorf("read module path: %w", err)
		}
	}

	// Ensure target repo is updated and unshallowed for ancestry checks (if online)
	if !res.Local && !noFetch {
		unshallowTargetRepo(targetRepoPath, defaultUnshallowTimeout, cfg.UnshallowBranches)
		if branch != "" {
			// Ensure the specific branch we are interested in is fetched
			_ = exec.Command("git", "-C", targetRepoPath, "fetch", "origin", branch+":"+branch, "--quiet").Run()
		}
		_ = exec.Command("git", "-C", targetRepoPath, "fetch", "--all", "--tags", "--quiet").Run()
	}

	// Phase 1: Diff
	fmt.Printf("Phase 1: Diffing target module %s (%s → %s)...\n", targetModule, shortenHash(from), shortenHash(to))

	if err := mgr.CheckoutCommit(targetRepo, from); err != nil {
		return err
	}
	oldIndex, _ := analysis.BuildSymbolIndex(targetRepoPath, targetModule)

	if err := mgr.CheckoutCommit(targetRepo, to); err != nil {
		return err
	}
	newIndex, _ := analysis.BuildSymbolIndex(targetRepoPath, targetModule)

	changes := analysis.DiffSymbols(oldIndex, newIndex)
	var interesting []analysis.SymbolChange
	var symbolTargets []string // func, method, const, var — scanned via ScanCallSites
	var typeTargets []string   // struct, interface, type — scanned via ScanTypeReferences
	symbolIntroCommits := make(map[string]string)

	for _, c := range changes {
		if c.Breaking || c.Kind == analysis.ChangeLogic || c.Kind == analysis.ChangeAffected {
			switch c.Category {
			case analysis.KindFunc, analysis.KindMethod, analysis.KindConst, analysis.KindVar:
				interesting = append(interesting, c)

				fullPkg, symName := analysis.SplitSymbolKey(c.Symbol)
				if symName != "" {
					relPkg := ""
					if fullPkg != targetModule {
						relPkg = strings.TrimPrefix(fullPkg, targetModule+"/")
					}
					target := symName
					if relPkg != "" {
						target = relPkg + "." + symName
					}
					symbolTargets = append(symbolTargets, target)

					for _, sym := range newIndex {
						if sym.QualifiedName() == c.Symbol {
							intro := findSymbolIntroCommit(targetRepoPath, from, to, sym.File, sym.StartLine, sym.EndLine)
							symbolIntroCommits[c.Symbol] = intro
							break
						}
					}
				}

			case analysis.KindStruct, analysis.KindInterface, analysis.KindType:
				interesting = append(interesting, c)
				_, typeName := analysis.SplitQualifiedName(c.Symbol)
				if typeName != "" {
					typeTargets = append(typeTargets, typeName)
				}
			}
		}
	}

	if len(interesting) == 0 {
		fmt.Printf("\nNo impactful changes detected.\n")
		return nil
	}
	fmt.Printf("  Found %d impactful changes (%d symbols, %d types)\n\n", len(interesting), len(symbolTargets), len(typeTargets))

	// Phase 2: Concurrent Scan
	fmt.Printf("Phase 2: Syncing and scanning consumers (concurrent)...\n")

	var mu sync.Mutex
	repoCallSites := make(map[string][]analysis.CallSite)
	repoTypeRefs := make(map[string][]analysis.TypeRef)
	repoVersions := make(map[string]string)
	scannedCount := 0

	// scanConsumerImpact is the per-repo scan body, parameterized on the
	// consumer group's manager so repoPath resolves against the right cache dir.
	scanConsumerImpact := func(cmgr *repo.Manager, r gitea.Repository, synced bool) {
		if !synced {
			return
		}
		repoPath := cmgr.GetRepoPath(r.Name)

		goModPath := filepath.Join(repoPath, "go.mod")
		if _, err := os.Stat(goModPath); err != nil {
			return
		}
		info, _ := analysis.ParseGoMod(goModPath, targetModule)
		if !info.Found {
			return
		}

		var allSites []analysis.CallSite
		for _, target := range symbolTargets {
			sites, _, _ := analysis.ScanSymbolReferences(repoPath, targetModule, target, analysis.ReturnTypeRegistry{})
			allSites = append(allSites, sites...)
		}

		var allTypeRefs []analysis.TypeRef
		for _, target := range typeTargets {
			refs, _ := analysis.ScanTypeReferences(repoPath, targetModule, target)
			allTypeRefs = append(allTypeRefs, refs...)
		}

		mu.Lock()
		scannedCount++
		if len(allSites) > 0 || len(allTypeRefs) > 0 {
			if len(allSites) > 0 {
				repoCallSites[r.Name] = allSites
			}
			if len(allTypeRefs) > 0 {
				repoTypeRefs[r.Name] = allTypeRefs
			}
			repoVersions[r.Name] = info.Version
		}
		mu.Unlock()
	}

	for _, c := range cfg.Consumers {
		cres, rerr := repo.ResolveProvider(c, cfg.CacheDir, cfg.Offline, factory)
		if rerr != nil {
			return fmt.Errorf("resolve consumer: %w", rerr)
		}
		fmt.Printf("  %s: %d repositories\n", cres.Group, len(cres.Repos))

		if cres.Local {
			for _, r := range cres.Repos {
				scanConsumerImpact(cres.Mgr, r, true)
			}
			continue
		}
		if branch != "" && !cfg.Offline {
			cres.Mgr.PipelineSyncBranchAndProcess(cres.Repos, branch, noFetch, 4, func(r gitea.Repository, s bool) {
				scanConsumerImpact(cres.Mgr, r, s)
			})
		} else {
			cres.Mgr.PipelineSyncAndProcess(cres.Repos, noFetch, 4, func(r gitea.Repository, s bool) {
				scanConsumerImpact(cres.Mgr, r, s)
			})
		}
	}
	fmt.Println()

	// Phase 3: Analyzing impact
	fmt.Printf("Phase 3: Analyzing impact for %d matched repositories...\n", scannedCount)
	funcImpacts := analysis.AnalyzeImpact(changes, repoCallSites)
	typeImpacts := analysis.AnalyzeTypeImpact(changes, repoTypeRefs)

	// Merge func + type impacts per repo
	impactMap := make(map[string]*analysis.RepoImpact)
	for _, ri := range funcImpacts {
		copy := ri
		impactMap[ri.RepoName] = &copy
	}
	for _, ri := range typeImpacts {
		if existing, ok := impactMap[ri.RepoName]; ok {
			existing.Impacts = append(existing.Impacts, ri.Impacts...)
			existing.BreakingCount += ri.BreakingCount
			existing.TotalSites += ri.TotalSites
		} else {
			copy := ri
			impactMap[ri.RepoName] = &copy
		}
	}

	var impacts []analysis.RepoImpact
	for _, ri := range impactMap {
		ri.CurrentVersion = repoVersions[ri.RepoName]
		impacts = append(impacts, *ri)
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
			label := "call sites"
			if c.Category == analysis.KindConst || c.Category == analysis.KindVar {
				label = "references"
			} else if c.Category == analysis.KindStruct || c.Category == analysis.KindInterface || c.Category == analysis.KindType {
				label = "type refs"
			}
			fmt.Printf("  %s %s: 0 %s\n", icon, c.Symbol, label)
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

			siteLabel := "call sites"
			if imp.Change.Category == analysis.KindConst || imp.Change.Category == analysis.KindVar {
				siteLabel = "references"
			}
			formatter.Printf("  %s %s — %d %s:", icon, imp.Change.Symbol, len(imp.Sites), siteLabel)
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
