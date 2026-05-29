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


// staleness levels for branch-aware scanning.
const (
	statusOK       = "ok"
	statusStale    = "stale"
	statusCritical = "critical" // commit not on expected branch
	statusUnknown  = "unknown"
)

// callSiteResult is the JSON-serializable representation of a function or method call site.
type callSiteResult struct {
	SearchedFor string `json:"searched_for"`
	File        string `json:"file"`
	Line        int    `json:"line"`
	Column      int    `json:"column"`
	FuncName    string `json:"func_name"`
	RawName     string `json:"raw_name"`
	ArgCount    int    `json:"arg_count"`
}

// typeRefResult is the JSON-serializable representation of a type reference.
type typeRefResult struct {
	SearchedFor string `json:"searched_for"`
	File        string `json:"file"`
	Line        int    `json:"line"`
	TypeName    string `json:"type_name"`
	RawName     string `json:"raw_name"`
	Context     string `json:"context"`
}

// symbolRefResult is the JSON-serializable representation of a const or var reference.
type symbolRefResult struct {
	SearchedFor string `json:"searched_for"`
	File        string `json:"file"`
	Line        int    `json:"line"`
	Column      int    `json:"column"`
	Name        string `json:"name"`
	RawName     string `json:"raw_name"`
}

type repoScanResult struct {
	Name          string            `json:"name"`
	Branch        string            `json:"branch,omitempty"`
	HasGoMod      bool              `json:"has_go_mod"`
	UsesTarget    bool              `json:"uses_target"`
	TargetVersion string            `json:"target_version,omitempty"`
	CommitHash    string            `json:"commit_hash,omitempty"`
	LatestHash    string            `json:"latest_hash,omitempty"`
	Status        string            `json:"status,omitempty"`
	StatusDetail  string            `json:"status_detail,omitempty"`
	Packages      []string          `json:"packages,omitempty"`
	CallSites     []callSiteResult  `json:"call_sites,omitempty"`
	MethodSites   []callSiteResult  `json:"method_sites,omitempty"`
	TypeRefs      []typeRefResult   `json:"type_refs,omitempty"`
	ConstRefs     []symbolRefResult `json:"const_refs,omitempty"`
	VarRefs       []symbolRefResult `json:"var_refs,omitempty"`
	CloneURL      string            `json:"clone_url,omitempty"`
}

type scanOutput struct {
	Repos        []repoScanResult        `json:"repos"`
	TargetModule string                  `json:"target_module"`
	Branch       string                  `json:"branch,omitempty"`
	FuncNames    []string                `json:"func_names,omitempty"`
	MethodNames  []string                `json:"method_names,omitempty"`
	TypeNames    []string                `json:"type_names,omitempty"`
	ConstNames   []string                `json:"const_names,omitempty"`
	VarNames     []string                `json:"var_names,omitempty"`
	Signatures   map[string]int          `json:"signatures,omitempty"` // funcName → param count
	Total        int                     `json:"total"`
	GoModCount   int                     `json:"go_mod_count"`
	TargetCount  int                     `json:"target_count"`
}

func newScanCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "scan",
		Short: "List all org repos and detect shared library usage and staleness",
		RunE:  runScan,
	}

	cmd.Flags().BoolVar(&packages, "packages", false, "show which sub-packages of the target module are imported")
	cmd.Flags().StringSliceVar(&funcNames, "func", nil, "show call sites for functions, comma-separated (e.g. \"Must,helper.Process\")")
	cmd.Flags().StringSliceVar(&methodNames, "method", nil, "show call sites for methods, comma-separated (e.g. \"Client.Do,Conn.Close\")")
	cmd.Flags().StringSliceVar(&typeNames, "type", nil, "show references to types or interfaces, comma-separated (e.g. \"Logger,Config\")")
	cmd.Flags().StringSliceVar(&constNames, "const", nil, "show references to constants, comma-separated (e.g. \"ErrNotFound,StatusOK\")")
	cmd.Flags().StringSliceVar(&varNames, "var", nil, "show references to package-level variables, comma-separated (e.g. \"DefaultClient\")")
	cmd.Flags().BoolVar(&check, "check", false, "validate arg counts against target module signatures (requires exactly one --func)")
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

	type orgScanSet struct {
		orgName string
		mgr     *repo.Manager
		repos   []gitea.Repository
	}

	activeOrgs := cfg.ActiveOrgs()
	var orgSets []orgScanSet

	for _, orgCfg := range activeOrgs {
		orgMgr := repo.NewManager(cfg.CacheDir, orgCfg.Name)
		var orgRepos []gitea.Repository

		if cfg.Offline {
			noFetch = true
			fmt.Printf("Listing repositories from local cache: %s\n", orgMgr.GetOrgPath())
			var lerr error
			orgRepos, lerr = orgMgr.ListLocalRepos()
			if lerr != nil {
				return fmt.Errorf("list local repos for %s: %w", orgCfg.Name, lerr)
			}
		} else {
			giteaClient := gitea.NewClient(cfg.Gitea.URL, cfg.Gitea.Token)
			fmt.Printf("Fetching repository list for %s from %s...\n", orgCfg.Name, cfg.Gitea.URL)
			var lerr error
			orgRepos, lerr = giteaClient.ListOrgRepos(orgCfg.Name)
			if lerr != nil {
				return fmt.Errorf("list repos for %s: %w", orgCfg.Name, lerr)
			}
		}

		orgRepos = filterRepos(orgRepos, orgCfg.IncludeRepos, orgCfg.ExcludeRepos)
		fmt.Printf("  %s: %d repositories\n", orgCfg.Name, len(orgRepos))
		orgSets = append(orgSets, orgScanSet{orgName: orgCfg.Name, mgr: orgMgr, repos: orgRepos})
	}
	fmt.Println()

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

	// If --check is active, parse signatures for each --func name.
	targetSigs := make(map[string]*analysis.FuncSignature)
	if check {
		if len(funcNames) != 1 {
			return fmt.Errorf("--check requires exactly one --func value")
		}
		targetOwner, targetRepo := gitea.ParseModuleOwnerRepo(cfg.TargetModule)
		if targetOwner != "" {
			targetMgr := repo.NewManager(cfg.CacheDir, targetOwner)
			targetPath := targetMgr.GetRepoPath(targetRepo)
			if !cfg.Offline {
				if _, statErr := os.Stat(targetPath); statErr != nil {
					fmt.Printf("Syncing target module for signature check...\n")
					_, syncErr := targetMgr.SyncBranch(targetRepo, fmt.Sprintf("%s/%s/%s.git", cfg.Gitea.URL, targetOwner, targetRepo), targetBranch)
					if syncErr != nil {
						fmt.Fprintf(os.Stderr, "  warn: failed to sync target module: %v\n", syncErr)
					}
				}
			}
			if _, statErr := os.Stat(targetPath); statErr == nil {
				for _, name := range funcNames {
					sig, sigErr := analysis.ParseSignature(targetPath, name)
					if sigErr != nil {
						fmt.Fprintf(os.Stderr, "  warn: could not parse signature for %q: %v\n", name, sigErr)
						continue
					}
					targetSigs[name] = sig
					variadic := ""
					if sig.IsVariadic {
						variadic = "+"
					}
					fmt.Printf("Parsed signature for %q: %d params%s\n", name, sig.ParamsCount, variadic)
				}
				fmt.Println()
			} else if cfg.Offline {
				fmt.Fprintf(os.Stderr, "  warn: target module %q not found in cache (%s), cannot check signatures offline\n", targetRepo, targetPath)
			}
		}
	}

	multiOrg := len(orgSets) > 1

	// Accumulate totals across orgs for the final summary.
	var totalGoMod, totalTarget int

	for _, orgSet := range orgSets {
		currentMgr := orgSet.mgr

		var (
			mu          sync.Mutex
			results     []repoScanResult
			goModCount  int
			targetCount int
		)

		processFn := func(r gitea.Repository, synced bool) {
			repoPath := currentMgr.GetRepoPath(r.Name)
			goModPath := filepath.Join(repoPath, "go.mod")
			_, statErr := os.Stat(goModPath)
			hasGoMod := statErr == nil

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

					if branch != "" && latestTargetHash != "" {
						commitHash, status, statusDetail = detectStaleness(info.Version, latestTargetHash)
					}
				}
			}

			var pkgs []string
			if packages && usesTarget {
				var scanErr error
				pkgs, scanErr = analysis.ScanImports(repoPath, cfg.TargetModule)
				if scanErr != nil {
					fmt.Fprintf(os.Stderr, "  warn: scan imports for %s: %v\n", r.Name, scanErr)
				}
			}

			var callSites []callSiteResult
			if len(funcNames) > 0 && usesTarget {
				for _, name := range funcNames {
					sites, warnings, scanErr := analysis.ScanSymbolReferences(repoPath, cfg.TargetModule, name)
					if scanErr != nil {
						fmt.Fprintf(os.Stderr, "  warn: scan call sites for %s: %v\n", r.Name, scanErr)
					}
					for _, w := range warnings {
						fmt.Fprintf(os.Stderr, "  warn: %s: %s\n", r.Name, w)
					}
					for _, s := range sites {
						callSites = append(callSites, callSiteResult{
							SearchedFor: name,
							File:        s.File,
							Line:        s.Line,
							Column:      s.Column,
							FuncName:    s.FuncName,
							RawName:     s.RawName,
							ArgCount:    s.ArgCount,
						})
					}
				}
			}

			var methodSites []callSiteResult
			if len(methodNames) > 0 && usesTarget {
				for _, name := range methodNames {
					sites, warnings, scanErr := analysis.ScanSymbolReferences(repoPath, cfg.TargetModule, name)
					if scanErr != nil {
						fmt.Fprintf(os.Stderr, "  warn: scan method sites for %s: %v\n", r.Name, scanErr)
					}
					for _, w := range warnings {
						fmt.Fprintf(os.Stderr, "  warn: %s: %s\n", r.Name, w)
					}
					for _, s := range sites {
						methodSites = append(methodSites, callSiteResult{
							SearchedFor: name,
							File:        s.File,
							Line:        s.Line,
							Column:      s.Column,
							FuncName:    s.FuncName,
							RawName:     s.RawName,
							ArgCount:    s.ArgCount,
						})
					}
				}
			}

			var typeRefs []typeRefResult
			if len(typeNames) > 0 && usesTarget {
				for _, name := range typeNames {
					refs, scanErr := analysis.ScanTypeReferences(repoPath, cfg.TargetModule, name)
					if scanErr != nil {
						fmt.Fprintf(os.Stderr, "  warn: scan type refs for %s: %v\n", r.Name, scanErr)
					}
					for _, ref := range refs {
						typeRefs = append(typeRefs, typeRefResult{
							SearchedFor: name,
							File:        ref.File,
							Line:        ref.Line,
							TypeName:    ref.TypeName,
							RawName:     ref.RawName,
							Context:     ref.Context,
						})
					}
				}
			}

			var constRefs []symbolRefResult
			if len(constNames) > 0 && usesTarget {
				for _, name := range constNames {
					sites, warnings, scanErr := analysis.ScanSymbolReferences(repoPath, cfg.TargetModule, name)
					if scanErr != nil {
						fmt.Fprintf(os.Stderr, "  warn: scan const refs for %s: %v\n", r.Name, scanErr)
					}
					for _, w := range warnings {
						fmt.Fprintf(os.Stderr, "  warn: %s: %s\n", r.Name, w)
					}
					for _, s := range sites {
						constRefs = append(constRefs, symbolRefResult{
							SearchedFor: name,
							File:        s.File,
							Line:        s.Line,
							Column:      s.Column,
							Name:        s.FuncName,
							RawName:     s.RawName,
						})
					}
				}
			}

			var varRefs []symbolRefResult
			if len(varNames) > 0 && usesTarget {
				for _, name := range varNames {
					sites, warnings, scanErr := analysis.ScanSymbolReferences(repoPath, cfg.TargetModule, name)
					if scanErr != nil {
						fmt.Fprintf(os.Stderr, "  warn: scan var refs for %s: %v\n", r.Name, scanErr)
					}
					for _, w := range warnings {
						fmt.Fprintf(os.Stderr, "  warn: %s: %s\n", r.Name, w)
					}
					for _, s := range sites {
						varRefs = append(varRefs, symbolRefResult{
							SearchedFor: name,
							File:        s.File,
							Line:        s.Line,
							Column:      s.Column,
							Name:        s.FuncName,
							RawName:     s.RawName,
						})
					}
				}
			}

			result := repoScanResult{
				Name:          r.Name,
				Branch:        targetBranchForRepo,
				HasGoMod:      hasGoMod,
				UsesTarget:    usesTarget,
				Packages:      pkgs,
				CallSites:     callSites,
				MethodSites:   methodSites,
				TypeRefs:      typeRefs,
				ConstRefs:     constRefs,
				VarRefs:       varRefs,
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
			targetBranch := cfg.GetBranchForRepo(r.Name, branch)
			ok, err := currentMgr.SyncBranchQuiet(r.Name, r.CloneURL, targetBranch)
			if err != nil {
				fmt.Fprintf(os.Stderr, "  warn: sync %s@%s: %v\n", r.Name, targetBranch, err)
				processFn(r, false)
				return
			}
			processFn(r, ok)
		}

		if branch == "" {
			currentMgr.PipelineSyncAndProcess(orgSet.repos, noFetch, 4, processFn)
		} else {
			currentMgr.PipelineSyncAndProcess(orgSet.repos, noFetch, 4, processFnWithBranch)
		}
		fmt.Println()

		sortResults(results)
		totalGoMod += goModCount
		totalTarget += targetCount

		if format == "json" {
			sigs := make(map[string]int)
			for name, sig := range targetSigs {
				sigs[name] = sig.ParamsCount
			}
			out := scanOutput{
				Repos:        results,
				TargetModule: cfg.TargetModule,
				Branch:       branch,
				FuncNames:    funcNames,
				MethodNames:  methodNames,
				TypeNames:    typeNames,
				ConstNames:   constNames,
				VarNames:     varNames,
				Total:        len(results),
				GoModCount:   goModCount,
				TargetCount:  targetCount,
			}
			if len(sigs) > 0 {
				out.Signatures = sigs
			}
			if err := json.NewEncoder(os.Stdout).Encode(out); err != nil {
				return err
			}
			continue
		}

		if multiOrg {
			fmt.Printf("--- %s ---\n", orgSet.orgName)
		}

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
					fmt.Fprintf(w, "  %s✗%s\t%s\t%s\t\n", formatter.ColorRed(), formatter.ColorReset(), r.Name, "(no go.mod)")
				} else {
					fmt.Fprintf(w, "  %s✗%s\t%s\t%s\n", formatter.ColorRed(), formatter.ColorReset(), r.Name, "(no go.mod)")
				}
			case !r.UsesTarget:
				if branch != "" {
					fmt.Fprintf(w, "  %s·%s\t%s\t%s\t\n", formatter.ColorRed(), formatter.ColorReset(), r.Name, "(not used)")
				} else {
					fmt.Fprintf(w, "  %s·%s\t%s\t%s\n", formatter.ColorRed(), formatter.ColorReset(), r.Name, "(not used)")
				}
			case branch != "" && r.Status != "":
				versionCol := r.TargetVersion
				if r.CommitHash != "" {
					versionCol = r.CommitHash
				}
				statusColor := statusToColor(r.Status)
				fmt.Fprintf(w, "  %s%s%s\t%s\t%s\t%s%s%s\n",
					statusColor, statusIcon(r.Status), formatter.ColorReset(),
					r.Name, versionCol,
					statusColor, r.StatusDetail, formatter.ColorReset())
			default:
				fmt.Fprintf(w, "  %s✓%s\t%s\t%s\n", formatter.ColorGreen(), formatter.ColorReset(), r.Name, r.TargetVersion)
			}
		}
		w.Flush()

		if packages {
			fmt.Println("\nSub-package usage:")
			for _, r := range results {
				if !r.UsesTarget || len(r.Packages) == 0 {
					continue
				}
				fmt.Printf("  %-35s %s\n", r.Name, strings.Join(r.Packages, ", "))
			}
		}

		for _, name := range funcNames {
			printCallSites("Call sites", name, targetCount, cfg.TargetModule, results, targetSigs,
				func(r repoScanResult) []callSiteResult { return r.CallSites })
		}
		for _, name := range methodNames {
			printCallSites("Method call sites", name, targetCount, cfg.TargetModule, results, nil,
				func(r repoScanResult) []callSiteResult { return r.MethodSites })
		}
		for _, name := range typeNames {
			printTypeRefs(name, targetCount, cfg.TargetModule, results)
		}
		for _, name := range constNames {
			printSymbolRefs("Const references", name, targetCount, cfg.TargetModule, results, func(r repoScanResult) []symbolRefResult { return r.ConstRefs })
		}
		for _, name := range varNames {
			printSymbolRefs("Var references", name, targetCount, cfg.TargetModule, results, func(r repoScanResult) []symbolRefResult { return r.VarRefs })
		}
	}

	fmt.Printf("\nTarget: %s\n", cfg.TargetModule)
	if branch != "" {
		fmt.Printf("Branch: %s -> %s@%s (latest: %s)\n", branch, cfg.TargetModule, targetBranch, shortenHash(latestTargetHash))
	}
	fmt.Printf("Summary: %d/%d Go repos depend on target module\n", totalTarget, totalGoMod)
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
		return formatter.ColorGreen()
	case statusStale:
		return formatter.ColorYellow()
	case statusCritical:
		return formatter.ColorRed()
	default:
		return formatter.ColorReset()
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

