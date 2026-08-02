package e2eselfhost

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// TestSelfHostMethodReturnsClosureIR pins that a closure returned from a
// METHOD is bound as a closure local at the call site, so calling it unpacks
// the env box instead of treating the box pointer as a function index.
//
// irlower's `var f = <call>` closure-binding decision only ever inspected a
// bare-IDENT callee (a free function). A method call's callee is an
// ExprFieldAccess, which fell through the match unhandled — so `var f =
// m.make()` left `f` a plain fn-value local and `f(21)` lowered to a direct
// `call_indirect` passing the BOX POINTER as the table index, with the wrong
// signature type to boot (the wrapper takes env + arg, the call site declared
// one param). wasm trapped at runtime:
//
//	wasm trap: undefined element: out of bounds table access
//
// (the emitted box stored function index 1 against a 1-entry table holding
// only index 0), and the register backends jumped through the box pointer.
//
// Both legs are oracle-checked against the reference interpreter. Part of
// #4801, and the same closure-dispatch family as #5001 / #5007 / #5009 /
// #5026.
func TestSelfHostMethodReturnsClosureIR(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping self-host method-returns-closure IR e2e")
	}
	gcc, runner := x86_64Tooling(t)
	interpBin := buildLangBinForInterp(t)

	dir := t.TempDir()
	for _, name := range []string{
		"util.fern", "astwalk.fern", "asmcore.fern", "lexer.fern", "parser.fern",
		"ir.fern", "irlower.fern", "asm_ir.fern", "wasm_ir.fern", "wasm_run.fern",
	} {
		src, err := os.ReadFile(filepath.Join("../../examples/self_host", name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		if err := os.WriteFile(filepath.Join(dir, name), src, 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	driverBin := buildSelfHostBin(t, gcc, dir, "wasm_run.fern", "wasm_run")

	cases := []struct {
		name string
		src  string
	}{
		{
			// The method returns a NON-capturing lambda. Still an env box, so
			// the call site must dispatch env-first.
			name: "noncapturing",
			src: `struct Maker { }
function (m: Maker) make(): (i32) => i32 { return function(x: i32): i32 { return x * 2; }; }
function main(): i32 { var m = Maker { }; var f = m.make(); return f(21); }`,
		},
		{
			// The lambda captures the method's PARAMETER, so the env box
			// actually carries a value — a wrong dispatch here reads garbage
			// rather than merely mis-indexing.
			name: "captures-method-param",
			src: `struct F { }
function (f: F) mul(k: i32): (i32) => i32 { return function(x: i32): i32 { return x * k; }; }
function main(): i32 { var f = F { }; var g = f.mul(7); return g(6); }`,
		},
		{
			// Called immediately without binding to a local, so the closure
			// never becomes a local at all — a different lowering path to the
			// two above.
			name: "called-without-binding",
			src: `struct Maker { }
function (m: Maker) make(): (i32) => i32 { return function(x: i32): i32 { return x * 3; }; }
function main(): i32 { var m = Maker { }; return m.make()(14); }`,
		},
		{
			// A free function returning a closure — the path that already
			// worked. Pins that the new ExprFieldAccess arm did not disturb it.
			name: "free-fn-unaffected",
			src: `function make(): (i32) => i32 { return function(x: i32): i32 { return x * 2; }; }
function main(): i32 { var f = make(); return f(21); }`,
		},
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
			watFile := filepath.Join(dir, tc.name+".wat")
			if err := os.WriteFile(watFile, wat, 0o644); err != nil {
				t.Fatalf("write wat: %v", err)
			}
			rcmd := exec.Command("wasmtime", "run", watFile)
			_ = rcmd.Run()
			if rcmd.ProcessState == nil || !rcmd.ProcessState.Exited() {
				t.Fatalf("wasmtime did not exit normally:\n%s", wat)
			}
			want := interpExit(t, interpBin, tc.src)
			if got := rcmd.ProcessState.ExitCode(); got != want {
				t.Errorf("%s: wasm exited %d, want %d (interp oracle)", tc.name, got, want)
			}
		})
	}
}
