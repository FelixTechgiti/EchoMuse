package config

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"testing"
)

// A config key can be APPLIED and still be unreadable, and nothing says so.
//
// `Apply` writes every key the controller pushes onto the Device. Every
// consumer then reads it back through `Snapshot`, which is a hand-written
// field-by-field copy — so a key added to Apply and forgotten in Snapshot is
// stored, persisted, reported as set by the dashboard, and read by its own
// consumer as nil. There is no error, no log line and no failing test.
//
// That is what happened to `airplayVolumeControl`. It shipped in the config
// push, in the schema, in the dashboard and in `Apply`; `Snapshot` did not
// carry it, so `airplayVolume()` read nil and installed no handler, and the
// AirPlay volume feature could not work on any device from the day it
// shipped. Everything downstream of it was correct — the metadata build, the
// FIFO, the reader goroutine, the config rewrite — and every one of them was
// gated behind a value that was always nil.
//
// A key with its own accessor is exempt and must say so here, which is the
// point: the exemption is a sentence somebody has to write, rather than an
// omission nobody can see.
// Note the console pair is absent rather than exempt: `Apply` does not store
// `consolePassword`/`consoleTimeoutMin` at all — the console package writes
// them straight to disk for emOS's init — so the guard never asks about them,
// and the day one of them IS applied it has to answer like everything else.
var snapshotExempt = map[string]string{
	// The output chain has its own accessor, OutputChain(), because it is
	// read per audio period and wants one struct rather than nine fields.
	"EqBands":          "read via OutputChain()",
	"EqLoudness":       "read via OutputChain()",
	"LimiterEnabled":   "read via OutputChain()",
	"LimiterThreshold": "read via OutputChain()",
	"LimiterRelease":   "read via OutputChain()",
	"BassGuardEnabled": "read via OutputChain()",
	"BassGuardDb":      "read via OutputChain()",
	"DuckDb":           "read via OutputChain()",
}

func parseConfigGo(t *testing.T) *ast.File {
	t.Helper()
	src, err := os.ReadFile("config.go")
	if err != nil {
		t.Fatalf("config.go: %v", err)
	}
	f, err := parser.ParseFile(token.NewFileSet(), "config.go", src, 0)
	if err != nil {
		t.Fatalf("parse config.go: %v", err)
	}
	return f
}

// appliedFields are the ConfigMessage fields Apply reads off the message.
func appliedFields(t *testing.T, f *ast.File) map[string]bool {
	t.Helper()
	out := map[string]bool{}
	for _, d := range f.Decls {
		fn, ok := d.(*ast.FuncDecl)
		if !ok || fn.Name.Name != "Apply" {
			continue
		}
		ast.Inspect(fn, func(n ast.Node) bool {
			sel, ok := n.(*ast.SelectorExpr)
			if !ok {
				return true
			}
			if id, ok := sel.X.(*ast.Ident); ok && id.Name == "msg" {
				out[sel.Sel.Name] = true
			}
			return true
		})
	}
	if len(out) == 0 {
		t.Fatal("found no msg.* reads in Apply — the guard is not looking at anything")
	}
	return out
}

// snapshotFields are the keys of the ConfigMessage literal Snapshot returns.
func snapshotFields(t *testing.T, f *ast.File) map[string]bool {
	t.Helper()
	out := map[string]bool{}
	for _, d := range f.Decls {
		fn, ok := d.(*ast.FuncDecl)
		if !ok || fn.Name.Name != "Snapshot" {
			continue
		}
		ast.Inspect(fn, func(n ast.Node) bool {
			lit, ok := n.(*ast.CompositeLit)
			if !ok {
				return true
			}
			if id, ok := lit.Type.(*ast.Ident); !ok || id.Name != "ConfigMessage" {
				return true
			}
			for _, e := range lit.Elts {
				kv, ok := e.(*ast.KeyValueExpr)
				if !ok {
					continue
				}
				if k, ok := kv.Key.(*ast.Ident); ok {
					out[k.Name] = true
				}
			}
			return true
		})
	}
	if len(out) == 0 {
		t.Fatal("found no ConfigMessage literal in Snapshot")
	}
	return out
}

func TestEverySettingAppliedCanBeReadBack(t *testing.T) {
	f := parseConfigGo(t)
	applied := appliedFields(t, f)
	snap := snapshotFields(t, f)

	for name := range applied {
		if snap[name] {
			continue
		}
		if why, ok := snapshotExempt[name]; ok {
			if why == "" {
				t.Errorf("%s is exempt from Snapshot with no reason given", name)
			}
			continue
		}
		t.Errorf("Apply stores msg.%s and Snapshot never returns it, so every "+
			"reader sees nil — add it to Snapshot, or to snapshotExempt with "+
			"the accessor that reads it instead", name)
	}
}

func TestTheExemptionListDoesNotGoStale(t *testing.T) {
	// An exemption for a field that is now IN Snapshot is a sentence that has
	// stopped being true, and the next person reads it as still authoritative.
	f := parseConfigGo(t)
	snap := snapshotFields(t, f)
	applied := appliedFields(t, f)
	for name := range snapshotExempt {
		if snap[name] {
			t.Errorf("%s is listed as exempt but Snapshot does return it — "+
				"drop the exemption", name)
		}
		if !applied[name] {
			t.Errorf("%s is exempt from a rule it is no longer subject to — "+
				"Apply does not store it", name)
		}
	}
}

func TestAirplayVolumeControlSurvivesASnapshot(t *testing.T) {
	// The regression itself, as behaviour rather than as source shape: the
	// AST guard above would pass on a Snapshot that returned a hardcoded nil.
	on := true
	d := &Device{}
	d.Apply(ConfigMessage{AirplayVolumeControl: &on})
	got := d.Snapshot().AirplayVolumeControl
	if got == nil || !*got {
		t.Fatalf("airplayVolumeControl did not survive Apply→Snapshot: %v got", got)
	}

	// And nil must stay nil rather than flattening to false — absent and
	// off are the same answer here only by luck, and the next field to use
	// this shape may not be so lucky.
	if (&Device{}).Snapshot().AirplayVolumeControl != nil {
		t.Error("an unset airplayVolumeControl came back non-nil")
	}
}
