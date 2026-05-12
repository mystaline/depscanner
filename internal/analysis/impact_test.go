package analysis

import (
	"testing"
)

func TestAnalyzeImpactNoBreakingChanges(t *testing.T) {
	changes := []SymbolChange{
		{
			Kind:     ChangeAdded,
			Symbol:   "pkg.NewFunc",
			Breaking: false,
		},
	}
	repoCallSites := map[string][]CallSite{
		"consumer-app": {{FuncName: "util.Process", RawName: "util.Process", File: "main.go", Line: 42}},
	}

	impacts := AnalyzeImpact(changes, repoCallSites)

	if len(impacts) != 0 {
		t.Errorf("AnalyzeImpact with no breaking changes should return 0 impacts, got %d", len(impacts))
	}
}

func TestAnalyzeImpactNoCallSites(t *testing.T) {
	changes := []SymbolChange{
		{
			Kind:     ChangeRemoved,
			Symbol:   "pkg.OldFunc",
			Breaking: true,
		},
	}
	repoCallSites := map[string][]CallSite{
		"consumer-app": {}, // No call sites
	}

	impacts := AnalyzeImpact(changes, repoCallSites)

	if len(impacts) != 0 {
		t.Errorf("AnalyzeImpact with no matching call sites should return 0 impacts, got %d", len(impacts))
	}
}

func TestAnalyzeImpactSingleRepo(t *testing.T) {
	changes := []SymbolChange{
		{
			Kind:     ChangeRemoved,
			Symbol:   "pkg.OldFunc",
			Breaking: true,
		},
	}
	repoCallSites := map[string][]CallSite{
		"consumer-app": {
			{FuncName: "pkg.OldFunc", RawName: "pkg.OldFunc", File: "handler.go", Line: 42},
			{FuncName: "pkg.OldFunc", RawName: "pkg.OldFunc", File: "main.go", Line: 10},
		},
	}

	impacts := AnalyzeImpact(changes, repoCallSites)

	if len(impacts) != 1 {
		t.Fatalf("expected 1 repo impact, got %d", len(impacts))
	}
	if impacts[0].RepoName != "consumer-app" {
		t.Errorf("RepoName = %q, want \"consumer-app\"", impacts[0].RepoName)
	}
	if len(impacts[0].Impacts) != 1 {
		t.Errorf("expected 1 symbol impact, got %d", len(impacts[0].Impacts))
	}
	// BreakingCount = 1 (one breaking change)
	// TotalSites = 2 (two call sites for that change)
	if impacts[0].BreakingCount != 1 {
		t.Errorf("BreakingCount = %d, want 1", impacts[0].BreakingCount)
	}
	if impacts[0].TotalSites != 2 {
		t.Errorf("TotalSites = %d, want 2", impacts[0].TotalSites)
	}
}

func TestAnalyzeImpactMultipleRepos(t *testing.T) {
	changes := []SymbolChange{
		{
			Kind:     ChangeSignature,
			Symbol:   "util.Process",
			Breaking: true,
		},
	}
	repoCallSites := map[string][]CallSite{
		"app1": {
			{FuncName: "util.Process", RawName: "util.Process", File: "a.go", Line: 10},
		},
		"app2": {
			{FuncName: "util.Process", RawName: "util.Process", File: "b.go", Line: 20},
		},
	}

	impacts := AnalyzeImpact(changes, repoCallSites)

	if len(impacts) != 2 {
		t.Fatalf("expected 2 repo impacts, got %d", len(impacts))
	}
}

func TestAnalyzeImpactSortedByBreakingCount(t *testing.T) {
	changes := []SymbolChange{
		{Kind: ChangeRemoved, Symbol: "pkg.A", Breaking: true},
		{Kind: ChangeRemoved, Symbol: "pkg.B", Breaking: true},
	}
	repoCallSites := map[string][]CallSite{
		"app1": {
			{FuncName: "pkg.A", RawName: "pkg.A", File: "a.go", Line: 1},
		},
		"app2": {
			{FuncName: "pkg.A", RawName: "pkg.A", File: "a.go", Line: 2},
			{FuncName: "pkg.A", RawName: "pkg.A", File: "a.go", Line: 3},
			{FuncName: "pkg.B", RawName: "pkg.B", File: "b.go", Line: 4},
		},
	}

	impacts := AnalyzeImpact(changes, repoCallSites)

	if len(impacts) != 2 {
		t.Fatalf("expected 2 impacts, got %d", len(impacts))
	}
	// app2 has 2 breaking changes (A and B), app1 has 1 (A)
	if impacts[0].RepoName != "app2" {
		t.Errorf("first impact RepoName = %q, want \"app2\" (higher breaking count)", impacts[0].RepoName)
	}
	if impacts[0].BreakingCount != 2 {
		t.Errorf("first impact BreakingCount = %d, want 2 (two changes matched)", impacts[0].BreakingCount)
	}
}

