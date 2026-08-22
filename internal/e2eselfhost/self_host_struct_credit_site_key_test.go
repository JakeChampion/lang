package e2eselfhost

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// --- The struct reclaim credit, keyed on the binding (#7253 step 1) ----------
//
// `slot_is_reclaimable_struct` resolved its credit with a bare-NAME lookup into
// `reclaimable_names` — the one class whose entries carry no "TAG:" prefix at
// all. A name has no scope, so two `var v` in sibling blocks are two slots under
// one key, and when only one of them was proven fresh the other inherited its
// verdict:
//
//	if (i % 2 == 0) { var v: P = P { xs: [i, i + 1], s: w("p") }; … }   // credited
//	if (i % 2 == 1) { var v: P = base;                            … }   // an alias
//
// The alias holds the CALLER's box. Releasing it frees memory a live parameter
// still owns, which the rc detector reports at exit 99 — self-host 99 against 34
// on native and interp, with `allocs=204 frees=204 live_bytes=0` on both sides.
// The byte census is useless for this bug, as it is for every over-release: a
// doubly-released block goes straight back to the freelist.
//
// This class is the one where the collision is not merely a leak. #7335 and
// #7281 hit the same defect on name-keyed ARRAY and TUPLE classes and got a
// counted double free; here the shape is a use-after-free, and once the credit
// is widened at all (the #7343 producer-local case) the same program SIGSEGVs
// rather than reporting anything.
//
// The fix is #7253's step 1 for this family: a `StmtVar` carries line and col,
// `bind_var_slot` records `name@line:col` on the slot, and both struct
// predicates resolve the credit their own binding earned. No credit is widened
// here — every shape that was refused before is still refused, `producer_local`
// included, which is #7343's job and needs this landed first.
//
// THE SET MUST NOT MOVE OTHERWISE, and that is what most of the rows below are
// for. Two failure modes look nothing alike: the one being fixed is loud in the
// underflow counter, and the one a key migration introduces — a binding whose
// slot carries no site key, so it resolves NO credit — is silent there and shows
// only as a leak. `block_scoped` is the row that caught exactly that during
// development: the "NODEEP:" / "FLDCHECKED:" markers are derived from the same
// entries, and leaving their readers on the name key cost every block-scoped
// struct local its deep drop (400/100, 7200 bytes) while every exit code stayed
// correct.
//
// Every want was confirmed against BOTH oracles — bin/fern -interp and the native
// x86-64 backend agreed on each — never read off the self-host run under test.

type structKeyCase struct {
	name string
	src  string
	want int
	// allocs/frees the self-host must report, or -1 to assert nothing. These are
	// the SELF-HOST's numbers, not native's: several of these shapes carry
	// residual leaks that belong to other issues (#7343, #7351) and would be
	// pinned wrongly by asserting a balance.
	allocs int64
	frees  int64
}

const structKeyP = `struct P { xs: i32[], s: string }
function w(a: string): string { return a + "!"; }
`

const structKeyMainB = `
function main(): i32 {
    var b: P = P { xs: [7, 8], s: w("b") };
    var t: i32 = 0; var i: i32 = 0;
    while (i < 100) { t = t + round(b, i); i = i + 1; }
    if (__rc_underflow_count() != 0) { return 99; }
    return t % 83;
}`

const structKeyMain = `
function main(): i32 { var t: i32 = 0; var i: i32 = 0; ` +
	`while (i < 100) { t = t + round(i); i = i + 1; } ` +
	`if (__rc_underflow_count() != 0) { return 99; } return t % 83; }`

