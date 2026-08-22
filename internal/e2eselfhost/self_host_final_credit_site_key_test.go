package e2eselfhost

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// --- The last name-keyed reclaim credits, keyed on the binding (#7253) -------
//
// The final block of #7253 step 1, and the one that RETIRES reclaim_slot_name:
// "ARRTUP:", "ARRSTRUCT:", "ARRSTRUCTA:", "ARRENUM:", "STRUCTARR:",
// "STRUCTARRA:", "RCENUM:", "RCENUMS:", "SCENUMS:" and the "DYN:" / "DYNCAND:"
// pair. After this nothing in irlower.fern resolves a reclaim credit by name.
//
// Every one had the same defect: a name has no scope, so two same-named locals
// in sibling blocks are two slots under one key, and when only one is credited
// the other inherits its verdict. THREE different signals came out of it:
//
//	fault     ARRTUP / ARRSTRUCT / STRUCTARR / STRUCTARRA / ARRENUM  -> exit 99
//	latent    SCENUMS — the class leaks its own source, so nothing dissents
//	denial    DYN — tagged_value_of returns the FIRST match, so the alias's
//	          entry SHADOWED the credited one and suppressed a release
//
// The latent form is the dangerous one and it is why the ordering matters: the
// stray dec lands on a box nothing else claimed, the census reads BETTER than
// the correct program's, and it becomes a double free the moment the class's own
// leak is fixed. So `scenum_collide` gets a LARGER leak after this change —
// removing a release that was never owed exposes the leak it was masking — and
// converges exactly onto its rename control.
//
// The census cannot see the faulting form either: `structarr_collide` and its
// rename control both read `allocs=400 frees=200 live_bytes=9600` and differ
// only in the exit code. Every row is therefore asserted on
// `__rc_underflow_count()` AND on exact counts; neither alone is sufficient.
//
// ADMISSION MATTERS MORE THAN THE SHAPE. `arrenum_collide` needs an rc-bearing
// payload: the `A(i32)` version of the same program never earns the credit and
// measured "no collision" while the bug was fully present. Two other classes
// were probed the same wrong way before this table settled.
//
// No credit is widened. The eight `credited_*` rows pin that every class still
// fires where there is no collision — the silent half of a key migration, where
// a site key resolving to nothing denies the credit and no exit code moves.
//
// Every want was confirmed against BOTH oracles — bin/fern -interp and the
// native x86-64 backend agreed on each — never read off the self-host run.

type finalKeyCase struct {
	name   string
	src    string
	want   int
	allocs int64
	frees  int64
}

