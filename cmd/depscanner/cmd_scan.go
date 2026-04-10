package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path"
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

const (
	colorGreen  = "\033[32m"
	colorRed    = "\033[31m"
	colorYellow = "\033[33m"
	colorReset  = "\033[0m"
)

// staleness levels for branch-aware scanning.
const (
	statusOK       = "ok"
	statusStale    = "stale"
	statusCritical = "critical" // commit not on expected branch
	statusUnknown  = "unknown"
)

// callSiteResult is the JSON-serializable representation of a function call site.
type callSiteResult struct {
	File     string `json:"file"`
	Line     int    `json:"line"`
	Column   int    `json:"column"`
	FuncName string `json:"func_name"`
	RawName  string `json:"raw_name"`
	ArgCount int    `json:"arg_count"`
}

type repoScanResult struct {
	Name          string           `json:"name"`
	Branch        string           `json:"branch,omitempty"`
	HasGoMod      bool             `json:"has_go_mod"`
	UsesTarget    bool             `json:"uses_target"`
	TargetVersion string           `json:"target_version,omitempty"`
	CommitHash    string           `json:"commit_hash,omitempty"`
	LatestHash    string           `json:"latest_hash,omitempty"`
	Status        string           `json:"status,omitempty"`
	StatusDetail  string           `json:"status_detail,omitempty"`
	Packages      []string         `json:"packages,omitempty"`
	CallSites     []callSiteResult `json:"call_sites,omitempty"`
	CloneURL      string           `json:"clone_url,omitempty"`
}

type scanOutput struct {
	Repos           []repoScanResult        `json:"repos"`
	TargetModule    string                  `json:"target_module"`
	Branch          string                  `json:"branch,omitempty"`
	FuncName        string                  `json:"func_name,omitempty"`
	TargetSignature *analysis.FuncSignature `json:"target_signature,omitempty"`
	Total           int                     `json:"total"`
	GoModCount      int                     `json:"go_mod_count"`
	TargetCount     int                     `json:"target_count"`
}

func newScanCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "scan",
		Short: "List all org repos and detect shared library usage and staleness",
		RunE:  runScan,
	}

	cmd.Flags().BoolVar(&packages, "packages", false, "show which sub-packages of the target module are imported")
	cmd.Flags().StringVar(&funcName, "func", "", "search for call sites of a specific function (e.g. \"Must\" or \"helper.Must\")")
	cmd.Flags().BoolVar(&check, "check", false, "check call-site signatures against target module (requires --func)")
	return cmd
}

