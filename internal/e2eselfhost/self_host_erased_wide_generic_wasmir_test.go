package e2eselfhost

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestSelfHostErasedWideGenericWasm pins the wasm side of the erased-generic
// 64-bit widening (#5464): a module passing a 64-bit or f64 value through a
// bare-typevar (erased-generic) PASS-THROUGH fn (`ident[T](x: T): T`) now
// LOWERS on the wasm IR path. The erased param/return/locals are typed i64 —
// the uniform 8-byte slot the register backends give every value — and the
// caller coerces its arg/result at the boundary (f64 <-> i64 reinterpret,
// i32/pointer <-> i64 extend/wrap). Before this the module deferred to the
// legacy AST emitter, which emitted type-INVALID WAT (`(call $ident
// (f64.const 2.5))` into an i32 erased param) that wasmtime rejects. The test
// asserts the module reaches the IR path (no `$__lit` AST-fallback scratch
// locals) AND computes the right value under wasmtime. Container-returning
// erased fns (tuple/array/Option/Result of T) still defer — a later slice.
func TestSelfHostErasedWideGenericWasm(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping erased-wide generic wasm e2e")
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
		src  string
		want int
	}{
		{"erased-f64-roundtrip",
			`function ident[T](x: T): T { return x; } function main(): i32 { var d: f64 = ident[f64](2.5); if (d == 2.5) { return 42; } return 38; }`,
			42},
		{"erased-i64-roundtrip",
			`function ident[T](x: T): T { return x; } function main(): i32 { var big: i64 = ident[i64](4200000000 as i64); if (big == 4200000000 as i64) { return 42; } return 38; }`,
			42},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var cmd *exec.Cmd
			if len(runner) == 0 {
				cmd = exec.Command(driverBin, "-ir")
			} else {
				cmd = exec.Command(runner[0], append(append(append([]string{}, runner[1:]...), driverBin), "-ir")...)
			}
			cmd.Stdin = bytes.NewReader([]byte(tc.src + "\n"))
			wat, err := cmd.Output()
			if err != nil || len(wat) == 0 {
				t.Fatalf("driver failed for %s: %v", tc.name, err)
			}
			// The AST fallback pre-declares `$__lit0` scratch locals; the IR
			// emitter does not. Its absence proves the module lowered on the IR
			// path (the point of #5464) rather than deferring to the AST emitter
			// that miscompiled it — a value-correct AST fallback would still be a
			// regression here.
			if strings.Contains(string(wat), "$__lit0") {
				t.Errorf("%s deferred to the AST path (found $__lit0); expected IR lowering", tc.name)
			}
			watFile := filepath.Join(dir, tc.name+".wat")
			if err := os.WriteFile(watFile, wat, 0o644); err != nil {
				t.Fatalf("write wat: %v", err)
			}
			rcmd := exec.Command("wasmtime", "run", watFile)
			_ = rcmd.Run()
			if rcmd.ProcessState == nil || !rcmd.ProcessState.Exited() {
				t.Fatalf("wasmtime did not exit normally for %s (an invalid module fails to load)", tc.name)
			}
			if got := rcmd.ProcessState.ExitCode(); got != tc.want {
				t.Errorf("%s = %d, want %d (38 = width truncated)", tc.name, got, tc.want)
			}
		})
	}
}
