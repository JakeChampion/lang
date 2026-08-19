package e2eselfhost

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// unionTupleElemReclaimCases pin the release of a TAGGED UNION constructed at a
// tuple element position — a built-in `Some(x)` / `Ok(x)` / `Err(x)` box or a
// user-enum variant.
//
// emit_tuple_child_drops had arms for array, string, nested-tuple and
// reclaim-struct elements and none for a union, so once the tuple around it was
// reclaimed the union's own box was still never released: exactly one leaked box
// per union element, whatever its payload. Measured per round before the fix, as
// x86-64 | arm64 | wasm, native flat on every row: 40 | 40 | 16 for one element,
// 80 | 80 | 32 for two.
//
// The box is [tag, payload] and this construction allocated it, so a shallow
// __fern_rc_dec is balanced. The PAYLOAD is deliberately left alone — releasing
// it needs the variant's own drop plan, and leaking it is safe where freeing an
// aliased one would not be. `union-payload-alias-safe` pins that residue as a
// value, not a byte count.
var unionTupleElemReclaimCases = []struct {
	name string
	src  string
	want int
}{
	{"option-elem-box-reclaimed", `function churn(n: i32): i32 {
    var acc: i32 = 0;
    var i: i32 = 0;
    while (i < n) {
        var t: (i32, i32[], Option[i32]) = (i, [i, i + 1], Some(i));
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
	// Two union elements leaked two boxes; the release is per position, so this
	// separates "the walk reached one element" from "the walk reached them all".
	{"two-option-elems-reclaimed", `function churn(n: i32): i32 {
    var acc: i32 = 0;
    var i: i32 = 0;
    while (i < n) {
        var t: (i32, i32[], Option[i32], Option[i32]) = (i, [i, i + 1], Some(i), Some(i + 1));
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
	// The USER-ENUM path is a different test from the built-in one:
	// expr_enum_type resolves the ctor against the program's declared variants,
	// where expr_opt_elem_tag matches the built-in Option shape.
	{"user-enum-variant-elem-reclaimed", `enum Tag { Num(i32), Nil }
function churn(n: i32): i32 {
    var acc: i32 = 0;
    var i: i32 = 0;
    while (i < n) {
        var t: (i32, i32[], Tag) = (i, [i, i + 1], Tag.Num(i));
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
	// ALIASING negative: a bare IDENT of union type at an element position is a
	// live local's box, not a construction. Only construction makes the box
	// fresh, so this position must keep its skip — `o` is read after the tuple's
	// reclaim point, and freeing it corrupts the read or ticks the detector.
	{"ident-union-elem-alias-safe", `function churn(n: i32): i32 {
    var acc: i32 = 0;
    var i: i32 = 0;
    while (i < n) {
        var o: Option[i32] = Some(i);
        var t: (i32, i32[], Option[i32]) = (i, [i, i + 1], o);
        var r: i32 = 0;
        match (o) { Some(v) => { r = t.0 + t.1[0] + v; }, None => {} }
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
}`, 3},
	// PAYLOAD negative: the box is freed, its rc payload is not. The payload is
	// read back through the tuple, so a shallow dec that wrongly took the
	// payload with it would corrupt this or tick the detector.
	{"union-payload-alias-safe", `function churn(n: i32): i32 {
    var acc: i32 = 0;
    var i: i32 = 0;
    while (i < n) {
        var t: (i32, i32[], Option[i32[]]) = (i, [i, i + 1], Some([i, i + 2]));
        var r: i32 = 0;
        match (t.2) { Some(v) => { r = t.0 + t.1[0] + v[1]; }, None => {} }
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
}`, 1},
}

const unionTupleElemFailFmt = "%s = %d, want %d (a small non-zero on a byte case is the leaked bytes per round; 99 = over-release; 97 = value corrupted)"

func TestSelfHostUnionTupleElemReclaimIRX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_ir_run.fern", "driver")

	for _, tc := range unionTupleElemReclaimCases {
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
				t.Errorf(unionTupleElemFailFmt, tc.name, code, tc.want)
			}
		})
	}
}

func TestSelfHostUnionTupleElemReclaimIRArm64(t *testing.T) {
	arm64gcc, qemu := arm64Tooling(t)
	x86gcc, x86runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, x86gcc, dir, "asm_ir_run.fern", "driver")

	for _, tc := range unionTupleElemReclaimCases {
		t.Run(tc.name, func(t *testing.T) {
			asm := runCaptureStrictIR(t, x86gcc, x86runner, driverBin, []byte(tc.src+"\n"), "-target", "arm64-linux")
			if len(asm) == 0 {
				t.Fatal("self-host arm64 compiler emitted 0 bytes")
			}
			bin := buildBinArm64(t, arm64gcc, dir, tc.name, string(asm))
			cmd := runArm64Bin(qemu, bin)
			_ = cmd.Run()
			if code := cmd.ProcessState.ExitCode(); code != tc.want {
				t.Errorf(unionTupleElemFailFmt, tc.name, code, tc.want)
			}
		})
	}
}

func TestSelfHostUnionTupleElemReclaimWasmIR(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping union tuple-element reclaim wasm IR e2e")
	}
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "wasm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "wasm_ir_run.fern", "driver")

	for _, tc := range unionTupleElemReclaimCases {
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
				t.Errorf(unionTupleElemFailFmt, tc.name, got, tc.want)
			}
		})
	}
}
