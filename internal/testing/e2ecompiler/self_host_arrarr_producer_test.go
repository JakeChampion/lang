package e2ecompiler

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// --- A nested array from a producer that returns a LOCAL (#7335) -------------
//
// `let g: T[][] = mk(..)` must reclaim the whole structure — the outer buffer
// and both inner arrays — whether mk returns the literal directly or a local
// bound from it:
//
//	function mk(): i32[][] { return [[1,2],[3,4]]; }
//	function mk(): i32[][] { let a: i32[][] = [[1,2],[3,4]]; return a; }
//
// Returning a local is the form real code has: anything that builds rows
// before handing them back cannot use the literal form.
//
// The sibling_alias cases guard the other direction: a same-named `v` bound to
// a borrowed parameter must not inherit the producer binding's release. A
// double free there moves no byte count, so the exit code carries it.
//
// Every want was confirmed against BOTH oracles — bin/fern -interp and the native
// x86-64 backend agreed on each — never read off the self-host run under test.

type arrarrProdCase struct {
	name string
	src  string
	want int
}

// `id` hides a row from the static-box plan, so the outer array is a heap box.
const arrarrProdID = "\nfunction id(xs: i32[]): i32[] { return xs; }"

const arrarrProdMain = arrarrProdID + "\nfunction main(): i32 { let t: i32 = 0; let i: i32 = 0; " +
	"while (i < 200) { t = t + round(i); i = i + 1; } " +
	"if (__rc_underflow_count() != 0) { return 99; } return t % 83; }"

func arrarrProdCases() []arrarrProdCase {
	return []arrarrProdCase{
		{
			// The repro: the producer returns a LOCAL bound from the literal.
			name: "producer_returns_local",
			src: `function mk(): i32[][] { let a: i32[][] = [id([1,2]),[3,4]]; return a; }
function round(i: i32): i32 { let v: i32[][] = mk(); return v.len(); }` + arrarrProdMain,
			want: 68,
		},
		{
			// The same producer returning the literal directly — admitted before
			// this change, and the one-statement diff that isolated the cause.
			name: "producer_returns_literal",
			src: `function mk(): i32[][] { return [id([1,2]),[3,4]]; }
function round(i: i32): i32 { let v: i32[][] = mk(); return v.len(); }` + arrarrProdMain,
			want: 68,
		},
		{
			// No producer at all: the literal bound straight into the local. This
			// path was always credited and must stay so.
			name: "literal_init",
			src:  `function round(i: i32): i32 { let v: i32[][] = [id([1,2]),[3,4]]; return v.len(); }` + arrarrProdMain,
			want: 68,
		},
		{
			// THE OVER-RELEASE GUARD. Two same-named `v`, one from the producer and
			// one a bare alias of a parameter. With the registry widened but the
			// credit still name-keyed this is 99, and no byte count shows it.
			name: "sibling_alias",
			src: `function mk(): i32[][] { let a: i32[][] = [id([1,2]),[3,4]]; return a; }
function round(base: i32[][], i: i32): i32 {
    let t: i32 = 0;
    if (i % 2 == 0) { let v: i32[][] = mk();  t = t + v.len(); }
    if (i % 2 == 1) { let v: i32[][] = base;  t = t + v.len(); }
    return t;
}
function main(): i32 { let b: i32[][] = [id([7,8]),[9,10]]; let t: i32 = 0; let i: i32 = 0; while (i < 100) { t = t + round(b, i); i = i + 1; } if (__rc_underflow_count() != 0) { return 99; } return t % 83; }` + arrarrProdID,
			want: 34,
		},
		{
			// The string-inner sibling, which takes the STRICT "ARRARRS:" credit and
			// the per-element __fern_str_arr_free walk — a different credit, the same
			// collision.
			name: "sibling_alias_strings",
			src: `function w(a: string): string { return a + "!"; }
function mk(): string[][] { let a: string[][] = [[w("p")],[w("q")]]; return a; }
function round(base: string[][], i: i32): i32 {
    let t: i32 = 0;
    if (i % 2 == 0) { let v: string[][] = mk();  t = t + v.len(); }
    if (i % 2 == 1) { let v: string[][] = base;  t = t + v.len(); }
    return t;
}
function main(): i32 { let b: string[][] = [[w("a")],[w("b")]]; let t: i32 = 0; let i: i32 = 0; while (i < 100) { t = t + round(b, i); i = i + 1; } if (__rc_underflow_count() != 0) { return 99; } return t % 83; }`,
			want: 34,
		},
		{
			// The literal and an alias of it in ONE body.
			name: "local_alias",
			src:  `function round(i: i32): i32 { let a: i32[][] = [id([1,2]),[3,4]]; let v: i32[][] = a; return v.len(); }` + arrarrProdMain,
			want: 68,
		},
	}
}

