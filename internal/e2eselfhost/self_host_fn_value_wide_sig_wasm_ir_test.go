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

	// --- the SHADOW rows (#7253) --------------------------------------------
	//
	// A nested `var g = <lambda>` shadowing a top-level one. `subst_fcall_expr`
	// rewrites every `g(…)` callee it walks past to the outer lambda's hoisted
	// `__lam_N`, and it recurses into if / while / for / match bodies with no
	// notion of scope — so the INNER binding's own calls ran the OUTER lambda.
	// In the emitted asm all three call sites were `call __fn___lam_0` while
	// `__fn___lam_1` was emitted and never reached.
	//
	// Each row pairs a shadowing program with a one-token RENAME control: rename
	// the inner binding and nothing else, and the answer must not move. Both are
	// oracle-checked against the interpreter, so a row that regresses fails on
	// its own, and the pair is what says the CAUSE was the name.
	//
	// The victim is always the inner binding — the outer one is what the lift
	// hoisted — so a probe that shadows the other way measures nothing.
	{"shadowed-sig", `function apply(v: i64): i64 {
    var g: (i64) => i64 = (x: i64) => x * 2i64;
    var t: i64 = g(v);
    if (v > 0i64) {
        var g: (i32) => i32 = (y: i32) => y + 1;
        t = t + (g(3) as i64);
    }
    return t + g(v + 1i64);
}
function main(): i32 { return apply(20i64) as i32; }`},
	{"shadowed-sig-rename-control", `function apply(v: i64): i64 {
    var g: (i64) => i64 = (x: i64) => x * 2i64;
    var t: i64 = g(v);
    if (v > 0i64) {
        var h: (i32) => i32 = (y: i32) => y + 1;
        t = t + (h(3) as i64);
    }
    return t + g(v + 1i64);
}
function main(): i32 { return apply(20i64) as i32; }`},

	// The RETURN half of the same seed: the inner call inherited the outer's
	// declared i64 return, so lower_i64 skipped the sign-extend its i32 result
	// needed and infer_expr_width width-tracked the binding at 64.
	{"shadowed-ret", `function apply(v: i64): i64 {
    var g: (i32) => i64 = (x: i32) => (x as i64) * 3000000000i64;
    var t: i64 = g(1);
    if (v > 0i64) {
        var g: (i32) => i32 = (y: i32) => y + 7;
        var u: i64 = g(3) as i64;
        t = t + u;
    }
    return (t / 1000000000i64) + g(2);
}
function main(): i32 { return (apply(4i64) % 83i64) as i32; }`},
	{"shadowed-ret-rename-control", `function apply(v: i64): i64 {
    var g: (i32) => i64 = (x: i32) => (x as i64) * 3000000000i64;
    var t: i64 = g(1);
    if (v > 0i64) {
        var h: (i32) => i32 = (y: i32) => y + 7;
        var u: i64 = h(3) as i64;
        t = t + u;
    }
    return (t / 1000000000i64) + g(2);
}
function main(): i32 { return (apply(4i64) % 83i64) as i32; }`},

	// The DYN-position half (#5276): the inner call inherited the outer's
	// dyn-boxed argument positions, so a plain integer argument was handed to
	// lower_dyn_arg and the callee compared a box POINTER against 4. The inner
	// body compares against a literal deliberately — a `y * 3` body would
	// multiply an arena address and give a non-reproducible exit.
	{"shadowed-dyn", `trait Show { function show(self: Self): i32; }
struct A { v: i32 }
impl Show for A { function show(self: Self): i32 { return self.v; } }
function run(k: i32): i32 {
    var d: (dyn Show) => i32 = (s: dyn Show) => s.show();
    var t: i32 = d(A { v: k });
    if (k % 2 == 0) {
        var d: (i32) => i32 = (y: i32) => if (y == 4) { 1 } else { 0 };
        t = t + d(4);
    }
    return t + d(A { v: 1 });
}
function main(): i32 {
    var t: i32 = 0; var i: i32 = 0;
    while (i < 100) { t = t + run(i); i = i + 1; }
    return t % 83;
}`},
	{"shadowed-dyn-rename-control", `trait Show { function show(self: Self): i32; }
struct A { v: i32 }
impl Show for A { function show(self: Self): i32 { return self.v; } }
function run(k: i32): i32 {
    var d: (dyn Show) => i32 = (s: dyn Show) => s.show();
    var t: i32 = d(A { v: k });
    if (k % 2 == 0) {
        var e: (i32) => i32 = (y: i32) => if (y == 4) { 1 } else { 0 };
        t = t + e(4);
    }
    return t + d(A { v: 1 });
}
function main(): i32 {
    var t: i32 = 0; var i: i32 = 0;
    while (i < 100) { t = t + run(i); i = i + 1; }
    return t % 83;
}`},
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
		"ir.fern", "irlower.fern", "irverify.fern", "irverifystack.fern", "irverifygate.fern", "ircore.fern", "asm_ir.fern", "wasm_ir.fern", "wasm_ir_run.fern")
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

// TestSelfHostFnValueWideSigIRX86_64 runs the same corpus through the self-host
// x86-64 asm path.
//
// The wasm leg is not sufficient for it. Wasm is where a wrong funcref type is
// LOUD — the validator refuses the module or the dispatch traps — so the rows
// #6282 was written for fail there first. The register backends have no
// structural funcref check at all: a mis-widthed argument is a wrong VALUE and
// nothing complains. Both #7253 shadow rows that move the answer rather than
// the module (`shadowed-ret`, `shadowed-dyn`) are that shape, and this is the
// leg that sees them.
func TestSelfHostFnValueWideSigIRX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	interpBin := buildLangBinForInterp(t)
	dir := writeSelfHostAsmProject(t)
	src, err := os.ReadFile("../../examples/self_host/asm_run.fern")
	if err != nil {
		t.Fatalf("read asm_run.fern: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "asm_run.fern"), src, 0o644); err != nil {
		t.Fatalf("write asm_run.fern: %v", err)
	}
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_run.fern", "driver")

	for _, tc := range fnValueWideSigCases {
		t.Run(tc.name, func(t *testing.T) {
			want := interpExit(t, interpBin, tc.src)

			asm := runCapture(t, gcc, runner, driverBin, []byte(tc.src))
			if len(asm) == 0 {
				t.Fatalf("self-host compiler emitted 0 bytes")
			}
			bin := buildBin(t, gcc, dir, tc.name, string(asm))
			var cmd *exec.Cmd
			if len(runner) == 0 {
				cmd = exec.Command(bin)
			} else {
				cmd = exec.Command(runner[0], append(runner[1:], bin)...)
			}
			_ = cmd.Run()
			if got := cmd.ProcessState.ExitCode(); got != want {
				t.Errorf("%s exited %d, want %d (interp oracle)", tc.name, got, want)
			}
		})
	}
}
