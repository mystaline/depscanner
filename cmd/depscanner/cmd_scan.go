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
	SourceName    string            `json:"source_name"`
	Group         string            `json:"group"`
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
	Sources      []string                `json:"sources,omitempty"`
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

	factory := giteaListerFactory()

	// Resolve each consumer provider into a group of repos to scan.
	var consumerSets []repo.Resolved
	for _, c := range cfg.Consumers {
		res, rerr := repo.ResolveProvider(c, cfg.CacheDir, cfg.Offline, factory)
		if rerr != nil {
			return fmt.Errorf("resolve consumer: %w", rerr)
		}
		fmt.Printf("  %s: %d repositories\n", res.Group, len(res.Repos))
		consumerSets = append(consumerSets, res)
	}

	// Resolve each source: local dir/module path + latest hash for staleness.
	var sources []resolvedSource
	for _, s := range cfg.Sources {
		rs, serr := resolveSourceModule(s, cfg, factory)
		if serr != nil {
			return fmt.Errorf("resolve source: %w", serr)
		}
		sources = append(sources, rs)
	}
	fmt.Println()

	// If --check is active, parse signatures for each --func name (single-source only).
	targetSigs := make(map[string]*analysis.FuncSignature)
	if check {
		if len(funcNames) != 1 {
			return fmt.Errorf("--check requires exactly one --func value")
		}
		if len(sources) != 1 {
			return fmt.Errorf("--check requires exactly one source")
		}
		targetOwner, targetRepo := gitea.ParseModuleOwnerRepo(sources[0].module)
		if targetOwner != "" {
			targetMgr := repo.NewManager(cfg.CacheDir, targetOwner)
			targetPath := targetMgr.GetRepoPath(targetRepo)
			if !cfg.Offline {
				if _, statErr := os.Stat(targetPath); statErr != nil {
					fmt.Printf("Syncing target module for signature check...\n")
					_, syncErr := targetMgr.SyncBranch(targetRepo, fmt.Sprintf("%s/%s/%s.git", cfg.Gitea.URL, targetOwner, targetRepo), branch)
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

	multiGroup := len(consumerSets) > 1 || len(sources) > 1

	var allResults []repoScanResult
	var totalGoMod, totalTarget int

	for _, cset := range consumerSets {
		mgr := cset.Mgr

		var (
			mu      sync.Mutex
			results []repoScanResult
			seen    = map[string]bool{} // repoName → counted for go.mod once
		)

		processFn := func(r gitea.Repository, synced bool) {
			repoPath := mgr.GetRepoPath(r.Name)
			repoBranch := cfg.GetBranchForRepo(r.Name, branch)
			countedGoMod := false
			for _, src := range sources {
				res, hasGoMod, usesTarget := scanRepoForSource(repoPath, src.module, src.name, cset.Group, r.Name, repoBranch, src.latestHash, r)
				mu.Lock()
				results = append(results, res)
				if hasGoMod && !countedGoMod {
					countedGoMod = true
				}
				if usesTarget {
					totalTarget++
				}
				mu.Unlock()
			}
			mu.Lock()
			if countedGoMod && !seen[r.Name] {
				seen[r.Name] = true
				totalGoMod++
			}
			mu.Unlock()
		}

		processFnWithBranch := func(r gitea.Repository, _ bool) {
			repoBranch := cfg.GetBranchForRepo(r.Name, branch)
			ok, serr := mgr.SyncBranchQuiet(r.Name, r.CloneURL, repoBranch)
			if serr != nil {
				fmt.Fprintf(os.Stderr, "  warn: sync %s@%s: %v\n", r.Name, repoBranch, serr)
			}
			processFn(r, ok)
		}

		switch {
		case cset.Local:
			for _, r := range cset.Repos {
				processFn(r, true)
			}
		case branch == "":
			mgr.PipelineSyncAndProcess(cset.Repos, noFetch, 0, processFn)
		default:
			mgr.PipelineSyncAndProcess(cset.Repos, noFetch, 0, processFnWithBranch)
		}
		fmt.Println()

		sortResults(results)
		allResults = append(allResults, results...)
	}

	if format == "json" {
		sourceNames := make([]string, len(sources))
		for i, s := range sources {
			sourceNames[i] = s.name
		}
		sigs := make(map[string]int)
		for name, sig := range targetSigs {
			sigs[name] = sig.ParamsCount
		}
		out := scanOutput{
			Repos:       allResults,
			Sources:     sourceNames,
			Branch:      branch,
			FuncNames:   funcNames,
			MethodNames: methodNames,
			TypeNames:   typeNames,
			ConstNames:  constNames,
			VarNames:    varNames,
			Total:       len(allResults),
			GoModCount:  totalGoMod,
			TargetCount: totalTarget,
		}
		if len(sources) == 1 {
			out.TargetModule = sources[0].module
		}
		if len(sigs) > 0 {
			out.Signatures = sigs
		}
		return json.NewEncoder(os.Stdout).Encode(out)
	}

	printGroupedResults(allResults, sources, multiGroup, targetSigs)
	fmt.Printf("\nSummary: %d/%d Go repos depend on a source module\n", totalTarget, totalGoMod)
	return nil
}

// resolvedSource is a source module ready to scan against: its display name,
// resolved module path, and (when branch tracking is active) the latest
// commit hash on the tracked branch for staleness comparisons.
type resolvedSource struct {
	name       string
	module     string
	latestHash string
}

// resolveSourceModule ensures the source repo is available and determines
// its module path (reading go.mod when config leaves it unset) plus the
// latest branch commit hash for staleness detection.
func resolveSourceModule(s config.Source, cfg *config.Config, factory repo.ListerFactory) (rs resolvedSource, err error) {
	res, rerr := repo.ResolveProvider(s.Provider, cfg.CacheDir, cfg.Offline, factory)
	if rerr != nil {
		return rs, rerr
	}
	rs.name = s.Name
	if rs.name == "" {
		rs.name = res.Group
	}

	module := s.Module
	repoName := res.Repos[0].Name
	repoPath := res.Mgr.GetRepoPath(repoName)

	// Ensure the source repo is present so we can read go.mod when module is unset.
	if module == "" {
		if !res.Local {
			if _, statErr := os.Stat(filepath.Join(repoPath, ".git")); os.IsNotExist(statErr) {
				if cfg.Offline {
					return rs, fmt.Errorf("source %q not cached and offline", rs.name)
				}
				if serr := res.Mgr.SyncRepos(res.Repos, false); serr != nil {
					return rs, fmt.Errorf("sync source %q: %w", rs.name, serr)
				}
			}
		}
		m, merr := analysis.ReadModulePath(filepath.Join(repoPath, "go.mod"))
		if merr != nil {
			return rs, fmt.Errorf("read module path for %q: %w", rs.name, merr)
		}
		module = m
	}
	rs.module = module

	// Staleness (branch mode, gitea sources only).
	if branch != "" && !cfg.Offline && s.Gitea != nil {
		targetBranch := cfg.BranchTracking[branch]
		if targetBranch == "" {
			targetBranch = branch
		}
		owner, repoN := gitea.ParseModuleOwnerRepo(module)
		if owner != "" {
			if h, herr := gitea.NewClient(s.Gitea.URL, s.Gitea.Token).GetBranchCommitHash(owner, repoN, targetBranch); herr == nil {
				rs.latestHash = h
			}
		}
	}
	return rs, nil
}

// giteaListerFactory builds a repo.ListerFactory backed by *gitea.Client.
func giteaListerFactory() repo.ListerFactory {
	return func(u, t string) repo.Lister { return gitea.NewClient(u, t) }
}

// scanRepoForSource runs go.mod detection + symbol/type scans for one repo
// against one source module. ok is false when the repo has no go.mod or does
// not require the module.
func scanRepoForSource(repoPath, moduleParam, sourceName, group, repoName, repoBranch, latestTargetHash string, r gitea.Repository) (res repoScanResult, hasGoMod, usesTarget bool) {
	goModPath := filepath.Join(repoPath, "go.mod")
	if _, err := os.Stat(goModPath); err != nil {
		return repoScanResult{Name: repoName, SourceName: sourceName, Group: group, Branch: repoBranch, HasGoMod: false, CloneURL: r.CloneURL}, false, false
	}
	hasGoMod = true

	var targetVersion, commitHash, status, statusDetail string
	info, perr := analysis.ParseGoMod(goModPath, moduleParam)
	if perr != nil {
		fmt.Fprintf(os.Stderr, "  warn: parse go.mod for %s: %v\n", repoName, perr)
	} else if info.Found {
		usesTarget = true
		targetVersion = info.Version
		if branch != "" && latestTargetHash != "" {
			commitHash, status, statusDetail = detectStaleness(info.Version, latestTargetHash)
		}
	}

	var pkgs []string
	if packages && usesTarget {
		var scanErr error
		if pkgs, scanErr = analysis.ScanImports(repoPath, moduleParam); scanErr != nil {
			fmt.Fprintf(os.Stderr, "  warn: scan imports for %s: %v\n", repoName, scanErr)
		}
	}

	res = repoScanResult{
		Name:          repoName,
		SourceName:    sourceName,
		Group:         group,
		Branch:        repoBranch,
		HasGoMod:      hasGoMod,
		UsesTarget:    usesTarget,
		Packages:      pkgs,
		TargetVersion: targetVersion,
		CommitHash:    commitHash,
		LatestHash:    shortenHash(latestTargetHash),
		Status:        status,
		StatusDetail:  statusDetail,
		CloneURL:      r.CloneURL,
	}
	if !usesTarget {
		return res, hasGoMod, false
	}

	res.CallSites = collectCallSites(repoPath, moduleParam, funcNames, repoName)
	res.MethodSites = collectCallSites(repoPath, moduleParam, methodNames, repoName)
	res.TypeRefs = collectTypeRefs(repoPath, moduleParam, typeNames)
	res.ConstRefs = collectSymbolRefs(repoPath, moduleParam, constNames, repoName)
	res.VarRefs = collectSymbolRefs(repoPath, moduleParam, varNames, repoName)
	return res, hasGoMod, true
}

func collectCallSites(repoPath, moduleParam string, names []string, repoName string) []callSiteResult {
	var out []callSiteResult
	for _, name := range names {
		sites, warnings, err := analysis.ScanSymbolReferences(repoPath, moduleParam, name)
		if err != nil {
			fmt.Fprintf(os.Stderr, "  warn: scan %s in %s: %v\n", name, repoName, err)
		}
		for _, w := range warnings {
			fmt.Fprintf(os.Stderr, "  warn: %s: %s\n", repoName, w)
		}
		for _, s := range sites {
			out = append(out, callSiteResult{SearchedFor: name, File: s.File, Line: s.Line, Column: s.Column, FuncName: s.FuncName, RawName: s.RawName, ArgCount: s.ArgCount})
		}
	}
	return out
}

func collectSymbolRefs(repoPath, moduleParam string, names []string, repoName string) []symbolRefResult {
	var out []symbolRefResult
	for _, name := range names {
		sites, warnings, err := analysis.ScanSymbolReferences(repoPath, moduleParam, name)
		if err != nil {
			fmt.Fprintf(os.Stderr, "  warn: scan %s in %s: %v\n", name, repoName, err)
		}
		for _, w := range warnings {
			fmt.Fprintf(os.Stderr, "  warn: %s: %s\n", repoName, w)
		}
		for _, s := range sites {
			out = append(out, symbolRefResult{SearchedFor: name, File: s.File, Line: s.Line, Column: s.Column, Name: s.FuncName, RawName: s.RawName})
		}
	}
	return out
}

func collectTypeRefs(repoPath, moduleParam string, names []string) []typeRefResult {
	var out []typeRefResult
	for _, name := range names {
		refs, err := analysis.ScanTypeReferences(repoPath, moduleParam, name)
		if err != nil {
			fmt.Fprintf(os.Stderr, "  warn: scan type %s: %v\n", name, err)
		}
		for _, ref := range refs {
			out = append(out, typeRefResult{SearchedFor: name, File: ref.File, Line: ref.Line, TypeName: ref.TypeName, RawName: ref.RawName, Context: ref.Context})
		}
	}
	return out
}

// printGroupedResults prints the table-format scan output, grouped by
// consumer group and (when more than one) source module.
//
// ponytail: minimal port of the old single-source table printer, grouped by
// (Group, SourceName); Task 7 owns the real grouped-table design.
func printGroupedResults(allResults []repoScanResult, sources []resolvedSource, multiGroup bool, targetSigs map[string]*analysis.FuncSignature) {
	type groupKey struct{ group, source string }
	order := []groupKey{}
	byGroup := map[groupKey][]repoScanResult{}
	for _, r := range allResults {
		k := groupKey{r.Group, r.SourceName}
		if _, ok := byGroup[k]; !ok {
			order = append(order, k)
		}
		byGroup[k] = append(byGroup[k], r)
	}

	moduleFor := map[string]string{}
	for _, s := range sources {
		moduleFor[s.name] = s.module
	}

	for _, k := range order {
		results := byGroup[k]
		targetCount := 0
		for _, r := range results {
			if r.UsesTarget {
				targetCount++
			}
		}

		if multiGroup {
			fmt.Printf("--- %s / %s ---\n", k.group, k.source)
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

		module := moduleFor[k.source]

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
			printCallSites("Call sites", name, targetCount, module, results, targetSigs,
				func(r repoScanResult) []callSiteResult { return r.CallSites })
		}
		for _, name := range methodNames {
			printCallSites("Method call sites", name, targetCount, module, results, nil,
				func(r repoScanResult) []callSiteResult { return r.MethodSites })
		}
		for _, name := range typeNames {
			printTypeRefs(name, targetCount, module, results)
		}
		for _, name := range constNames {
			printSymbolRefs("Const references", name, targetCount, module, results, func(r repoScanResult) []symbolRefResult { return r.ConstRefs })
		}
		for _, name := range varNames {
			printSymbolRefs("Var references", name, targetCount, module, results, func(r repoScanResult) []symbolRefResult { return r.VarRefs })
		}
	}
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