func printCallSites(label, name string, targetCount int, targetModule string, results []repoScanResult, sigs map[string]*analysis.FuncSignature, getSites func(repoScanResult) []callSiteResult) {
	fmt.Printf("\n%s for %q:\n", label, name)
	if targetCount == 0 {
		fmt.Printf("  (no repos use target module %s)\n", targetModule)
		return
	}
	sig := sigs[name]
	total, reposWithSites := 0, 0
	for _, r := range results {
		var matching []callSiteResult
		for _, cs := range getSites(r) {
			if cs.SearchedFor == name {
				matching = append(matching, cs)
			}
		}
		if len(matching) == 0 {
			continue
		}
		reposWithSites++
		total += len(matching)
		fmt.Printf("\n  %s%s%s (%d call sites):\n", formatter.ColorGreen(), r.Name, formatter.ColorReset(), len(matching))
		for _, cs := range matching {
			matchStr := ""
			if sig != nil {
				var match bool
				if sig.IsVariadic {
					match = cs.ArgCount >= sig.ParamsCount-1
				} else {
					match = cs.ArgCount == sig.ParamsCount
				}
				if match {
					matchStr = fmt.Sprintf("  %s✓ %d args%s", formatter.ColorGreen(), cs.ArgCount, formatter.ColorReset())
				} else {
					matchStr = fmt.Sprintf("  %s✗ %d args (expected %d)%s", formatter.ColorRed(), cs.ArgCount, sig.ParamsCount, formatter.ColorReset())
				}
			}
			fmt.Printf("    %s:%d  %s%s\n", cs.File, cs.Line, cs.RawName, matchStr)
		}
	}
	if reposWithSites == 0 {
		fmt.Printf("  (no call sites found in %d repos that use target module)\n", targetCount)
	} else {
		fmt.Printf("\n  Total: %d call sites across %d repos\n", total, reposWithSites)
	}
}

