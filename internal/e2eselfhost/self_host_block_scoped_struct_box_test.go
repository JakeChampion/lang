package e2eselfhost

import (
	"strings"
	"testing"
)

// --- The block-scoped struct BOX, but not its fields (#6127) -----------------
//
// A struct declared inside a `while` body is credited under its source name, and
// its slot_name carries the "!retired!" prefix once the block ends, so the exit
// sweep's exact-match lookup misses it and nothing is swept: 8800 bytes over 100
// rounds on `S { xs: i32[], n: i32 }`, against 0 on native.
//
// Switching this class on wholesale SEGFAULTS the gen1 self-compile. The
// culprit is NOT the other thirteen consumers of slot_is_reclaimable_struct;
// three fixpoint runs, one variable each, isolate it:
//
//	the sweep + entry-zeroing only, deep drop included   SEGFAULT (gen1)
//	entry-zeroing alone (frees nothing)                  green, 402s
//	the sweep + entry-zeroing, BOX-ONLY free             green, 390s
//
// So the unsafe operation is specifically `__struct_drop_<T>` on a block-scoped
// slot. That got the BOX free landed (8800 -> 4000) with the field drop withheld,
// and left the why open.
//
// The why, from a stack trace rather than a fourth theory: gen1 faulted in
// `asmcore.EmitState.has_need` -> `__fern_str_eq` on a freed string. The shape is
// `var lo: StringLitOut = add_string_lit(s, ..); s = lo.state;` — `lo` is
// block-scoped and its `EmitState` FIELD is moved into the live threaded `s`, so
// the deep walk freed that state's arrays out from under it.
//
// Both escape analyses missed it identically: `expr_unsafe_for` and
// `moves_fields_expr` treat `name.field` as a borrow, which is right for a SCALAR
// field and wrong for a nested-struct / array / string one. The `"NODEEP:"`
// detector now consults the field TYPE (`optstruct_body_moves_field`), so the
// withholding is the marker's job — and with the marker able to see this move,
// the deep drop is granted to everyone else and the shape reaches **0**.
//
// The trade is visible in `field_extracted_out_of_the_block` below: it now
// reclaims LESS (500 frees, was 800) because the marker correctly refuses a local
// whose field escapes. Across the 164-program probe corpus that is the only shape
// that lost reclaim, against three that reached zero.
//
// None of this is visible to the probe corpus: all 162 differential programs agreed
// with native on both the segfaulting and the green build. The self-compiler is the
// only program that shows it, which is why the fixpoint runs FIRST on a reclaim
// change and not last.

