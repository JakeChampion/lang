package e2eselfhost

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// --- The Option-family reclaim credits, keyed on the binding (#7253 step 1) --
//
// Seven tags — "OPTAARR:", "OPTTUP:", "OPTSTRUCT:", "OPTARRARR:", "OPTARR:",
// "OPTARRERR:", "OPTSTR:" — all resolved their credit through
// reclaim_slot_name, i.e. by the variable's NAME. A name has no scope, so two
// same-named Option locals in sibling blocks are two slots under one key, and
// when only one is proven fresh the other inherits its verdict and releases a
// payload it does not own.
//
// THE SEVERITY IS NOT A PROPERTY OF THE CLASS. It depends on whether the
// aliased source has another owner:
//
//	OPTTUP / OPTSTRUCT / OPTAARR   the source is released elsewhere -> exit 99
//	OPTARRARR / OPTARR / OPTSTR    the class leaks its own source   -> silent
//
// The second group is the dangerous one. Its stray dec lands on a box nothing
// else claimed, so no underflow fires and the census even looks *better* — the
// colliding program frees more than its rename control. It becomes a double
// free the moment the class's own leak is fixed. That is why three of the rows
// below get a LARGER leak after this change: removing a release that was never
// owed exposes the leak it was masking.
//
// The census cannot see the first group either: `allocs == frees` at
// `live_bytes == 0` on both sides of the fault, because a doubly-released block
// goes straight back to the freelist. `optaarr_collide` is the extreme case —
// it and its rename control have byte-identical censuses (600/300, 12000) and
// differ only in the exit code. Every row is therefore asserted on
// `__rc_underflow_count()` AND on exact counts; neither alone is sufficient.
//
// No credit is widened. The six `credited_*` rows pin that every class still
// fires where there is no collision — the silent half of a key migration, where
// a site key that resolves to nothing denies the credit and no exit code moves.
//
// Every want was confirmed against BOTH oracles — bin/fern -interp and the
// native x86-64 backend agreed on each — never read off the self-host run.

type optKeyCase struct {
	name string
	src  string
	// want is the exit code; 99 means the rc over-release detector fired.
	want int
	// allocs/frees are the SELF-HOST's numbers. Several of these shapes carry
	// residual leaks belonging to other issues (#7351's string doubling among
	// them), so a balance assertion would pin the wrong thing.
	allocs int64
	frees  int64
}

