package e2eselfhost

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// deferValueBlockCases pin a `defer` written inside a value-position `{ … }`
// block through the self-host IR path (#6857). The self-host parses such a
// block into an immediately-invoked zero-parameter lambda that irlower inlines,
// so the parse-time defer rewrite — which walked statements only — never reached
// the defer and the whole module refused to lower.
//
// Scope is native's rule (#6851/#6852): a block expression is neither a function
// nor a loop, so the defer belongs to the innermost enclosing loop body if there
// is one and to the function otherwise. Each case is oracle-checked against the
// interpreter, which is what separates "it lowers" from "it lowers correctly".
var deferValueBlockCases = []struct {
	name string
	src  string
}{
	// The headline shape: a defer in a `var` initialiser's value block runs at
	// the enclosing function's exit, so the return expression still reads the
	// pre-cleanup value.
	{"var_value_block", `function g(a: Cell[i32]): i32 {
    var x: i32 = { defer a.set(a.get() + 1); 3 };
    return x * 10 + a.get();
}
function main(): i32 {
    var a: Cell[i32] = cell_new(0);
    var inside: i32 = g(a);
    return inside + a.get();
}`},
	// A value block in a loop body: the defer is lexically inside that body, so
	// it fires per iteration and its flag is cleared at each iteration's end.
	{"loop_body_value_block", `function g(a: Cell[i32]): i32 {
    var t: i32 = 0;
    var i: i32 = 0;
    while (i < 3) {
        t = t + { defer a.set(a.get() + 1); i };
        i = i + 1;
    }
    return t * 100 + a.get();
}
function main(): i32 {
    var a: Cell[i32] = cell_new(0);
    return g(a) + a.get();
}`},
	// A `return` INSIDE the value block is a real function return and has to
	// replay the cleanup in front of it.
	{"return_inside_value_block", `function g(a: Cell[i32], c: boolean): i32 {
    defer a.set(a.get() + 5);
    var x: i32 = { if (c) { return 7; } 3 };
    return x;
}
function main(): i32 {
    var a: Cell[i32] = cell_new(0);
    var e: i32 = g(a, true);
    var s1: i32 = a.get();
    var b: Cell[i32] = cell_new(0);
    var f: i32 = g(b, false);
    return e * 1000 + s1 * 100 + f * 10 + b.get();
}`},
	// A `break` inside a value block is an edge out of the iteration, so the
	// per-iteration cleanup runs before it.
	{"break_inside_value_block", `function g(a: Cell[i32]): i32 {
    var t: i32 = 0;
    var i: i32 = 0;
    while (i < 5) {
        defer a.set(a.get() + 1);
        t = t + { if (i == 2) { break; } i };
        i = i + 1;
    }
    return t * 100 + a.get();
}
function main(): i32 {
    var a: Cell[i32] = cell_new(0);
    return g(a) + a.get();
}`},
	// A match-EXPRESSION arm desugars to the same zero-parameter IIFE, so an
	// arm's defer takes the same path.
	{"match_expression_arm", `function g(a: Cell[i32], n: i32): i32 {
    var x: i32 = match (n) {
        0 => { defer a.set(a.get() + 1); 10 },
        _ => { defer a.set(a.get() + 2); 20 }
    };
    return x + a.get();
}
function main(): i32 {
    var a: Cell[i32] = cell_new(0);
    var p: i32 = g(a, 0);
    var b: Cell[i32] = cell_new(0);
    var q: i32 = g(b, 1);
    return p * 1000 + a.get() * 100 + q + b.get();
}`},
	// An errdefer inside a value block fires only on the failure return.
	{"errdefer_in_value_block", `function g(a: Cell[i32], ok: boolean): Option[i32] {
    var x: i32 = { errdefer a.set(a.get() + 1); 3 };
    if (ok) { return Some(x); }
    return None;
}
function main(): i32 {
    var a: Cell[i32] = cell_new(0);
    var r: Option[i32] = g(a, true);
    var b: Cell[i32] = cell_new(0);
    var s: Option[i32] = g(b, false);
    var got: i32 = 0;
    match (r) { Some(v) => { got = v; }, None => { got = 9; } }
    match (s) { Some(_) => { got = got + 90; }, None => { got = got + 40; } }
    return got * 100 + a.get() * 10 + b.get();
}`},
}

// TestSelfHostDeferValueBlockIR_X86_64 drives deferValueBlockCases through the
// self-host x86-64 IR path under FERN_STRICT_IR, so a per-function bail is a
// hard failure rather than a route that quietly reaches the same answer.
func TestSelfHostDeferValueBlockIR_X86_64(t *testing.T) {
	dir, mmc, stdlibRoot, gcc, interpBin := annotateF64ProjDir(t)
	_, runner := x86_64Tooling(t)

	for _, tc := range deferValueBlockCases {
		t.Run(tc.name, func(t *testing.T) {
			want := interpExit(t, interpBin, tc.src)
			proj := t.TempDir()
			mainPath := filepath.Join(proj, "main.fern")
			if err := os.WriteFile(mainPath, []byte(tc.src), 0o644); err != nil {
				t.Fatalf("write main.fern: %v", err)
			}

			cmd := exec.Command(mmc, mainPath, stdlibRoot)
			cmd.Env = append(os.Environ(), "FERN_STRICT_IR=1")
			asm, cerr := cmd.Output()
			if cerr != nil {
				t.Fatalf("strict-IR compile: %v: %s", cerr, exitStderr(cerr))
			}
			if len(asm) == 0 {
				t.Fatal("loader emitted 0 bytes")
			}
			progBin := buildBin(t, gcc, dir, "defervb_"+tc.name, string(asm))
			argv := append(append([]string{}, runner...), progBin)
			run := exec.Command(argv[0], argv[1:]...)
			_ = run.Run()
			if code := run.ProcessState.ExitCode(); code != want {
				t.Errorf("%s exited %d, want %d (interp oracle)", tc.name, code, want)
			}
		})
	}
}

// exitStderr surfaces the strict-IR bail message, which the driver writes to
// stderr and exec.Command's error string drops.
func exitStderr(err error) string {
	if ee, ok := err.(*exec.ExitError); ok {
		return string(ee.Stderr)
	}
	return ""
}
