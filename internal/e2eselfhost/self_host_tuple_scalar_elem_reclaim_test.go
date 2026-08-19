package e2eselfhost

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// tupleScalarElemReclaimCases pin an rc-tuple whose literal carries a SCALAR
// arithmetic element alongside its rc children.
//
// tuple_lit_rc_reclaimable sent every ExprBinary and ExprCall element through
// tuple_str_elem_fresh — the fresh-STRING producer test — and refused the whole
// tuple when it came back false. `i + 1` is a numeric add, so a tuple that
// merely counts (`(i + 1, [i, i + 1])`) lost its deep reclaim entirely and
// leaked its array buffer, its box, and any string element beside them.
//
// emit_tuple_child_drops already skips such an element (its ExprBinary and
// ExprCall arms free only what tuple_str_elem_fresh proves fresh), so the
// admission is now the leak-safe skip the bare-ident arm has always used rather
// than a refusal.
//
// Each byte case returns the MEASURED bytes per round as its exit code, so a
// regression reports its own size. Before: 80 | 80 | 48 (flat), 120 | 120 | 72
// (nested) and 112 | 112 | 80 (fresh-string sibling) as x86-64 | arm64 | wasm.
// Native is flat on all three.
var tupleScalarElemReclaimCases = []struct {
	name string
	src  string
	want int
}{
	// The scalar binary element on its own. Nothing is nested and nothing is a
	// string: the tuple leaked only because `i + 1` is an ExprBinary.
	{"tuple-scalar-binary-elem-flat", `function churn(n: i32): i32 {
    var acc: i32 = 0;
    var i: i32 = 0;
    while (i < n) {
        var t: (i32, i32[]) = (i + 1, [i, i + 1]);
        acc = (acc + t.0 + t.1[1]) % 251;
        i = i + 1;
    }
    return acc;
}
function main(): i32 {
    var w: i32 = churn(1000);
    var b1: i32 = (__heap_bump_bytes() as i32);
    var x: i32 = churn(1000);
    var b2: i32 = (__heap_bump_bytes() as i32);
    if (__rc_underflow() != 0) { return 99; }
    if (w != x) { return 97; }
    return (b2 - b1) / 1000;
}`, 0},
	// The shape §9 recorded as "a NESTED rc-tuple is not reclaimed at all". The
	// nesting was never the fault: the same tuple with a bare ident in place of
	// `i + 1` was already flat, and the un-nested tuple leaks as soon as it
	// carries the binary.
	{"tuple-nested-scalar-binary-flat", `function churn(n: i32): i32 {
    var acc: i32 = 0;
    var i: i32 = 0;
    while (i < n) {
        var t: ((i32, i32[]), i32) = ((i, [i, i + 1]), i + 1);
        acc = (acc + t.1) % 251;
        i = i + 1;
    }
    return acc;
}
function main(): i32 {
    var w: i32 = churn(1000);
    var b1: i32 = (__heap_bump_bytes() as i32);
    var x: i32 = churn(1000);
    var b2: i32 = (__heap_bump_bytes() as i32);
    if (__rc_underflow() != 0) { return 99; }
    if (w != x) { return 97; }
    return (b2 - b1) / 1000;
}`, 0},
	// The refusal cost the OTHER elements their release too: this tuple's string
	// is a provable fresh producer, admitted and deep-freed on its own, and the
	// scalar sibling is all that held it back.
	{"tuple-string-elem-with-scalar-sibling-flat", `function churn(n: i32, s: string): i32 {
    var acc: i32 = 0;
    var i: i32 = 0;
    while (i < n) {
        var t: (i32, string) = (i + 1, "a-wide-string-past-the-inline-threshold-" + s);
        acc = (acc + t.0 + t.1.len()) % 251;
        i = i + 1;
    }
    return acc;
}
function main(): i32 {
    var s: string = "tail";
    var w: i32 = churn(1000, s);
    var b1: i32 = (__heap_bump_bytes() as i32);
    var x: i32 = churn(1000, s);
    var b2: i32 = (__heap_bump_bytes() as i32);
    if (__rc_underflow() != 0) { return 99; }
    if (w != x) { return 97; }
    if (s.len() != 4) { return 96; }
    return (b2 - b1) / 1000;
}`, 0},
	// ALIASING negative: a CALL element handing back one of its own parameters.
	// The tuple is admitted now (the array literal is its rc child), so its box
	// is freed each round while the call element still points at a live local.
	// The emitter must leave that position alone; freeing it corrupts `live`.
	{"tuple-call-elem-alias-safe", `function id(xs: i32[]): i32[] { return xs; }
function churn(n: i32): i32 {
    var live: i32[] = [7, 8, 9];
    var acc: i32 = 0;
    var i: i32 = 0;
    while (i < n) {
        var t: (i32, i32[], i32[]) = (i + 1, [i, i + 1], id(live));
        acc = (acc + t.0 + t.1[1] + t.2[0] + t.2[2]) % 251;
        i = i + 1;
    }
    return (acc + live[0] * 3 + live[1] * 5 + live[2] * 7) % 251;
}
function main(): i32 {
    var w: i32 = churn(1000);
    var x: i32 = churn(1000);
    if (__rc_underflow() != 0) { return 99; }
    if (w != x) { return 97; }
    return w;
}`, 72},
	// ESCAPE negative: the tuple outlives the scope whose reclaim now frees it.
	// `last = t` carries each round's tuple out of the loop body and the function
	// returns it, so the escape gate (body_unsafe_for, applied by
	// reclaimable_names_of) must withhold the credit the admission would
	// otherwise hand out. A freed-then-returned tuple reads its buffer back
	// wrong. 1000 + 999 + 1000 = 2999, reduced mod 97 to 89 — WASI's proc_exit
	// rejects a status of 126 or more, so an expectation carried in the exit
	// code has to stay under it on the wasm leg.
	{"tuple-scalar-binary-escape-safe", `function churn(n: i32): (i32, i32[]) {
    var last: (i32, i32[]) = (0, [0, 0]);
    var i: i32 = 0;
    while (i < n) {
        var t: (i32, i32[]) = (i + 1, [i, i + 1]);
        last = t;
        i = i + 1;
    }
    return last;
}
function main(): i32 {
    var r: (i32, i32[]) = churn(1000);
    if (__rc_underflow() != 0) { return 99; }
    return (r.0 + r.1[0] + r.1[1]) % 97;
}`, 89},
	// UNPROVABLE-CONCAT negative: `a + b` of two live strings is fresh in fact,
	// but tuple_str_elem_fresh cannot show it, so the position keeps no release.
	// That is a leak, not a fault — what must hold is that both operands survive
	// the tuple's reclaim.
	{"tuple-unproven-concat-safe", `function churn(n: i32, a: string, b: string): i32 {
    var acc: i32 = 0;
    var i: i32 = 0;
    while (i < n) {
        var t: (i32, string, i32[]) = (i + 1, a + b, [i, i + 1]);
        acc = (acc + t.0 + t.1.len() + t.2[0]) % 251;
        i = i + 1;
    }
    return (acc + a.len() + b.len()) % 251;
}
function main(): i32 {
    var a: string = "a-wide-left-side-past-the-inline-threshold";
    var b: string = "-and-a-wide-right-side-too";
    var w: i32 = churn(1000, a, b);
    var x: i32 = churn(1000, a, b);
    if (__rc_underflow() != 0) { return 99; }
    if (w != x) { return 97; }
    if (a.len() != 41 || b.len() != 26) { return 96; }
    return w;
}`, 96},
}

