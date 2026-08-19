package e2eselfhost

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// optStructPayloadFieldIRCases pin a struct FIELD whose Option/Result payload is
// itself a STRUCT (`Option[Inner]` / `Result[Inner, …]`) to the self-host IR path
// on x86-64 + wasm. Scalar-payload Option/Result fields were already admitted (see
// self_host_opt_struct_field_ir_test.go); the construction and match-on-Option
// lowering already handled a leak-safe STRUCT payload on every backend (the match
// path reads it as a leak-only pointer at offset 8, like a string), but the
// eligibility predicate is_leaksafe_opt_field rejected any non-scalar payload, so
// such a field bailed the whole module to the legacy AST emitter. #2691 adds the
// structs-aware is_leaksafe_opt_field_d so an Option[Struct]/Result[Struct,…] field
// is admitted. ENUM payloads stay excluded (the legacy AST backend miscompiles an
// Option[enum] field — see the negative pin below). Each case is oracle-checked
// against the interpreter and returns <= 126. Mirrors self_host_nested_array_ir_test.go.
var optStructPayloadFieldIRCases = []struct {
	name string
	main string
}{
	// Option[Struct] field, Some arm reads the nested struct's field. 5 + 10 = 15.
	{"opt-struct-some", `struct Inner { a: i32 } struct Outer { v: i32, opt: Option[Inner] } function main(): i32 { var o = Outer { v: 5, opt: Some(Inner { a: 10 }) }; match (o.opt) { Some(n) => { return o.v + n.a; }, None => { return o.v; } } }`},
	// Option[Struct] field = None — the None arm. 5.
	{"opt-struct-none", `struct Inner { a: i32 } struct Outer { v: i32, opt: Option[Inner] } function main(): i32 { var o = Outer { v: 5, opt: None }; match (o.opt) { Some(n) => { return o.v + n.a; }, None => { return o.v; } } }`},
	// Result[Struct, string] field, Ok arm. 33.
	{"result-struct-ok", `struct Inner { a: i32 } struct Outer { res: Result[Inner, string] } function main(): i32 { var o = Outer { res: Ok(Inner { a: 33 }) }; match (o.res) { Ok(n) => { return n.a; }, Err(e) => { return 0; } } }`},
	// Outer (with the Option[Struct] field) flows through a by-value function param. 123.
	{"opt-struct-fn-param", `struct Inner { a: i32 } struct Outer { v: i32, opt: Option[Inner] } function total(o: Outer): i32 { match (o.opt) { Some(n) => { return o.v + n.a; }, None => { return o.v; } } } function main(): i32 { return total(Outer { v: 100, opt: Some(Inner { a: 23 }) }); }`},
	// The Option[Struct] field read into a typed local, then matched. 42.
	{"opt-struct-field-to-local", `struct Inner { a: i32 } struct Outer { opt: Option[Inner] } function main(): i32 { var o = Outer { opt: Some(Inner { a: 42 }) }; var x: Option[Inner] = o.opt; match (x) { Some(n) => { return n.a; }, None => { return 0; } } }`},
}

// TestSelfHostOptStructPayloadFieldIRX86_64 routes each case through the self-hosted
// x86-64 IR driver, oracle-checked, with routing pinned to "ir". It also asserts an
// Option[enum] struct field STILL routes "ast" — the legacy AST backend miscompiles
// that shape, so the struct-payload widening must not pull it onto the IR path.
func TestSelfHostOptStructPayloadFieldIRX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	interpBin := buildLangBinForInterp(t)
	dir := writeSelfHostAsmProject(t)
	copySelfHostDriver(t, dir, "asm_run.fern", "asm_pathprobe_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_run.fern", "driver")
	probeBin := buildSelfHostBin(t, gcc, dir, "asm_pathprobe_run.fern", "pathprobe")

	for _, tc := range optStructPayloadFieldIRCases {
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

	// Negative pin: an Option[enum] struct field must NOT be admitted to the IR
	// path (the legacy AST backend miscompiles it; the widening is struct-only).
	t.Run("opt-enum-field-stays-ast", func(t *testing.T) {
		src := []byte(`enum Color { Red, Blue } struct Box { c: Option[Color] } function main(): i32 { var b = Box { c: Some(Blue) }; match (b.c) { Some(x) => { return 2; }, None => { return 0; } } }` + "\n")
		path := strings.TrimSpace(string(runCapture(t, gcc, runner, probeBin, src)))
		if path != "ast" {
			t.Fatalf("Option[enum] struct field routed through %q path, want \"ast\" (must stay off the IR path)", path)
		}
	})
}

// TestSelfHostOptStructPayloadFieldIRWasm runs the same cases through the wasm IR backend.
func TestSelfHostOptStructPayloadFieldIRWasm(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping self-host opt-struct-payload-field wasm IR e2e")
	}
	gcc, runner := x86_64Tooling(t)
	interpBin := buildLangBinForInterp(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "wasm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "wasm_ir_run.fern", "driver")

	for _, tc := range optStructPayloadFieldIRCases {
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
			watFile := filepath.Join(dir, "opt_struct_payload_field_prog.wat")
			if err := os.WriteFile(watFile, wat, 0o644); err != nil {
				t.Fatalf("write wat: %v", err)
			}
			run := exec.Command("wasmtime", "run", watFile)
			_ = run.Run()
			if run.ProcessState == nil || !run.ProcessState.Exited() {
				t.Fatalf("wasmtime did not exit normally for %q:\n%s", tc.name, wat)
			}
			if code := run.ProcessState.ExitCode(); code != want {
				t.Errorf("opt-struct-payload-field wasm IR %q = %d, want %d (interp oracle)", tc.name, code, want)
			}
		})
	}
}
