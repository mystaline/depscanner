package main

import "testing"

// TestCountScanResults_MultiSource covers the bug where a repo importing two
// sources inflated totalTarget (a relationship count) while totalGoMod only
// counted the repo once, producing an impossible "2/1" summary. The fix
// counts totalUsingAnySource once per repo so it pairs correctly with
// totalGoMod, while totalTarget stays a relationship count.
func TestCountScanResults_MultiSource(t *testing.T) {
	results := []repoScanResult{
		// svc-a has go.mod and imports both sources: one repo, two relationships.
		{Name: "svc-a", SourceName: "ts-utils", HasGoMod: true, UsesTarget: true},
		{Name: "svc-a", SourceName: "core", HasGoMod: true, UsesTarget: true},
		// svc-b has go.mod but uses neither source.
		{Name: "svc-b", SourceName: "ts-utils", HasGoMod: true, UsesTarget: false},
		{Name: "svc-b", SourceName: "core", HasGoMod: true, UsesTarget: false},
	}

	totalUsingAnySource, totalGoMod, totalTarget := countScanResults(results)

	if totalGoMod != 2 {
		t.Errorf("totalGoMod = %d, want 2 (svc-a, svc-b)", totalGoMod)
	}
	if totalUsingAnySource != 1 {
		t.Errorf("totalUsingAnySource = %d, want 1 (svc-a only, counted once)", totalUsingAnySource)
	}
	if totalUsingAnySource > totalGoMod {
		t.Errorf("totalUsingAnySource (%d) > totalGoMod (%d): impossible summary fraction", totalUsingAnySource, totalGoMod)
	}
	if totalTarget != 2 {
		t.Errorf("totalTarget = %d, want 2 (relationship count: svc-a x 2 sources)", totalTarget)
	}
}

func TestCountScanResults_NoGoMod(t *testing.T) {
	results := []repoScanResult{
		{Name: "svc-c", SourceName: "ts-utils", HasGoMod: false, UsesTarget: false},
	}
	totalUsingAnySource, totalGoMod, totalTarget := countScanResults(results)
	if totalGoMod != 0 || totalUsingAnySource != 0 || totalTarget != 0 {
		t.Errorf("got (%d, %d, %d), want all zero", totalUsingAnySource, totalGoMod, totalTarget)
	}
}
