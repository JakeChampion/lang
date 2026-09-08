package e2eselfhost

import (
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
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

// These cases keep their observable status below 126 for the Wasm runner.
func deferValueBlockBindingCases(t *testing.T) []struct{ name, src string } {
	t.Helper()
	source, err := os.ReadFile(filepath.Join("..", "..", "conformance", "cases", "defer_binding_value_block", "main.fern"))
	if err != nil {
		t.Fatal(err)
	}
	return []struct{ name, src string }{
		{"captured_array_value_block", string(source)},
		{"sibling_value_block_bindings", `function main(): i32 {
    var seen: i32 = 0;
    loop {
        var first: i32 = { var items: i32[] = [3]; defer seen = seen * 10 + items[0]; 1 };
        var second: i32 = { var items: i32[] = [5]; defer seen = seen * 10 + items[0]; 1 };
        break;
    }
    return seen;
}`},
		{"shadowed_value_block_binding", `function main(): i32 {
    var items: i32[] = [1];
    var seen: i32 = 0;
    var before: i32 = 0;
    loop {
        var value: i32 = { var items: i32[] = [9]; defer seen = items[0]; 1 };
        before = items[0];
        break;
    }
    return before * 10 + seen;
}`},
		{"nested_value_block_bindings", `function main(): i32 {
    var seen: i32 = 0;
    loop {
        var first: i32 = {
            var items: i32[] = [3];
            defer seen = seen * 10 + items[0];
            var second: i32 = { var items: i32[] = [5]; defer seen = seen * 10 + items[0]; 1 };
            items[0]
        };
        break;
    }
    return seen;
}`},
		{"function_value_block_binding", `function f(a: Cell[i32]): i32 {
    var value: i32 = { var items: i32[] = [3]; defer a.set(items[0]); 1 };
    var items: i32[] = [9];
    return items[0];
}
function main(): i32 {
    var a: Cell[i32] = cell_new(0);
    var value: i32 = f(a);
    return value * 10 + a.get();
}`},
		{"lambda_registration_namespace", `function f(a: Cell[i32]): i32 {
    if (true) {
        var items: i32[] = [3];
        defer a.set(a.get() * 10 + items[0]);
        var run = (b: Cell[i32]): i32 => {
            var items: i32[] = [5];
            defer b.set(b.get() * 10 + items[0]);
            return 1;
        };
        var value: i32 = { run(a) };
    }
    return 1;
}
function main(): i32 {
    var a: Cell[i32] = cell_new(0);
    var value: i32 = f(a);
    return a.get();
}`},
	}
}

// TestSelfHostDeferValueBlockIR_X86_64 drives deferValueBlockCases through the
// self-host x86-64 IR path under FERN_STRICT_IR, so a per-function bail is a
// hard failure rather than a route that quietly reaches the same answer.
func TestSelfHostDeferValueBlockIR_X86_64(t *testing.T) {
	dir, mmc, stdlibRoot, gcc, runner, interpBin := annotateF64ProjDir(t)

	cases := slices.Concat(deferValueBlockCases, deferValueBlockBindingCases(t))
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			want := interpExit(t, interpBin, tc.src)
			proj := t.TempDir()
			mainPath := filepath.Join(proj, "main.fern")
			if err := os.WriteFile(mainPath, []byte(tc.src), 0o644); err != nil {
				t.Fatalf("write main.fern: %v", err)
			}

			cmd := runX86_64Bin(runner, mmc, mainPath, stdlibRoot)
			cmd.Env = append(os.Environ(), "FERN_STRICT_IR=1")
			asm, cerr := cmd.Output()
			if cerr != nil {
				t.Fatalf("strict-IR compile: %v: %s", cerr, exitStderr(cerr))
			}
			if len(asm) == 0 {
				t.Fatal("loader emitted 0 bytes")
			}
			progBin := buildBin(t, gcc, dir, "defervb_"+tc.name, string(asm))
			run := runX86_64Bin(runner, progBin)
			_ = run.Run()
			if code := run.ProcessState.ExitCode(); code != want {
				t.Errorf("%s exited %d, want %d (interp oracle)", tc.name, code, want)
			}
		})
	}
}

func TestSelfHostDeferValueBlockBindingsIRArm64(t *testing.T) {
	armgcc, runner := arm64Tooling(t)
	dir, mmc, stdlibRoot, _, compilerRunner, interpBin := annotateF64ProjDir(t)
	for _, tc := range deferValueBlockBindingCases(t) {
		t.Run(tc.name, func(t *testing.T) {
			want := interpExit(t, interpBin, tc.src)
			mainPath := filepath.Join(t.TempDir(), "main.fern")
			if err := os.WriteFile(mainPath, []byte(tc.src), 0o644); err != nil {
				t.Fatal(err)
			}
			cmd := runX86_64Bin(compilerRunner, mmc, mainPath, stdlibRoot, "-target", "arm64-linux")
			cmd.Env = append(os.Environ(), "FERN_STRICT_IR=1")
			asm, err := cmd.Output()
			if err != nil {
				t.Fatalf("strict-IR compile: %v: %s", err, exitStderr(err))
			}
			bin := buildBinArm64(t, armgcc, dir, tc.name, string(asm))
			run := runArm64Bin(runner, bin)
			_ = run.Run()
			if run.ProcessState == nil {
				t.Fatal("program did not start")
			}
			if got := run.ProcessState.ExitCode(); got != want {
				t.Errorf("exit %d, want %d (interp oracle)", got, want)
			}
		})
	}
}

func TestSelfHostDeferValueBlockBindingsIRWasm(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH")
	}
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "wasm_ir_run.fern")
	driver := buildSelfHostBin(t, gcc, dir, "wasm_ir_run.fern", "driver")
	interpBin := buildLangBinForInterp(t)
	for _, tc := range deferValueBlockBindingCases(t) {
		t.Run(tc.name, func(t *testing.T) {
			want := interpExit(t, interpBin, tc.src)
			cmd := runX86_64Bin(runner, driver, "-ir")
			cmd.Env = append(os.Environ(), "FERN_STRICT_IR=1")
			cmd.Stdin = strings.NewReader(tc.src)
			wat, err := cmd.Output()
			if err != nil {
				t.Fatalf("strict-IR compile: %v: %s", err, exitStderr(err))
			}
			path := filepath.Join(t.TempDir(), "main.wat")
			if err := os.WriteFile(path, wat, 0o644); err != nil {
				t.Fatal(err)
			}
			run := exec.Command("wasmtime", "run", path)
			output, _ := run.CombinedOutput()
			if run.ProcessState == nil {
				t.Fatalf("program did not start: %s", output)
			}
			if got := run.ProcessState.ExitCode(); got != want {
				t.Errorf("exit %d, want %d (interp oracle): %s", got, want, output)
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