func TestAnalyzeImpactSortedByNameWhenEqual(t *testing.T) {
	changes := []SymbolChange{
		{Kind: ChangeRemoved, Symbol: "pkg.Func", Breaking: true},
	}
	repoCallSites := map[string][]CallSite{
		"zebra": {
			{FuncName: "pkg.Func", RawName: "pkg.Func", File: "a.go", Line: 1},
		},
		"apple": {
			{FuncName: "pkg.Func", RawName: "pkg.Func", File: "a.go", Line: 1},
		},
	}

	impacts := AnalyzeImpact(changes, repoCallSites)

	if len(impacts) != 2 {
		t.Fatalf("expected 2 impacts, got %d", len(impacts))
	}
	// Both have same breaking count, should sort by name
	if impacts[0].RepoName != "apple" {
		t.Errorf("first impact RepoName = %q, want \"apple\" (alphabetical)", impacts[0].RepoName)
	}
	if impacts[1].RepoName != "zebra" {
		t.Errorf("second impact RepoName = %q, want \"zebra\"", impacts[1].RepoName)
	}
}

func TestMatchesCallSiteBasic(t *testing.T) {
	tests := []struct {
		name        string
		site        CallSite
		pkgPath     string
		funcName    string
		shouldMatch bool
	}{
		{
			name:        "exact package alias match",
			site:        CallSite{FuncName: "pkg/util.Process"},
			pkgPath:     "pkg/util",
			funcName:    "Process",
			shouldMatch: true,
		},
		{
			name:        "function name mismatch",
			site:        CallSite{FuncName: "pkg/util.Process"},
			pkgPath:     "pkg/util",
			funcName:    "Other",
			shouldMatch: false,
		},
		{
			name:        "package base name match",
			site:        CallSite{FuncName: "pkg/helper.Run"},
			pkgPath:     "pkg/helper",
			funcName:    "Run",
			shouldMatch: true,
		},
		{
			name:        "no package info provided",
			site:        CallSite{FuncName: "some/pkg.Run"},
			pkgPath:     "",
			funcName:    "Run",
			shouldMatch: true,
		},
		{
			name:        "nested package path",
			site:        CallSite{FuncName: "myapp/internal/core.Process"},
			pkgPath:     "myapp/internal/core",
			funcName:    "Process",
			shouldMatch: true,
		},
		{
			name:        "package alias mismatch",
			site:        CallSite{FuncName: "pkg/util.Process"},
			pkgPath:     "pkg/utils",
			funcName:    "Process",
			shouldMatch: false,
		},
		{
			name:        "full domain path with hyphen",
			site:        CallSite{FuncName: "gitea.my-org.net/pkg.Func"},
			pkgPath:     "gitea.my-org.net/pkg",
			funcName:    "Func",
			shouldMatch: true,
		},
		{
			name:        "method call on object (heuristic)",
			site:        CallSite{FuncName: "obj.Method"},
			pkgPath:     "pkg.Receiver",
			funcName:    "Method",
			shouldMatch: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := matchesCallSite(tt.site, tt.pkgPath, tt.funcName)
			if got != tt.shouldMatch {
				t.Errorf("matchesCallSite() = %v, want %v", got, tt.shouldMatch)
			}
		})
	}
}