func runScan(_ *cobra.Command, _ []string) error {
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

	mgr := repo.NewManager(cfg.CacheDir, cfg.Gitea.Org)

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
		fmt.Printf("Fetching repository list from %s...\n", cfg.Gitea.URL)
		var lerr error
		repos, lerr = giteaClient.ListOrgRepos(cfg.Gitea.Org)
		if lerr != nil {
			return fmt.Errorf("list repos: %w", lerr)
		}
	}
	repos = filterRepos(repos, cfg.IncludeRepos, cfg.ExcludeRepos)
	fmt.Printf("Found %d repositories\n\n", len(repos))

	// Resolve the target module's latest commit for the tracked branch.
	var latestTargetHash string
	targetBranch := ""
	if branch != "" && !cfg.Offline {
		giteaClient := gitea.NewClient(cfg.Gitea.URL, cfg.Gitea.Token)
		targetBranch = cfg.BranchTracking[branch]
		if targetBranch == "" {
			targetBranch = branch // default: same name
		}

		targetOwner, targetRepo := gitea.ParseModuleOwnerRepo(cfg.TargetModule)
		if targetOwner == "" {
			fmt.Fprintf(os.Stderr, "  warn: target_module %q does not look like a full module path (expected host/owner/repo); staleness detection disabled\n", cfg.TargetModule)
		} else {
			fmt.Printf("Resolving latest commit for %s@%s...\n", cfg.TargetModule, targetBranch)
			latestTargetHash, err = giteaClient.GetBranchCommitHash(targetOwner, targetRepo, targetBranch)
			if err != nil {
				fmt.Fprintf(os.Stderr, "  warn: could not resolve target branch %s: %v\n", targetBranch, err)
			} else if latestTargetHash != "" {
				fmt.Printf("  latest: %s\n\n", shortenHash(latestTargetHash))
			}
		}
	}

	// If --check is active, parse the target module's signature.
	var targetSig *analysis.FuncSignature
	if funcName != "" && check {
		targetOwner, targetRepo := gitea.ParseModuleOwnerRepo(cfg.TargetModule)
		if targetOwner != "" {
			targetPath := mgr.GetRepoPath(targetRepo)
			
			// Try to sync unless offline
			if !cfg.Offline {
				if _, statErr := os.Stat(targetPath); statErr != nil {
					fmt.Printf("Syncing target module for signature check...\n")
					_, syncErr := mgr.SyncBranch(targetRepo, fmt.Sprintf("%s/%s/%s.git", cfg.Gitea.URL, targetOwner, targetRepo), targetBranch)
					if syncErr != nil {
						fmt.Fprintf(os.Stderr, "  warn: failed to sync target module: %v\n", syncErr)
					}
				}
			}

			if _, statErr := os.Stat(targetPath); statErr == nil {
				sig, sigErr := analysis.ParseSignature(targetPath, funcName)
				if sigErr != nil {
					fmt.Fprintf(os.Stderr, "  warn: could not parse signature for %q: %v\n", funcName, sigErr)
				} else {
					targetSig = sig
					fmt.Printf("Parsed signature for %q: %d params%s\n\n",
						funcName, sig.ParamsCount, func() string {
							if sig.IsVariadic {
								return "+"
							}
							return ""
						}())
				}
			} else if cfg.Offline {
				fmt.Fprintf(os.Stderr, "  warn: target module %q not found in cache (%s), cannot check signatures offline\n", targetRepo, targetPath)
			}
		}
	}

	// Pipeline: sync repos concurrently and process each one as soon as it's ready.
	// Results are buffered and sorted at the end for deterministic output.
	var (
		mu          sync.Mutex
		results     []repoScanResult
		goModCount  int
		targetCount int
	)

	processFn := func(r gitea.Repository, synced bool) {
		repoPath := mgr.GetRepoPath(r.Name)
		goModPath := filepath.Join(repoPath, "go.mod")
		_, statErr := os.Stat(goModPath)
		hasGoMod := statErr == nil

		// Use the correct branch for display/logic
		targetBranchForRepo := cfg.GetBranchForRepo(r.Name, branch)

		var usesTarget bool
		var targetVersion, commitHash, status, statusDetail string
		if hasGoMod {
			info, parseErr := analysis.ParseGoMod(goModPath, cfg.TargetModule)
			if parseErr != nil {
				fmt.Fprintf(os.Stderr, "  warn: parse go.mod for %s: %v\n", r.Name, parseErr)
			} else if info.Found {
				usesTarget = true
				targetVersion = info.Version

				// Staleness detection when branch is active.
				if branch != "" && latestTargetHash != "" {
					commitHash, status, statusDetail = detectStaleness(info.Version, latestTargetHash)
				}
			}
		}

		// Sub-package import scanning.
		var pkgs []string
		if packages && usesTarget {
			var scanErr error
			pkgs, scanErr = analysis.ScanImports(repoPath, cfg.TargetModule)
			if scanErr != nil {
				fmt.Fprintf(os.Stderr, "  warn: scan imports for %s: %v\n", r.Name, scanErr)
			}
		}

		// Function call-site search.
		var callSites []callSiteResult
		if funcName != "" && usesTarget {
			sites, warnings, scanErr := analysis.ScanCallSites(repoPath, cfg.TargetModule, funcName)
			if scanErr != nil {
				fmt.Fprintf(os.Stderr, "  warn: scan call sites for %s: %v\n", r.Name, scanErr)
			}
			for _, w := range warnings {
				fmt.Fprintf(os.Stderr, "  warn: %s: %s\n", r.Name, w)
			}
			for _, s := range sites {
				callSites = append(callSites, callSiteResult{
					File:     s.File,
					Line:     s.Line,
					Column:   s.Column,
					FuncName: s.FuncName,
					RawName:  s.RawName,
					ArgCount: s.ArgCount,
				})
			}
		}

		result := repoScanResult{
			Name:          r.Name,
			Branch:        targetBranchForRepo,
			HasGoMod:      hasGoMod,
			UsesTarget:    usesTarget,
			Packages:      pkgs,
			CallSites:     callSites,
			TargetVersion: targetVersion,
			CommitHash:    commitHash,
			LatestHash:    shortenHash(latestTargetHash),
			Status:        status,
			StatusDetail:  statusDetail,
			CloneURL:      r.CloneURL,
		}

		mu.Lock()
		results = append(results, result)
		if hasGoMod {
			goModCount++
		}
		if usesTarget {
			targetCount++
		}
		mu.Unlock()
	}

	processFnWithBranch := func(r gitea.Repository, _ bool) {
		// This is a dummy call to SyncBranch inside the pipeline loop,
		// but since we want to sync the CORRECT branch for each repo:
		targetBranch := cfg.GetBranchForRepo(r.Name, branch)
		ok, err := mgr.SyncBranchQuiet(r.Name, r.CloneURL, targetBranch)
		if err != nil {
			fmt.Fprintf(os.Stderr, "  warn: sync %s@%s: %v\n", r.Name, targetBranch, err)
			processFn(r, false)
			return
		}
		processFn(r, ok)
	}

	if branch == "" {
		mgr.PipelineSyncAndProcess(repos, noFetch, 4, processFn)
	} else {
		// Use generic pipeline but sync the custom branch per repo
		mgr.PipelineSyncAndProcess(repos, noFetch, 4, processFnWithBranch)
	}
	fmt.Println()

	// Sort results by name for deterministic output.
	sortResults(results)

	if format == "json" {
		return json.NewEncoder(os.Stdout).Encode(scanOutput{
			Repos:           results,
			TargetModule:    cfg.TargetModule,
			Branch:          branch,
			FuncName:        funcName,
			TargetSignature: targetSig,
			Total:           len(results),
			GoModCount:      goModCount,
			TargetCount:     targetCount,
		})
	}

	// Table output.
	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	if branch != "" {
		fmt.Fprintf(w, "  STATUS\tREPOSITORY\tVERSION/COMMIT\tSTALENESS\n")
		fmt.Fprintf(w, "  ------\t----------\t--------------\t---------\n")
	} else {
		fmt.Fprintf(w, "  STATUS\tREPOSITORY\tTARGET VERSION\n")
		fmt.Fprintf(w, "  ------\t----------\t--------------\n")
	}

	for _, r := range results {
		switch {
		case !r.HasGoMod:
			if branch != "" {
				fmt.Fprintf(w, "  %s✗%s\t%s\t%s\t\n", colorRed, colorReset, r.Name, "(no go.mod)")
			} else {
				fmt.Fprintf(w, "  %s✗%s\t%s\t%s\n", colorRed, colorReset, r.Name, "(no go.mod)")
			}
		case !r.UsesTarget:
			if branch != "" {
				fmt.Fprintf(w, "  %s·%s\t%s\t%s\t\n", colorRed, colorReset, r.Name, "(not used)")
			} else {
				fmt.Fprintf(w, "  %s·%s\t%s\t%s\n", colorRed, colorReset, r.Name, "(not used)")
			}
		case branch != "" && r.Status != "":
			versionCol := r.TargetVersion
			if r.CommitHash != "" {
				versionCol = r.CommitHash
			}
			statusColor := statusToColor(r.Status)
			fmt.Fprintf(w, "  %s%s%s\t%s\t%s\t%s%s%s\n",
				statusColor, statusIcon(r.Status), colorReset,
				r.Name, versionCol,
				statusColor, r.StatusDetail, colorReset)
		default:
			fmt.Fprintf(w, "  %s✓%s\t%s\t%s\n", colorGreen, colorReset, r.Name, r.TargetVersion)
		}
	}
	w.Flush()

	// Show per-repo package usage when --packages is active.
	if packages {
		fmt.Println("\nSub-package usage:")
		for _, r := range results {
			if !r.UsesTarget || len(r.Packages) == 0 {
				continue
			}
			fmt.Printf("  %-35s %s\n", r.Name, strings.Join(r.Packages, ", "))
		}
	}

	// Show function call sites when --func is active.
	if funcName != "" {
		fmt.Printf("\nCall sites for %q:\n", funcName)
		if targetCount == 0 {
			fmt.Printf("  (no repos use target module %s)\n", cfg.TargetModule)
		} else {
			totalSites := 0
			reposWithSites := 0
			for _, r := range results {
				if len(r.CallSites) == 0 {
					continue
				}
				reposWithSites++
				totalSites += len(r.CallSites)
				fmt.Printf("\n  %s%s%s (%d call sites):\n", colorGreen, r.Name, colorReset, len(r.CallSites))
				for _, cs := range r.CallSites {
					matchStr := ""
					if targetSig != nil {
						match := false
						if targetSig.IsVariadic {
							match = cs.ArgCount >= targetSig.ParamsCount-1
						} else {
							match = cs.ArgCount == targetSig.ParamsCount
						}

						if match {
							matchStr = fmt.Sprintf("  %s✓ %d args%s", colorGreen, cs.ArgCount, colorReset)
						} else {
							matchStr = fmt.Sprintf("  %s✗ %d args (expected %d)%s", colorRed, cs.ArgCount, targetSig.ParamsCount, colorReset)
						}
					}
					fmt.Printf("    %s:%d  %s%s\n", cs.File, cs.Line, cs.RawName, matchStr)
				}
			}
			if reposWithSites == 0 {
				fmt.Printf("  (no call sites found in %d repos that use target module)\n", targetCount)
			} else {
				fmt.Printf("\n  Total: %d call sites across %d repos\n", totalSites, reposWithSites)
			}
		}
	}

	fmt.Printf("\nTarget: %s\n", cfg.TargetModule)
	if branch != "" {
		fmt.Printf("Branch: %s -> %s@%s (latest: %s)\n", branch, cfg.TargetModule, targetBranch, shortenHash(latestTargetHash))
	}
	fmt.Printf("Summary: %d/%d Go repos depend on target module\n", targetCount, goModCount)
	return nil
}