func optKeyCases() []optKeyCase {
	return []optKeyCase{
		{
			// THE FAULT. Two `var o` in sibling `if` arms: the first a fresh
			// Some((..)) that earns "OPTTUP:", the second a bare alias of a local
			// that outlives the block AND is released elsewhere. Base: 99.
			name: "opttup_collide",
			src: `
function round(b: Option[(i32, i32[])], i: i32): i32 {
    var t: i32 = 0;
    if (i % 2 == 0) { var o: Option[(i32, i32[])] = Some((i, [i, i + 1])); match (o) { Some(p) => { t = t + p.0; }, None => {} } }
    if (i % 2 == 1) { var o: Option[(i32, i32[])] = b; match (o) { Some(p) => { t = t + p.0; }, None => {} } }
    return t;
}

function main(): i32 {
    var b: Option[(i32, i32[])] = Some((7, [7, 8]));
    var t: i32 = 0; var i: i32 = 0;
    while (i < 100) { t = t + round(b, i); i = i + 1; }
    if (__rc_underflow_count() != 0) { return 99; }
    return t % 83;
}`,
			want: 61, allocs: 153, frees: 150,
		},
		{
			// The pairwise control — the same program with the second local
			// named `u`. Already correct at base, and after the fix the colliding
			// program measures identically to it, exit code and census alike.
			name: "opttup_renamed",
			src: `
function round(b: Option[(i32, i32[])], i: i32): i32 {
    var t: i32 = 0;
    if (i % 2 == 0) { var o: Option[(i32, i32[])] = Some((i, [i, i + 1])); match (o) { Some(p) => { t = t + p.0; }, None => {} } }
    if (i % 2 == 1) { var u: Option[(i32, i32[])] = b; match (u) { Some(p) => { t = t + p.0; }, None => {} } }
    return t;
}

function main(): i32 {
    var b: Option[(i32, i32[])] = Some((7, [7, 8]));
    var t: i32 = 0; var i: i32 = 0;
    while (i < 100) { t = t + round(b, i); i = i + 1; }
    if (__rc_underflow_count() != 0) { return 99; }
    return t % 83;
}`,
			want: 61, allocs: 153, frees: 150,
		},
		{
			// The same fault on "OPTSTRUCT:". Base: 99.
			name: "optstruct_collide",
			src: `struct P { xs: i32[] }
function round(b: Option[P], i: i32): i32 {
    var t: i32 = 0;
    if (i % 2 == 0) { var o: Option[P] = Some(P { xs: [i, i + 1] }); match (o) { Some(p) => { t = t + p.xs.len(); }, None => {} } }
    if (i % 2 == 1) { var o: Option[P] = b; match (o) { Some(p) => { t = t + p.xs.len(); }, None => {} } }
    return t;
}

function main(): i32 {
    var b: Option[P] = Some(P { xs: [7, 8] });
    var t: i32 = 0; var i: i32 = 0;
    while (i < 100) { t = t + round(b, i); i = i + 1; }
    if (__rc_underflow_count() != 0) { return 99; }
    return t % 83;
}`,
			want: 34, allocs: 153, frees: 150,
		},
		{
			// Its pairwise control.
			name: "optstruct_renamed",
			src: `struct P { xs: i32[] }
function round(b: Option[P], i: i32): i32 {
    var t: i32 = 0;
    if (i % 2 == 0) { var o: Option[P] = Some(P { xs: [i, i + 1] }); match (o) { Some(p) => { t = t + p.xs.len(); }, None => {} } }
    if (i % 2 == 1) { var u: Option[P] = b; match (u) { Some(p) => { t = t + p.xs.len(); }, None => {} } }
    return t;
}

function main(): i32 {
    var b: Option[P] = Some(P { xs: [7, 8] });
    var t: i32 = 0; var i: i32 = 0;
    while (i < 100) { t = t + round(b, i); i = i + 1; }
    if (__rc_underflow_count() != 0) { return 99; }
    return t % 83;
}`,
			want: 34, allocs: 153, frees: 150,
		},
		{
			// The same fault on "OPTAARR:", and the sharpest row here: the
			// colliding and renamed programs have BYTE-IDENTICAL censuses —
			// 600/300 live_bytes=12000 both — and differ only in the exit code,
			// 99 against 68. No reading of FERN_LEAKCHECK separates them.
			name: "optaarr_collide",
			src: `function round(i: i32): i32 {
    var keep: Option[i32[]][] = [Some([7, 8]), None];
    var t: i32 = 0;
    if (i % 2 == 0) { var xs: Option[i32[]][] = [Some([i, i + 1]), None]; t = t + xs.len(); }
    if (i % 2 == 1) { var xs: Option[i32[]][] = keep; t = t + xs.len(); }
    return t + keep.len();
}
function main(): i32 { var t: i32 = 0; var i: i32 = 0; while (i < 100) { t = t + round(i); i = i + 1; } if (__rc_underflow_count() != 0) { return 99; } return t % 83; }`,
			want: 68, allocs: 600, frees: 300,
		},
		{
			// Its pairwise control, identical census to the row above at base.
			name: "optaarr_renamed",
			src: `function round(i: i32): i32 {
    var keep: Option[i32[]][] = [Some([7, 8]), None];
    var t: i32 = 0;
    if (i % 2 == 0) { var xs: Option[i32[]][] = [Some([i, i + 1]), None]; t = t + xs.len(); }
    if (i % 2 == 1) { var ys: Option[i32[]][] = keep; t = t + ys.len(); }
    return t + keep.len();
}
function main(): i32 { var t: i32 = 0; var i: i32 = 0; while (i < 100) { t = t + round(i); i = i + 1; } if (__rc_underflow_count() != 0) { return 99; } return t % 83; }`,
			want: 68, allocs: 600, frees: 300,
		},
		{
			// LATENT, not faulting: the credit crosses bindings exactly as
			// above, but "OPTARRARR:" leaks its own source box, so the stray
			// release lands on something nothing else claimed and no underflow
			// fires. Base 600/400 (8000); after, 600/200 (16000) — the same as
			// its rename control. THE LEAK GETS BIGGER AND THAT IS THE FIX: the
			// stray dec was releasing a structure this binding does not own, and
			// it was masking half the class's own leak. It becomes a double free
			// the moment that leak is closed.
			name: "optarrarr_collide",
			src: `
function round(i: i32): i32 {
    var keep: Option[i32[][]] = Some([[7, 8], [9, 10]]);
    var t: i32 = 0;
    if (i % 2 == 0) { var o: Option[i32[][]] = Some([[i, i + 1], [i + 2, i + 3]]); match (o) { Some(p) => { t = t + p.len(); }, None => {} } }
    if (i % 2 == 1) { var o: Option[i32[][]] = keep; match (o) { Some(p) => { t = t + p.len(); }, None => {} } }
    match (keep) { Some(q) => { t = t + q.len(); }, None => {} }
    return t;
}
function main(): i32 { var t: i32 = 0; var i: i32 = 0; while (i < 100) { t = t + round(i); i = i + 1; } if (__rc_underflow_count() != 0) { return 99; } return t % 83; }`,
			want: 68, allocs: 600, frees: 200,
		},
		{
			// Its pairwise control — unchanged by this change, and the number
			// the colliding row converges to.
			name: "optarrarr_renamed",
			src: `
function round(i: i32): i32 {
    var keep: Option[i32[][]] = Some([[7, 8], [9, 10]]);
    var t: i32 = 0;
    if (i % 2 == 0) { var o: Option[i32[][]] = Some([[i, i + 1], [i + 2, i + 3]]); match (o) { Some(p) => { t = t + p.len(); }, None => {} } }
    if (i % 2 == 1) { var u: Option[i32[][]] = keep; match (u) { Some(p) => { t = t + p.len(); }, None => {} } }
    match (keep) { Some(q) => { t = t + q.len(); }, None => {} }
    return t;
}
function main(): i32 { var t: i32 = 0; var i: i32 = 0; while (i < 100) { t = t + round(i); i = i + 1; } if (__rc_underflow_count() != 0) { return 99; } return t % 83; }`,
			want: 68, allocs: 600, frees: 200,
		},
		{
			// The same latent form on "OPTARR:" (the unmatched half).
			// Base 300/200 (4000) -> 300/100 (8000).
			name: "optarr_collide",
			src: `
function round(i: i32): i32 {
    var keep: Option[i32[]] = Some([7, 8]);
    var t: i32 = 0;
    if (i % 2 == 0) { var o: Option[i32[]] = Some([i, i + 1]); t = t + 1; }
    if (i % 2 == 1) { var o: Option[i32[]] = keep; t = t + 2; }
    match (keep) { Some(q) => { t = t + q[0]; }, None => {} }
    return t;
}
function main(): i32 { var t: i32 = 0; var i: i32 = 0; while (i < 100) { t = t + round(i); i = i + 1; } if (__rc_underflow_count() != 0) { return 99; } return t % 83; }`,
			want: 20, allocs: 300, frees: 100,
		},
		{
			// Its pairwise control.
			name: "optarr_renamed",
			src: `
function round(i: i32): i32 {
    var keep: Option[i32[]] = Some([7, 8]);
    var t: i32 = 0;
    if (i % 2 == 0) { var o: Option[i32[]] = Some([i, i + 1]); t = t + 1; }
    if (i % 2 == 1) { var u: Option[i32[]] = keep; t = t + 2; }
    match (keep) { Some(q) => { t = t + q[0]; }, None => {} }
    return t;
}
function main(): i32 { var t: i32 = 0; var i: i32 = 0; while (i < 100) { t = t + round(i); i = i + 1; } if (__rc_underflow_count() != 0) { return 99; } return t % 83; }`,
			want: 20, allocs: 300, frees: 100,
		},
		{
			// The same latent form on "OPTSTR:". Base 150/100 (2000) ->
			// 150/50 (4000).
			name: "optstr_collide",
			src: `function round(i: i32): i32 {
    var keep: Option[string] = Some("keeper");
    var t: i32 = 0;
    if (i % 2 == 0) { var o: Option[string] = Some("fresh"); t = t + 1; }
    if (i % 2 == 1) { var o: Option[string] = keep; t = t + 2; }
    match (keep) { Some(q) => { t = t + q.len(); }, None => {} }
    return t;
}
function main(): i32 { var t: i32 = 0; var i: i32 = 0; while (i < 100) { t = t + round(i); i = i + 1; } if (__rc_underflow_count() != 0) { return 99; } return t % 83; }`,
			want: 3, allocs: 150, frees: 50,
		},
		{
			// Its pairwise control.
			name: "optstr_renamed",
			src: `function round(i: i32): i32 {
    var keep: Option[string] = Some("keeper");
    var t: i32 = 0;
    if (i % 2 == 0) { var o: Option[string] = Some("fresh"); t = t + 1; }
    if (i % 2 == 1) { var u: Option[string] = keep; t = t + 2; }
    match (keep) { Some(q) => { t = t + q.len(); }, None => {} }
    return t;
}
function main(): i32 { var t: i32 = 0; var i: i32 = 0; while (i < 100) { t = t + round(i); i = i + 1; } if (__rc_underflow_count() != 0) { return 99; } return t % 83; }`,
			want: 3, allocs: 150, frees: 50,
		},
		{
			// POSITIVE CONTROL — a single credited binding with no sibling.
			// This is the silent half of a key migration: a site key that resolves
			// to nothing denies the credit, which no exit code would show. All six
			// of these must keep balancing at live_bytes 0.
			name: "credited_opttup",
			src: `function round(i: i32): i32 {
    var o: Option[(i32, i32[])] = Some((i, [i, i + 1]));
    var t: i32 = 0;
    match (o) { Some(p) => { t = t + p.0; }, None => {} }
    return t;
}
function main(): i32 { var t: i32 = 0; var i: i32 = 0; while (i < 100) { t = t + round(i); i = i + 1; } if (__rc_underflow_count() != 0) { return 99; } return t % 83; }`,
			want: 53, allocs: 300, frees: 300,
		},
		{
			// Positive control.
			name: "credited_optstruct",
			src: `struct P { xs: i32[] }
function round(i: i32): i32 {
    var o: Option[P] = Some(P { xs: [i, i + 1] });
    var t: i32 = 0;
    match (o) { Some(p) => { t = t + p.xs.len(); }, None => {} }
    return t;
}
function main(): i32 { var t: i32 = 0; var i: i32 = 0; while (i < 100) { t = t + round(i); i = i + 1; } if (__rc_underflow_count() != 0) { return 99; } return t % 83; }`,
			want: 34, allocs: 300, frees: 300,
		},
		{
			// Positive control. Note allocs=400 against native's 300 — an
			// allocation-volume divergence that is not this change's (#7351).
			name: "credited_optaarr",
			src: `function round(i: i32): i32 {
    var xs: Option[i32[]][] = [Some([i, i + 1]), None];
    return xs.len();
}
function main(): i32 { var t: i32 = 0; var i: i32 = 0; while (i < 100) { t = t + round(i); i = i + 1; } if (__rc_underflow_count() != 0) { return 99; } return t % 83; }`,
			want: 34, allocs: 400, frees: 400,
		},
		{
			// Positive control.
			name: "credited_optarrarr",
			src: `function round(i: i32): i32 {
    var o: Option[i32[][]] = Some([[i, i + 1], [i + 2, i + 3]]);
    var t: i32 = 0;
    match (o) { Some(p) => { t = t + p.len(); }, None => {} }
    return t;
}
function main(): i32 { var t: i32 = 0; var i: i32 = 0; while (i < 100) { t = t + round(i); i = i + 1; } if (__rc_underflow_count() != 0) { return 99; } return t % 83; }`,
			want: 34, allocs: 400, frees: 400,
		},
		{
			// Positive control.
			name: "credited_optarr",
			src: `function round(i: i32): i32 {
    var o: Option[i32[]] = Some([i, i + 1]);
    return i + 1;
}
function main(): i32 { var t: i32 = 0; var i: i32 = 0; while (i < 100) { t = t + round(i); i = i + 1; } if (__rc_underflow_count() != 0) { return 99; } return t % 83; }`,
			want: 70, allocs: 200, frees: 200,
		},
		{
			// Positive control.
			name: "credited_optstr",
			src: `function round(i: i32): i32 {
    var o: Option[string] = Some("fresh");
    return i + 1;
}
function main(): i32 { var t: i32 = 0; var i: i32 = 0; while (i < 100) { t = t + round(i); i = i + 1; } if (__rc_underflow_count() != 0) { return 99; } return t % 83; }`,
			want: 70, allocs: 100, frees: 100,
		},
		{
			// THE PREDICTED CLASS, made reachable. A `for` element binder is not a
			// StmtVar, so no collector ever credited it — but it has a NAME, and
			// under the name key it inherited the "OPTARR:" verdict of the `var o`
			// in the sibling block. It was then releasing elements of `keep` that
			// `keep` still owns: base 700/500 (8000) against the rename control's
			// 700/300 (16000) — two stray releases a round.
			//
			// A for-in binder carries no binding site, so the site key refuses it
			// and this row converges onto its control. This is the row class the
			// emit-hash prediction names; whether any corpus FIXTURE has the shape
			// is a separate question, and this case is what proves the shape real
			// either way.
			name: "binder_forin_collide",
			src: `function round(i: i32): i32 {
    var keep: Option[i32[]][] = [Some([i, i + 1]), Some([i + 2, i + 3])];
    var t: i32 = 0;
    { var o: Option[i32[]] = Some([i + 7, i + 8]); t = t + 1; }
    for o in keep { match (o) { Some(p) => { t = t + p[0]; }, None => {} } }
    return t + keep.len();
}
function main(): i32 { var t: i32 = 0; var i: i32 = 0; while (i < 100) { t = t + round(i); i = i + 1; } if (__rc_underflow_count() != 0) { return 99; } return t % 83; }`,
			want: 25, allocs: 700, frees: 300,
		},
		{
			// Its pairwise control — the element binder named `e`. Unchanged by
			// this change, and the number the colliding row converges to.
			name: "binder_forin_renamed",
			src: `function round(i: i32): i32 {
    var keep: Option[i32[]][] = [Some([i, i + 1]), Some([i + 2, i + 3])];
    var t: i32 = 0;
    { var o: Option[i32[]] = Some([i + 7, i + 8]); t = t + 1; }
    for e in keep { match (e) { Some(p) => { t = t + p[0]; }, None => {} } }
    return t + keep.len();
}
function main(): i32 { var t: i32 = 0; var i: i32 = 0; while (i < 100) { t = t + round(i); i = i + 1; } if (__rc_underflow_count() != 0) { return 99; } return t % 83; }`,
			want: 25, allocs: 700, frees: 300,
		}}
}

