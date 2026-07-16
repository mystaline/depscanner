package main

import "testing"

func TestGroupBySourceAndGroup(t *testing.T) {
	results := []repoScanResult{
		{Name: "svc-a", SourceName: "ts-utils", Group: "BETS-V2", UsesTarget: true, HasGoMod: true},
		{Name: "svc-a", SourceName: "core", Group: "BETS-V2", UsesTarget: false, HasGoMod: true},
		{Name: "svc-b", SourceName: "ts-utils", Group: "Wangsit", UsesTarget: true, HasGoMod: true},
	}
	grouped := groupBySourceAndGroup(results)
	if len(grouped["ts-utils"]) != 2 {
		t.Fatalf("ts-utils groups = %d", len(grouped["ts-utils"]))
	}
	if len(grouped["ts-utils"]["BETS-V2"]) != 1 || grouped["ts-utils"]["BETS-V2"][0].Name != "svc-a" {
		t.Fatalf("unexpected BETS-V2 bucket: %+v", grouped["ts-utils"]["BETS-V2"])
	}
	if len(grouped["core"]["BETS-V2"]) != 1 {
		t.Fatalf("core bucket missing")
	}
}
