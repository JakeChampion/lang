package ir_test

import (
	"testing"

	"github.com/jakechampion/lang/internal/ast"
	"github.com/jakechampion/lang/internal/checker"
	"github.com/jakechampion/lang/internal/ir"
	"github.com/jakechampion/lang/internal/parser"
)

const resourcePrelude = `@import("wasi:io/poll@0.2.0", "pollable")
resource Pollable;

@import("wasi:clocks/monotonic-clock@0.2.0", "subscribe-duration")
function subscribe(ns: u64): own Pollable;

@import("wasi:io/poll@0.2.0", "[method]pollable.ready")
function ready(h: borrow Pollable): boolean;
`

func lowerResourceProg(t *testing.T, src string) *ir.Program {
	t.Helper()
	prog, err := parser.Parse(src)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	info, err := checker.Check(prog)
	if err != nil {
		t.Fatalf("check: %v", err)
	}
	prev := ast.RcFreeEnabled
	ast.RcFreeEnabled = true
	ip, err := ir.LowerWith(prog, info, 4)
	ast.RcFreeEnabled = prev
	if err != nil {
		t.Fatalf("lower: %v", err)
	}
	return ip
}

func countDropExterns(ip *ir.Program) (int, *ir.ExternFunc) {
	n := 0
	var found *ir.ExternFunc
	for _, e := range ip.Externs {
		if e.Name == "__resource_drop_Pollable" {
			n++
			found = e
		}
	}
	return n, found
}

// An owned handle that is only borrowed (passed to ready's `borrow` param) and
// then goes out of scope gets an auto-synthesized `[resource-drop]` import.
func TestAutoDropSynthesizesDropImport(t *testing.T) {
	ip := lowerResourceProg(t, resourcePrelude+`
function main(): i32 {
	var p: own Pollable = subscribe(0 as u64);
	if (ready(p)) { write("x"); }
	return 0;
}`)
	n, drop := countDropExterns(ip)
	if n != 1 {
		t.Fatalf("got %d __resource_drop_Pollable externs, want 1", n)
	}
	if drop.Iface != "wasi:io/poll@0.2.0" || drop.WITName != "[resource-drop]pollable" {
		t.Errorf("drop extern = {%q %q}, want {wasi:io/poll@0.2.0 [resource-drop]pollable}", drop.Iface, drop.WITName)
	}
}

// A handle that escapes (returned to the caller) is NOT auto-dropped — its
// consumer owns it, so dropping here would double-free.
func TestAutoDropSkipsMovedHandle(t *testing.T) {
	ip := lowerResourceProg(t, resourcePrelude+`
function take(): own Pollable {
	var p: own Pollable = subscribe(0 as u64);
	return p;
}
function main(): i32 { var q: own Pollable = take(); return 0; }`)
	// `take` returns its handle (moved); `main`'s q is kept. So exactly one
	// drop import is synthesized (for q), and `take` contributes none.
	n, _ := countDropExterns(ip)
	if n != 1 {
		t.Fatalf("got %d drop externs, want 1 (only main's kept handle)", n)
	}
}

// Lowering the same program twice must not double-insert the drop (the diff
// oracle / multi-backend compiles re-run LowerWith on the same Program).
func TestAutoDropIsIdempotent(t *testing.T) {
	prog, err := parser.Parse(resourcePrelude + `
function main(): i32 {
	var p: own Pollable = subscribe(0 as u64);
	if (ready(p)) { write("x"); }
	return 0;
}`)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	info, err := checker.Check(prog)
	if err != nil {
		t.Fatalf("check: %v", err)
	}
	prev := ast.RcFreeEnabled
	ast.RcFreeEnabled = true
	defer func() { ast.RcFreeEnabled = prev }()
	if _, err := ir.LowerWith(prog, info, 4); err != nil {
		t.Fatalf("lower 1: %v", err)
	}
	ip, err := ir.LowerWith(prog, info, 4)
	if err != nil {
		t.Fatalf("lower 2: %v", err)
	}
	if n, _ := countDropExterns(ip); n != 1 {
		t.Fatalf("after re-lowering got %d drop externs, want 1 (idempotent)", n)
	}
}
