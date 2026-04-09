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
		if c.Breaking || c.Kind == ChangeLogic || c.Kind == ChangeAffected {
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

func splitSymbolKey(key string) (pkg, name string) {
	dotIdx := strings.LastIndex(key, ".")
	if dotIdx != -1 {
		return key[:dotIdx], key[dotIdx+1:]
	}
	return "", key
}

func matchesCallSite(site CallSite, pkgPath, funcName string) bool {
	siteParts := strings.Split(site.FuncName, ".")
	siteFuncName := siteParts[len(siteParts)-1]
	if siteFuncName != funcName {
		return false
	}

	if pkgPath != "" && len(siteParts) >= 2 {
		siteAlias := strings.Join(siteParts[:len(siteParts)-1], ".")
		pkgParts := strings.Split(pkgPath, "/")
		pkgBaseName := pkgParts[len(pkgParts)-1]

		return strings.HasSuffix(siteAlias, pkgBaseName) || 
		       strings.Contains(siteAlias, pkgBaseName) ||
		       siteAlias == pkgBaseName
	}
	return true
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
