package e2eselfhost

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// --- A MIXED rc tuple — one bare ident, one fresh literal (#7281) ------------
//
// `var t: (i32[], i32[]) = (xs, [i + 2, i + 3])` released NOTHING: not the two
// buffers, not the tuple box.
//
//	100 rounds   allocs=300 frees=0    live_bytes=12000
//	200 rounds   allocs=600 frees=0    live_bytes=24000
//	400 rounds   allocs=1200 frees=0   live_bytes=48000
//
// 120 B/round, unbounded, against `300/300 live=0` on native and interp. The
// answers agreed throughout and `__rc_underflow_count()` was 0, so nothing but
// the byte count dissented.
//
// The mix is the whole of it, and it falls between two classes that each handle
// one half. `tuple_lit_rc_reclaimable` admits a bare-ident element, so the tuple
// is "TUPRC:" and out of "TUP:" (the two sets are kept disjoint). But "TUPRC:"
// is consumed only by the StmtVar rebind path; the scope-exit sweep needs
// "TUPRCS:", and that credit required tuple_arg_payload_fresh — EVERY rc
// position a fresh literal, so the blind type-driven drop sole-owns each one.
// Position 0 is a live local, so neither credit was granted and no site owed the
// release. Make either position rc-free and it balances: `(xs, ys)` is
// "TUP:"+"TUPELEM:" and `([..], [..])` is "TUPRCS:". Only the mix fell through.
//
// What the sweep actually needs is weaker than sole ownership: it needs the tuple
// to hold a COUNTED REFERENCE to every position it dec's. A bare-ident element
// has one — lower_expr's ExprTuple arm rc_inc's an element naming an rc-container
// local (#4350 / #7226), so the tuple is a second owner and the drop's dec gives
// exactly that retain back while the local's own sweep spends its own reference.
// tuple_arg_payload_retained is that weaker admission; tuple_arg_payload_fresh
// stays the gate for an Option's Some payload and an array-of-tuples element,
// where the payload is freed without its own box in the same breath.
//
// The rebind and discarded-literal sites owed the same give-back and skipped it
// for the same stale reason (emit_tuple_child_drops' bare-ident arm), so they
// move here too — `rebound`, `loop_scoped` and `discarded_literal` are those.
//
// Every want was confirmed against BOTH oracles — bin/fern -interp and the native
// x86-64 backend agreed on each — never read off the self-host run under test.

type tupMixedRcCase struct {
	name string
	src  string
	want int
	// balance asserts allocs == frees at live_bytes 0. False only for the row
	// that is deliberately still refused.
	balance bool
}

const tupMixedRcMain = "\nfunction main(): i32 { var x: i32 = 0; var r: i32 = 0; " +
	"while (r < 100) { x = x + round(r); r = r + 1; } " +
	"if (__rc_underflow_count() != 0) { return 99; } return x % 83; }"

