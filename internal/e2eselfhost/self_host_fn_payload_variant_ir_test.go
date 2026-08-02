package e2eselfhost

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// An enum variant carrying a FUNCTION-typed payload — the shape std/task's
// Step/Future need — must round-trip on the self-host IR path: construct the
// variant with a function value in payload position, store it, `match`-bind the
// payload back out, and call it indirectly. docs/SELF-HOST-FN-PAYLOAD-VARIANT-GAP.md
// (#4364) documented this failing on the older SSA/AST path (a variant
// constructor mis-emitted as `call __fn_<Variant>`). It now composes on the IR
// path — the closure-conv work (#4354 + the CLOSURE-CONV slices) lowers a
// function value in payload position as an ordinary pointer-sized payload — so
// this pins the construction → store → match-bind → indirect-call round-trip
// across the shapes the issue names.
//
// Both the x86-64 and wasm IR paths lower ALL five shapes, including the
// fully-generic-recursive `Future[T]` (a generic enum whose payload fn RETURNS
// the generic enum itself). On wasm that shape needs the fn payload's return
// type preserved through the variant desugar (StructFieldDecl.fn_ret) so the
// monomorphiser can rewrite the nested `match (cont(tok))`'s mono'd arm
// patterns — added in #4722 (the x86 path never monomorphizes, so it lowered
// the generic/recursive shapes all along).
//
// Each program computes 42 via the payload function; the interp oracle agrees.
var fnPayloadVariantCases = []struct {
	name string
	src  string
}{
	// Repro B from the gap doc: a named function as the payload of a 2-arg variant.
	{"named-fn", "enum Box { Fn(i32, (i32) => i32), Empty }\n" +
		"function add(x: i32): i32 { return x + 1; }\n" +
		"function main(): i32 {\n" +
		"    var b: Box = Fn(10, add);\n" +
		"    match (b) { Fn(n, f) => { return n + f(31); }, Empty => { return 0; } }\n" +
		"}\n"},
	// A CAPTURING closure (bound to a local) as the payload.
	{"capturing-closure", "enum Box { Fn(i32, (i32) => i32), Empty }\n" +
		"function main(): i32 {\n" +
		"    var k: i32 = 5;\n" +
		"    var g: (i32) => i32 = (x: i32): i32 => x + k;\n" +
		"    var b: Box = Fn(10, g);\n" +
		"    match (b) { Fn(n, f) => { return n + f(27); }, Empty => { return 0; } }\n" +
		"}\n"},
	// The recursive std/task `Step` shape: the payload fn returns the enum itself.
	{"recursive-step", "enum Step { Done(i32), Wait(i32, (i32) => Step) }\n" +
		"function resume(tok: i32): Step { return Done(tok + 1); }\n" +
		"function main(): i32 {\n" +
		"    var s: Step = Wait(41, resume);\n" +
		"    match (s) {\n" +
		"        Wait(tok, cont) => {\n" +
		"            match (cont(tok)) { Done(v) => { return v; }, Wait(t2, c2) => { return 0; } }\n" +
		"        },\n" +
		"        Done(v) => { return v; }\n" +
		"    }\n" +
		"}\n"},
	// A GENERIC enum instantiated at i32, with a fn payload.
	{"generic-enum", "enum Box[T] { Fn(T, (T) => T), Empty }\n" +
		"function inc(x: i32): i32 { return x + 1; }\n" +
		"function main(): i32 {\n" +
		"    var b: Box[i32] = Fn(41, inc);\n" +
		"    match (b) { Fn(n, f) => { return f(n); }, Empty => { return 0; } }\n" +
		"}\n"},
	// The canonical Blocker-2 shape (docs/ASYNC-SELFHOST-IR.md): a generic AND
	// recursive user enum whose payload fn RETURNS the generic enum itself —
	// exactly std/task's `Future[T] = Ready(T) | Pending(i32, (i32) => Future[T])`.
	{"future-generic-recursive", "enum Future[T] { Ready(T), Pending(i32, (i32) => Future[T]) }\n" +
		"function step(tok: i32): Future[i32] { return Ready(tok + 1); }\n" +
		"function main(): i32 {\n" +
		"    var f: Future[i32] = Pending(41, step);\n" +
		"    match (f) {\n" +
		"        Pending(tok, cont) => {\n" +
		"            match (cont(tok)) { Ready(v) => { return v; }, Pending(t2, c2) => { return 0; } }\n" +
		"        },\n" +
		"        Ready(v) => { return v; }\n" +
		"    }\n" +
		"}\n"},
}

// TestSelfHostFnPayloadVariantIR pins the round-trip on the x86-64 IR path.
func TestSelfHostFnPayloadVariantIR(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	if len(runner) != 0 {
		t.Skip("single-program IR driver test runs only natively")
	}
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_ir_run.fern", "driver")

	for _, tc := range fnPayloadVariantCases {
		t.Run(tc.name, func(t *testing.T) {
			cmd := exec.Command(driverBin, "-ir")
			cmd.Stdin = bytes.NewReader([]byte(tc.src))
			asm, err := cmd.Output()
			if err != nil || len(asm) == 0 {
				t.Fatalf("driver failed: %v", err)
			}
			// The whole module must reach the IR path — a bail here would drop to
			// the AST emitter that mis-emits `call __fn_<Variant>` (the #4364 bug).
			if bytes.Contains(asm, []byte("call __fn_Fn")) || bytes.Contains(asm, []byte("call __fn_Wait")) {
				t.Fatalf("%s: variant constructor mis-emitted as a direct call (#4364 regression)\n%s", tc.name, asm)
			}
			progBin := buildBin(t, gcc, dir, tc.name, string(asm))
			run := exec.Command(progBin)
			_ = run.Run()
			if code := run.ProcessState.ExitCode(); code != 42 {
				t.Errorf("%s (x86-64 IR) exited %d, want 42", tc.name, code)
			}
		})
	}
}

// TestSelfHostFnPayloadVariantIRWasm is the wasm leg — all five shapes lower on
// wasm IR (the fully-generic-recursive Future[T] shape closed by #4722).
func TestSelfHostFnPayloadVariantIRWasm(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping self-host fn-payload-variant wasm IR e2e")
	}
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "wasm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "wasm_ir_run.fern", "driver")

	for _, tc := range fnPayloadVariantCases {
		t.Run(tc.name, func(t *testing.T) {
			var cmd *exec.Cmd
			if len(runner) == 0 {
				cmd = exec.Command(driverBin, "-ir")
			} else {
				cmd = exec.Command(runner[0], append(append(append([]string{}, runner[1:]...), driverBin), "-ir")...)
			}
			cmd.Stdin = bytes.NewReader([]byte(tc.src))
			wat, err := cmd.Output()
			if err != nil || len(wat) == 0 {
				t.Fatalf("driver failed: %v", err)
			}
			watFile := filepath.Join(dir, tc.name+".wat")
			if err := os.WriteFile(watFile, wat, 0o644); err != nil {
				t.Fatalf("write wat: %v", err)
			}
			run := exec.Command("wasmtime", "run", watFile)
			run.Dir = dir
			_ = run.Run()
			if run.ProcessState == nil || !run.ProcessState.Exited() {
				t.Fatalf("wasmtime did not exit normally:\n%s", wat)
			}
			if code := run.ProcessState.ExitCode(); code != 42 {
				t.Errorf("%s (wasm IR) exited %d, want 42\n--- WAT ---\n%s", tc.name, code, wat)
			}
		})
	}
}
