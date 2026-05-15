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
	RawName  string `json:"raw_name"`
}

// SymbolImpact describes the impact of a single breaking change on a repo.
type SymbolImpact struct {
	Change SymbolChange `json:"change"`
	Sites  []ImpactSite `json:"sites"`
}

// RepoImpact describes all impacts on a single consumer repo.
type RepoImpact struct {
	RepoName       string         `json:"repo_name"`
	Impacts        []SymbolImpact `json:"impacts"`
	CurrentVersion string         `json:"current_version"`
	BreakingCount  int            `json:"breaking_count"`
	TotalSites     int            `json:"total_sites"`
}

type matchTarget struct {
	pkg      string
	funcName string
	change   SymbolChange
}

// AnalyzeImpact cross-references breaking changes with call sites across repos.
func AnalyzeImpact(changes []SymbolChange, repoCallSites map[string][]CallSite) []RepoImpact {
	var targets []matchTarget
	for _, c := range changes {
		// Report impact for any change that isn't just a simple Addition
		if c.Kind != ChangeAdded {
			pkg, name := splitSymbolKey(c.Symbol)
			targets = append(targets, matchTarget{
				pkg:      pkg,
				funcName: name,
				change:   c,
			})
		}
	}

	if len(targets) == 0 {
		return nil
	}

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
						RawName:  site.RawName,
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

	var result []RepoImpact
	for _, ri := range repoImpacts {
		result = append(result, *ri)
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].BreakingCount != result[j].BreakingCount {
			return result[i].BreakingCount > result[j].BreakingCount
		}
		return result[i].RepoName < result[j].RepoName
	})

	return result
}

// AnalyzeTypeImpact cross-references type/interface/struct changes with type refs
// found in consumer repos. Used alongside AnalyzeImpact for full change coverage.
func AnalyzeTypeImpact(changes []SymbolChange, repoTypeRefs map[string][]TypeRef) []RepoImpact {
	type matchTarget struct {
		pkg    string
		name   string
		change SymbolChange
	}
	var targets []matchTarget
	for _, c := range changes {
		if c.Category != KindStruct && c.Category != KindInterface && c.Category != KindType {
			continue
		}
		if c.Kind == ChangeAdded {
			continue
		}
		_, name := splitQualifiedName(c.Symbol)
		if name == "" {
			continue
		}
		targets = append(targets, matchTarget{
			name:   name,
			change: c,
		})
	}

	if len(targets) == 0 {
		return nil
	}

	repoImpacts := make(map[string]*RepoImpact)
	for repoName, refs := range repoTypeRefs {
		for _, target := range targets {
			var matched []ImpactSite
			for _, ref := range refs {
				_, refName := splitQualifiedName(ref.TypeName)
				if refName == target.name {
					matched = append(matched, ImpactSite{
						File:     ref.File,
						Line:     ref.Line,
						FuncName: ref.TypeName,
						RawName:  ref.RawName + " [" + ref.Context + "]",
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

	var result []RepoImpact
	for _, ri := range repoImpacts {
		result = append(result, *ri)
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].BreakingCount != result[j].BreakingCount {
			return result[i].BreakingCount > result[j].BreakingCount
		}
		return result[i].RepoName < result[j].RepoName
	})

	return result
}

// splitSymbolKey splits a symbol key into package path and symbol name.
// For module-path keys (contain "/"), splits at the first "." after the last "/",
// correctly handling method symbols like "github.com/org/lib/svc.Client.Do"
// -> pkg="github.com/org/lib/svc", name="Client.Do".
// For short keys without "/", falls back to LastIndex to preserve "pkg.Name" behavior.
func splitSymbolKey(key string) (pkg, name string) {
	slashIdx := strings.LastIndex(key, "/")
	if slashIdx != -1 {
		afterSlash := key[slashIdx+1:]
		dotIdx := strings.Index(afterSlash, ".")
		if dotIdx == -1 {
			return key, ""
		}
		split := slashIdx + 1 + dotIdx
		return key[:split], key[split+1:]
	}
	dotIdx := strings.LastIndex(key, ".")
	if dotIdx != -1 {
		return key[:dotIdx], key[dotIdx+1:]
	}
	return "", key
}

// SplitSymbolKey is the exported version of splitSymbolKey.
func SplitSymbolKey(key string) (pkg, name string) {
	return splitSymbolKey(key)
}

func matchesCallSite(site CallSite, pkgPath, symbolName string) bool {
	dotIdx := strings.LastIndex(site.FuncName, ".")
	if dotIdx == -1 {
		return false
	}
	sitePkg := site.FuncName[:dotIdx]
	siteFuncName := site.FuncName[dotIdx+1:]

	// For method symbols like "Client.Do", match against just the method name.
	targetName := symbolName
	if idx := strings.LastIndex(symbolName, "."); idx != -1 {
		targetName = symbolName[idx+1:]
	}

	if siteFuncName != targetName {
		return false
	}

	if pkgPath == "" {
		return true
	}

	// Exact package match — works for both short "pkg" and full module paths.
	if sitePkg == pkgPath {
		return true
	}

	// For module-path targets (pkgPath contains "/"), require exact match only.
	// A non-matching sitePkg means a different package; no heuristic fallback.
	if strings.Contains(pkgPath, "/") {
		return false
	}

	// For short-key targets (no "/" in pkgPath): receiver-qualified heuristic.
	// Only match if the target is a method: symbolName="Client.Do" or
	// pkgPath="pkg.Receiver" (old-style split for non-module keys).
	isMethodTarget := strings.Contains(symbolName, ".") ||
		(strings.Contains(pkgPath, ".") && !strings.Contains(pkgPath, "/"))
	return isMethodTarget
}

func CountTotalImpactedSites(impacts []RepoImpact) int {
	total := 0
	for _, ri := range impacts {
		total += ri.TotalSites
	}
	return total
}

func FormatImpactSummary(impacts []RepoImpact, totalRepos int) string {
	if len(impacts) == 0 {
		return fmt.Sprintf("No repos impacted out of %d scanned", totalRepos)
	}
	totalSites := CountTotalImpactedSites(impacts)
	return fmt.Sprintf("%d repos impacted, %d call sites affected out of %d repos scanned",
		len(impacts), totalSites, totalRepos)
}