func tupMixedRcCases() []tupMixedRcCase {
	return []tupMixedRcCase{
		{
			// The issue's repro. Base: allocs=300 frees=0 live_bytes=12000.
			name: "mixed_sweep",
			src: `function round(i: i32): i32 {
    var xs: i32[] = [i, i + 1];
    var t: (i32[], i32[]) = (xs, [i + 2, i + 3]);
    return t.0[0] + t.1[1];
}` + tupMixedRcMain,
			want: 74, balance: true,
		},
		{
			// Position order is not what decides it — the literal first leaks the
			// same 12000. A fix that only looked at position 0 would pass one.
			name: "mixed_literal_first",
			src: `function round(i: i32): i32 {
    var xs: i32[] = [i, i + 1];
    var t: (i32[], i32[]) = ([i + 2, i + 3], xs);
    return t.0[0] + t.1[1];
}` + tupMixedRcMain,
			want: 74, balance: true,
		},
		{
			// CONTROL, already correct: every position a bare ident. This is the
			// "TUP:" + "TUPELEM:" class (#7226) and must stay in it — a fix that
			// worked by pulling shapes into "TUPRCS:" would move this one.
			name: "all_ident",
			src: `function round(i: i32): i32 {
    var xs: i32[] = [i, i + 1];
    var ys: i32[] = [i + 4, i + 5];
    var t: (i32[], i32[]) = (xs, ys);
    return t.0[0] + t.1[1];
}` + tupMixedRcMain,
			want: 25, balance: true,
		},
		{
			// CONTROL, already correct: the strict all-fresh "TUPRCS:" shape the
			// widened admission must still admit unchanged.
			name: "all_fresh",
			src: `function round(i: i32): i32 {
    var t: (i32[], i32[]) = ([i, i + 1], [i + 2, i + 3]);
    return t.0[0] + t.1[1];
}` + tupMixedRcMain,
			want: 74, balance: true,
		},
		{
			// CONTROL, already correct: a scalar beside a bare ident is "TUP:".
			name: "scalar_ident",
			src: `function round(i: i32): i32 {
    var xs: i32[] = [i, i + 1];
    var t: (i32, i32[]) = (i, xs);
    return t.1[0] + t.1[1];
}` + tupMixedRcMain,
			want: 40, balance: true,
		},
		{
			// CONTROL, already correct: the SAME ident twice. Two retains, and the
			// release has to spend both — a walk that deduplicated positions would
			// leak one buffer per round here.
			name: "dup_ident",
			src: `function round(i: i32): i32 {
    var xs: i32[] = [i, i + 1];
    var t: (i32[], i32[]) = (xs, xs);
    return t.0[0] + t.1[1];
}` + tupMixedRcMain,
			want: 40, balance: true,
		},
		{
			// The ident is a PARAM, not a local: the caller still owns the buffer
			// after the callee's dec, so this is the row that would report an
			// over-release rather than a leak if the give-back were a free.
			// Base: allocs=201 frees=0 live_bytes=8040.
			name: "param_element",
			src: `function round(base: i32[], i: i32): i32 {
    var t: (i32[], i32[]) = (base, [i + 2, i + 3]);
    return t.0[0] + t.1[1];
}
function main(): i32 { var b: i32[] = [7, 8]; var x: i32 = 0; var r: i32 = 0; while (r < 100) { x = x + round(b, r); r = r + 1; } if (__rc_underflow_count() != 0) { return 99; } return x % 83; }`,
			want: 57, balance: true,
		},
		{
			// THE OVER-RELEASE GUARD. Two same-named `v` in sibling `if` arms, one
			// mixed and one all-ident, so they are in different classes. The tuple
			// family has been site-keyed since #7272; without that this widening
			// would hand one binding the other's credit and free a live buffer,
			// which no byte count would show. Base: allocs=151 frees=50 live=4040.
			name: "sibling_alias",
			src: `function round(base: i32[], i: i32): i32 {
    var t: i32 = 0;
    if (i % 2 == 0) { var v: (i32[], i32[]) = (base, [i + 1, i + 2]); t = t + v.1[0]; }
    if (i % 2 == 1) { var v: (i32[], i32[]) = (base, base); t = t + v.1[0]; }
    return t;
}
function main(): i32 { var b: i32[] = [7, 8]; var t: i32 = 0; var i: i32 = 0; while (i < 100) { t = t + round(b, i); i = i + 1; } if (__rc_underflow_count() != 0) { return 99; } return t % 83; }`,
			want: 28, balance: true,
		},
		{
			// The mix one level down: the admission recurses into a nested tuple
			// position, so an inner mixed tuple is admitted with the outer one.
			// Base: allocs=400 frees=0 live_bytes=16000.
			name: "nested_mixed",
			src: `function round(i: i32): i32 {
    var xs: i32[] = [i, i + 1];
    var t: (i32, (i32[], i32[])) = (i, (xs, [i + 2, i + 3]));
    return t.1.0[0] + t.1.1[1];
}` + tupMixedRcMain,
			want: 74, balance: true,
		},
		{
			// The ident's own binding carries no annotation — the retain is decided
			// from the SLOT, not the declaration, so this must behave identically.
			// Base: allocs=300 frees=0 live_bytes=12000.
			name: "unannotated_source",
			src: `function mk(i: i32): i32[] { return [i, i + 1]; }
function round(i: i32): i32 {
    var xs = mk(i);
    var t: (i32[], i32[]) = (xs, [i + 2, i + 3]);
    return t.0[0] + t.1[1];
}` + tupMixedRcMain,
			want: 74, balance: true,
		},
		{
			// A block-scoped mixed tuple rebound once per iteration: the sweep half
			// and the rebind-store half both have to fire, and each on its own
			// leaves a different remainder. Base: allocs=700 frees=400 live=12000.
			name: "loop_scoped",
			src: `function round(i: i32): i32 {
    var xs: i32[] = [i, i + 1];
    var acc: i32 = 0;
    var k: i32 = 0;
    while (k < 3) { var t: (i32[], i32[]) = (xs, [i + k, i + k + 1]); acc = acc + t.1[0]; k = k + 1; }
    return acc;
}` + tupMixedRcMain,
			want: 44, balance: true,
		},
		{
			// A REASSIGNED mixed tuple: the superseded boxes go through
			// emit_tuple_deep_reinit_store, which skipped their bare-ident retains.
			// Base: allocs=900 frees=600 live_bytes=12000 — the n-of-n signature
			// with the fresh half already released and the ident half stranded.
			name: "rebound",
			src: `function round(i: i32): i32 {
    var xs: i32[] = [i, i + 1];
    var t: (i32[], i32[]) = (xs, [i + 2, i + 3]);
    var k: i32 = 0;
    while (k < 3) { t = (xs, [i + k, i + k + 1]); k = k + 1; }
    return t.0[0] + t.1[1];
}` + tupMixedRcMain,
			want: 74, balance: true,
		},
		{
			// The discarded-literal statement site, which frees the box outright
			// and owed the same give-back. Base: allocs=300 frees=200 live=4000.
			//
			// NATIVE leaks this one (allocs=300 frees=200 live_bytes=3200), so the
			// self-host is now AHEAD of the oracle here; only the answer is taken
			// from native. That native gap is its own bug, not this one's.
			name: "discarded_literal",
			src: `function round(i: i32): i32 {
    var xs: i32[] = [i, i + 1];
    (xs, [i + 2, i + 3]);
    return xs[0];
}` + tupMixedRcMain,
			want: 53, balance: true,
		},
		{
			// DELIBERATELY still refused, and asserted on the exit code alone.
			// `var keep: i32[] = t.0` extracts an owned pointer element, so
			// rctuple_payload_escapes denies the credit and this keeps its 12000 —
			// the safe direction. What the row pins is that it must not start
			// OVER-releasing while it waits, which is where a careless widening of
			// the escape gate would take it.
			name: "elem_escapes_still_leaks",
			src: `function round(i: i32): i32 {
    var xs: i32[] = [i, i + 1];
    var t: (i32[], i32[]) = (xs, [i + 2, i + 3]);
    var keep: i32[] = t.0;
    return keep[0] + t.1[1];
}` + tupMixedRcMain,
			want: 74,
		},
	}
}

