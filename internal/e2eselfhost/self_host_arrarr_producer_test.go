package e2eselfhost

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// --- A nested array from a producer that returns a LOCAL (#7335) -------------
//
// `collect_fresh_arrarr_names` admits `var g: T[][] = mk(..)` off the "AAC:"
// registry, and `opt_fresh_ret_fns_of` builds that registry by proving every
// return of the callee is a fresh arr-of-arr LITERAL — syntactically. One extra
// statement inside the callee disqualified it:
//
//	function mk(): i32[][] { return [[1,2],[3,4]]; }                       clean
//	function mk(): i32[][] { var a: i32[][] = [[1,2],[3,4]]; return a; }   leaks
//
// Same caller either way. The refused form leaked BOTH inner arrays every round
// — allocs matched native exactly and frees were exactly one third of them, the
// outer buffer reclaimed and the two inners stranded:
//
//	200 rounds   allocs=600  frees=200   live_bytes=16000
//	400 rounds   allocs=1200 frees=400   live_bytes=32000
//	800 rounds   allocs=2400 frees=800   live_bytes=64000
//
// 80 B/round, unbounded, against 0 on native and interp. Returning a local is
// the form real code has — anything that builds rows before handing them back
// cannot use the literal form — so the refused case was the common one.
//
// The return predicates now resolve `return <ident>` through arrarr_row_effective,
// which already carried the consumption proof one level down for ROWS: declared
// earlier in this statement list from an array literal, mentioned nowhere between
// that declaration and the use, and this is its last use. Those together say the
// returned value solely owns the structure, which is the invariant the literal
// form gets for free.
//
// THE ORDER MATTERS, and this is the case that shows it. Widening the registry
// ALONE takes sibling_alias and sibling_alias_strings from 34 to 99 (rc
// underflow): "ARRARR:" resolved through reclaim_slot_name, so the credit the
// widening newly grants the fresh binding was inherited by a same-named aliasing
// sibling, which then freed a buffer the caller still owned. The byte counts
// stayed clean while it double-freed. A name-keyed credit cannot be widened
// safely — the site-keying (#7253) is a prerequisite, not a parallel cleanup.
//
// Every want was confirmed against BOTH oracles — bin/fern -interp and the native
// x86-64 backend agreed on each — never read off the self-host run under test.

type arrarrProdCase struct {
	name    string
	src     string
	want    int
	balance bool // assert allocs == frees at live_bytes 0
}

const arrarrProdMain = "\nfunction main(): i32 { var t: i32 = 0; var i: i32 = 0; " +
	"while (i < 200) { t = t + round(i); i = i + 1; } " +
	"if (__rc_underflow() != 0) { return 99; } return t % 83; }"

func arrarrProdCases() []arrarrProdCase {
	return []arrarrProdCase{
		{
			// The repro: the producer returns a LOCAL bound from the literal.
			name: "producer_returns_local",
			src: `function mk(): i32[][] { var a: i32[][] = [[1,2],[3,4]]; return a; }
function round(i: i32): i32 { var v: i32[][] = mk(); return v.len(); }` + arrarrProdMain,
			want: 68, balance: true,
		},
		{
			// The same producer returning the literal directly — admitted before
			// this change, and the one-statement diff that isolated the cause.
			name: "producer_returns_literal",
			src: `function mk(): i32[][] { return [[1,2],[3,4]]; }
function round(i: i32): i32 { var v: i32[][] = mk(); return v.len(); }` + arrarrProdMain,
			want: 68, balance: true,
		},
		{
			// No producer at all: the literal bound straight into the local. This
			// path was always credited and must stay so.
			name: "literal_init",
			src:  `function round(i: i32): i32 { var v: i32[][] = [[1,2],[3,4]]; return v.len(); }` + arrarrProdMain,
			want: 68, balance: true,
		},
		{
			// THE OVER-RELEASE GUARD. Two same-named `v`, one from the producer and
			// one a bare alias of a parameter. With the registry widened but the
			// credit still name-keyed this is 99, and no byte count shows it.
			name: "sibling_alias",
			src: `function mk(): i32[][] { var a: i32[][] = [[1,2],[3,4]]; return a; }
function round(base: i32[][], i: i32): i32 {
    var t: i32 = 0;
    if (i % 2 == 0) { var v: i32[][] = mk();  t = t + v.len(); }
    if (i % 2 == 1) { var v: i32[][] = base;  t = t + v.len(); }
    return t;
}
function main(): i32 { var b: i32[][] = [[7,8],[9,10]]; var t: i32 = 0; var i: i32 = 0; while (i < 100) { t = t + round(b, i); i = i + 1; } if (__rc_underflow() != 0) { return 99; } return t % 83; }`,
			want: 34,
		},
		{
			// The string-inner sibling, which takes the STRICT "ARRARRS:" credit and
			// the per-element __fern_str_arr_free walk — a different credit, the same
			// collision.
			name: "sibling_alias_strings",
			src: `function w(a: string): string { return a + "!"; }
function mk(): string[][] { var a: string[][] = [[w("p")],[w("q")]]; return a; }
function round(base: string[][], i: i32): i32 {
    var t: i32 = 0;
    if (i % 2 == 0) { var v: string[][] = mk();  t = t + v.len(); }
    if (i % 2 == 1) { var v: string[][] = base;  t = t + v.len(); }
    return t;
}
function main(): i32 { var b: string[][] = [[w("a")],[w("b")]]; var t: i32 = 0; var i: i32 = 0; while (i < 100) { t = t + round(b, i); i = i + 1; } if (__rc_underflow() != 0) { return 99; } return t % 83; }`,
			want: 34,
		},
		{
			// STILL OPEN, deliberately: the literal and the alias in ONE body. The
			// literal is credited to `a`, and reading it into `v` disqualifies it, so
			// this leaks 16000 exactly as before. That is an escape-gate question,
			// not a registry one. Asserted on the exit code only — the point here is
			// that the shape must not start OVER-releasing while it waits for its own
			// fix, which is the direction a careless widening would take it.
			name: "local_alias_still_leaks",
			src:  `function round(i: i32): i32 { var a: i32[][] = [[1,2],[3,4]]; var v: i32[][] = a; return v.len(); }` + arrarrProdMain,
			want: 68,
		},
	}
}

// TestSelfHostArrArrProducerX86_64 — a nested array from a local-returning
// producer is reclaimed, and no same-named sibling inherits its credit.
func TestSelfHostArrArrProducerX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_ir_run.fern", "driver")

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
			if tc.balance && (live != 0 || allocs != frees) {
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
	copySelfHostDriver(t, dir, "wasm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "wasm_ir_run.fern", "driver")

	for _, tc := range arrarrProdCases() {
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
	copySelfHostDriver(t, dir, "asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, x86gcc, dir, "asm_ir_run.fern", "driver")

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
