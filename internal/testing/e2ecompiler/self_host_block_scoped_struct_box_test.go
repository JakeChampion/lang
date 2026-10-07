package e2ecompiler

import (
	"strings"
	"testing"
)

// --- A struct declared inside a block (#6127) ---------------------------------
//
// A struct local declared inside a `while` body is released when its block
// ends — the box and its rc fields — so `S { xs: i32[], n: i32 }` over 100
// rounds reaches live_bytes 0.
//
// The field drop is the hazardous half. When a block-scoped struct's FIELD is
// moved out — `let lo: StringLitOut = add_string_lit(s, ..); s = lo.state;` —
// the field must not be freed under its new owner: a read of a nested-struct /
// array / string field is a move, not a borrow. Getting that wrong segfaulted
// the gen1 self-compile while every differential probe program still agreed
// with the oracle, which is why the fixpoint runs FIRST on a reclaim change and
// not last.

func TestSelfHostBlockScopedStructBoxX86_64(t *testing.T) {
	boxedProbes(t)
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "drivers/asm_ir_run.fern", "driver")

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

	// The box and its `xs` buffer are both reclaimed.
	t.Run("struct_declared_in_a_loop", func(t *testing.T) {
		src := `struct S { xs: i32[], n: i32 }
function round(r: i32): i32 {
    let acc: i32 = 0;
    let i: i32 = 0;
    while (i < 4) {
        let s: S = S { xs: [i, i + 1], n: i };
        acc = acc + s.n + s.xs.len();
        i = i + 1;
    }
    return acc + r;
}
function main(): i32 {
    let x: i32 = 0;
    let r: i32 = 0;
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
    let acc: i32 = 0;
    let i: i32 = 0;
    while (i < 4) {
        let s: S = S { a: i, b: i + 1 };
        acc = acc + s.a + s.b;
        i = i + 1;
    }
    return acc + r;
}
function main(): i32 {
    let x: i32 = 0;
    let r: i32 = 0;
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

// TestSelfHostBlockScopedStructBoxHazardsX86_64 — block-scoped shapes whose box
// is not the sole owner. Each keeps a live reference past the block and reads it
// after, so a wrongly-granted free is a use-after-free rather than a leak.
//
// Free counts are exact and pinned at what this build produces (every row
// reclaims all it allocates); `__rc_underflow_count()` is asserted separately,
// because only the counter tells a safe release from one landing on a live box.
// An initial `held` takes the round number, so it is a heap box rather than a
// static one.
//
// Every `want` is from `fern -interp`.
func TestSelfHostBlockScopedStructBoxHazardsX86_64(t *testing.T) {
	boxedProbes(t)
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "drivers/asm_ir_run.fern", "driver")

	for _, tc := range []struct {
		name      string
		body      string
		want      int
		wantFrees int64
	}{
		{
			// Each block-scoped box is moved into a container read after
			// the loop. The exit code (a read of every element after the
			// loop) says the release lands after the last live use rather
			// than under it.
			name: "boxes_escape_into_an_outer_container",
			body: `struct S { xs: i32[], n: i32 }
function round(r: i32): i32 {
    let keep: S[] = [];
    let i: i32 = 0;
    while (i < 4) {
        let s: S = S { xs: [i, i + 1], n: i };
        keep = keep.append(s);
        i = i + 1;
    }
    let acc: i32 = 0;
    let j: i32 = 0;
    while (j < keep.len()) { acc = acc + keep[j].xs[0] + keep[j].n; j = j + 1; }
    return acc + r;
}`,
			want:      8,
			wantFrees: 900,
		},
		{
			// Aliased to a local that outlives the block and is read after
			// it. The typed lowering reclaims every block, allocs == frees;
			// the answer (57) is what says no release landed under `held`.
			name: "aliased_to_an_outer_local",
			body: `struct S { xs: i32[], n: i32 }
function round(r: i32): i32 {
    let held: S = S { xs: [0], n: r };
    let acc: i32 = 0;
    let i: i32 = 0;
    while (i < 4) {
        let s: S = S { xs: [i, i + 1], n: i };
        held = s;
        acc = acc + s.n;
        i = i + 1;
    }
    return acc + held.xs[1] + r;
}`,
			want:      57,
			wantFrees: 900,
		},
		{
			// Returned out of the block to the caller. Every block is
			// reclaimed, allocs == frees, and the answer agrees with
			// interp.
			name: "returned_from_inside_the_block",
			body: `struct S { xs: i32[], n: i32 }
function build(r: i32): S {
    let i: i32 = 0;
    while (i < 4) {
        let s: S = S { xs: [i, i + 1], n: i + r };
        if (i == 3) { return s; }
        i = i + 1;
    }
    return S { xs: [0], n: 0 };
}
function round(r: i32): i32 {
    let g: S = build(r);
    return g.n + g.xs[1];
}`,
			want:      6,
			wantFrees: 800,
		},
		{
			// Passed to a callee that keeps it.
			name: "passed_to_a_callee_that_keeps_it",
			body: `struct S { xs: i32[], n: i32 }
@noinline function keepit(s: S): S { return s; }
function round(r: i32): i32 {
    let held: S = S { xs: [0], n: r };
    let acc: i32 = 0;
    let i: i32 = 0;
    while (i < 4) {
        let s: S = S { xs: [i, i + 1], n: i };
        held = keepit(s);
        acc = acc + s.n;
        i = i + 1;
    }
    return acc + held.xs[1] + r;
}`,
			want:      57,
			wantFrees: 900,
		},
		{
			// The FIELD extracted out of the block and read after the loop;
			// every block is reclaimed.
			name: "field_extracted_out_of_the_block",
			body: `struct S { xs: i32[], n: i32 }
function round(r: i32): i32 {
    let held: i32[] = [];
    let acc: i32 = 0;
    let i: i32 = 0;
    while (i < 4) {
        let s: S = S { xs: [i, i + 1], n: i };
        held = s.xs;
        acc = acc + s.n;
        i = i + 1;
    }
    return acc + held[1] + r;
}`,
			want:      57,
			wantFrees: 800,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tail := `
function main(): i32 {
    let x: i32 = 0;
    let r: i32 = 0;
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
    let x: i32 = 0;
    let r: i32 = 0;
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