func TestSelfHostBlockScopedStructBoxX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_ir_run.fern", "driver")

	counts := func(t *testing.T, name, src string, wantExit int) (int64, int64, int64) {
		t.Helper()
		asm := hevCompile(t, runner, driverBin, src, []string{"FERN_LEAKCHECK=1"})
		progBin := buildBin(t, gcc, dir, name, asm)
		stderr, exit := hevRun(t, runner, progBin)
		if exit != wantExit {
			t.Fatalf("%s exited %d, want %d", name, exit, wantExit)
		}
		summary := ""
		for _, line := range strings.Split(stderr, "\n") {
			if strings.HasPrefix(line, "leakcheck: ") {
				summary = line
			}
		}
		if summary == "" {
			t.Fatalf("%s: no leakcheck summary", name)
		}
		var allocs, frees, live int64
		if _, err := fmtSscan(summary, &allocs, &frees, &live); err != nil {
			t.Fatalf("%s: parse %q: %v", name, summary, err)
		}
		return allocs, frees, live
	}

	// The box is reclaimed; the field buffer is not. Both halves are asserted, so
	// this fails if the box free is lost AND if the field drop is ever granted
	// without the fixpoint question above being answered.
	t.Run("struct_declared_in_a_loop", func(t *testing.T) {
		src := `struct S { xs: i32[], n: i32 }
function round(r: i32): i32 {
    var acc: i32 = 0;
    var i: i32 = 0;
    while (i < 4) {
        var s: S = S { xs: [i, i + 1], n: i };
        acc = acc + s.n + s.xs.len();
        i = i + 1;
    }
    return acc + r;
}
function main(): i32 {
    var x: i32 = 0;
    var r: i32 = 0;
    while (r < 100) { x = x + round(r); r = r + 1; }
    return x % 83;
}`
		allocs, frees, live := counts(t, "bss_in_loop", src, 42)
		if allocs != 800 {
			t.Fatalf("allocs=%d, want 800 — the probe's shape changed and the numbers "+
				"below no longer mean what the comment says", allocs)
		}
		if frees != 800 {
			t.Errorf("frees=%d, want 800 — the boxes AND their `xs` buffers. This was 700 "+
				"while the deep field drop was withheld here; the withholding is now the "+
				"NODEEP marker's job, and the marker finally sees the bare-field-read move "+
				"that made the deep drop unsafe", frees)
		}
		if live != 0 {
			t.Errorf("live_bytes=%d, want 0 — 8800 before the box free landed, 4000 while "+
				"the field drop was withheld wholesale, and 0 now", live)
		}
	})

	// A scalar-only struct has no field drop to withhold, so it reaches zero.
	t.Run("scalar_only_struct_in_a_loop_reaches_zero", func(t *testing.T) {
		src := `struct S { a: i32, b: i32 }
function round(r: i32): i32 {
    var acc: i32 = 0;
    var i: i32 = 0;
    while (i < 4) {
        var s: S = S { a: i, b: i + 1 };
        acc = acc + s.a + s.b;
        i = i + 1;
    }
    return acc + r;
}
function main(): i32 {
    var x: i32 = 0;
    var r: i32 = 0;
    while (r < 100) { x = x + round(r); r = r + 1; }
    return x % 83;
}`
		allocs, frees, live := counts(t, "bss_scalar_in_loop", src, 76)
		if live != 0 {
			t.Errorf("live_bytes=%d, want 0 — a scalar-only struct box is fully released "+
				"by the box dec alone", live)
		}
		if allocs != frees {
			t.Errorf("allocs=%d frees=%d — must balance exactly", allocs, frees)
		}
	})
}

