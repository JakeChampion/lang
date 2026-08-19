package e2eselfhost

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// matchElemBorrowReclaimCases pin a tuple one of whose elements is read as a
// MATCH SCRUTINEE.
//
// rctuple_esc_stmt walked `m.scrutinee` through rctuple_esc_expr, whose
// ExprFieldAccess arm reports a non-scalar `name.<i>` read as a bare pointer
// extraction. Matching on a union element therefore looked like an escape and
// cost the WHOLE tuple its reclaim — buffer, element boxes and the tuple box —
// not merely the union box at that position. The same tuple is flat when the
// element is read any other way: `(i, [i, i+1], Some(i))` measured 0, and 128
// with `match (t.2)` added.
//
// A scrutinee that is exactly `name.<i>` reads the element's tag and copies its
// payload into the arm binding, storing the box nowhere, so it is a borrow. That
// a binding taken from it stays valid is not an argument but a measurement: the
// reclaim frees the tuple's own children and its box, never a union element's
// PAYLOAD, so `pointer-payload-*` below hold even when an arm binds an `i32[]`
// and carries it out of the loop.
//
// Byte cases return measured bytes per round. Before, as x86-64 | arm64 | wasm,
// native flat on both: 80 | 80 | 40 and 128 | 128 | 72.
var matchElemBorrowReclaimCases = []struct {
	name string
	src  string
	want int
}{
	{"match-on-option-only-child", `function churn(n: i32): i32 {
    var acc: i32 = 0;
    var i: i32 = 0;
    while (i < n) {
        var t: (i32, Option[i32]) = (i, Some(i));
        var r: i32 = 0;
        match (t.1) { Some(v) => { r = t.0 + v; }, None => {} }
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
	// The array sibling shows the cost is the WHOLE tuple, not the union box:
	// this shape is flat without the match and 128 B/round with it.
	{"match-on-union-elem-beside-array", `function churn(n: i32): i32 {
    var acc: i32 = 0;
    var i: i32 = 0;
    while (i < n) {
        var t: (i32, i32[], Option[i32]) = (i, [i, i + 1], Some(i));
        var r: i32 = t.0 + t.1[0];
        match (t.2) { Some(v) => { r = r + v; }, None => {} }
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
	// POINTER-PAYLOAD safety: the arm binds an i32[] out of the union. The
	// reclaim now runs on this tuple, so the binding must still be readable.
	{"pointer-payload-bound-safe", `function churn(n: i32): i32 {
    var acc: i32 = 0;
    var i: i32 = 0;
    while (i < n) {
        var t: (i32, i32[], Option[i32[]]) = (i, [i, i + 1], Some([i, i + 2]));
        var r: i32 = t.0 + t.1[0];
        match (t.2) { Some(v) => { r = r + v[0] + v[1]; }, None => {} }
        acc = (acc + r) % 91;
        i = i + 1;
    }
    return acc;
}
function main(): i32 {
    var w: i32 = churn(1000);
    var x: i32 = churn(1000);
    if (__rc_underflow() != 0) { return 99; }
    if (w != x) { return 97; }
    return w;
}`, 2},
	// The sharper version: the arm CARRIES the pointer payload out to a local
	// read after the loop, so it outlives every reclaim point.
	{"pointer-payload-carried-out-safe", `function churn(n: i32): i32 {
    var keep: i32[] = [0, 0];
    var acc: i32 = 0;
    var i: i32 = 0;
    while (i < n) {
        var t: (i32, i32[], Option[i32[]]) = (i, [i, i + 1], Some([i, i + 2]));
        match (t.2) { Some(v) => { keep = v; }, None => {} }
        acc = (acc + t.0) % 91;
        i = i + 1;
    }
    return (acc + keep[0] + keep[1]) % 91;
}
function main(): i32 {
    var w: i32 = churn(1000);
    var x: i32 = churn(1000);
    if (__rc_underflow() != 0) { return 99; }
    if (w != x) { return 97; }
    return w;
}`, 90},
	// EXTRACTION negative: `keep = t.2` lifts the element out whole before any
	// match, which is the shape the escape walk is really for. It must stay
	// refused — the borrow only covers a scrutinee that is exactly `name.<i>`.
	{"extracted-elem-still-refused", `function churn(n: i32): i32 {
    var keep: Option[i32] = None;
    var acc: i32 = 0;
    var i: i32 = 0;
    while (i < n) {
        var t: (i32, i32[], Option[i32]) = (i, [i, i + 1], Some(i));
        keep = t.2;
        acc = (acc + t.0 + t.1[0]) % 91;
        i = i + 1;
    }
    var r: i32 = 0;
    match (keep) { Some(v) => { r = v % 91; }, None => {} }
    return (acc + r + 7) % 91;
}
function main(): i32 {
    var w: i32 = churn(1000);
    var x: i32 = churn(1000);
    if (__rc_underflow() != 0) { return 99; }
    if (w != x) { return 97; }
    return w;
}`, 7},
}

const matchElemBorrowFailFmt = "%s = %d, want %d (a small non-zero on a byte case is the leaked bytes per round; 99 = over-release; 97 = value corrupted)"

func TestSelfHostMatchElemBorrowReclaimIRX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_ir_run.fern", "driver")

	for _, tc := range matchElemBorrowReclaimCases {
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
				t.Errorf(matchElemBorrowFailFmt, tc.name, code, tc.want)
			}
		})
	}
}

func TestSelfHostMatchElemBorrowReclaimIRArm64(t *testing.T) {
	arm64gcc, qemu := arm64Tooling(t)
	x86gcc, x86runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, x86gcc, dir, "asm_ir_run.fern", "driver")

	for _, tc := range matchElemBorrowReclaimCases {
		t.Run(tc.name, func(t *testing.T) {
			asm := runCaptureStrictIR(t, x86gcc, x86runner, driverBin, []byte(tc.src+"\n"), "-target", "arm64-linux")
			if len(asm) == 0 {
				t.Fatal("self-host arm64 compiler emitted 0 bytes")
			}
			bin := buildBinArm64(t, arm64gcc, dir, tc.name, string(asm))
			cmd := runArm64Bin(qemu, bin)
			_ = cmd.Run()
			if code := cmd.ProcessState.ExitCode(); code != tc.want {
				t.Errorf(matchElemBorrowFailFmt, tc.name, code, tc.want)
			}
		})
	}
}

func TestSelfHostMatchElemBorrowReclaimWasmIR(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping match-element borrow reclaim wasm IR e2e")
	}
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "wasm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "wasm_ir_run.fern", "driver")

	for _, tc := range matchElemBorrowReclaimCases {
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
				t.Errorf(matchElemBorrowFailFmt, tc.name, got, tc.want)
			}
		})
	}
}