func finalKeyCases() []finalKeyCase {
	return []finalKeyCase{
		{
			// THE FAULT on "ARRTUP:". Two same-named locals in sibling `if`
			// arms — one a fresh array-of-tuples literal that earns the credit, one
			// a bare alias of a local that outlives the block. Base: 99.
			name: "arrtup_collide",
			src: `function round(i: i32): i32 {
    var keep: (i32, i32[])[] = [(i, [i, i + 1]), (i + 1, [i + 2, i + 3])];
    var t: i32 = 0;
    if (i % 2 == 0) { var o: (i32, i32[])[] = [(i, [i, i + 1])]; t = t + o.len(); }
    if (i % 2 == 1) { var o: (i32, i32[])[] = keep; t = t + o.len(); }
    return t + keep.len();
}
function main(): i32 { var t: i32 = 0; var i: i32 = 0; while (i < 100) { t = t + round(i); i = i + 1; } if (__rc_underflow_count() != 0) { return 99; } return t % 83; }`,
			want: 18, allocs: 650, frees: 250,
		},
		{
			// Its pairwise control — the same program with the second local
			// renamed. Already correct at base; the colliding row converges onto
			// these exact numbers.
			name: "arrtup_renamed",
			src: `function round(i: i32): i32 {
    var keep: (i32, i32[])[] = [(i, [i, i + 1]), (i + 1, [i + 2, i + 3])];
    var t: i32 = 0;
    if (i % 2 == 0) { var o: (i32, i32[])[] = [(i, [i, i + 1])]; t = t + o.len(); }
    if (i % 2 == 1) { var u: (i32, i32[])[] = keep; t = t + u.len(); }
    return t + keep.len();
}
function main(): i32 { var t: i32 = 0; var i: i32 = 0; while (i < 100) { t = t + round(i); i = i + 1; } if (__rc_underflow_count() != 0) { return 99; } return t % 83; }`,
			want: 18, allocs: 650, frees: 250,
		},
		{
			// The same fault on "ARRSTRUCT:" (a struct with an rc-array field).
			// Base: 99.
			name: "arrstruct_collide",
			src: `struct P { xs: i32[] }
function round(i: i32): i32 {
    var keep: P[] = [P { xs: [i, i + 1] }, P { xs: [i + 2, i + 3] }];
    var t: i32 = 0;
    if (i % 2 == 0) { var o: P[] = [P { xs: [i, i + 1] }]; t = t + o.len(); }
    if (i % 2 == 1) { var o: P[] = keep; t = t + o.len(); }
    return t + keep.len();
}
function main(): i32 { var t: i32 = 0; var i: i32 = 0; while (i < 100) { t = t + round(i); i = i + 1; } if (__rc_underflow_count() != 0) { return 99; } return t % 83; }`,
			want: 18, allocs: 650, frees: 250,
		},
		{
			// Its pairwise control — the same program with the second local
			// renamed. Already correct at base; the colliding row converges onto
			// these exact numbers.
			name: "arrstruct_renamed",
			src: `struct P { xs: i32[] }
function round(i: i32): i32 {
    var keep: P[] = [P { xs: [i, i + 1] }, P { xs: [i + 2, i + 3] }];
    var t: i32 = 0;
    if (i % 2 == 0) { var o: P[] = [P { xs: [i, i + 1] }]; t = t + o.len(); }
    if (i % 2 == 1) { var u: P[] = keep; t = t + u.len(); }
    return t + keep.len();
}
function main(): i32 { var t: i32 = 0; var i: i32 = 0; while (i < 100) { t = t + round(i); i = i + 1; } if (__rc_underflow_count() != 0) { return 99; } return t % 83; }`,
			want: 18, allocs: 650, frees: 250,
		},
		{
			// The same fault on "STRUCTARR:" (a scalar-field struct array), and
			// another byte-identical row: base and control both read 400/200
			// live_bytes=9600, differing only in the exit code.
			name: "structarr_collide",
			src: `struct N { a: i32, b: i32 }
function round(i: i32): i32 {
    var keep: N[] = [N { a: i, b: i + 1 }, N { a: i + 2, b: i + 3 }];
    var t: i32 = 0;
    if (i % 2 == 0) { var o: N[] = [N { a: i, b: i }]; t = t + o.len(); }
    if (i % 2 == 1) { var o: N[] = keep; t = t + o.len(); }
    return t + keep.len();
}
function main(): i32 { var t: i32 = 0; var i: i32 = 0; while (i < 100) { t = t + round(i); i = i + 1; } if (__rc_underflow_count() != 0) { return 99; } return t % 83; }`,
			want: 18, allocs: 400, frees: 200,
		},
		{
			// Its pairwise control — the same program with the second local
			// renamed. Already correct at base; the colliding row converges onto
			// these exact numbers.
			name: "structarr_renamed",
			src: `struct N { a: i32, b: i32 }
function round(i: i32): i32 {
    var keep: N[] = [N { a: i, b: i + 1 }, N { a: i + 2, b: i + 3 }];
    var t: i32 = 0;
    if (i % 2 == 0) { var o: N[] = [N { a: i, b: i }]; t = t + o.len(); }
    if (i % 2 == 1) { var u: N[] = keep; t = t + u.len(); }
    return t + keep.len();
}
function main(): i32 { var t: i32 = 0; var i: i32 = 0; while (i < 100) { t = t + round(i); i = i + 1; } if (__rc_underflow_count() != 0) { return 99; } return t % 83; }`,
			want: 18, allocs: 400, frees: 200,
		},
		{
			// The APPEND-BUILT sibling, "STRUCTARRA:". Its entry and its "A|"
			// marker row are the same key, so the self-lookup that separates
			// append-built from literal-built stays key-to-key. Base: 99.
			name: "structarra_collide",
			src: `struct N { a: i32, b: i32 }
function round(i: i32): i32 {
    var keep: N[] = [];
    keep = keep.append(N { a: i, b: i + 1 });
    var t: i32 = 0;
    if (i % 2 == 0) { var o: N[] = []; o = o.append(N { a: i, b: i }); t = t + o.len(); }
    if (i % 2 == 1) { var o: N[] = keep; t = t + o.len(); }
    return t + keep.len();
}
function main(): i32 { var t: i32 = 0; var i: i32 = 0; while (i < 100) { t = t + round(i); i = i + 1; } if (__rc_underflow_count() != 0) { return 99; } return t % 83; }`,
			want: 34, allocs: 450, frees: 250,
		},
		{
			// Its pairwise control — the same program with the second local
			// renamed. Already correct at base; the colliding row converges onto
			// these exact numbers.
			name: "structarra_renamed",
			src: `struct N { a: i32, b: i32 }
function round(i: i32): i32 {
    var keep: N[] = [];
    keep = keep.append(N { a: i, b: i + 1 });
    var t: i32 = 0;
    if (i % 2 == 0) { var o: N[] = []; o = o.append(N { a: i, b: i }); t = t + o.len(); }
    if (i % 2 == 1) { var u: N[] = keep; t = t + u.len(); }
    return t + keep.len();
}
function main(): i32 { var t: i32 = 0; var i: i32 = 0; while (i < 100) { t = t + round(i); i = i + 1; } if (__rc_underflow_count() != 0) { return 99; } return t % 83; }`,
			want: 34, allocs: 450, frees: 250,
		},
		{
			// The same fault on "ARRENUM:", whose entry is "<key>#<Enum>" — the
			// element enum rides the credit because an `E[]` slot records its
			// element type nowhere. A site key contains no '#', so the split is
			// unambiguous. Base: 99.
			//
			// The payload must be rc-bearing: an `A(i32)` version of this program
			// never earns the credit at all, and measured "no collision" while the
			// bug was fully present.
			name: "arrenum_collide",
			src: `enum E { A(string), B }
function round(pre: string, i: i32): i32 {
    var keep: E[] = [E.A(pre + "k"), E.B];
    var t: i32 = 0;
    if (i % 2 == 0) { var o: E[] = [E.A(pre + "x"), E.B]; t = t + o.len(); }
    if (i % 2 == 1) { var o: E[] = keep; t = t + o.len(); }
    return t + keep.len();
}
function main(): i32 { var pre: string = "ab"; var t: i32 = 0; var i: i32 = 0; while (i < 100) { t = t + round(pre, i); i = i + 1; } if (__rc_underflow_count() != 0) { return 99; } return t % 83; }`,
			want: 68, allocs: 750, frees: 350,
		},
		{
			// Its pairwise control — the same program with the second local
			// renamed. Already correct at base; the colliding row converges onto
			// these exact numbers.
			name: "arrenum_renamed",
			src: `enum E { A(string), B }
function round(pre: string, i: i32): i32 {
    var keep: E[] = [E.A(pre + "k"), E.B];
    var t: i32 = 0;
    if (i % 2 == 0) { var o: E[] = [E.A(pre + "x"), E.B]; t = t + o.len(); }
    if (i % 2 == 1) { var u: E[] = keep; t = t + u.len(); }
    return t + keep.len();
}
function main(): i32 { var pre: string = "ab"; var t: i32 = 0; var i: i32 = 0; while (i < 100) { t = t + round(pre, i); i = i + 1; } if (__rc_underflow_count() != 0) { return 99; } return t % 83; }`,
			want: 68, allocs: 750, frees: 350,
		},
		{
			// LATENT, not faulting: "SCENUMS:" leaks its own source box, so the
			// stray release lands on something nothing else claimed. Base 150/100
			// (2000) -> 150/50 (4000), converging on its control. THE LEAK GETS
			// BIGGER AND THAT IS THE FIX.
			name: "scenum_collide",
			src: `enum S { P(i32), Q }
function round(i: i32): i32 {
    var keep: S = S.P(i);
    var t: i32 = 0;
    if (i % 2 == 0) { var o: S = S.P(i + 1); t = t + 1; }
    if (i % 2 == 1) { var o: S = keep; t = t + 2; }
    match (keep) { S.P(v) => { t = t + v; }, S.Q => {} }
    return t;
}
function main(): i32 { var t: i32 = 0; var i: i32 = 0; while (i < 100) { t = t + round(i); i = i + 1; } if (__rc_underflow_count() != 0) { return 99; } return t % 83; }`,
			want: 37, allocs: 150, frees: 50,
		},
		{
			// Its pairwise control — the same program with the second local
			// renamed. Already correct at base; the colliding row converges onto
			// these exact numbers.
			name: "scenum_renamed",
			src: `enum S { P(i32), Q }
function round(i: i32): i32 {
    var keep: S = S.P(i);
    var t: i32 = 0;
    if (i % 2 == 0) { var o: S = S.P(i + 1); t = t + 1; }
    if (i % 2 == 1) { var u: S = keep; t = t + 2; }
    match (keep) { S.P(v) => { t = t + v; }, S.Q => {} }
    return t;
}
function main(): i32 { var t: i32 = 0; var i: i32 = 0; while (i < 100) { t = t + round(i); i = i + 1; } if (__rc_underflow_count() != 0) { return 99; } return t % 83; }`,
			want: 37, allocs: 150, frees: 50,
		},
		{
			// The "DYN:" / "DYNCAND:" family, and a THIRD severity flavour: here
			// the collision DENIED a release rather than granting a stray one —
			// base 150/0 (6000) against the control's 150/50 (4000), because
			// tagged_value_of returns the FIRST match and the alias's entry
			// shadowed the credited one. Converges on the control after.
			name: "dyn_collide",
			src: `trait Show { function id(self: Self): i32; }
struct A { v: i32 }
impl Show for A { function id(self: Self): i32 { return self.v; } }
function round(i: i32): i32 {
    var keep: dyn Show = A { v: i };
    var t: i32 = 0;
    if (i % 2 == 0) { var d: dyn Show = A { v: i + 1 }; t = t + d.id(); }
    if (i % 2 == 1) { var d: dyn Show = keep; t = t + d.id(); }
    return t + keep.id();
}
function main(): i32 { var t: i32 = 0; var i: i32 = 0; while (i < 100) { t = t + round(i); i = i + 1; } if (__rc_underflow_count() != 0) { return 99; } return t % 83; }`,
			want: 73, allocs: 150, frees: 50,
		},
		{
			// Its pairwise control — the same program with the second local
			// renamed. Already correct at base; the colliding row converges onto
			// these exact numbers.
			name: "dyn_renamed",
			src: `trait Show { function id(self: Self): i32; }
struct A { v: i32 }
impl Show for A { function id(self: Self): i32 { return self.v; } }
function round(i: i32): i32 {
    var keep: dyn Show = A { v: i };
    var t: i32 = 0;
    if (i % 2 == 0) { var d: dyn Show = A { v: i + 1 }; t = t + d.id(); }
    if (i % 2 == 1) { var e: dyn Show = keep; t = t + e.id(); }
    return t + keep.id();
}
function main(): i32 { var t: i32 = 0; var i: i32 = 0; while (i < 100) { t = t + round(i); i = i + 1; } if (__rc_underflow_count() != 0) { return 99; } return t % 83; }`,
			want: 73, allocs: 150, frees: 50,
		},
		{
			// POSITIVE CONTROL — a single credited binding with no sibling. The
			// silent half of a key migration: a site key that resolves to nothing
			// denies the credit, which no exit code would show.
			name: "credited_arrtup",
			src: `function round(i: i32): i32 {
    var o: (i32, i32[])[] = [(i, [i, i + 1])];
    return o.len();
}
function main(): i32 { var t: i32 = 0; var i: i32 = 0; while (i < 100) { t = t + round(i); i = i + 1; } if (__rc_underflow_count() != 0) { return 99; } return t % 83; }`,
			want: 17, allocs: 300, frees: 300,
		},
		{
			// POSITIVE CONTROL — a single credited binding with no sibling. The
			// silent half of a key migration: a site key that resolves to nothing
			// denies the credit, which no exit code would show.
			name: "credited_arrstruct",
			src: `struct P { xs: i32[] }
function round(i: i32): i32 {
    var o: P[] = [P { xs: [i, i + 1] }];
    return o.len();
}
function main(): i32 { var t: i32 = 0; var i: i32 = 0; while (i < 100) { t = t + round(i); i = i + 1; } if (__rc_underflow_count() != 0) { return 99; } return t % 83; }`,
			want: 17, allocs: 300, frees: 300,
		},
		{
			// POSITIVE CONTROL — a single credited binding with no sibling. The
			// silent half of a key migration: a site key that resolves to nothing
			// denies the credit, which no exit code would show.
			name: "credited_structarr",
			src: `struct N { a: i32, b: i32 }
function round(i: i32): i32 {
    var o: N[] = [N { a: i, b: i + 1 }];
    return o.len();
}
function main(): i32 { var t: i32 = 0; var i: i32 = 0; while (i < 100) { t = t + round(i); i = i + 1; } if (__rc_underflow_count() != 0) { return 99; } return t % 83; }`,
			want: 17, allocs: 200, frees: 200,
		},
		{
			// POSITIVE CONTROL — a single credited binding with no sibling. The
			// silent half of a key migration: a site key that resolves to nothing
			// denies the credit, which no exit code would show.
			name: "credited_structarra",
			src: `struct N { a: i32, b: i32 }
function round(i: i32): i32 {
    var o: N[] = [];
    o = o.append(N { a: i, b: i + 1 });
    return o.len();
}
function main(): i32 { var t: i32 = 0; var i: i32 = 0; while (i < 100) { t = t + round(i); i = i + 1; } if (__rc_underflow_count() != 0) { return 99; } return t % 83; }`,
			want: 17, allocs: 300, frees: 300,
		},
		{
			// POSITIVE CONTROL — a single credited binding with no sibling. The
			// silent half of a key migration: a site key that resolves to nothing
			// denies the credit, which no exit code would show.
			name: "credited_arrenum",
			src: `enum E { A(string), B }
function round(pre: string, i: i32): i32 {
    var o: E[] = [E.A(pre + "x"), E.B];
    return o.len();
}
function main(): i32 { var pre: string = "ab"; var t: i32 = 0; var i: i32 = 0; while (i < 100) { t = t + round(pre, i); i = i + 1; } if (__rc_underflow_count() != 0) { return 99; } return t % 83; }`,
			want: 34, allocs: 500, frees: 500,
		},
		{
			// POSITIVE CONTROL — a single credited binding with no sibling. The
			// silent half of a key migration: a site key that resolves to nothing
			// denies the credit, which no exit code would show.
			name: "credited_rcenum",
			src: `enum R { Full(i32[]), Empty }
function round(i: i32): i32 {
    var o: R = R.Full([i, i + 1]);
    var t: i32 = 0;
    match (o) { R.Full(xs) => { t = t + xs[0]; }, R.Empty => {} }
    return t;
}
function main(): i32 { var t: i32 = 0; var i: i32 = 0; while (i < 100) { t = t + round(i); i = i + 1; } if (__rc_underflow_count() != 0) { return 99; } return t % 83; }`,
			want: 53, allocs: 200, frees: 200,
		},
		{
			// POSITIVE CONTROL — a single credited binding with no sibling. The
			// silent half of a key migration: a site key that resolves to nothing
			// denies the credit, which no exit code would show.
			name: "credited_scenum",
			src: `enum S { P(i32), Q }
function round(i: i32): i32 {
    var o: S = S.P(i);
    return i + 1;
}
function main(): i32 { var t: i32 = 0; var i: i32 = 0; while (i < 100) { t = t + round(i); i = i + 1; } if (__rc_underflow_count() != 0) { return 99; } return t % 83; }`,
			want: 70, allocs: 100, frees: 100,
		},
		{
			// POSITIVE CONTROL — a single credited binding with no sibling. The
			// silent half of a key migration: a site key that resolves to nothing
			// denies the credit, which no exit code would show.
			name: "credited_dyn",
			src: `trait Show { function id(self: Self): i32; }
struct A { v: i32 }
impl Show for A { function id(self: Self): i32 { return self.v; } }
function round(i: i32): i32 {
    var d: dyn Show = A { v: i };
    return d.id();
}
function main(): i32 { var t: i32 = 0; var i: i32 = 0; while (i < 100) { t = t + round(i); i = i + 1; } if (__rc_underflow_count() != 0) { return 99; } return t % 83; }`,
			want: 53, allocs: 100, frees: 100,
		}}
}