// detectStaleness compares a go.mod version against the latest target branch commit.
func detectStaleness(version, latestHash string) (commitHash, status, detail string) {
	if !analysis.IsPseudoVersion(version) {
		// Tagged version — can't compare against branch commit directly.
		return "", statusUnknown, "tagged version (no commit hash to compare)"
	}

	pv, err := analysis.ParsePseudoVersion(version)
	if err != nil {
		return "", statusUnknown, fmt.Sprintf("parse error: %v", err)
	}

	shortCurrent := pv.CommitHash
	shortLatest := shortenHash(latestHash)

	if strings.HasPrefix(strings.ToLower(latestHash), strings.ToLower(pv.CommitHash)) {
		return shortCurrent, statusOK, "up to date"
	}

	detail = fmt.Sprintf("STALE (have %s, want %s)", shortCurrent, shortLatest)
	return shortCurrent, statusStale, detail
}

func statusToColor(status string) string {
	switch status {
	case statusOK:
		return colorGreen
	case statusStale:
		return colorYellow
	case statusCritical:
		return colorRed
	default:
		return colorReset
	}
}

func statusIcon(status string) string {
	switch status {
	case statusOK:
		return "✓"
	case statusStale:
		return "⚠"
	case statusCritical:
		return "✗"
	default:
		return "?"
	}
}

// filterRepos applies include/exclude lists from config.
// Both include and exclude support glob patterns (*, ?, [...]) via path.Match.
// include is applied first (allowlist); exclude then removes matched repos.
func filterRepos(repos []gitea.Repository, include, exclude []string) []gitea.Repository {
	var filtered []gitea.Repository
	for _, r := range repos {
		if matchesAny(r.Name, exclude) {
			continue
		}
		if len(include) > 0 && !matchesAny(r.Name, include) {
			continue
		}
		filtered = append(filtered, r)
	}
	return filtered
}

// matchesAny checks if name matches any pattern in the list.
// Supports exact match and glob patterns (*, ?, [...]).
func matchesAny(name string, patterns []string) bool {
	for _, p := range patterns {
		if p == name {
			return true
		}
		if matched, _ := path.Match(p, name); matched {
			return true
		}
	}
	return false
}

// sortResults sorts scan results by name for deterministic output.
func sortResults(results []repoScanResult) {
	sort.Slice(results, func(i, j int) bool {
		return results[i].Name < results[j].Name
	})
}
