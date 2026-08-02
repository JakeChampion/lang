package e2eselfhost

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// TestSelfHostWasmIRStructDropEmitted pins the wasm IR driver's RC-helper
// emission. The Perceus deep-drop work (#4083) inserts `call $__struct_drop_<T>`
// into the lowered IR for any struct with rc fields at scope exit, and
// emit_module's IR orchestration emits the matching `$__struct_drop_<T>`
// FUNCTION BODY. The differential wasm_ir_run driver assembles its own helper
// section, so it must emit the same bodies (via wasm.emit_ir_rc_bodies) —
// otherwise the lowered `call $__struct_drop_<T>` references an undefined
// function and wasmtime rejects the module ("unknown func $__struct_drop_<T>"),
// trapping (exit 1) for EVERY struct program with a nested-struct or rc-array
// field. This test feeds two such programs through the driver and asserts both
// that the WAT carries the `(func $__struct_drop_<T>` DEFINITION (not just the
// call) and that the module runs to the expected exit code.
func TestSelfHostWasmIRStructDropEmitted(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping self-host wasm IR struct-drop e2e")
	}
	gcc, runner := x86_64Tooling(t)
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

	cases := []struct {
		name string
		// drop is the struct type whose `$__struct_drop_<drop>` definition the
		// WAT must contain.
		drop string
		want int
		src  string
	}{
		// Nested-struct field: Box{p:Point} passed to bx() -> Perceus drops the
		// owned param, and $__struct_drop_Box recursively struct_drops the nested
		// Point (slice-3 deep-drop). This is the exact program that trapped with
		// `unknown func $__struct_drop_Box` before the driver emitted the body.
		{"nested-struct", "Box", 42,
			`struct Point { x: i32, y: i32 } struct Box { p: Point } function bx(b: Box): i32 { return b.p.x + b.p.y; } function main(): i32 { var b = Box { p: Point { x: 30, y: 12 } }; return bx(b); }`},
		// Three-deep nesting: Outer{Mid{Inner}} consumed by f() -> the drop chain
		// $__struct_drop_Outer -> $__struct_drop_Mid -> $__struct_drop_Inner must
		// all be DEFINED (the transitive deep-drop closure).
		{"deep-nested", "Outer", 105,
			`struct Inner { v: i32 } struct Mid { inner: Inner, n: i32 } struct Outer { mid: Mid } function f(o: Outer): i32 { return o.mid.inner.v + o.mid.n; } function main(): i32 { var o = Outer { mid: Mid { inner: Inner { v: 100 }, n: 5 } }; return f(o); }`},
	}

	for _, tc := range cases {
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
			// The CALL must be present (proving the program routed IR and the
			// deep-drop fired) AND its DEFINITION must be emitted by the driver.
			call := []byte("call $__struct_drop_" + tc.drop)
			def := []byte("(func $__struct_drop_" + tc.drop)
			if !bytes.Contains(wat, call) {
				t.Fatalf("no `%s` in WAT — program did not route through the IR struct-drop path\n%s", call, wat)
			}
			if !bytes.Contains(wat, def) {
				t.Fatalf("WAT calls $__struct_drop_%s but never defines it (driver missing emit_ir_rc_bodies)\n%s", tc.drop, wat)
			}
			watFile := filepath.Join(dir, "drop_"+tc.name+".wat")
			if err := os.WriteFile(watFile, wat, 0o644); err != nil {
				t.Fatalf("write wat: %v", err)
			}
			run := exec.Command("wasmtime", "run", watFile)
			_ = run.Run()
			if run.ProcessState == nil || !run.ProcessState.Exited() {
				t.Fatalf("wasmtime did not exit normally:\n%s", wat)
			}
			if code := run.ProcessState.ExitCode(); code != tc.want {
				t.Errorf("struct-drop program %q exited %d, want %d\n--- WAT ---\n%s", tc.name, code, tc.want, wat)
			}
		})
	}
}
