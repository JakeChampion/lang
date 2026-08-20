package e2eselfhost

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// mapBoxColumnCases pin #4353 item 3: a map whose VALUE column holds heap BOXES
// — a scalar-element array (`Map[K, i32[]]`) or an all-scalar struct
// (`Map[K, Q]`) — must reclaim those boxes, and a value read back out of such a
// column must survive the reading frame's dec-sweep.
//
// Two defects, one column:
//
//  1. RECLAIM. The column deep-release only ever knew the STRING kind (MAPVS: /
//     MAPKS:), so a box column kept the shallow buffer-only free and every
//     element leaked. Measured against the native oracle (flat on all four
//     shapes): 86 B/round for `Map[i32, i32[]]` and 94 B/round for `Map[i32, Q]`
//     on x86-64, and 55 / 63 B/round on wasm. The wasm half was a second cause —
//     op_map_set emitted vis=0 for a scalar-element array value, so
//     $__fern_map_release never released that column at all.
//
//  2. USE-AFTER-FREE, pre-existing and independent of the leak. `var v: i32[] =
//     m.get_or(k, d)` binds the column's RAW pointer — the register map read
//     hands back an uncounted alias — into a slot the exit dec-sweep releases
//     unconditionally, so the sweep freed the map's live value. The read now
//     retains, the read-side twin of #6880's insert-side vretain. The `read-then-
//     recycle` case below is the one that returned another local's contents on
//     the self-host register backends while the interpreter and native agreed on
//     the right answer.
//
// The flatness cases are DIFFERENTIAL against a same-shape scalar-valued map:
// both share the map's __fern_arr_push grow-leak (a separate, documented
// LOAD-BEARING leak), so that baseline cancels and only the VALUE column can
// make the box map grow more. Building the map in a HELPER is deliberate — a map
// declared directly in a loop is not freed per iteration (a separate gap).
//
// Exit 0 is correct throughout; each nonzero code names the check that failed.
var mapBoxColumnCases = []struct {
	name string
	src  string
}{
	// RECLAIM, array column. Returns the page delta of the steady window, so a
	// leaking column exits nonzero and a flat one exits 0.
	{"arr-column-flat", `function build(n: i32): i32 {
    var m: Map[i32, i32[]] = Map { 1: [n, n + 1], 2: [n + 2, n + 3, n + 4] };
    var r: i32 = 0;
    if (m.has(1)) { r = r + 1; }
    if (m.has(2)) { r = r + 1; }
    return r;
}

function build_i32(n: i32): i32 {
    var m: Map[i32, i32] = Map { 1: n, 2: n + 1 };
    var r: i32 = 0;
    if (m.has(1)) { r = r + 1; }
    if (m.has(2)) { r = r + 1; }
    return r;
}

function main(): i32 {
    var acc: i32 = 0;
    var i: i32 = 0;
    while (i < 200) { acc = acc + build(i) + build_i32(i); i = i + 1; }
    var s1: i32 = (__heap_bump_bytes() as i32);
    var j: i32 = 0;
    while (j < 2000) { acc = acc + build(j) + build_i32(j); j = j + 1; }
    var s2: i32 = (__heap_bump_bytes() as i32);
    if (acc != 8800) { return 90; }
    return (s2 - s1) / 4096;
}
`},
	// RECLAIM, struct column. Q is all-scalar, so one dec per element box frees
	// it completely — which is exactly what the credit's gate demands.
	{"struct-column-flat", `struct Q { a: i32, b: i32 }

function build(n: i32): i32 {
    var m: Map[i32, Q] = Map { 1: Q { a: n, b: n + 1 }, 2: Q { a: n + 2, b: n + 3 } };
    var r: i32 = 0;
    if (m.has(1)) { r = r + 1; }
    if (m.has(2)) { r = r + 1; }
    return r;
}

function main(): i32 {
    var acc: i32 = 0;
    var i: i32 = 0;
    while (i < 200) { acc = acc + build(i); i = i + 1; }
    var s1: i32 = (__heap_bump_bytes() as i32);
    var j: i32 = 0;
    while (j < 2000) { acc = acc + build(j); j = j + 1; }
    var s2: i32 = (__heap_bump_bytes() as i32);
    if (acc != 4400) { return 90; }
    return (s2 - s1) / 4096;
}
`},
	// USE-AFTER-FREE. `inner` binds the column's value into an array slot whose
	// sweep releases it; `junk` then recycles the freed block, and the second
	// read sees it. Without the read-side retain the self-host register
	// backends returned 90 here while the interpreter and native returned 0.
	{"read-then-recycle", `function inner(m: Map[i32, i32[]]): i32 {
    var v: i32[] = m.get_or(2, []);
    return v.len();
}

function build(n: i32): i32 {
    var m: Map[i32, i32[]] = Map { 2: [n + 2, n + 3, n + 4] };
    var a: i32 = inner(m);
    var junk: i32[] = [999, 998, 997];
    var v2: i32[] = m.get_or(2, []);
    var s: i32 = 0;
    var i: i32 = 0;
    while (i < v2.len()) { s = s + v2[i]; i = i + 1; }
    if (a != 3) { return 0 - 1; }
    if (junk.len() != 3) { return 0 - 2; }
    return s;
}

function main(): i32 {
    var i: i32 = 0;
    while (i < 100) {
        if (build(i) != 3 * i + 9) { return 90; }
        i = i + 1;
    }
    return 0;
}
`},
	// Both columns at once, with the values read back and the RC underflow
	// counter consulted: the per-element free must not double-release. This is
	// the case that caught the missing read-side retain — the column free turned
	// the silent early free above into a reported over-release.
	{"read-back-no-over-release", `struct Q { a: i32, b: i32 }

function build_arr(n: i32): i32 {
    var m: Map[i32, i32[]] = Map { 1: [n, n + 1], 2: [n + 2, n + 3, n + 4] };
    var v: i32[] = m.get_or(2, []);
    var s: i32 = 0;
    var i: i32 = 0;
    while (i < v.len()) { s = s + v[i]; i = i + 1; }
    return s;
}

function build_struct(n: i32): i32 {
    var m: Map[i32, Q] = Map { 1: Q { a: n, b: n + 1 }, 2: Q { a: n + 2, b: n + 3 } };
    var q: Q = m.get_or(2, Q { a: 0, b: 0 });
    return q.a + q.b;
}

function main(): i32 {
    var i: i32 = 0;
    while (i < 500) {
        if (build_arr(i) != 3 * i + 9) { return 90; }
        if (build_struct(i) != 2 * i + 5) { return 91; }
        i = i + 1;
    }
    if (__fern_rc_underflow_get() != 0) { return 99; }
    return 0;
}
`},
	// Control: a `string[]` value column is NOT credited — its elements are
	// pointers that one dec does not release — so it keeps the shallow free.
	// It must still read back correctly and report no over-release, which is
	// what says the gate refused rather than half-freeing.
	{"strarr-column-uncredited-control", `function build(n: i32): i32 {
    var m: Map[i32, string[]] = Map { 1: ["a" + "b", "c" + "d"] };
    var v: string[] = m.get_or(1, []);
    if (v.len() != 2) { return 0 - 1; }
    if (v[0].len() != 2) { return 0 - 2; }
    return 1;
}

function main(): i32 {
    var i: i32 = 0;
    while (i < 200) {
        if (build(i) != 1) { return 90; }
        i = i + 1;
    }
    if (__fern_rc_underflow_get() != 0) { return 99; }
    return 0;
}
`},
}