func TestSplitSymbolKey(t *testing.T) {
	tests := []struct {
		key       string
		wantPkg   string
		wantName  string
	}{
		{
			key:      "pkg.Func",
			wantPkg:  "pkg",
			wantName: "Func",
		},
		{
			key:      "a.b.c.Func",
			wantPkg:  "a.b.c",
			wantName: "Func",
		},
		{
			key:      "NoPackage",
			wantPkg:  "",
			wantName: "NoPackage",
		},
		{
			key:      "",
			wantPkg:  "",
			wantName: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.key, func(t *testing.T) {
			pkg, name := splitSymbolKey(tt.key)
			if pkg != tt.wantPkg || name != tt.wantName {
				t.Errorf("splitSymbolKey(%q) = (%q, %q), want (%q, %q)",
					tt.key, pkg, name, tt.wantPkg, tt.wantName)
			}
		})
	}
}

func TestCountTotalImpactedSites(t *testing.T) {
	impacts := []RepoImpact{
		{
			RepoName: "app1",
			TotalSites: 2,
		},
		{
			RepoName: "app2",
			TotalSites: 5,
		},
		{
			RepoName: "app3",
			TotalSites: 1,
		},
	}

	total := CountTotalImpactedSites(impacts)

	if total != 8 {
		t.Errorf("CountTotalImpactedSites() = %d, want 8", total)
	}
}

func TestCountTotalImpactedSitesEmpty(t *testing.T) {
	total := CountTotalImpactedSites([]RepoImpact{})

	if total != 0 {
		t.Errorf("CountTotalImpactedSites(empty) = %d, want 0", total)
	}
}

func TestFormatImpactSummaryNoImpacts(t *testing.T) {
	summary := FormatImpactSummary([]RepoImpact{}, 50)

	if summary != "No repos impacted out of 50 scanned" {
		t.Errorf("FormatImpactSummary() = %q, unexpected", summary)
	}
}

func TestFormatImpactSummaryWithImpacts(t *testing.T) {
	impacts := []RepoImpact{
		{RepoName: "app1", TotalSites: 3},
		{RepoName: "app2", TotalSites: 2},
	}

	summary := FormatImpactSummary(impacts, 100)

	if summary != "2 repos impacted, 5 call sites affected out of 100 repos scanned" {
		t.Errorf("FormatImpactSummary() = %q, unexpected", summary)
	}
}

func TestAnalyzeTypeImpact(t *testing.T) {
	changes := []SymbolChange{
		{
			Symbol:   "gitea.example.com/org/shared-lib/service.SchedulerService",
			Kind:     ChangeRemoved,
			Category: KindInterface,
			Breaking: true,
		},
		{
			Symbol:   "gitea.example.com/org/shared-lib/service.Config",
			Kind:     ChangeFieldRemoved,
			Category: KindStruct,
			Breaking: false,
		},
		// Func changes should be ignored by AnalyzeTypeImpact
		{
			Symbol:   "gitea.example.com/org/shared-lib/helper.DoWork",
			Kind:     ChangeRemoved,
			Category: KindFunc,
			Breaking: true,
		},
	}

	repoTypeRefs := map[string][]TypeRef{
		"app1": {
			{File: "main.go", Line: 10, TypeName: "gitea.example.com/org/shared-lib/service.SchedulerService", RawName: "svc.SchedulerService", Context: "field"},
			{File: "main.go", Line: 15, TypeName: "gitea.example.com/org/shared-lib/service.SchedulerService", RawName: "svc.SchedulerService", Context: "param"},
			{File: "service.go", Line: 5, TypeName: "gitea.example.com/org/shared-lib/service.Config", RawName: "svc.Config", Context: "var"},
		},
		"app2": {
			{File: "handler.go", Line: 20, TypeName: "gitea.example.com/org/shared-lib/service.Config", RawName: "s.Config", Context: "field"},
		},
	}

	impacts := AnalyzeTypeImpact(changes, repoTypeRefs)
	if len(impacts) != 2 {
		t.Fatalf("expected 2 impacted repos, got %d", len(impacts))
	}

	if impacts[0].RepoName != "app1" {
		t.Errorf("first repo = %q, want app1", impacts[0].RepoName)
	}
	if impacts[0].TotalSites != 3 {
		t.Errorf("app1 TotalSites = %d, want 3", impacts[0].TotalSites)
	}

	if impacts[1].RepoName != "app2" {
		t.Errorf("second repo = %q, want app2", impacts[1].RepoName)
	}
	if impacts[1].TotalSites != 1 {
		t.Errorf("app2 TotalSites = %d, want 1", impacts[1].TotalSites)
	}
}
