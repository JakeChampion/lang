package e2eselfhost

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// tupleDestructureNestedStrIRCases pin the two #5306 gaps — string elements of
// a tuple destructure that miscompiled ON the IR path (silent wrong answers,
// not bails; native/interp is the oracle):
//
//  1. NESTED destructure: `var (p, c) = t; var (s, b) = p` over
//     ((string, i32), i32) read the string element empty (11 → 9). The
//     destructure binding chain never recorded a tuple-typed element's
//     element tags, so the second-level destructure resolved dtag "" and the
//     string binding was never str-marked (the all-i32 shape only worked
//     because the untyped fallback happens to be i32). Fixed by the
//     mark_tuple_elems branch in the destructure chain.
//
//  2. A struct fn-FIELD closure call chained into a string op:
//     `h.f(1).len()` where H.f: (i32) => string read 0 (6 → 4) — for ANY
//     captured or literal string return, destructure or not. The field's
//     declared type coarsens to "fn" (losing the return), so expr_is_str
//     didn't know the call yields a string. Fixed by preserving a `string`
//     fn return in StructFieldDecl.fn_ret (parser) and consuming it in
//     expr_is_str's fn-field-call arm. (An annotated rebind
//     `var r: string = h.f(1)` already worked — only the direct chain broke.)
//
// Each case is routing-pinned to "ir" and oracle-checked against the
// interpreter; results stay <= 120 (the wasm exit-code clamp, #2908).
var tupleDestructureNestedStrIRCases = []struct {
	name string
	main string
}{
	// Gap 1: nested string destructure. 11 (was 9 — s.len() read 0).
	{"nested-str", `function g(): i32 {
    var t: ((string, i32), i32) = (("hi", 4), 5);
    var (p, c) = t;
    var (s, b) = p;
    return s.len() + b + c;
}
function main(): i32 { return g(); }`},
	// Gap 1 sibling: read the nested element via p.N instead of a second
	// destructure (the recorded element tags drive both). 11.
	{"nested-str-dotn", `function g(): i32 {
    var t: ((string, i32), i32) = (("hi", 4), 5);
    var (p, c) = t;
    return p.0.len() + p.1 + c;
}
function main(): i32 { return g(); }`},
	// Gap 1 regression: the all-i32 nested shape (#5201) still works. 16.
	{"nested-i32-regress", `function g(): i32 {
    var t: ((i32, i32), i32) = ((7, 4), 5);
    var (p, c) = t;
    var (a, b) = p;
    return a + b + c;
}
function main(): i32 { return g(); }`},
	// Gap 2: struct fn-field closure call chained into .len(), capturing a
	// destructure-bound string. 6 (was 4 — the .len() leg read 0).
	{"fnfield-captured-destructure-str", `struct H { f: (i32) => string, id: i32 }
function g(): i32 {
    var t: (string, i32) = ("hi", 4);
    var (s, b) = t;
    var h: H = H { f: function (x: i32): string { return s; }, id: b };
    return h.f(1).len() + h.id;
}
function main(): i32 { return g(); }`},
	// Gap 2, plain-local capture (the same break, no destructure involved). 6.
	{"fnfield-captured-str", `struct H { f: (i32) => string, id: i32 }
function g(): i32 {
    var s: string = "hi";
    var h: H = H { f: function (x: i32): string { return s; }, id: 4 };
    return h.f(1).len() + h.id;
}
function main(): i32 { return g(); }`},
	// Gap 2, no capture — a literal string return through the fn field. 6.
	{"fnfield-literal-str", `struct H { f: (i32) => string, id: i32 }
function g(): i32 {
    var h: H = H { f: function (x: i32): string { return "hi"; }, id: 4 };
    return h.f(1).len() + h.id;
}
function main(): i32 { return g(); }`},
	// Gap 2 concat shape: the fn-field call result feeds `+` as a string. 8.
	{"fnfield-str-concat", `struct H { f: (i32) => string, id: i32 }
function g(): i32 {
    var h: H = H { f: function (x: i32): string { return "hi"; }, id: 4 };
    return (h.f(1) + "!!").len() + h.id;
}
function main(): i32 { return g(); }`},
	// Regression: an i32-returning fn field is untouched by the fn_ret
	// threading. 12.
	{"fnfield-i32-regress", `struct H { f: (i32) => i32, id: i32 }
function g(): i32 {
    var n: i32 = 7;
    var h: H = H { f: function (x: i32): i32 { return n + x; }, id: 4 };
    return h.f(1) + h.id;
}
function main(): i32 { return g(); }`},
	// Regression: annotated rebind of the fn-field result (already worked). 6.
	{"fnfield-rebind-regress", `struct H { f: (i32) => string, id: i32 }
function g(): i32 {
    var s: string = "hi";
    var h: H = H { f: function (x: i32): string { return s; }, id: 4 };
    var r: string = h.f(1);
    return r.len() + h.id;
}
function main(): i32 { return g(); }`},
}