func structKeyCases() []structKeyCase {
	return []structKeyCase{
		{
			// THE BUG. Two `var v` in sibling `if` arms: the first is a fresh
			// struct literal and earns the credit, the second aliases a param and
			// must not. Base: 99, `204/204 live_bytes=0` — the census cannot see it.
			name: "collide_literal",
			src: structKeyP + `function round(base: P, i: i32): i32 {
    var t: i32 = 0;
    if (i % 2 == 0) { var v: P = P { xs: [i, i + 1], s: w("p") }; t = t + v.xs.len(); }
    if (i % 2 == 1) { var v: P = base;  t = t + v.xs.len(); }
    return t;
}` + structKeyMainB,
			want: 34, allocs: 204, frees: 200,
		},
		{
			// THE PAIRWISE CONTROL, and the assertion that carries the most weight:
			// the same program with the second local renamed `u`. It never
			// collided, so it was already correct at base — and after the fix the
			// COLLIDING program measures identically to it, exit code and census
			// alike. That is checkable without deciding whether the residual 4
			// blocks are correct, which they are not.
			//
			// Those 4 blocks are `main`'s own `b`, escape-flagged into the call
			// argument and never released at exit. They are FLAT — 120 bytes at
			// 100, 200 and 400 rounds alike — and they grow with `b` rather than
			// with the loop: widening its array to 8 elements moves them to 168.
			// So they are neither this bug nor a per-round one, which is why the
			// row asserts counts rather than a balance.
			name: "collide_literal_renamed",
			src: structKeyP + `function round(base: P, i: i32): i32 {
    var t: i32 = 0;
    if (i % 2 == 0) { var v: P = P { xs: [i, i + 1], s: w("p") }; t = t + v.xs.len(); }
    if (i % 2 == 1) { var u: P = base;  t = t + u.xs.len(); }
    return t;
}` + structKeyMainB,
			want: 34, allocs: 204, frees: 200,
		},
		{
			// The same collision reached through a LOOP rather than two `if`s, so
			// the two bindings are the same statement pair on different iterations.
			// Base: 99, `404/404`.
			name: "collide_loop",
			src: structKeyP + `function round(base: P, i: i32): i32 {
    var t: i32 = 0;
    var k: i32 = 0;
    while (k < 2) {
        if (k == 0) { var v: P = P { xs: [i, i + 1], s: w("p") }; t = t + v.xs.len(); }
        if (k == 1) { var v: P = base; t = t + v.xs.len(); }
        k = k + 1;
    }
    return t;
}` + structKeyMainB,
			want: 68, allocs: 404, frees: 400,
		},
		{
			// CONTROL — a BLOCK-SCOPED struct local, credited through
			// slot_is_reclaimable_struct_scoped and the "FLDCHECKED:" witness.
			// This is the row that catches the silent half of a key migration: the
			// markers are derived from the same entries, and a reader left on the
			// name key takes this to 400/100 (7200 bytes) with the exit code
			// unchanged.
			name: "block_scoped",
			src: structKeyP + `function round(i: i32): i32 {
    var t: i32 = 0;
    { var v: P = P { xs: [i, i + 1], s: w("p") }; t = t + v.xs.len(); }
    return t;
}` + structKeyMain,
			want: 34, allocs: 400, frees: 400,
		},
		{
			// CONTROL — the same binding at FUNCTION scope, which resolves through
			// the strict (retirement-refusing) predicate instead. Both predicates
			// have to keep their own answer: the site key survives retire_locals'
			// rename where the name did not, so resolving it without restoring that
			// refusal would newly credit block-scoped slots at the fourteen other
			// consumers — the flip reclaim_slot_name's class note records as
			// segfaulting the gen1 self-compile.
			name: "fn_scoped",
			src: structKeyP + `function round(i: i32): i32 {
    var v: P = P { xs: [i, i + 1], s: w("p") };
    return v.xs.len();
}` + structKeyMain,
			want: 34, allocs: 400, frees: 400,
		},
		{
			// CONTROL — a struct from a producer returning the literal DIRECTLY,
			// the shape `return_fresh_struct_ret_fns` already admits. Credited
			// before and after.
			name: "producer_literal",
			src: structKeyP + `function mk(i: i32): P { return P { xs: [i, i + 1], s: w("p") }; }
function round(i: i32): i32 {
    var v: P = mk(i);
    return v.xs.len();
}` + structKeyMain,
			want: 34, allocs: 400, frees: 400,
		},
		{
			// #7343, now CREDITED. This row was "producer_local_still_refused"
			// when the keying landed: it pinned that the re-key widened nothing,
			// and it was that fix's fails-before case. #7343 then supplied the
			// ExprIdent arm the return predicate lacked, so the shape balances at
			// 400/400 and the name would be a lie if it stayed — the same rename
			// the rc-log records for `string-concat-temps-still-leak`.
			//
			// It still earns its place, one direction over: it is now the row that
			// fails if that credit is ever withdrawn.
			name: "producer_local_now_credited",
			src: structKeyP + `function mk(i: i32): P { var p: P = P { xs: [i, i + 1], s: w("p") }; return p; }
function round(i: i32): i32 {
    var v: P = mk(i);
    return v.xs.len();
}` + structKeyMain,
			want: 34, allocs: 400, frees: 400,
		},
		{
			// The "NODEEP:" witness. The header above explains that the marker is
			// derived from the same entry as the credit and had to move with it —
			// this is the row that FAILS if it ever stops resolving.
			//
			// The builder shape is the one the marker exists for: `s = s.emit(x)`
			// hands the struct's array field to the callee's result with no counted
			// reference (an in-place `b.ops.append(x)` returns the same buffer), so
			// the deep drop of the superseded box must be withheld. A NODEEP that
			// resolved to nothing would grant it and free a buffer the result still
			// holds — an over-release the byte counts show as balanced, which is why
			// `want` carries it rather than the frees column.
			//
			// Confirmed against both oracles: interp and native x86-64 each exit 51.
			name: "builder_nodeep",
			src: `struct B { ops: i32[] }
function (b: B) emit(x: i32): B { return B { ops: b.ops.append(x) }; }
function round(i: i32): i32 {
    var s: B = B { ops: [1] };
    s = s.emit(i);
    s = s.emit(i + 1);
    return s.ops.len();
}` + structKeyMain,
			want: 51, allocs: 800, frees: 800,
		},
	}
}

