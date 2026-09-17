package e2eselfhost

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// TestSelfHostTupleIdentTagWasmIR pins the fix for #9468: an i64 tuple element
// named by an ident the tag pass cannot resolve was stored 32 bits wide.
//
// elem_type_tag classifies each element of a tuple literal, and the kind list it
// produces is what emit_wasm_tuple_make selects the store instruction from.
// ident_is_fn_value — the ident arm's predicate — ended in `return true`, so any
// ident that was not a local slot, `None`, a struct / unit-variant name or a
// const answered "a function value" and the element was tagged "fn": one 4-byte
// pointer. A match-arm payload binding is exactly such an ident (it has no slot),
// so an i64 payload was emitted with i32.store and the module failed validation
// with "type mismatch: expected i32, found i64".
//
// wasm alone, because the register backends ignore the kind list and read the
// element's own width.
//
// Both cases build the same tuple from a match-arm binding, one bound by `%?`'s
// desugaring and one by a plain Option match, since the cause is the unresolved
// ident rather than either binder. The exit code is oracle-checked against the
// interpreter, and an invalid module fails before that: wasmtime cannot run it.
func TestSelfHostTupleIdentTagWasmIR(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping self-host tuple ident-tag wasm IR e2e")
	}
	gcc, runner := x86_64Tooling(t)
	interpBin := buildLangBinForInterp(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "wasm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "wasm_ir_run.fern", "driver")

	// 9000000000000000007 % 4000000000000000000 is 1000000000000000007, which
	// needs all 64 bits: read back 32 bits wide it loses the high half, so the
	// division below answers 0 instead of 31.
	cases := []struct{ name, src string }{
		{"try_op_payload_binding", `function mk(a: i64, b: i64): (i32, i64) {
    return (7i32, (match (a %? b) { Some(v) => v, None => 0i64 }));
}

function main(): i32 {
    var t: (i32, i64) = mk(9000000000000000007i64, 4000000000000000000i64);
    return ((t.1 / 4294967296i64) % 251i64) as i32;
}`},
		{"option_match_binding", `function pick(a: i64): Option[i64] { if (a > 0i64) { return Some(a); } return None; }

function mk(a: i64): (i32, i64) {
    return (7i32, (match (pick(a)) { Some(w) => w, None => 0i64 }));
}

function main(): i32 {
    var t: (i32, i64) = mk(1000000000000000007i64);
    return ((t.1 / 4294967296i64) % 251i64) as i32;
}`},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			want := interpExit(t, interpBin, tc.src)
			if want == 0 {
				t.Fatalf("the oracle answers 0, so a truncated element would pass — the program stopped discriminating")
			}

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
			rcmd := exec.Command("wasmtime", "run", watFile)
			out, _ := rcmd.CombinedOutput()
			if rcmd.ProcessState == nil || !rcmd.ProcessState.Exited() {
				t.Fatalf("wasmtime did not exit normally (an invalid module is the bug):\n%s", out)
			}
			if got := rcmd.ProcessState.ExitCode(); got != want {
				t.Errorf("exited %d, want %d (interp oracle)\n%s", got, want, out)
			}
		})
	}
}
