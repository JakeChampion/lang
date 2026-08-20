package e2eselfhost

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// nestedBodyPassBlindnessCases pin two parser passes that walked a flat
// statement list and never reached the bodies nested inside it (#7199, the
// #7174 / #6993 family).
//
// Both were invisible at top level and only appeared one block down, which is
// what kept them hidden: the same program with the construct unwrapped compiles
// correctly on every path.
//
//  1. cf_inline_fn_body — blind to ALL nested bodies, not just lambdas; a plain
//     `if` was enough. Its `fn[]` annotation never fired, so the array lowering
//     const-CALLED each element and `fns[0]()` called an integer as a code
//     pointer: the self-host binary SIGSEGVs (139) where native answers 42.
//     The `fn[]` annotation must be ABSENT for this to bite — writing
//     `var fns: (() => i32)[]` compiles correctly even unfixed, so an annotated
//     probe proves nothing.
//
//  2. settle_block — descended into a lambda but passed empty name/type lists,
//     so neither the lambda's own params nor the enclosing locals it captures
//     were in scope for literal settling. A literal assigned to an `f64` param
//     settled as an integer and the compiled binary read the raw i32 bit
//     pattern as a denormal: 0 where native gives 3, silently. A method call
//     resolves only from the receiver's ANNOTATION, so for a CAPTURED receiver
//     that annotation lives in the enclosing scope and its arguments went
//     unsettled too. An annotated local in the same lambda was always correct,
//     which is what pins the gap to the dropped scope rather than the descent.
//
// The third defect in #7199 (resolve_labels_block) landed earlier; its case is
// kept here as a regression guard because a `continue outer` degrading to the
// innermost loop hangs the compiled binary forever, which no timeout in the
// suite would attribute correctly.
//
// Exit 0 is correct; each nonzero code names the check that failed.
var nestedBodyPassBlindnessCases = []struct {
	name string
	src  string
}{
	// cf_inline: an UNANNOTATED fn array, one `if` deep.
	{"fn-array-in-if", `function mk(): i32 { return 42; }
function mk2(): i32 { return 7; }

function main(): i32 {
    if (true) {
        var fns = [mk, mk2];
        if (fns[0]() != 42) { return 90; }
        if (fns[1]() != 7) { return 91; }
        return 0;
    }
    return 92;
}
`},
	// cf_inline: an unannotated fn LOCAL, one `if` deep.
	{"fn-local-in-if", `function mk(): i32 { return 42; }

function main(): i32 {
    if (true) {
        var f = mk;
        if (f() != 42) { return 90; }
        return 0;
    }
    return 91;
}
`},
	// cf_inline: inside a lambda body — the expression-hosted statement list.
	{"fn-array-in-lambda", `function mk(): i32 { return 42; }
function mk2(): i32 { return 7; }

function main(): i32 {
    var g: () => i32 = function (): i32 {
        var fns = [mk, mk2];
        return fns[0]() + fns[1]();
    };
    if (g() != 49) { return 90; }
    return 0;
}
`},
	// cf_inline: two levels down, through a loop then an if.
	{"fn-local-in-while-if", `function mk(): i32 { return 42; }

function main(): i32 {
    var i: i32 = 0;
    while (i < 1) {
        if (true) {
            var f = mk;
            if (f() != 42) { return 90; }
            return 0;
        }
        i = i + 1;
    }
    return 91;
}
`},
	// settle: a literal assigned to the lambda's own f64 PARAM.
	{"lambda-f64-param-literal", `function main(): i32 {
    var f: (f64) => i32 = function (x: f64): i32 {
        x = 3;
        if (x > 2.5) { return 0; }
        return 90;
    };
    return f(1.0);
}
`},
	// settle control: an annotated LOCAL in the same lambda was always correct,
	// so this passes either side of the fix and isolates the param scope.
	{"lambda-f64-local-control", `function main(): i32 {
    var f: () => i32 = function (): i32 {
        var y: f64 = 3;
        if (y > 2.5) { return 0; }
        return 90;
    };
    return f();
}
`},
	// settle: a method call on a CAPTURED annotated local. The receiver type
	// comes from `acc`'s annotation in the ENCLOSING scope, so dropping that
	// scope leaves Acc.bump unresolved and its `2` unsettled in an f64 param.
	{"lambda-captured-receiver", `struct Acc { v: f64 }

function (a: Acc) bump(d: f64): f64 { return a.v + d; }

function main(): i32 {
    var acc: Acc = Acc { v: 1.0 };
    var f: () => f64 = function (): f64 { return acc.bump(2); };
    var r: f64 = f();
    if (r > 2.5 && r < 3.5) { return 0; }
    return 90;
}
`},
	// The same capture two lambdas deep: the inner body only has a scope to
	// inherit if the outer one was given one.
	{"lambda-captured-receiver-nested", `struct Acc { v: f64 }

function (a: Acc) bump(d: f64): f64 { return a.v + d; }

function main(): i32 {
    var acc: Acc = Acc { v: 1.0 };
    var f: () => f64 = function (): f64 {
        var g: () => f64 = function (): f64 { return acc.bump(2); };
        return g();
    };
    var r: f64 = f();
    if (r > 2.5 && r < 3.5) { return 0; }
    return 90;
}
`},
	// Shadowing: the param is appended after the inherited locals and
	// settle_local_type scans from the end, so `acc.bump(2)` inside the lambda
	// must settle against Ctr.bump's i32 param, not the captured Acc.bump's
	// f64 one. Inheriting the enclosing scope must not outrank a param.
	{"lambda-param-shadows-capture", `struct Acc { v: f64 }

function (a: Acc) bump(d: f64): f64 { return a.v + d; }

struct Ctr { n: i32 }

function (c: Ctr) bump(d: i32): i32 { return c.n + d; }

function run(f: (Ctr) => i32): i32 { return f(Ctr { n: 40 }); }

function main(): i32 {
    var acc: Acc = Acc { v: 1.0 };
    var r: i32 = run(function (acc: Ctr): i32 { return acc.bump(2); });
    if (r != 42) { return 90; }
    var q: f64 = acc.bump(1);
    if (q > 1.5 && q < 2.5) { return 0; }
    return 91;
}
`},
	// resolve_labels regression guard: `break outer` must leave the OUTER loop.
	{"labeled-break-in-lambda", `function main(): i32 {
    var f: () => i32 = function (): i32 {
        var n: i32 = 0;
        outer: while (n < 100) {
            inner: while (true) {
                n = n + 1;
                break outer;
            }
            n = n + 100;
        }
        return n;
    };
    if (f() != 1) { return 90; }
    return 0;
}
`},
	// Control: the same fn-array shape at TOP level, which always worked. It
	// pins that the new descent did not disturb the flat path it already had.
	{"fn-array-top-level-control", `function mk(): i32 { return 42; }
function mk2(): i32 { return 7; }

function main(): i32 {
    var fns = [mk, mk2];
    if (fns[0]() != 42) { return 90; }
    if (fns[1]() != 7) { return 91; }
    return 0;
}
`},
}

