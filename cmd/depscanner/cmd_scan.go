package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
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
	cmd.Flags().StringVar(&branch, "branch", "", "scan a specific branch (enables staleness detection)")
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

	giteaClient := gitea.NewClient(cfg.Gitea.URL, cfg.Gitea.Token)

	fmt.Printf("Fetching repository list from %s...\n", cfg.Gitea.URL)
	repos, err := giteaClient.ListOrgRepos(cfg.Gitea.Org)
	if err != nil {
		return fmt.Errorf("list repos: %w", err)
	}
	repos = filterRepos(repos, cfg.IncludeRepos, cfg.ExcludeRepos)
	fmt.Printf("Found %d repositories\n\n", len(repos))

	mgr := repo.NewManager(cfg.CacheDir, cfg.Gitea.Org)

	// Resolve the target module's latest commit for the tracked branch.
	var latestTargetHash string
	targetBranch := ""
	if branch != "" {
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

	// Sync repos.
	if !noFetch {
		fmt.Println("Syncing repositories...")
		if branch != "" {
			for _, r := range repos {
				_, syncErr := mgr.SyncBranch(r.Name, r.CloneURL, branch)
				if syncErr != nil {
					// Error already printed inline by SyncBranch (ok/FAIL/skip).
					_ = syncErr
				}
			}
		} else {
			if err := mgr.SyncRepos(repos, noFetch); err != nil {
				return fmt.Errorf("sync repos: %w", err)
			}
		}
		fmt.Println()
	}

	// If --check is active, parse the target module's signature.
	var targetSig *analysis.FuncSignature
	if funcName != "" && check {
		targetOwner, targetRepo := gitea.ParseModuleOwnerRepo(cfg.TargetModule)
		if targetOwner != "" {
			targetPath := mgr.GetRepoPath(targetRepo)
			// Ensure it exists locally
			if _, statErr := os.Stat(targetPath); statErr != nil {
				fmt.Printf("Syncing target module for signature check...\n")
				_, syncErr := mgr.SyncBranch(targetRepo, fmt.Sprintf("%s/%s/%s.git", cfg.Gitea.URL, targetOwner, targetRepo), targetBranch)
				if syncErr != nil {
					fmt.Fprintf(os.Stderr, "  warn: failed to sync target module: %v\n", syncErr)
				}
			}

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
		}
	}

	var results []repoScanResult
	goModCount := 0
	targetCount := 0
	for _, r := range repos {
		repoPath := mgr.GetRepoPath(r.Name)
		goModPath := filepath.Join(repoPath, "go.mod")
		_, statErr := os.Stat(goModPath)
		hasGoMod := statErr == nil

		var usesTarget bool
		var targetVersion, commitHash, status, statusDetail string
		if hasGoMod {
			goModCount++
			info, parseErr := analysis.ParseGoMod(goModPath, cfg.TargetModule)
			if parseErr != nil {
				fmt.Fprintf(os.Stderr, "  warn: parse go.mod for %s: %v\n", r.Name, parseErr)
			} else if info.Found {
				usesTarget = true
				targetVersion = info.Version
				targetCount++

				// Staleness detection when --branch is active.
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
					ArgCount: s.ArgCount,
				})
			}
		}

		results = append(results, repoScanResult{
			Name:          r.Name,
			Branch:        branch,
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
		})
	}

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
					fmt.Printf("    %s:%d  %s%s\n", cs.File, cs.Line, cs.FuncName, matchStr)
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

func shortenHash(hash string) string {
	if len(hash) > 12 {
		return hash[:12]
	}
	return hash
}

// filterRepos applies include/exclude lists from config.
// include is applied first (allowlist); exclude then removes specific repos.
func filterRepos(repos []gitea.Repository, include, exclude []string) []gitea.Repository {
	excludeSet := make(map[string]struct{}, len(exclude))
	for _, e := range exclude {
		excludeSet[e] = struct{}{}
	}
	includeSet := make(map[string]struct{}, len(include))
	for _, i := range include {
		includeSet[i] = struct{}{}
	}

	var filtered []gitea.Repository
	for _, r := range repos {
		if _, excluded := excludeSet[r.Name]; excluded {
			continue
		}
		if len(includeSet) > 0 {
			if _, included := includeSet[r.Name]; !included {
				continue
			}
		}
		filtered = append(filtered, r)
	}
	return filtered
}
