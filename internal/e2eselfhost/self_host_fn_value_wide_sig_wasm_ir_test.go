package e2eselfhost

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// fnValueWideSigCases pin #6282: a function VALUE whose declared signature
// mentions a 64-bit type.
//
// A funcref type in wasm is STRUCTURAL — the validator checks the type a
// `call_indirect` names against the type of whatever the table holds at that
// index. The emitter keyed those types by arity alone and hardcoded every
// param and the result as i32, so every 2-arg closure in a module shared one
// `(type $fn2 (func (param i32) (param i32) (result i32)))` regardless of what
// it actually took or returned. The trampolines in the table carried their REAL
// signatures, so the two disagreed the moment a 64-bit type appeared:
//
//	(i64) => boolean   the module will not LOAD  ("expected i32, found i64")
//	(i32) => i64       it loads and TRAPS        ("indirect call type mismatch")
//
// Two failure modes, one cause — whether the validator catches the disagreement
// at the call's arguments or the mismatch survives to dispatch. Neither is
// diagnosable for a user: one names a wasm offset, the other has no source
// location at all.
//
// Getting the type right is necessary and not sufficient. The result width also
// has to flow: irlower typed an indirect call's result as i32 and appended
// `i64.extend_i32_s` in an i64 context, which after the type fix is a widen
// applied to an already-i64 operand — the same "expected i32, found i64", now
// raised INSIDE the caller. Both halves are needed for the `(i32) => i64` rows
// to pass, which is why they are here together.
//
// Every case is oracle-checked against the reference interpreter rather than a
// hardcoded exit code: a wrong-but-stable answer is exactly what the wrong-type
// dispatch produced on the rows that did not trap.
var fnValueWideSigCases = []struct {
	name string
	src  string
}{
	// A wide PARAM, called in a loop — sim.sweep_seeds reduced. The module did
	// not load: the call pushes a real i64 where $fn2 declared i32.
	{"wide-param-loop", `function sweep(n: i64, prop: (i64) => boolean): i64 {
    var s: i64 = 1;
    while (s <= n) {
        if (!prop(s)) { return s; }
        s = s + 1;
    }
    return 0;
}
function main(): i32 { return sweep(5 as i64, (x: i64) => x < 3) as i32; }`},
	// A wide RESULT — the sharper row: it loaded and trapped at dispatch.
	{"wide-result", `function apply(g: (i32) => i64): i64 { return g(5); }
function main(): i32 { return (apply((x: i32) => (x as i64) * 3000000000i64) / 3000000000i64) as i32; }`},
	// Wide on BOTH sides, so neither half of the fix alone can carry it.
	{"wide-param-and-result", `function apply(g: (i64) => i64, v: i64): i64 { return g(v); }
function main(): i32 { return (apply((x: i64) => x * 2i64, 21i64)) as i32; }`},
	// An f64 parameter. f64 is a distinct wasm value type for exactly the same
	// reason i64 is, so it fails identically — and it is the case a fix written
	// only against the i64 reports would miss.
	{"f64-param", `function pick(g: (f64) => i32, v: f64): i32 { return g(v); }
function main(): i32 { return pick((x: f64) => (x * 2.0) as i32, 21.0); }`},
	// The wide result bound to a local rather than returned — the path through
	// infer_expr_width rather than lower_i64's return context.
	{"wide-result-bound", `function apply(g: (i32) => i64): i64 { var v: i64 = g(5); return v + 1i64; }
function main(): i32 { return (apply((x: i32) => (x as i64) * 3000000000i64) / 1000000000i64) as i32; }`},
	// Control: an all-i32 signature keeps the arity-keyed $fn<N> type and must
	// be unaffected. If this ever fails the fallback has stopped falling back.
	{"all-i32-control", `function apply(g: (i32) => i32): i32 { return g(5); }
function main(): i32 { return apply((x: i32) => x * 2); }`},
}

// TestSelfHostFnValueWideSigWasmIR runs the corpus through the self-host wasm
// IR path (wasm_ir_run `-ir`) and checks each program against the interpreter.
func TestSelfHostFnValueWideSigWasmIR(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping self-host fn-value wide-signature wasm IR e2e")
	}
	gcc, runner := x86_64Tooling(t)
	interpBin := buildLangBinForInterp(t)
	dir := t.TempDir()
	copySelfHostFiles(t, dir,
		"util.fern", "astwalk.fern", "asmcore.fern", "lexer.fern", "parser.fern",
		"ir.fern", "irlower.fern", "irverify.fern", "irverifystack.fern", "irverifygate.fern", "asm_ir.fern", "wasm_ir.fern", "wasm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "wasm_ir_run.fern", "driver")

	for _, tc := range fnValueWideSigCases {
		t.Run(tc.name, func(t *testing.T) {
			want := interpExit(t, interpBin, tc.src)

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

			// The control must keep the arity-keyed name: the signature keying
			// falls back to it for every all-i32 and every erased-generic call
			// site, and that fallback is what keeps the rest of the corpus
			// byte-identical. A test that only checked exit codes would pass
			// with the fallback removed and every module churned.
			if tc.name == "all-i32-control" && !strings.Contains(string(wat), "(type $fn2 ") {
				t.Errorf("all-i32 control no longer emits the arity-keyed $fn2 type; the fallback moved")
			}

			rcmd := exec.Command("wasmtime", "run", watFile)
			var so, se bytes.Buffer
			rcmd.Stdout, rcmd.Stderr = &so, &se
			_ = rcmd.Run()
			if rcmd.ProcessState == nil || !rcmd.ProcessState.Exited() {
				t.Fatalf("wasmtime did not exit normally (invalid module or a trap — the bug):\n%s", se.String())
			}
			if got := rcmd.ProcessState.ExitCode(); got != want {
				t.Errorf("%s exited %d, want %d (interp oracle)\nstderr: %s", tc.name, got, want, se.String())
			}
		})
	}
}
