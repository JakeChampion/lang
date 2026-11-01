package e2ecompiler

import (
	"strings"
	"testing"
)

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

	t.Run("struct_declared_in_a_loop", func(t *testing.T) {
		src := `struct S { xs: i32[], n: i32 }
@noinline function four(): i32 { return 4; }
function round(r: i32): i32 {
    let acc: i32 = 0;
    let i: i32 = 0;
    while (i < four()) {
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

func TestSelfHostBlockScopedStructBoxHazardsX86_64(t *testing.T) {
	boxedProbes(t)
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "drivers/asm_ir_run.fern", "driver")

	const tail = `
function main(): i32 {
    let x: i32 = 0;
    let r: i32 = 0;
    while (r < 100) { x = x + round(r); r = r + 1; }
    return x % 83;
}`

	check := func(t *testing.T, name, body string, want int, wantFrees int64) {
		t.Helper()
		asm := hevCompile(t, runner, driverBin, body+tail, []string{"FERN_LEAKCHECK=1"})
		progBin := buildBin(t, gcc, dir, "bssh_"+name, asm)
		stderr, exit := hevRun(t, runner, progBin)
		if exit != want {
			t.Fatalf("exited %d, want %d — a wrong answer or a crash here means a "+
				"block-scoped box was freed at function exit while something else still "+
				"held it (use-after-free), not merely that it leaked", exit, want)
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
		assertBalancedCensus(t, stderr)
		if frees != wantFrees {
			t.Errorf("frees=%d, want exactly %d (allocs=%d live=%d)", frees, wantFrees, allocs, live)
		}

		ufAsm := hevCompile(t, runner, driverBin, body+`
function main(): i32 {
    let x: i32 = 0;
    let r: i32 = 0;
    while (r < 100) { x = x + round(r); r = r + 1; }
    if (x == 999999) { return 90; }
    return __rc_underflow_count();
}`, nil)
		ufBin := buildBin(t, gcc, dir, "bsshu_"+name, ufAsm)
		_, ufExit := hevRun(t, runner, ufBin)
		if ufExit != 0 {
			t.Errorf("__rc_underflow_count() == %d, want 0 — a box released twice", ufExit)
		}
		safeAsm := hevCompile(t, runner, driverBin, body+tail, []string{"FERN_SANITIZE=1", "FERN_LEAKCHECK=1"})
		safeBin := buildBin(t, gcc, dir, "bsshs_"+name, safeAsm)
		safeStderr, safeExit := hevRun(t, runner, safeBin)
		if safeExit != want || forArrStructSanitizerFault(safeStderr, true) {
			t.Fatalf("sanitize: exit=%d, want %d\n%s", safeExit, want, safeStderr)
		}
		assertBalancedCensus(t, safeStderr)

	}

	for _, tc := range []struct {
		name              string
		body              string
		want              int
		wantFrees         int64
		wantSnapshotFrees int64
	}{
		{
			name: "boxes_escape_into_an_outer_container",
			body: `struct S { xs: i32[], n: i32 }
@noinline function four(): i32 { return 4; }
function round(r: i32): i32 {
    let keep: S[] = [];
    let i: i32 = 0;
    while (i < four()) {
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
			name: "aliased_to_an_outer_local",
			body: `struct S { xs: i32[], n: i32 }
@noinline function four(): i32 { return 4; }
function round(r: i32): i32 {
    let held: S = S { xs: [0], n: r };
    let acc: i32 = 0;
    let i: i32 = 0;
    while (i < four()) {
        let s: S = S { xs: [i, i + 1], n: i };
        held = s;
        acc = acc + s.n;
        i = i + 1;
    }
    return acc + held.xs[1] + r;
}`,
			want:              57,
			wantFrees:         500,
			wantSnapshotFrees: 900,
		},
		{
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
			name: "passed_to_a_callee_that_keeps_it",
			body: `struct S { xs: i32[], n: i32 }
@noinline function four(): i32 { return 4; }
@noinline function keepit(s: S): S { return s; }
function round(r: i32): i32 {
    let held: S = S { xs: [0], n: r };
    let acc: i32 = 0;
    let i: i32 = 0;
    while (i < four()) {
        let s: S = S { xs: [i, i + 1], n: i };
        held = keepit(s);
        acc = acc + s.n;
        i = i + 1;
    }
    return acc + held.xs[1] + r;
}`,
			want:              57,
			wantFrees:         500,
			wantSnapshotFrees: 900,
		},
		{
			name: "field_extracted_out_of_the_block",
			body: `struct S { xs: i32[], n: i32 }
@noinline function four(): i32 { return 4; }
function round(r: i32): i32 {
    let held: i32[] = [];
    let acc: i32 = 0;
    let i: i32 = 0;
    while (i < four()) {
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
			check(t, tc.name, tc.body, tc.want, tc.wantFrees)
		})
		if tc.wantSnapshotFrees == 0 {
			continue
		}
		t.Run(tc.name+"_prior_snapshot", func(t *testing.T) {
			const snapshot = `@noinline
function snapshot(s: S, n: i32, first: i32, last: i32, size: i32): boolean {
    return s.n == n && s.xs.len() == size && s.xs[0] == first && s.xs[size - 1] == last;
}
`
			const checks = `        if (i == 0) {
            if (!snapshot(prior, r, 0, 0, 1)) { exit(91); }
        } else {
            if (!snapshot(prior, i - 1, i - 1, i, 2)) { exit(92); }
        }
`
			if strings.Count(tc.body, "        let s: S =") != 1 || strings.Count(tc.body, "        acc = acc + s.n;") != 1 {
				t.Fatal("the snapshot control needs one construction and one later read")
			}
			body := strings.Replace(tc.body, "        let s: S =", "        let prior: S = held;\n        let s: S =", 1)
			body = snapshot + strings.Replace(body, "        acc = acc + s.n;", checks+"        acc = acc + s.n;", 1)
			check(t, tc.name+"_snapshot", body, tc.want, tc.wantSnapshotFrees)
		})
	}
}