// TestSelfHostMapBoxColumnReclaimIRX86_64 runs each case through the self-hosted
// x86-64 IR driver, pinned to the "ir" path.
func TestSelfHostMapBoxColumnReclaimIRX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := writeSelfHostAsmProject(t)
	copySelfHostDriver(t, dir, "asm_run.fern", "asm_pathprobe_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_run.fern", "driver")
	probeBin := buildSelfHostBin(t, gcc, dir, "asm_pathprobe_run.fern", "pathprobe")

	for _, tc := range mapBoxColumnCases {
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
			progBin := buildBin(t, gcc, dir, "mbox_"+tc.name, string(asm))
			var cmd *exec.Cmd
			if len(runner) == 0 {
				cmd = exec.Command(progBin)
			} else {
				cmd = exec.Command(runner[0], append(runner[1:], progBin)...)
			}
			_ = cmd.Run()
			if code := cmd.ProcessState.ExitCode(); code != 0 {
				t.Errorf("%s exited %d, want 0 (a page count = the box column still leaks; 90/91 = wrong value read back; 99 = over-release)", tc.name, code)
			}
		})
	}
}

// TestSelfHostMapBoxColumnReclaimIRArm64 is the arm64 leg: same programs through
// the arm64 map-free family, run under qemu.
func TestSelfHostMapBoxColumnReclaimIRArm64(t *testing.T) {
	arm64gcc, qemu := arm64Tooling(t)
	x86gcc, x86runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, x86gcc, dir, "asm_ir_run.fern", "driver")

	for _, tc := range mapBoxColumnCases {
		t.Run(tc.name, func(t *testing.T) {
			asm := runCapture(t, x86gcc, x86runner, driverBin, []byte(tc.src), "-target", "arm64-linux")
			if len(asm) == 0 {
				t.Fatalf("%s: self-host arm64 compiler emitted 0 bytes", tc.name)
			}
			bin := buildBinArm64(t, arm64gcc, dir, "mbox_"+tc.name, string(asm))
			cmd := runArm64Bin(qemu, bin)
			_ = cmd.Run()
			if code := cmd.ProcessState.ExitCode(); code != 0 {
				t.Errorf("%s exited %d, want 0 (a page count = the box column still leaks; 90/91 = wrong value read back; 99 = over-release)", tc.name, code)
			}
		})
	}
}

// TestSelfHostMapBoxColumnReclaimIRWasm is the wasm leg. It fails without the
// fix for its own reason: op_map_set emitted vis=0 for a scalar-element array
// value, so $__fern_map_release never released that column, and vconsume was
// string-only, so a fresh box value was retained with nothing to balance it.
func TestSelfHostMapBoxColumnReclaimIRWasm(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping self-host map-box-column wasm IR e2e")
	}
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "wasm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "wasm_ir_run.fern", "driver")

	for _, tc := range mapBoxColumnCases {
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
			watFile := filepath.Join(dir, "mapboxcol_prog.wat")
			if err := os.WriteFile(watFile, wat, 0o644); err != nil {
				t.Fatalf("write wat: %v", err)
			}
			run := exec.Command("wasmtime", "run", watFile)
			out, runErr := run.CombinedOutput()
			if run.ProcessState == nil || !run.ProcessState.Exited() {
				t.Fatalf("wasmtime did not exit normally for %q: %v\n%s", tc.name, runErr, out)
			}
			if code := run.ProcessState.ExitCode(); code != 0 {
				t.Errorf("map-box-column wasm IR %q = %d, want 0\n%s", tc.name, code, out)
			}
		})
	}
}