// TestSelfHostBlockScopedStructBoxHazardsX86_64 — the block-scoped shapes the box
// free must still REFUSE, because the box is not the sole owner. Each keeps a live
// reference past the block and reads it after, so a wrongly-granted free is a
// use-after-free rather than a leak.
//
// Free counts are exact and pinned at what this build produces; `__rc_underflow_count()`
// is asserted separately, because the box free legitimately moves some counts and
// only the counter distinguishes a new safe release from one landing on a live box.
//
// Every `want` is from `fern -interp`.
func TestSelfHostBlockScopedStructBoxHazardsX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_ir_run.fern", "driver")

	for _, tc := range []struct {
		name      string
		body      string
		want      int
		wantFrees int64
	}{
		{
			// Each block-scoped box is moved into a container read after the loop.
			name: "boxes_escape_into_an_outer_container",
			body: `struct S { xs: i32[], n: i32 }
function round(r: i32): i32 {
    var keep: S[] = [];
    var i: i32 = 0;
    while (i < 4) {
        var s: S = S { xs: [i, i + 1], n: i };
        keep = keep.append(s);
        i = i + 1;
    }
    var acc: i32 = 0;
    var j: i32 = 0;
    while (j < keep.len()) { acc = acc + keep[j].xs[0] + keep[j].n; j = j + 1; }
    return acc + r;
}`,
			want:      8,
			wantFrees: 200,
		},
		{
			// Aliased to a local that outlives the block and is read after it.
			name: "aliased_to_an_outer_local",
			body: `struct S { xs: i32[], n: i32 }
function round(r: i32): i32 {
    var held: S = S { xs: [0], n: 0 };
    var acc: i32 = 0;
    var i: i32 = 0;
    while (i < 4) {
        var s: S = S { xs: [i, i + 1], n: i };
        held = s;
        acc = acc + s.n;
        i = i + 1;
    }
    return acc + held.xs[1] + r;
}`,
			want:      57,
			wantFrees: 0,
		},
		{
			// Returned out of the block to the caller.
			name: "returned_from_inside_the_block",
			body: `struct S { xs: i32[], n: i32 }
function build(r: i32): S {
    var i: i32 = 0;
    while (i < 4) {
        var s: S = S { xs: [i, i + 1], n: i + r };
        if (i == 3) { return s; }
        i = i + 1;
    }
    return S { xs: [0], n: 0 };
}
function round(r: i32): i32 {
    var g: S = build(r);
    return g.n + g.xs[1];
}`,
			want:      6,
			wantFrees: 0,
		},
		{
			// Passed to a callee that keeps it.
			name: "passed_to_a_callee_that_keeps_it",
			body: `struct S { xs: i32[], n: i32 }
function keepit(s: S): S { return s; }
function round(r: i32): i32 {
    var held: S = S { xs: [0], n: 0 };
    var acc: i32 = 0;
    var i: i32 = 0;
    while (i < 4) {
        var s: S = S { xs: [i, i + 1], n: i };
        held = keepit(s);
        acc = acc + s.n;
        i = i + 1;
    }
    return acc + held.xs[1] + r;
}`,
			want:      57,
			wantFrees: 1000,
		},
		{
			// The FIELD extracted out of the block. The box free is still granted —
			// it does not touch the field — so this one checks that the withheld deep
			// drop really is withheld.
			name: "field_extracted_out_of_the_block",
			body: `struct S { xs: i32[], n: i32 }
function round(r: i32): i32 {
    var held: i32[] = [];
    var acc: i32 = 0;
    var i: i32 = 0;
    while (i < 4) {
        var s: S = S { xs: [i, i + 1], n: i };
        held = s.xs;
        acc = acc + s.n;
        i = i + 1;
    }
    return acc + held[1] + r;
}`,
			want:      57,
			wantFrees: 500,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tail := `
function main(): i32 {
    var x: i32 = 0;
    var r: i32 = 0;
    while (r < 100) { x = x + round(r); r = r + 1; }
    return x % 83;
}`
			asm := hevCompile(t, runner, driverBin, tc.body+tail, []string{"FERN_LEAKCHECK=1"})
			progBin := buildBin(t, gcc, dir, "bssh_"+tc.name, asm)
			stderr, exit := hevRun(t, runner, progBin)
			if exit != tc.want {
				t.Fatalf("exited %d, want %d — a wrong answer or a crash here means a "+
					"block-scoped box was freed at function exit while something else still "+
					"held it (use-after-free), not merely that it leaked", exit, tc.want)
			}
			summary := ""
			for _, line := range strings.Split(stderr, "\n") {
				if strings.HasPrefix(line, "leakcheck: ") {
					summary = line
				}
			}
			var allocs, frees, live int64
			if _, err := fmtSscan(summary, &allocs, &frees, &live); err != nil {
				t.Fatalf("parse %q: %v", summary, err)
			}
			if frees != tc.wantFrees {
				t.Errorf("frees=%d, want exactly %d (allocs=%d live=%d) — a HIGHER count is "+
					"a value released under a live reference; a lower one means this probe "+
					"stopped exercising the path it was written for", frees, tc.wantFrees, allocs, live)
			}

			ufAsm := hevCompile(t, runner, driverBin, tc.body+`
function main(): i32 {
    var x: i32 = 0;
    var r: i32 = 0;
    while (r < 100) { x = x + round(r); r = r + 1; }
    if (x == 999999) { return 90; }
    return __rc_underflow_count();
}`, nil)
			ufBin := buildBin(t, gcc, dir, "bsshu_"+tc.name, ufAsm)
			_, ufExit := hevRun(t, runner, ufBin)
			if ufExit != 0 {
				t.Errorf("__rc_underflow_count() == %d, want 0 — a box released twice", ufExit)
			}
		})
	}
}