// TestSelfHostStructCreditSiteKeyX86_64 — each struct binding resolves the credit
// it earned itself, and no same-named sibling inherits one.
//
// Both assertions carry signal, and they catch opposite failures. The exit code
// is the over-release detector: a doubly-released block returns to the freelist,
// so `live_bytes` reads 0 through the double free and only
// `__rc_underflow_count()` dissents. The alloc/free counts are the leak detector,
// which the exit code cannot see — and which is where a site key that resolves to
// NO credit shows up.
func TestSelfHostStructCreditSiteKeyX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_ir_run.fern", "driver")

	for _, tc := range structKeyCases() {
		t.Run(tc.name, func(t *testing.T) {
			asm := hevCompile(t, runner, driverBin, tc.src, []string{"FERN_LEAKCHECK=1"})
			progBin := buildBin(t, gcc, dir, "structkey_"+tc.name, asm)
			stderr, exit := hevRun(t, runner, progBin)
			if exit != tc.want {
				t.Fatalf("%s exited %d, want %d (99 = rc underflow: a same-named "+
					"struct local inherited another binding's reclaim credit)", tc.name, exit, tc.want)
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
			if tc.allocs >= 0 && allocs != tc.allocs {
				t.Errorf("%s: %s — want allocs=%d. A change in what is ALLOCATED means the "+
					"probe stopped measuring this shape", tc.name, summary, tc.allocs)
			}
			if tc.frees >= 0 && frees != tc.frees {
				t.Errorf("%s: %s — want frees=%d. Fewer means a binding resolved no credit "+
					"at all (the silent half of a key migration); more means one was widened, "+
					"which this change deliberately does not do", tc.name, summary, tc.frees)
			}
		})
	}
}

// TestSelfHostStructCreditSiteKeyWasmIR — the wasm sibling. Exit codes only,
// which is the whole signal for an over-release: it moves no byte count on any
// backend.
func TestSelfHostStructCreditSiteKeyWasmIR(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping struct credit site-key wasm IR e2e")
	}
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "wasm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "wasm_ir_run.fern", "driver")

	for _, tc := range structKeyCases() {
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
			watFile := filepath.Join(dir, "structkey_"+tc.name+".wat")
			if err := os.WriteFile(watFile, wat, 0o644); err != nil {
				t.Fatalf("write wat: %v", err)
			}
			rcmd := exec.Command("wasmtime", "run", watFile)
			_ = rcmd.Run()
			if rcmd.ProcessState == nil || !rcmd.ProcessState.Exited() {
				t.Fatalf("wasmtime did not exit normally for %q:\n%s", tc.name, wat)
			}
			if got := rcmd.ProcessState.ExitCode(); got != tc.want {
				t.Errorf("struct credit site-key wasm IR %q = %d, want %d", tc.name, got, tc.want)
			}
		})
	}
}

// TestSelfHostStructCreditSiteKeyIRArm64 — the arm64 sibling under qemu.
func TestSelfHostStructCreditSiteKeyIRArm64(t *testing.T) {
	arm64gcc, qemu := arm64Tooling(t)
	x86gcc, x86runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, x86gcc, dir, "asm_ir_run.fern", "driver")

	for _, tc := range structKeyCases() {
		t.Run(tc.name, func(t *testing.T) {
			asm := runCapture(t, x86gcc, x86runner, driverBin, []byte(tc.src), "-target", "arm64-linux")
			if len(asm) == 0 {
				t.Fatalf("%s: self-host arm64 compiler emitted 0 bytes", tc.name)
			}
			bin := buildBinArm64(t, arm64gcc, dir, "structkey_"+tc.name+"_arm64", string(asm))
			cmd := runArm64Bin(qemu, bin)
			_ = cmd.Run()
			if code := cmd.ProcessState.ExitCode(); code != tc.want {
				t.Errorf("%s exited %d, want %d", tc.name, code, tc.want)
			}
		})
	}
}
