package main

import "testing"

func TestGroupBySourceAndGroup(t *testing.T) {
	results := []repoScanResult{
		{Name: "svc-a", SourceName: "acme-lib", Group: "org-a", UsesTarget: true, HasGoMod: true},
		{Name: "svc-a", SourceName: "core", Group: "org-a", UsesTarget: false, HasGoMod: true},
		{Name: "svc-b", SourceName: "acme-lib", Group: "org-b", UsesTarget: true, HasGoMod: true},
	}
	grouped := groupBySourceAndGroup(results)
	if len(grouped["acme-lib"]) != 2 {
		t.Fatalf("acme-lib groups = %d", len(grouped["acme-lib"]))
	}
	if len(grouped["acme-lib"]["org-a"]) != 1 || grouped["acme-lib"]["org-a"][0].Name != "svc-a" {
		t.Fatalf("unexpected org-a bucket: %+v", grouped["acme-lib"]["org-a"])
	}
	if len(grouped["core"]["org-a"]) != 1 {
		t.Fatalf("core bucket missing")
	}
}