const tupleScalarElemReclaimFailFmt = "%s = %d, want %d (a small non-zero is the leaked bytes per round; 99 = over-release; 97 = value corrupted; 96 = an operand was freed under a live local)"

// TestSelfHostTupleScalarElemReclaimIRX86_64 drives the cases through the
// self-hosted x86-64 compiler.
func TestSelfHostTupleScalarElemReclaimIRX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_ir_run.fern", "driver")

	for _, tc := range tupleScalarElemReclaimCases {
		t.Run(tc.name, func(t *testing.T) {
			asm := runCaptureStrictIR(t, gcc, runner, driverBin, []byte(tc.src+"\n"))
			if len(asm) == 0 {
				t.Fatal("self-host compiler emitted 0 bytes")
			}
			bin := buildBin(t, gcc, dir, tc.name, string(asm))
			var cmd *exec.Cmd
			if len(runner) == 0 {
				cmd = exec.Command(bin)
			} else {
				cmd = exec.Command(runner[0], append(runner[1:], bin)...)
			}
			_ = cmd.Run()
			if code := cmd.ProcessState.ExitCode(); code != tc.want {
				t.Errorf(tupleScalarElemReclaimFailFmt, tc.name, code, tc.want)
			}
		})
	}
}

// TestSelfHostTupleScalarElemReclaimIRArm64 is the arm64 leg. Case table shared
// with the x86-64 one; the pre-fix byte figures were identical on both.
func TestSelfHostTupleScalarElemReclaimIRArm64(t *testing.T) {
	arm64gcc, qemu := arm64Tooling(t)
	x86gcc, x86runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, x86gcc, dir, "asm_ir_run.fern", "driver")

	for _, tc := range tupleScalarElemReclaimCases {
		t.Run(tc.name, func(t *testing.T) {
			asm := runCaptureStrictIR(t, x86gcc, x86runner, driverBin, []byte(tc.src+"\n"), "-target", "arm64-linux")
			if len(asm) == 0 {
				t.Fatal("self-host arm64 compiler emitted 0 bytes")
			}
			bin := buildBinArm64(t, arm64gcc, dir, tc.name, string(asm))
			cmd := runArm64Bin(qemu, bin)
			_ = cmd.Run()
			if code := cmd.ProcessState.ExitCode(); code != tc.want {
				t.Errorf(tupleScalarElemReclaimFailFmt, tc.name, code, tc.want)
			}
		})
	}
}

// TestSelfHostTupleScalarElemReclaimWasmIR is the wasm leg. The admission is
// shared lowering, so the refusal cost all three backends their release; the
// wasm byte figures differ only because its boxes are narrower.
func TestSelfHostTupleScalarElemReclaimWasmIR(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping tuple scalar-element reclaim wasm IR e2e")
	}
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "wasm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "wasm_ir_run.fern", "driver")

	for _, tc := range tupleScalarElemReclaimCases {
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
			watFile := filepath.Join(dir, tc.name+".wat")
			if err := os.WriteFile(watFile, wat, 0o644); err != nil {
				t.Fatalf("write wat: %v", err)
			}
			rcmd := exec.Command("wasmtime", "run", watFile)
			_ = rcmd.Run()
			if rcmd.ProcessState == nil || !rcmd.ProcessState.Exited() {
				t.Fatalf("wasmtime did not exit normally for %s", tc.name)
			}
			if got := rcmd.ProcessState.ExitCode(); got != tc.want {
				t.Errorf(tupleScalarElemReclaimFailFmt, tc.name, got, tc.want)
			}
		})
	}
}
