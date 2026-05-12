package analysis

import (
	"os"
	"path/filepath"
	"testing"
)

func TestScanTypeReferences(t *testing.T) {
	dir := t.TempDir()

	consumer := filepath.Join(dir, "main.go")
	err := os.WriteFile(consumer, []byte(`package main

import (
	svc "example.com/org/shared-lib/service"
	util "example.com/org/shared-lib/util"
	. "example.com/org/shared-lib/dot"
)

// field + dot import embedded
type Wrapper struct {
	agenda    svc.SchedulerService
	config    svc.Config
	validator util.Validator
	EmbeddedType // dot import anonymous field
}

// param
func NewWrapper(a svc.SchedulerService, c svc.Config) *Wrapper {
	return &Wrapper{agenda: a, config: c}
}

// param + var + return (pointer)
func process(a svc.SchedulerService, c svc.Config) error {
	var temp svc.Config
	return nil
}

// type assertion
func cast(x interface{}) {
	if _, ok := x.(svc.SchedulerService); ok {
		println("agenda")
	}
}

// composite literal
func literal() {
	_ = svc.Config{URL: "http://localhost"}
}

// return type (named)
func returns() svc.Config {
	return svc.Config{}
}

// pointer return
func unused() *svc.Client {
	return nil
}

// array/slice type
type ItemList struct {
	items []svc.Item
}

// map type
type Lookup struct {
	cache map[string]svc.CacheEntry
}

// chan type
type Stream struct {
	ch chan svc.Event
}

// receiver method
func (f *Wrapper) Debug() svc.Config {
	return svc.Config{}
}

// embedded field (non-dot, explicit selector)
type ExtWrapper struct {
	svc.ExtConfig
}

// global var
var globalValidator util.Validator

// dot import usage — bare type names
func dotUsage() {
	_ = DotType{}
	_ = DotType{Field: "x"}
	var d DotType
	if _, ok := interface{}(nil).(DotType); ok {
		println("dot")
	}
}

type dotField struct {
	f DotType
}

// array with dot import
var dotSlice []DotType

// map with dot import
var dotMap map[int]DotType
`), 0644)
	if err != nil {
		t.Fatal(err)
	}

	target := "example.com/org/shared-lib"

	tests := []struct {
		label   string
		typ     string
		wantMin int
	}{
		{"plain type", "SchedulerService", 4},
		{"qualified type", "service.SchedulerService", 4},
		{"plain struct (Config)", "Config", 9},             // field, param×2, var, composite×2, return, receiver return, composite in receiver
		{"qualified struct", "service.Config", 9},
		{"plain interface (Validator)", "Validator", 2},     // field + global var
		{"qualified interface", "util.Validator", 2},
		{"plain unused type", "Client", 1},                   // *svc.Client return
		{"qualified unused", "service.Client", 1},
		{"pointer unwrap", "Item", 1},                        // []svc.Item in struct field
		{"qualified pointer", "service.Item", 1},
		{"map value type", "CacheEntry", 1},                  // map[string]svc.CacheEntry
		{"qualified map", "service.CacheEntry", 1},
		{"chan type", "Event", 1},                            // chan svc.Event
		{"qualified chan", "service.Event", 1},
		{"embedded type", "ExtConfig", 1},                    // svc.ExtConfig embedded
		{"qualified embedded", "service.ExtConfig", 1},
		{"dot import type", "DotType", 6},                    // composite×2, var, assert, field, slice, map
		{"qualified dot import", "dot.DotType", 0},           // dot has no qualifier
		{"nonexistent", "NoSuchType", 0},
	}

	for _, tt := range tests {
		t.Run(tt.label, func(t *testing.T) {
			refs, err := ScanTypeReferences(dir, target, tt.typ)
			if err != nil {
				t.Fatalf("ScanTypeReferences(%q): %v", tt.typ, err)
			}
			if len(refs) < tt.wantMin {
				t.Errorf("%q: got %d refs, want >= %d", tt.typ, len(refs), tt.wantMin)
				for _, r := range refs {
					t.Logf("  %s:%d %s [%s]", r.File, r.Line, r.RawName, r.Context)
				}
			}
		})
	}
}

func TestDotImportRef(t *testing.T) {
	dir := t.TempDir()

	err := os.WriteFile(filepath.Join(dir, "main.go"), []byte(`package main
import . "example.com/org/shared-lib/service"
type S struct { SchedulerService }
func f(a SchedulerService) {}
var v SchedulerService
func cast(x interface{}) { if _, ok := x.(SchedulerService); ok {} }
func lit() { _ = SchedulerService{} }
`), 0644)
	if err != nil {
		t.Fatal(err)
	}

	refs, err := ScanTypeReferences(dir, "example.com/org/shared-lib", "SchedulerService")
	if err != nil {
		t.Fatal(err)
	}
	if len(refs) < 5 {
		t.Errorf("dot import: got %d refs, want >= 5 (field, param, var, assert, composite)", len(refs))
		for _, r := range refs {
			t.Logf("  %s:%d %s [%s]", r.File, r.Line, r.RawName, r.Context)
		}
	}
}