// TestSelfHostTupleMixedRcElemX86_64 — a tuple mixing a bare-ident and a fresh
// rc element releases both positions and its box, exactly once each.
//
// Both assertions carry signal and they catch opposite failures. The exit code is
// the over-release detector: a doubly-released block goes straight back to the
// freelist, so `live_bytes` stays 0 through a double free and only
// `__rc_underflow_count()` dissents. The byte balance is the leak detector, which
// the exit code cannot see.
func TestSelfHostTupleMixedRcElemX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_ir_run.fern", "driver")

	for _, tc := range tupMixedRcCases() {
		t.Run(tc.name, func(t *testing.T) {
			asm := hevCompile(t, runner, driverBin, tc.src, []string{"FERN_LEAKCHECK=1"})
			progBin := buildBin(t, gcc, dir, "tupmixedrc_"+tc.name, asm)
			stderr, exit := hevRun(t, runner, progBin)
			if exit != tc.want {
				t.Fatalf("%s exited %d, want %d (99 = rc underflow: a position was "+
					"released that the construction never retained)", tc.name, exit, tc.want)
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
				t.Errorf("%s: %s — must balance at live_bytes 0. A mixed rc tuple in "+
					"neither class releases nothing at all, box included", tc.name, summary)
			}
		})
	}
}

// TestSelfHostTupleMixedRcElemWasmIR — the wasm sibling. Exit codes only: a leak
// moves no exit code and an over-release moves no byte count, so what this leg
// adds is that the new releases do not free a LIVE box on wasm.
func TestSelfHostTupleMixedRcElemWasmIR(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping mixed rc tuple element wasm IR e2e")
	}
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "wasm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "wasm_ir_run.fern", "driver")

	for _, tc := range tupMixedRcCases() {
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
			watFile := filepath.Join(dir, "tupmixedrc_"+tc.name+".wat")
			if err := os.WriteFile(watFile, wat, 0o644); err != nil {
				t.Fatalf("write wat: %v", err)
			}
			rcmd := exec.Command("wasmtime", "run", watFile)
			_ = rcmd.Run()
			if rcmd.ProcessState == nil || !rcmd.ProcessState.Exited() {
				t.Fatalf("wasmtime did not exit normally for %q:\n%s", tc.name, wat)
			}
			if got := rcmd.ProcessState.ExitCode(); got != tc.want {
				t.Errorf("mixed rc tuple element wasm IR %q = %d, want %d", tc.name, got, tc.want)
			}
		})
	}
}

// TestSelfHostTupleMixedRcElemIRArm64 — the arm64 sibling under qemu.
func TestSelfHostTupleMixedRcElemIRArm64(t *testing.T) {
	arm64gcc, qemu := arm64Tooling(t)
	x86gcc, x86runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, x86gcc, dir, "asm_ir_run.fern", "driver")

	for _, tc := range tupMixedRcCases() {
		t.Run(tc.name, func(t *testing.T) {
			asm := runCapture(t, x86gcc, x86runner, driverBin, []byte(tc.src), "-target", "arm64-linux")
			if len(asm) == 0 {
				t.Fatalf("%s: self-host arm64 compiler emitted 0 bytes", tc.name)
			}
			bin := buildBinArm64(t, arm64gcc, dir, "tupmixedrc_"+tc.name+"_arm64", string(asm))
			cmd := runArm64Bin(qemu, bin)
			_ = cmd.Run()
			if code := cmd.ProcessState.ExitCode(); code != tc.want {
				t.Errorf("%s exited %d, want %d", tc.name, code, tc.want)
			}
		})
	}
}