// TestSelfHostFinalCreditSiteKeyX86_64 — each binding resolves the credit it
// earned itself, across the last name-keyed families.
func TestSelfHostFinalCreditSiteKeyX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_ir_run.fern", "driver")

	for _, tc := range finalKeyCases() {
		t.Run(tc.name, func(t *testing.T) {
			asm := hevCompile(t, runner, driverBin, tc.src, []string{"FERN_LEAKCHECK=1"})
			progBin := buildBin(t, gcc, dir, "finalkey_"+tc.name, asm)
			stderr, exit := hevRun(t, runner, progBin)
			if exit != tc.want {
				t.Fatalf("%s exited %d, want %d (99 = rc underflow: a same-named "+
					"local inherited another binding's reclaim credit)", tc.name, exit, tc.want)
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

// TestSelfHostFinalCreditSiteKeyWasmIR — the wasm sibling. Exit codes only,
// which is the whole signal for the five faulting rows.
func TestSelfHostFinalCreditSiteKeyWasmIR(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping final credit site-key wasm IR e2e")
	}
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "wasm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "wasm_ir_run.fern", "driver")

	for _, tc := range finalKeyCases() {
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
			watFile := filepath.Join(dir, "finalkey_"+tc.name+".wat")
			if err := os.WriteFile(watFile, wat, 0o644); err != nil {
				t.Fatalf("write wat: %v", err)
			}
			rcmd := exec.Command("wasmtime", "run", watFile)
			_ = rcmd.Run()
			if rcmd.ProcessState == nil || !rcmd.ProcessState.Exited() {
				t.Fatalf("wasmtime did not exit normally for %q:\n%s", tc.name, wat)
			}
			if got := rcmd.ProcessState.ExitCode(); got != tc.want {
				t.Errorf("final credit site-key wasm IR %q = %d, want %d", tc.name, got, tc.want)
			}
		})
	}
}

// TestSelfHostFinalCreditSiteKeyIRArm64 — the arm64 sibling under qemu.
func TestSelfHostFinalCreditSiteKeyIRArm64(t *testing.T) {
	arm64gcc, qemu := arm64Tooling(t)
	x86gcc, x86runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, x86gcc, dir, "asm_ir_run.fern", "driver")

	for _, tc := range finalKeyCases() {
		t.Run(tc.name, func(t *testing.T) {
			asm := runCapture(t, x86gcc, x86runner, driverBin, []byte(tc.src), "-target", "arm64-linux")
			if len(asm) == 0 {
				t.Fatalf("%s: self-host arm64 compiler emitted 0 bytes", tc.name)
			}
			bin := buildBinArm64(t, arm64gcc, dir, "finalkey_"+tc.name+"_arm64", string(asm))
			cmd := runArm64Bin(qemu, bin)
			_ = cmd.Run()
			if code := cmd.ProcessState.ExitCode(); code != tc.want {
				t.Errorf("%s exited %d, want %d", tc.name, code, tc.want)
			}
		})
	}
}
