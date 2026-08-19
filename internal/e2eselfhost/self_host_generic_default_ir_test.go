package e2eselfhost

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// genericDefaultIRCases exercise `@derive(Default)` on a GENERIC struct —
// `Box.default()` instantiated as `Box[Inner]` — through the stack-IR path.
// This is the generic case of associated-function dispatch (#2779 item 3): the
// synthesized `default()` body builds `Box { v: T.default() }`, and the
// monomorphiser must (a) clone the receiver-less associated method per
// instantiation, (b) substitute `T.default()` → `Inner.default()` in the cloned
// body (subst_expr), (c) infer the struct literal's instantiation from the
// defaulted field value (mono_infer of an associated call), and (d) retarget the
// call site `Box.default()` → `Box__Inner.default()` from the binding's
// annotation (a receiver-less constructor has no args to infer from).
//
// Scope: type params instantiated with a leaf-safe STRUCT or a PRIMITIVE. A
// primitive param (`Box[i32]`) needs a primitive `Default` impl in scope for
// the native compiler (#2864); the self-host substitutes the primitive's zero
// LITERAL for `T.default()` directly. An enum-typed field isn't leaf-safe in
// the IR struct model — left as a follow-up. The inline `trait Default` (+
// primitive impl, where needed) keeps each program valid for both compilers.
var genericDefaultIRCases = []struct {
	name     string
	src      string
	expected int
}{
	// Primitive type params: the self-host emits the zero literal for the
	// defaulted field; the native compiler dispatches through the primitive
	// `Default` impl. Both yield the same result.
	{"box-i32",
		`trait Default { function default(): Self; } impl Default for i32 { function default(): i32 { return 0; } } @derive(Default) struct Box[T] { v: T } function main(): i32 { var b: Box[i32] = Box.default(); return b.v + 7; }`, 7},
	{"box-string",
		`trait Default { function default(): Self; } impl Default for string { function default(): string { return ""; } } @derive(Default) struct Box[T] { v: T, k: i32 } function main(): i32 { var b: Box[string] = Box.default(); return b.v.len() + b.k + 4; }`, 4},
	{"box-boolean",
		`trait Default { function default(): Self; } impl Default for boolean { function default(): boolean { return false; } } @derive(Default) struct Box[T] { v: T, k: i32 } function main(): i32 { var b: Box[boolean] = Box.default(); if (b.v) { return 1; } return b.k + 8; }`, 8},
	// Box[Inner]: the type param defaults to a nested struct's own default. 5.
	{"box-inner",
		`trait Default { function default(): Self; } @derive(Default) struct Inner { n: i32 } @derive(Default) struct Box[T] { v: T } function main(): i32 { var b: Box[Inner] = Box.default(); return b.v.n + 5; }`, 5},
	// Generic field mixed with a concrete field. 0 + 0 + 9 = 9.
	{"two-field",
		`trait Default { function default(): Self; } @derive(Default) struct Inner { n: i32 } @derive(Default) struct Pair[T] { a: T, b: i32 } function main(): i32 { var p: Pair[Inner] = Pair.default(); return p.a.n + p.b + 9; }`, 9},
	// Two distinct instantiations of the same generic struct in one program. 12.
	{"two-instantiations",
		`trait Default { function default(): Self; } @derive(Default) struct A { n: i32 } @derive(Default) struct B { m: i32 } @derive(Default) struct Box[T] { v: T } function main(): i32 { var x: Box[A] = Box.default(); var y: Box[B] = Box.default(); return x.v.n + y.v.m + 12; }`, 12},
	// The instantiating struct has several fields, all defaulted. 15.
	{"multi-field-inner",
		`trait Default { function default(): Self; } @derive(Default) struct Pt { x: i32, y: i32, tag: string } @derive(Default) struct Box[T] { v: T } function main(): i32 { var b: Box[Pt] = Box.default(); return b.v.x + b.v.y + b.v.tag.len() + 15; }`, 15},
}

// TestSelfHostGenericDefaultIRX86_64 routes each case through the self-hosted
// x86-64 driver (asm_run) and asserts the exit code, AND probes the routing
// (asm_pathprobe_run) to pin each case to the "ir" path.
func TestSelfHostGenericDefaultIRX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := writeSelfHostAsmProject(t)
	copySelfHostDriver(t, dir, "asm_run.fern", "asm_pathprobe_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_run.fern", "driver")
	probeBin := buildSelfHostBin(t, gcc, dir, "asm_pathprobe_run.fern", "pathprobe")

	for _, tc := range genericDefaultIRCases {
		t.Run(tc.name, func(t *testing.T) {
			path := strings.TrimSpace(string(runCapture(t, gcc, runner, probeBin, []byte(tc.src))))
			if path != "ir" {
				t.Fatalf("%s routed through %q path, want \"ir\"", tc.name, path)
			}
			asm := runCapture(t, gcc, runner, driverBin, []byte(tc.src))
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
			if code := cmd.ProcessState.ExitCode(); code != tc.expected {
				t.Errorf("%s exited %d, want %d", tc.name, code, tc.expected)
			}
		})
	}
}

// TestSelfHostGenericDefaultIRWasm runs the same cases through the wasm IR
// backend (wasm_ir_run -ir).
func TestSelfHostGenericDefaultIRWasm(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping self-host generic-default wasm IR e2e")
	}
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "wasm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "wasm_ir_run.fern", "driver")

	for _, tc := range genericDefaultIRCases {
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
				t.Fatalf("driver failed for %q: %v", tc.src, err)
			}
			watFile := filepath.Join(dir, "genericdefault_prog.wat")
			if err := os.WriteFile(watFile, wat, 0o644); err != nil {
				t.Fatalf("write wat: %v", err)
			}
			run := exec.Command("wasmtime", "run", watFile)
			_ = run.Run()
			if run.ProcessState == nil || !run.ProcessState.Exited() {
				t.Fatalf("wasmtime did not exit normally for %q:\n%s", tc.src, wat)
			}
			if code := run.ProcessState.ExitCode(); code != tc.expected {
				t.Errorf("generic-default wasm IR %q = %d, want %d", tc.name, code, tc.expected)
			}
		})
	}
}
