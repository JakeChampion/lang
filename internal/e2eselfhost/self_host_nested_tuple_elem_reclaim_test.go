package e2eselfhost

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// nestedTupleElemReclaimCases pin an rc-tuple holding a NESTED TUPLE element.
// Two independent defects, either of which alone left the outer tuple with no
// reclaim at all — buffer, boxes and nested box.
//
//  1. Admission. tuple_lit_rc_reclaimable required a nested tuple element to be
//     reclaimable IN ITS OWN RIGHT, i.e. to carry an array. An all-scalar
//     `(i, j)` carries none, so it answered false and the ELEMENT arm turned
//     that into a refusal of the whole outer tuple — the same "I will not free
//     this element" / "I cannot free this tuple" conflation #4353 fixed in the
//     binary and call arms, left behind in the tuple arm. A nested box is an
//     allocation in its own right, so it is a child to free whatever it holds;
//     the predicate is now split into tuple_lit_elems_admissible (structural)
//     and tuple_lit_has_rc_child (worth freeing).
//
//  2. The read gate. rctuple_payload_escapes routed `t.2.0` — a scalar copied
//     out of a nested tuple element — through decl_field_type, which finds no
//     struct for a `(..)` tag and returns "". That is not a scalar type name,
//     so the read counted as a bare pointer extraction and disqualified the
//     tuple, though it is a borrow exactly like the struct scalar-field read
//     beside it. A nested tuple element indexes by POSITION, so it now reads
//     its tag with tuple_type_elem_tag.
//
// Byte cases return measured bytes per round, so a regression reports its own
// size. Before, as x86-64 | arm64 | wasm, native flat on every row:
// 128 | 128 | 80 for the three-element shapes and 80 | 80 | 48 for `(i, (j, k))`.
var nestedTupleElemReclaimCases = []struct {
	name string
	src  string
	want int
}{
	// Defect 1 alone: the nested element is never read, so no read gate is in
	// play. The array sibling is reclaimable on its own and still leaked,
	// because the all-scalar nested tuple refused the whole tuple.
	{"nested-scalar-tuple-elem-unread", `function churn(n: i32): i32 {
    var acc: i32 = 0;
    var i: i32 = 0;
    while (i < n) {
        var t: (i32, i32[], (i32, i32)) = (i, [i, i + 1], (i, i + 1));
        var r: i32 = t.0 + t.1[0];
        acc = (acc + r) % 91;
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
	// Defect 2 on top: the same shape, reading a scalar THROUGH the nested
	// element. Fixing only the admission leaves this at its full pre-fix size.
	{"nested-scalar-tuple-elem-read-through", `function churn(n: i32): i32 {
    var acc: i32 = 0;
    var i: i32 = 0;
    while (i < n) {
        var t: (i32, i32[], (i32, i32)) = (i, [i, i + 1], (i, i + 1));
        var r: i32 = t.0 + t.1[0] + t.2.0;
        acc = (acc + r) % 91;
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
	// No array anywhere: the nested box is the ONLY thing to free, which is the
	// case the old "does the inner carry an array" question could never admit.
	{"nested-scalar-tuple-only", `function churn(n: i32): i32 {
    var acc: i32 = 0;
    var i: i32 = 0;
    while (i < n) {
        var t: (i32, (i32, i32)) = (i, (i, i + 1));
        var r: i32 = t.0 + t.1.0;
        acc = (acc + r) % 91;
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
	// EXTRACTION negative: `keep = t.1` lifts the nested box out whole, so the
	// read gate must still refuse. Freeing t would free the box keep points at.
	// keep is read after the loop; over-release corrupts it or ticks 99.
	{"nested-tuple-whole-extraction-safe", `function churn(n: i32): i32 {
    var keep: (i32, i32) = (0, 0);
    var acc: i32 = 0;
    var i: i32 = 0;
    while (i < n) {
        var t: (i32, i32[], (i32, i32)) = (i, [i, i + 1], (i, i + 1));
        keep = t.2;
        acc = (acc + t.0) % 91;
        i = i + 1;
    }
    return (acc + keep.0 + keep.1) % 91;
}
function main(): i32 {
    var w: i32 = churn(1000);
    var x: i32 = churn(1000);
    if (__rc_underflow() != 0) { return 99; }
    if (w != x) { return 97; }
    return w;
}`, 89},
	// ALIASING negative: a bare-ident array element is a live local's buffer.
	// The tuple is admitted (its nested box is a child to free) and its box is
	// released each round, so the walk must leave the aliased buffer alone.
	{"nested-tuple-with-alias-elem-safe", `function churn(n: i32): i32 {
    var live: i32[] = [7, 8, 9];
    var acc: i32 = 0;
    var i: i32 = 0;
    while (i < n) {
        var t: (i32, i32[], (i32, i32)) = (i, live, (i, i + 1));
        acc = (acc + t.0 + t.1[0] + t.2.0) % 91;
        i = i + 1;
    }
    return (acc + live[0] * 3 + live[1] * 5 + live[2] * 7) % 91;
}
function main(): i32 {
    var w: i32 = churn(1000);
    var x: i32 = churn(1000);
    if (__rc_underflow() != 0) { return 99; }
    if (w != x) { return 97; }
    return w;
}`, 28},
}

const nestedTupleElemFailFmt = "%s = %d, want %d (a small non-zero is the leaked bytes per round; 99 = over-release; 97 = value corrupted)"

func TestSelfHostNestedTupleElemReclaimIRX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_ir_run.fern", "driver")

	for _, tc := range nestedTupleElemReclaimCases {
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
				t.Errorf(nestedTupleElemFailFmt, tc.name, code, tc.want)
			}
		})
	}
}

func TestSelfHostNestedTupleElemReclaimIRArm64(t *testing.T) {
	arm64gcc, qemu := arm64Tooling(t)
	x86gcc, x86runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, x86gcc, dir, "asm_ir_run.fern", "driver")

	for _, tc := range nestedTupleElemReclaimCases {
		t.Run(tc.name, func(t *testing.T) {
			asm := runCaptureStrictIR(t, x86gcc, x86runner, driverBin, []byte(tc.src+"\n"), "-target", "arm64-linux")
			if len(asm) == 0 {
				t.Fatal("self-host arm64 compiler emitted 0 bytes")
			}
			bin := buildBinArm64(t, arm64gcc, dir, tc.name, string(asm))
			cmd := runArm64Bin(qemu, bin)
			_ = cmd.Run()
			if code := cmd.ProcessState.ExitCode(); code != tc.want {
				t.Errorf(nestedTupleElemFailFmt, tc.name, code, tc.want)
			}
		})
	}
}

func TestSelfHostNestedTupleElemReclaimWasmIR(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping nested-tuple element reclaim wasm IR e2e")
	}
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "wasm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "wasm_ir_run.fern", "driver")

	for _, tc := range nestedTupleElemReclaimCases {
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
				t.Errorf(nestedTupleElemFailFmt, tc.name, got, tc.want)
			}
		})
	}
}
