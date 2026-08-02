package e2eselfhost

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// structUpdateIRCases pin functional struct-update expressions
// (`T { ...base, field: v }`) to the self-host IR path on x86-64 + wasm. irlower
// fully lowers an ExprStructLit with a base (emit each declared field in order;
// lower the overrides, struct_get-copy the rest from the base), gated only by
// decl_is_struct + decl_is_leaksafe — so an all-scalar or scalar+string struct
// routes IR. Eligibility is just "does lower_func return ok", so these route
// "ir". Two existing tests (self_host_struct_update_test.go,
// self_host_functional_update_test.go) exercise struct-update but assert ONLY
// exit codes, which the AST emitter also satisfies — a regression that kicked
// struct-update onto the AST fallback would pass silently. These cases close that
// gap with the path-probe pin (assert path == "ir") + interp oracle, mirroring
// self_host_block_expr_ir_test.go.
//
// Every struct is leaksafe (all-i32, or i32+string) and every result <= 126
// (wasmtime exit-code truncation, cf. #2908).
var structUpdateIRCases = []struct {
	name string
	main string
}{
	// Single field override; the other two fields copy from the base.
	// q = {1, 20, 3} -> 24.
	{"single-override", `struct P { x: i32, y: i32, z: i32 }
function main(): i32 { var p: P = P { x: 1, y: 2, z: 3 }; var q: P = P { ...p, y: 20 }; return q.x + q.y + q.z; }`},
	// A STRING field copies through the update unchanged while an i32 field is
	// overridden — exercises the leaksafe non-i32 copy path. t.name == "hi" -> 9.
	{"string-field-copy", `struct S { name: string, n: i32 }
function main(): i32 { var s: S = S { name: "hi", n: 3 }; var t: S = S { ...s, n: 9 }; if (t.name == "hi") { return t.n; } return 0; }`},
	// Update in return position with a NON-ident base computation (p.b + 100),
	// spilling the base once. q = {5, 106} -> 111.
	{"update-in-return", `struct P { a: i32, b: i32 }
function bump(p: P): P { return P { ...p, b: p.b + 100 }; }
function main(): i32 { var p: P = P { a: 5, b: 6 }; var q: P = bump(p); return q.a + q.b; }`},
	// Functional update threaded through a loop (the immutable-counter idiom).
	// inc 5 times from 0 -> 5.
	{"functional-loop", `struct C { n: i32 }
function inc(c: C): C { return C { ...c, n: c.n + 1 }; }
function main(): i32 { var c: C = C { n: 0 }; var i: i32 = 0; while (i < 5) { c = inc(c); i = i + 1; } return c.n; }`},
}

// TestSelfHostStructUpdateIRX86_64 routes each struct-update case through the
// self-hosted x86-64 IR driver, oracle-checked, with routing pinned to "ir".
func TestSelfHostStructUpdateIRX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	interpBin := buildLangBinForInterp(t)
	dir := writeSelfHostAsmProject(t)
	copySelfHostDriver(t, dir, "asm_run.fern", "asm_pathprobe_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_run.fern", "driver")
	probeBin := buildSelfHostBin(t, gcc, dir, "asm_pathprobe_run.fern", "pathprobe")

	for _, tc := range structUpdateIRCases {
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

// TestSelfHostStructUpdateIRWasm runs the same cases through the wasm IR backend.
func TestSelfHostStructUpdateIRWasm(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping self-host struct-update wasm IR e2e")
	}
	gcc, runner := x86_64Tooling(t)
	interpBin := buildLangBinForInterp(t)
	dir := t.TempDir()
	for _, name := range []string{
		"util.fern", "astwalk.fern", "asmcore.fern", "lexer.fern", "parser.fern",
		"ir.fern", "irlower.fern", "asm_ir.fern", "wasm_ir.fern", "wasm_ir_run.fern",
	} {
		src, err := os.ReadFile(filepath.Join("../../examples/self_host", name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		if err := os.WriteFile(filepath.Join(dir, name), src, 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	driverBin := buildSelfHostBin(t, gcc, dir, "wasm_ir_run.fern", "driver")

	for _, tc := range structUpdateIRCases {
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
			watFile := filepath.Join(dir, "struct_update_prog.wat")
			if err := os.WriteFile(watFile, wat, 0o644); err != nil {
				t.Fatalf("write wat: %v", err)
			}
			run := exec.Command("wasmtime", "run", watFile)
			_ = run.Run()
			if run.ProcessState == nil || !run.ProcessState.Exited() {
				t.Fatalf("wasmtime did not exit normally for %q:\n%s", tc.name, wat)
			}
			if code := run.ProcessState.ExitCode(); code != want {
				t.Errorf("struct-update wasm IR %q = %d, want %d (interp oracle)", tc.name, code, want)
			}
		})
	}
}
