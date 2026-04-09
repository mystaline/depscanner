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
	
	giteaClient := gitea.NewClient(cfg.Gitea.URL, cfg.Gitea.Token)
	mgr := repo.NewManager(cfg.CacheDir, cfg.Gitea.Org)

	_, targetRepo := gitea.ParseModuleOwnerRepo(cfg.TargetModule)
	targetRepoPath := mgr.GetRepoPath(targetRepo)

	// Ensure target repo is updated and unshallowed for ancestry checks
	unshallowTargetRepo(targetRepoPath)
	if branch != "" {
		// Ensure the specific branch we are interested in is fetched
		_ = exec.Command("git", "-C", targetRepoPath, "fetch", "origin", branch+":"+branch, "--quiet").Run()
	}
	_ = exec.Command("git", "-C", targetRepoPath, "fetch", "--all", "--tags", "--quiet").Run()

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
	symbolIntroCommits := make(map[string]string)

	for _, c := range changes {
		if c.Breaking || c.Kind == analysis.ChangeLogic || c.Kind == analysis.ChangeAffected {
			if c.Category == analysis.KindFunc || c.Category == analysis.KindMethod {
				interesting = append(interesting, c)
				
				dotIdx := strings.LastIndex(c.Symbol, ".")
				if dotIdx != -1 {
					fullPkg := c.Symbol[:dotIdx]
					funcName := c.Symbol[dotIdx+1:]
					pkgParts := strings.Split(fullPkg, "/")
					pkgBaseName := pkgParts[len(pkgParts)-1]
					funcTargets = append(funcTargets, pkgBaseName+"."+funcName)

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
		fmt.Printf("  \033[32m✓\033[0m No consumers are affected by these changes.\n")
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

		status := "\033[31m⚠ ACTION REQUIRED\033[0m"
		if repoIsResolved {
			status = "\033[32m✓ RESOLVED\033[0m"
		}
		
		fmt.Printf("%s \033[1m%s\033[0m (current: %s)\n", status, ri.RepoName, ri.CurrentVersion)
		for _, imp := range ri.Impacts {
			intro := introCommits[imp.Change.Symbol]
			repoVer := extractHash(ri.CurrentVersion)
			symbolIsResolved := intro != "" && isAncestor(targetRepoPath, intro, repoVer)
			
			icon := "\033[31m✗\033[0m"
			if imp.Change.Kind == analysis.ChangeLogic { icon = "\033[33m~\033[0m" }
			if imp.Change.Kind == analysis.ChangeAffected { icon = "\033[34m·\033[0m" }
			if symbolIsResolved { icon = "\033[32m✓\033[0m" }

			fmt.Printf("  %s %s — %d call sites:", icon, imp.Change.Symbol, len(imp.Sites))
			if symbolIsResolved {
				fmt.Printf(" \033[32m(resolved in %s)\033[0m", shortenHash(intro))
			} else if intro != "" {
				fmt.Printf(" \033[33m(needs commit %s)\033[0m", shortenHash(intro))
			}
			fmt.Println()

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

func findSymbolIntroCommit(repoPath, from, to, filePath string, startLine, endLine int) string {
	if filePath == "" || startLine == 0 {
		return to
	}
	// Relativize path if needed (internal symbols might have absolute paths)
	if strings.Contains(filePath, repoPath) {
		filePath, _ = filepath.Rel(repoPath, filePath)
	}

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

func isAncestor(repoPath, ancestor, descendant string) bool {
	if ancestor == "" || descendant == "" {
		return false
	}
	if strings.HasPrefix(descendant, ancestor) || strings.HasPrefix(ancestor, descendant) {
		return true
	}

	// Double check if descendant exists, if not, try to fetch it
	if err := exec.Command("git", "-C", repoPath, "rev-parse", "--verify", descendant).Run(); err != nil {
		// Not found locally, try to fetch it
		_ = exec.Command("git", "-C", repoPath, "fetch", "origin", descendant, "--quiet").Run()
	}

	cmd := exec.Command("git", "-C", repoPath, "merge-base", "--is-ancestor", ancestor, descendant)
	return cmd.Run() == nil
}

func unshallowTargetRepo(repoPath string) {
	// Ensure the repo is configured to fetch all branches, not just the default one
	// (Shallow clones are often single-branch)
	_ = exec.Command("git", "-C", repoPath, "config", "remote.origin.fetch", "+refs/heads/*:refs/remotes/origin/*").Run()

	if _, err := os.Stat(filepath.Join(repoPath, ".git", "shallow")); err == nil {
		_ = exec.Command("git", "-C", repoPath, "fetch", "--unshallow", "--quiet").Run()
	}
}

func extractHash(version string) string {
	parts := strings.Split(version, "-")
	if len(parts) > 1 {
		return parts[len(parts)-1]
	}
	// Handle raw hashes too
	return version
}
