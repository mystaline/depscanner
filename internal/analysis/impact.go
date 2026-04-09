package analysis

import (
	"fmt"
	"sort"
	"strings"
)

// ImpactSite represents a single location affected by a breaking change.
type ImpactSite struct {
	File     string `json:"file"`
	Line     int    `json:"line"`
	FuncName string `json:"func_name"`
}

// SymbolImpact describes the impact of a single breaking change on a repo.
type SymbolImpact struct {
	Change SymbolChange `json:"change"`
	Sites  []ImpactSite `json:"sites"`
}

// RepoImpact describes all impacts on a single consumer repo.
type RepoImpact struct {
	RepoName      string         `json:"repo_name"`
	Impacts       []SymbolImpact `json:"impacts"`
	BreakingCount int            `json:"breaking_count"`
	TotalSites    int            `json:"total_sites"`
}

// AnalyzeImpact cross-references breaking changes with call sites across repos.
// For each breaking change, it searches for call sites of the affected symbol
// in all consumer repos and produces a per-repo impact report.
//
// Parameters:
//   - changes: the diff result from DiffSymbols (Phase 5)
//   - repoCallSites: map of repo name -> list of CallSite found via ScanCallSites (Phase 4)
//   - targetModule: the target module path for context
func AnalyzeImpact(changes []SymbolChange, repoCallSites map[string][]CallSite) []RepoImpact {
	// Filter to breaking changes only.
	var breaking []SymbolChange
	for _, c := range changes {
		if c.Breaking {
			breaking = append(breaking, c)
		}
	}

	if len(breaking) == 0 {
		return nil
	}

	// Build a lookup: for each breaking change, extract the function/symbol
	// names that we need to search for in call sites.
	// A symbol key like "helper.Must" means we look for call sites where
	// FuncName contains "Must" from the "helper" package.
	type matchTarget struct {
		pkg      string // e.g. "helper"
		funcName string // e.g. "Must"
		change   SymbolChange
	}

	var targets []matchTarget
	for _, c := range breaking {
		pkg, name := splitSymbolKey(c.Symbol)
		targets = append(targets, matchTarget{
			pkg:      pkg,
			funcName: name,
			change:   c,
		})
	}

	// For each repo, find which call sites match breaking changes.
	repoImpacts := make(map[string]*RepoImpact)

	for repoName, sites := range repoCallSites {
		for _, target := range targets {
			var matched []ImpactSite
			for _, site := range sites {
				if matchesCallSite(site, target.pkg, target.funcName) {
					matched = append(matched, ImpactSite{
						File:     site.File,
						Line:     site.Line,
						FuncName: site.FuncName,
					})
				}
			}

			if len(matched) == 0 {
				continue
			}

			ri, exists := repoImpacts[repoName]
			if !exists {
				ri = &RepoImpact{RepoName: repoName}
				repoImpacts[repoName] = ri
			}

			ri.Impacts = append(ri.Impacts, SymbolImpact{
				Change: target.change,
				Sites:  matched,
			})
			ri.BreakingCount++
			ri.TotalSites += len(matched)
		}
	}

	// Convert map to sorted slice.
	var result []RepoImpact
	for _, ri := range repoImpacts {
		result = append(result, *ri)
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].BreakingCount != result[j].BreakingCount {
			return result[i].BreakingCount > result[j].BreakingCount // most impacted first
		}
		return result[i].RepoName < result[j].RepoName
	})

	return result
}

// splitSymbolKey splits "helper.Must" into ("helper", "Must"),
// "helper.Service.Init" into ("helper", "Init"),
// and "Must" into ("", "Must").
func splitSymbolKey(key string) (pkg, name string) {
	parts := strings.Split(key, ".")
	if len(parts) >= 2 {
		return parts[0], parts[len(parts)-1]
	}
	return "", key
}

// matchesCallSite checks if a call site matches a target symbol.
// The call site FuncName is like "helper.Must" or "util.NewService".
func matchesCallSite(site CallSite, pkg, funcName string) bool {
	// Extract the function name from the call site's resolved name.
	// site.FuncName can be "helper.Must", "util_helper.Must", or just "Must" (dot import).
	siteParts := strings.Split(site.FuncName, ".")
	siteFuncName := siteParts[len(siteParts)-1]

	if siteFuncName != funcName {
		return false
	}

	// If we have a package qualifier, check if the call site's package matches.
	// We do a suffix match because import aliases can vary
	// (e.g. "helper" might be aliased as "util_helper").
	if pkg != "" && len(siteParts) >= 2 {
		siteAlias := strings.Join(siteParts[:len(siteParts)-1], ".")
		// The alias might contain the package name as a suffix.
		return strings.HasSuffix(siteAlias, pkg) ||
			strings.Contains(siteAlias, pkg)
	}

	return true
}

// CountTotalImpactedSites returns the total number of call sites affected.
func CountTotalImpactedSites(impacts []RepoImpact) int {
	total := 0
	for _, ri := range impacts {
		total += ri.TotalSites
	}
	return total
}

// FormatImpactSummary returns a one-line summary of the impact analysis.
func FormatImpactSummary(impacts []RepoImpact, totalRepos int) string {
	if len(impacts) == 0 {
		return fmt.Sprintf("No repos impacted out of %d scanned", totalRepos)
	}
	totalSites := CountTotalImpactedSites(impacts)
	return fmt.Sprintf("%d repos impacted, %d call sites affected out of %d repos scanned",
		len(impacts), totalSites, totalRepos)
}
