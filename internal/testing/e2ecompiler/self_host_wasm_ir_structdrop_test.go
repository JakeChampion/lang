package e2ecompiler

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// TestSelfHostWasmIRStructDropEmitted pins the wasm driver's RC-helper
// emission: a program that releases a struct with a nested-struct field at
// scope exit calls `$__sem_release_<T>`, and the module must also DEFINE it, or
// wasmtime rejects it ("unknown func"). This test feeds two such programs
// through the driver and asserts both that the WAT carries the
// `(func $__sem_release_<T>` DEFINITION (not just the call) and that the module
// runs to the expected exit code.
func TestSelfHostWasmIRStructDropEmitted(t *testing.T) {
	boxedProbes(t)
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping self-host wasm IR struct-drop e2e")
	}
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/wasm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "drivers/wasm_ir_run.fern", "driver")

	cases := []struct {
		name string
		// drop is the struct type whose `$__sem_release_<drop>` definition the
		// WAT must contain.
		drop string
		want int
		src  string
	}{
		// Nested-struct field: Box{p:Point} passed to bx() -> the caller releases
		// the Box, nested Point and all.
		{"nested-struct", "Box", 42,
			`struct Point { x: i32, y: i32 } struct Box { p: Point } function bx(b: Box): i32 { return b.p.x + b.p.y; } function main(): i32 { let b = Box { p: Point { x: 30, y: 12 } }; return bx(b); }`},
		// Three-deep nesting: Outer{Mid{Inner}} consumed by f() -> the release
		// chain through Outer and Mid must all be DEFINED.
		{"deep-nested", "Outer", 105,
			`struct Inner { v: i32 } struct Mid { inner: Inner, n: i32 } struct Outer { mid: Mid } function f(o: Outer): i32 { return o.mid.inner.v + o.mid.n; } function main(): i32 { let o = Outer { mid: Mid { inner: Inner { v: 100 }, n: 5 } }; return f(o); }`},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var cmd *exec.Cmd
			if len(runner) == 0 {
				cmd = exec.Command(driverBin)
			} else {
				cmd = exec.Command(runner[0], append(append([]string{}, runner[1:]...), driverBin)...)
			}
			cmd.Stdin = bytes.NewReader([]byte(tc.src))
			wat, err := cmd.Output()
			if err != nil || len(wat) == 0 {
				t.Fatalf("driver failed: %v", err)
			}
			// The CALL must be present (proving the program routed IR and the
			// deep-drop fired) AND its DEFINITION must be emitted by the driver.
			call := []byte("call $__sem_release_" + tc.drop)
			def := []byte("(func $__sem_release_" + tc.drop)
			if !bytes.Contains(wat, call) {
				t.Fatalf("no `%s` in WAT — the struct is not released\n%s", call, wat)
			}
			if !bytes.Contains(wat, def) {
				t.Fatalf("WAT calls $__sem_release_%s but never defines it\n%s", tc.drop, wat)
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