func printTypeRefs(name string, targetCount int, targetModule string, results []repoScanResult) {
	fmt.Printf("\nType references for %q:\n", name)
	if targetCount == 0 {
		fmt.Printf("  (no repos use target module %s)\n", targetModule)
		return
	}
	total, reposWithRefs := 0, 0
	for _, r := range results {
		var matching []typeRefResult
		for _, ref := range r.TypeRefs {
			if ref.SearchedFor == name {
				matching = append(matching, ref)
			}
		}
		if len(matching) == 0 {
			continue
		}
		reposWithRefs++
		total += len(matching)
		fmt.Printf("\n  %s%s%s (%d references):\n", formatter.ColorGreen(), r.Name, formatter.ColorReset(), len(matching))
		for _, ref := range matching {
			fmt.Printf("    %s:%d  %s\n", ref.File, ref.Line, ref.RawName)
		}
	}
	if reposWithRefs == 0 {
		fmt.Printf("  (no type references found in %d repos that use target module)\n", targetCount)
	} else {
		fmt.Printf("\n  Total: %d references across %d repos\n", total, reposWithRefs)
	}
}

func printSymbolRefs(label, name string, targetCount int, targetModule string, results []repoScanResult, getRefs func(repoScanResult) []symbolRefResult) {
	fmt.Printf("\n%s for %q:\n", label, name)
	if targetCount == 0 {
		fmt.Printf("  (no repos use target module %s)\n", targetModule)
		return
	}
	total, reposWithRefs := 0, 0
	for _, r := range results {
		var matching []symbolRefResult
		for _, ref := range getRefs(r) {
			if ref.SearchedFor == name {
				matching = append(matching, ref)
			}
		}
		if len(matching) == 0 {
			continue
		}
		reposWithRefs++
		total += len(matching)
		fmt.Printf("\n  %s%s%s (%d references):\n", formatter.ColorGreen(), r.Name, formatter.ColorReset(), len(matching))
		for _, ref := range matching {
			fmt.Printf("    %s:%d  %s\n", ref.File, ref.Line, ref.RawName)
		}
	}
	if reposWithRefs == 0 {
		fmt.Printf("  (no references found in %d repos that use target module)\n", targetCount)
	} else {
		fmt.Printf("\n  Total: %d references across %d repos\n", total, reposWithRefs)
	}
}

// sortResults sorts scan results by name for deterministic output.
func sortResults(results []repoScanResult) {
	sort.Slice(results, func(i, j int) bool {
		return results[i].Name < results[j].Name
	})
}