// TestSelfHostOptionCreditSiteKeyX86_64 — each Option binding resolves the
// credit it earned itself, and no same-named sibling inherits one.
func TestSelfHostOptionCreditSiteKeyX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_ir_run.fern", "driver")

	for _, tc := range optKeyCases() {
		t.Run(tc.name, func(t *testing.T) {
			asm := hevCompile(t, runner, driverBin, tc.src, []string{"FERN_LEAKCHECK=1"})
			progBin := buildBin(t, gcc, dir, "optkey_"+tc.name, asm)
			stderr, exit := hevRun(t, runner, progBin)
			if exit != tc.want {
				t.Fatalf("%s exited %d, want %d (99 = rc underflow: a same-named "+
					"Option local inherited another binding's reclaim credit)", tc.name, exit, tc.want)
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
			if allocs != tc.allocs {
				t.Errorf("%s: %s — want allocs=%d. A change in what is ALLOCATED means the "+
					"probe stopped measuring this shape", tc.name, summary, tc.allocs)
			}
			if frees != tc.frees {
				t.Errorf("%s: %s — want frees=%d. MORE means a binding is releasing something "+
					"it does not own (the collision); FEWER means a binding resolved no credit "+
					"at all, which is the silent half of a key migration", tc.name, summary, tc.frees)
			}
		})
	}
}

