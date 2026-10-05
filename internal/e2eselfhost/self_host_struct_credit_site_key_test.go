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
// all. A name has no scope, so two `let v` in sibling blocks are two slots under
// one key, and when only one of them was proven fresh the other inherited its
// verdict:
//
//	if (i % 2 == 0) { let v: P = P { xs: [i, i + 1], s: w("p") }; … }   // credited
//	if (i % 2 == 1) { let v: P = base;                            … }   // an alias
//
// The alias holds the CALLER's box. Releasing it frees memory a live parameter
// still owns, which the rc detector reports at exit 99; the byte census cannot
// see it, because a doubly-released block goes straight back to the freelist.
// So each colliding row is asserted on the exit code, and must match its rename
// control's census exactly. On the typed lowering every row balances.
//
// Every want was confirmed against BOTH oracles — bin/fern -interp and the native
// x86-64 backend agreed on each — never read off the self-host run under test.

type structKeyCase struct {
	name string
	src  string
	want int
	// allocs/frees the self-host must report, or -1 to assert nothing.
	allocs int64
	frees  int64
}

const structKeyP = `struct P { xs: i32[], s: string }
function w(a: string): string { return a + "!"; }
`

const structKeyMainB = `
function main(): i32 {
    let b: P = P { xs: [7, 8], s: w("b") };
    let t: i32 = 0; let i: i32 = 0;
    while (i < 100) { t = t + round(b, i); i = i + 1; }
    if (__rc_underflow_count() != 0) { return 99; }
    return t % 83;
}`

const structKeyMain = `
function main(): i32 { let t: i32 = 0; let i: i32 = 0; ` +
	`while (i < 100) { t = t + round(i); i = i + 1; } ` +
	`if (__rc_underflow_count() != 0) { return 99; } return t % 83; }`

func structKeyCases() []structKeyCase {
	return []structKeyCase{
		{
			// Two `let v` in sibling `if` arms: the first is a fresh struct
			// literal, the second aliases a param. The census cannot see a
			// collision here; the underflow guard (exit 99) is what pins
			// it.
			name: "collide_literal",
			src: structKeyP + `function round(base: P, i: i32): i32 {
    let t: i32 = 0;
    if (i % 2 == 0) { let v: P = P { xs: [i, i + 1], s: w("p") }; t = t + v.xs.len(); }
    if (i % 2 == 1) { let v: P = base;  t = t + v.xs.len(); }
    return t;
}` + structKeyMainB,
			want: 34, allocs: 152, frees: 152,
		},
		{
			// THE PAIRWISE CONTROL: the same program with the second local
			// renamed `u`. The COLLIDING program must measure identically
			// to it, exit code and census alike.
			name: "collide_literal_renamed",
			src: structKeyP + `function round(base: P, i: i32): i32 {
    let t: i32 = 0;
    if (i % 2 == 0) { let v: P = P { xs: [i, i + 1], s: w("p") }; t = t + v.xs.len(); }
    if (i % 2 == 1) { let u: P = base;  t = t + u.xs.len(); }
    return t;
}` + structKeyMainB,
			want: 34, allocs: 152, frees: 152,
		},
		{
			// The same collision reached through a LOOP rather than two
			// `if`s, so the two bindings are the same statement pair on
			// different iterations.
			name: "collide_loop",
			src: structKeyP + `function round(base: P, i: i32): i32 {
    let t: i32 = 0;
    let k: i32 = 0;
    while (k < 2) {
        if (k == 0) { let v: P = P { xs: [i, i + 1], s: w("p") }; t = t + v.xs.len(); }
        if (k == 1) { let v: P = base; t = t + v.xs.len(); }
        k = k + 1;
    }
    return t;
}` + structKeyMainB,
			want: 68, allocs: 302, frees: 302,
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
    let t: i32 = 0;
    { let v: P = P { xs: [i, i + 1], s: w("p") }; t = t + v.xs.len(); }
    return t;
}` + structKeyMain,
			want: 34, allocs: 300, frees: 300,
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
    let v: P = P { xs: [i, i + 1], s: w("p") };
    return v.xs.len();
}` + structKeyMain,
			want: 34, allocs: 300, frees: 300,
		},
		{
			// CONTROL — a struct from a producer returning the literal DIRECTLY,
			// the shape `return_fresh_struct_ret_fns` already admits. Credited
			// before and after.
			name: "producer_literal",
			src: structKeyP + `function mk(i: i32): P { return P { xs: [i, i + 1], s: w("p") }; }
function round(i: i32): i32 {
    let v: P = mk(i);
    return v.xs.len();
}` + structKeyMain,
			want: 34, allocs: 300, frees: 300,
		},
		{
			// #7343, now CREDITED. This row was "producer_local_still_refused"
			// when the keying landed: it pinned that the re-key widened nothing,
			// and it was that fix's fails-before case. #7343 then supplied the
			// ExprIdent arm the return predicate lacked, so the shape balances at
			// 400/400 and the name would be a lie if it stayed — the same rename
			// the rc-log records for `string-concat-temps-still-leak`.
			//
			// It is still worth having, one direction over: it is now the row that
			// fails if that credit is ever withdrawn.
			name: "producer_local_now_credited",
			src: structKeyP + `function mk(i: i32): P { let p: P = P { xs: [i, i + 1], s: w("p") }; return p; }
function round(i: i32): i32 {
    let v: P = mk(i);
    return v.xs.len();
}` + structKeyMain,
			want: 34, allocs: 300, frees: 300,
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
			// The initial ops array goes through `id`, which hides the constant from the static-box plan,
			// so the first B is a heap box rather than a static one.
			//
			// Confirmed against both oracles: interp and native x86-64 each exit 51.
			name: "builder_nodeep",
			src: `struct B { ops: i32[] }
function (b: B) emit(x: i32): B { return B { ops: b.ops.append(x) }; }
function id(xs: i32[]): i32[] { return xs; }
function round(i: i32): i32 {
    let s: B = B { ops: id([1]) };
    s = s.emit(i);
    s = s.emit(i + 1);
    return s.ops.len();
}` + structKeyMain,
			want: 51, allocs: 500, frees: 500,
		},
	}
}

// TestSelfHostStructCreditSiteKeyX86_64 — each struct binding resolves the credit
// it earned itself, and no same-named sibling inherits one.
//
// Both assertions carry signal, and they catch opposite failures. The exit code
// is the over-release detector: a doubly-released block returns to the freelist,
// so `live_bytes` reads 0 through the double free and only
// `__rc_underflow_count()` reports it. The alloc/free counts are the leak detector,
// which the exit code cannot see — and which is where a site key that resolves to
// NO credit shows up.
func TestSelfHostStructCreditSiteKeyX86_64(t *testing.T) {
	boxedProbes(t)
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
				t.Errorf("%s: %s — want frees=%d", tc.name, summary, tc.frees)
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
