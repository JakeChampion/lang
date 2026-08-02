package e2e

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// TestWasmSelfHostArrPush is a CI-runnable regression guard for the
// self-host wasm IR path's array `.append` (op "arr_push"). It is named
// TestWasm* deliberately: the test-e2e-wasm workflow installs wasmtime and
// runs `-run '^Test(WASM|Wasm)'`, whereas the broader TestSelfHostWasmRun /
// TestSelfHostWasmIRPath suites are named TestSelfHost* and run in the
// self-host workflow, which does NOT install wasmtime — so they skip there
// and provide no CI coverage for wasm-executed behaviour. This focused test
// closes that gap for the append path.
//
// The bug it guards: the IR wasm backend lowered `a.append(v)` to a
// `call $__fern_arr_push` but the helper-emission gate (the IR
// runtime section) never emitted the $__fern_arr_push definition unless
// the module also used str_split, so an append-only program produced an
// invalid module (undefined function → wasmtime exits 1). Fixed by gating
// arr_push_helper on the arr_push op.
func TestWasmSelfHostArrPush(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping self-host wasm append e2e")
	}
	gcc, runner := x86_64Tooling(t)

	dir := t.TempDir()
	for _, name := range []string{"lexer.fern", "parser.fern", "util.fern", "astwalk.fern", "asmcore.fern", "ir.fern", "irlower.fern", "asm_ir.fern", "wasm_ir.fern", "wasm_run.fern"} {
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
		name   string
		source string
		exit   int
		stdout string
	}{
		// i32 append: len, indexed read, empty start, chain, grow-then-sum.
		{"append-len", "function main(): i32 { var a: i32[] = [1, 2, 3]; a = a.append(4); return a.len(); }", 4, ""},
		{"append-empty", "function main(): i32 { var a: i32[] = []; a = a.append(42); return a[0]; }", 42, ""},
		{"append-chain", "function main(): i32 { var a: i32[] = []; a = a.append(1); a = a.append(2); a = a.append(3); return a[0] + a[1] + a[2]; }", 6, ""},
		{"append-grow", "function main(): i32 { var a: i32[] = []; var i = 0; while (i < 10) { a = a.append(i); i = i + 1; } var s = 0; for x in a { s = s + x; } return s; }", 45, ""},
		// string append (a string is one 4-byte slot on wasm32, same push).
		{"append-string", "function main(): i32 { var xs: string[] = [\"a\"]; xs = xs.append(\"b\"); write(xs[1]); return xs.len(); }", 2, "b"},
		// append must coexist with str_split (which bundles its own
		// $__fern_arr_push) without a double-definition.
		{"append-with-split", "function main(): i32 { var parts = \"a,b,c\".split(\",\"); var xs: i32[] = []; xs = xs.append(parts.len()); return xs[0]; }", 3, ""},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			wat := runCapture(t, gcc, runner, driverBin, []byte(tc.source))
			if len(wat) == 0 {
				t.Fatal("wasm emitter produced 0 bytes")
			}
			watPath := filepath.Join(t.TempDir(), "prog.wat")
			if err := os.WriteFile(watPath, wat, 0o644); err != nil {
				t.Fatalf("write wat: %v", err)
			}
			cmd := exec.Command("wasmtime", "run", watPath)
			out, _ := cmd.Output()
			if code := cmd.ProcessState.ExitCode(); code != tc.exit {
				t.Errorf("%s: wasm exited %d, want %d\n--- WAT ---\n%s", tc.name, code, tc.exit, wat)
			}
			if tc.stdout != "" && string(out) != tc.stdout {
				t.Errorf("%s: wasm stdout = %q, want %q", tc.name, string(out), tc.stdout)
			}
		})
	}
}