// TestSelfHostArrArrProducerX86_64 — a nested array from a local-returning
// producer is reclaimed, and no same-named sibling inherits its credit.
func TestSelfHostArrArrProducerX86_64(t *testing.T) {
	boxedProbes(t)
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "drivers/asm_ir_run.fern", "driver")

	for _, tc := range arrarrProdCases() {
		t.Run(tc.name, func(t *testing.T) {
			asm := hevCompile(t, runner, driverBin, tc.src, []string{"FERN_LEAKCHECK=1"})
			progBin := buildBin(t, gcc, dir, "arrarrprod_"+tc.name, asm)
			stderr, exit := hevRun(t, runner, progBin)
			if exit != tc.want {
				t.Fatalf("%s exited %d, want %d (99 = rc underflow: a same-named "+
					"aliasing sibling inherited the widened ARRARR: credit)", tc.name, exit, tc.want)
			}
			summary := leakSummaryLine(stderr)
			if summary == "" {
				t.Fatalf("%s: no leakcheck summary", tc.name)
			}
			var allocs, frees, live int64
			if _, err := fmtSscan(summary, &allocs, &frees, &live); err != nil {
				t.Fatalf("%s: parse %q: %v", tc.name, summary, err)
			}
			if allocs == 0 {
				t.Fatalf("%s allocated nothing — the probe is not exercising the path", tc.name)
			}
			if live != 0 || allocs != frees {
				t.Errorf("%s: %s — must balance at live_bytes 0. The inner arrays are two "+
					"thirds of the allocations here, so a withheld deep walk shows up as "+
					"frees at one third of allocs", tc.name, summary)
			}
		})
	}
}

// TestSelfHostArrArrProducerWasmIR — the wasm sibling. Exit codes only: an
// over-release moves no byte count on any backend.
func TestSelfHostArrArrProducerWasmIR(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping arrarr producer wasm IR e2e")
	}
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/wasm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "drivers/wasm_ir_run.fern", "driver")

	for _, tc := range arrarrProdCases() {
		t.Run(tc.name, func(t *testing.T) {
			var cmd *exec.Cmd
			if len(runner) == 0 {
				cmd = exec.Command(driverBin)
			} else {
				cmd = exec.Command(runner[0], append(append([]string{}, runner[1:]...), driverBin)...)
			}
			cmd.Stdin = bytes.NewReader([]byte(tc.src))
			wat, err := cmd.Output()
			if err != nil || len(wat) == 0 {
				t.Fatalf("driver failed for %q: %v", tc.name, err)
			}
			watFile := filepath.Join(dir, "arrarrprod_"+tc.name+".wat")
			if err := os.WriteFile(watFile, wat, 0o644); err != nil {
				t.Fatalf("write wat: %v", err)
			}
			rcmd := exec.Command("wasmtime", "run", watFile)
			_ = rcmd.Run()
			if rcmd.ProcessState == nil || !rcmd.ProcessState.Exited() {
				t.Fatalf("wasmtime did not exit normally for %q:\n%s", tc.name, wat)
			}
			if got := rcmd.ProcessState.ExitCode(); got != tc.want {
				t.Errorf("arrarr producer wasm IR %q = %d, want %d", tc.name, got, tc.want)
			}
		})
	}
}

// TestSelfHostArrArrProducerIRArm64 — the arm64 sibling under qemu.
func TestSelfHostArrArrProducerIRArm64(t *testing.T) {
	arm64gcc, qemu := arm64Tooling(t)
	x86gcc, x86runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, x86gcc, dir, "drivers/asm_ir_run.fern", "driver")

	for _, tc := range arrarrProdCases() {
		t.Run(tc.name, func(t *testing.T) {
			asm := runCapture(t, x86gcc, x86runner, driverBin, []byte(tc.src), "-target", "arm64-linux")
			if len(asm) == 0 {
				t.Fatalf("%s: self-host arm64 compiler emitted 0 bytes", tc.name)
			}
			bin := buildBinArm64(t, arm64gcc, dir, "arrarrprod_"+tc.name+"_arm64", string(asm))
			cmd := runArm64Bin(qemu, bin)
			_ = cmd.Run()
			if code := cmd.ProcessState.ExitCode(); code != tc.want {
				t.Errorf("arrarr producer arm64 IR %q = %d, want %d", tc.name, code, tc.want)
			}
		})
	}
}