func TestSelfHostTupleDestructureNestedStrIRX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	interpBin := buildLangBinForInterp(t)
	dir := writeSelfHostAsmProject(t)
	copySelfHostDriver(t, dir, "asm_run.fern", "asm_pathprobe_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_run.fern", "driver")
	probeBin := buildSelfHostBin(t, gcc, dir, "asm_pathprobe_run.fern", "pathprobe")
	for _, tc := range tupleDestructureNestedStrIRCases {
		t.Run(tc.name, func(t *testing.T) {
			src := []byte(tc.main + "\n")
			want := interpExit(t, interpBin, string(src))
			path := strings.TrimSpace(string(runCapture(t, gcc, runner, probeBin, src)))
			if path != "ir" {
				t.Fatalf("%s routed through %q path, want \"ir\"", tc.name, path)
			}
			asm := runCapture(t, gcc, runner, driverBin, src)
			if len(asm) == 0 {
				t.Fatal("self-host compiler emitted 0 bytes")
			}
			progBin := buildBin(t, gcc, dir, tc.name, string(asm))
			var cmd *exec.Cmd
			if len(runner) == 0 {
				cmd = exec.Command(progBin)
			} else {
				cmd = exec.Command(runner[0], append(runner[1:], progBin)...)
			}
			_ = cmd.Run()
			if code := cmd.ProcessState.ExitCode(); code != want {
				t.Errorf("%s exited %d, want %d (interp oracle)", tc.name, code, want)
			}
		})
	}
}

func TestSelfHostTupleDestructureNestedStrIRWasm(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping nested string destructure wasm IR e2e")
	}
	gcc, runner := x86_64Tooling(t)
	interpBin := buildLangBinForInterp(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "wasm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "wasm_ir_run.fern", "driver")
	for _, tc := range tupleDestructureNestedStrIRCases {
		t.Run(tc.name, func(t *testing.T) {
			src := []byte(tc.main + "\n")
			want := interpExit(t, interpBin, string(src))
			var cmd *exec.Cmd
			if len(runner) == 0 {
				cmd = exec.Command(driverBin, "-ir")
			} else {
				cmd = exec.Command(runner[0], append(append(append([]string{}, runner[1:]...), driverBin), "-ir")...)
			}
			cmd.Stdin = bytes.NewReader(src)
			wat, err := cmd.Output()
			if err != nil || len(wat) == 0 {
				t.Fatalf("driver failed for %q: %v", tc.name, err)
			}
			watFile := filepath.Join(dir, "nested_str_prog.wat")
			if err := os.WriteFile(watFile, wat, 0o644); err != nil {
				t.Fatalf("write wat: %v", err)
			}
			run := exec.Command("wasmtime", "run", watFile)
			_ = run.Run()
			if run.ProcessState == nil || !run.ProcessState.Exited() {
				t.Fatalf("wasmtime did not exit normally for %q:\n%s", tc.name, wat)
			}
			if code := run.ProcessState.ExitCode(); code != want {
				t.Errorf("nested str destructure wasm IR %q = %d, want %d (interp oracle)", tc.name, code, want)
			}
		})
	}
}