// TestSelfHostOptionCreditSiteKeyWasmIR — the wasm sibling. Exit codes only,
// which is the whole signal for the three faulting rows: an over-release moves
// no byte count on any backend.
func TestSelfHostOptionCreditSiteKeyWasmIR(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping Option credit site-key wasm IR e2e")
	}
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "wasm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "wasm_ir_run.fern", "driver")

	for _, tc := range optKeyCases() {
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
			watFile := filepath.Join(dir, "optkey_"+tc.name+".wat")
			if err := os.WriteFile(watFile, wat, 0o644); err != nil {
				t.Fatalf("write wat: %v", err)
			}
			rcmd := exec.Command("wasmtime", "run", watFile)
			_ = rcmd.Run()
			if rcmd.ProcessState == nil || !rcmd.ProcessState.Exited() {
				t.Fatalf("wasmtime did not exit normally for %q:\n%s", tc.name, wat)
			}
			if got := rcmd.ProcessState.ExitCode(); got != tc.want {
				t.Errorf("Option credit site-key wasm IR %q = %d, want %d", tc.name, got, tc.want)
			}
		})
	}
}

// TestSelfHostOptionCreditSiteKeyIRArm64 — the arm64 sibling under qemu.
func TestSelfHostOptionCreditSiteKeyIRArm64(t *testing.T) {
	arm64gcc, qemu := arm64Tooling(t)
	x86gcc, x86runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, x86gcc, dir, "asm_ir_run.fern", "driver")

	for _, tc := range optKeyCases() {
		t.Run(tc.name, func(t *testing.T) {
			asm := runCapture(t, x86gcc, x86runner, driverBin, []byte(tc.src), "-target", "arm64-linux")
			if len(asm) == 0 {
				t.Fatalf("%s: self-host arm64 compiler emitted 0 bytes", tc.name)
			}
			bin := buildBinArm64(t, arm64gcc, dir, "optkey_"+tc.name+"_arm64", string(asm))
			cmd := runArm64Bin(qemu, bin)
			_ = cmd.Run()
			if code := cmd.ProcessState.ExitCode(); code != tc.want {
				t.Errorf("%s exited %d, want %d", tc.name, code, tc.want)
			}
		})
	}
}