// TestSelfHostNestedBodyPassBlindnessIRX86_64 runs each case through the
// self-hosted x86-64 IR driver, pinned to the "ir" path.
func TestSelfHostNestedBodyPassBlindnessIRX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := writeSelfHostAsmProject(t)
	copySelfHostDriver(t, dir, "asm_run.fern", "asm_pathprobe_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_run.fern", "driver")
	probeBin := buildSelfHostBin(t, gcc, dir, "asm_pathprobe_run.fern", "pathprobe")

	for _, tc := range nestedBodyPassBlindnessCases {
		t.Run(tc.name, func(t *testing.T) {
			src := []byte(tc.src)
			path := strings.TrimSpace(string(runCapture(t, gcc, runner, probeBin, src)))
			if path != "ir" {
				t.Fatalf("%s routed through %q path, want \"ir\"", tc.name, path)
			}
			asm := runCapture(t, gcc, runner, driverBin, src)
			if len(asm) == 0 {
				t.Fatal("self-host compiler emitted 0 bytes")
			}
			progBin := buildBin(t, gcc, dir, "nbpb_"+tc.name, string(asm))
			var cmd *exec.Cmd
			if len(runner) == 0 {
				cmd = exec.Command(progBin)
			} else {
				cmd = exec.Command(runner[0], append(runner[1:], progBin)...)
			}
			_ = cmd.Run()
			if code := cmd.ProcessState.ExitCode(); code != 0 {
				t.Errorf("%s exited %d, want 0 (139 = the fn array was const-called; 90+ = a check failed)", tc.name, code)
			}
		})
	}
}

// TestSelfHostNestedBodyPassBlindnessIRArm64 is the arm64 leg, run under qemu.
func TestSelfHostNestedBodyPassBlindnessIRArm64(t *testing.T) {
	arm64gcc, qemu := arm64Tooling(t)
	x86gcc, x86runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, x86gcc, dir, "asm_ir_run.fern", "driver")

	for _, tc := range nestedBodyPassBlindnessCases {
		t.Run(tc.name, func(t *testing.T) {
			asm := runCapture(t, x86gcc, x86runner, driverBin, []byte(tc.src), "-target", "arm64-linux")
			if len(asm) == 0 {
				t.Fatalf("%s: self-host arm64 compiler emitted 0 bytes", tc.name)
			}
			bin := buildBinArm64(t, arm64gcc, dir, "nbpb_"+tc.name, string(asm))
			cmd := runArm64Bin(qemu, bin)
			_ = cmd.Run()
			if code := cmd.ProcessState.ExitCode(); code != 0 {
				t.Errorf("%s exited %d, want 0 (139 = the fn array was const-called; 90+ = a check failed)", tc.name, code)
			}
		})
	}
}

// TestSelfHostNestedBodyPassBlindnessIRWasm is the wasm leg. Both passes live in
// parser.fern, so the blindness was backend-independent.
func TestSelfHostNestedBodyPassBlindnessIRWasm(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping self-host nested-body-pass-blindness wasm IR e2e")
	}
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "wasm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "wasm_ir_run.fern", "driver")

	for _, tc := range nestedBodyPassBlindnessCases {
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
				t.Fatalf("driver failed for %q: %v", tc.name, err)
			}
			watFile := filepath.Join(dir, "nestedbodyblind_prog.wat")
			if err := os.WriteFile(watFile, wat, 0o644); err != nil {
				t.Fatalf("write wat: %v", err)
			}
			run := exec.Command("wasmtime", "run", watFile)
			out, runErr := run.CombinedOutput()
			if run.ProcessState == nil || !run.ProcessState.Exited() {
				t.Fatalf("wasmtime did not exit normally for %q: %v\n%s", tc.name, runErr, out)
			}
			if code := run.ProcessState.ExitCode(); code != 0 {
				t.Errorf("nested-body-pass-blindness wasm IR %q = %d, want 0\n%s", tc.name, code, out)
			}
		})
	}
}
