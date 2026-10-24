package e2ecompiler

import (
	"strings"
	"testing"
)

func TestSelfHostBlockScopedClassesX86_64(t *testing.T) {
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
		if allocs == 0 {
			t.Fatalf("%s allocated nothing — the probe is not exercising the path", name)
		}
		return allocs, frees, live
	}

	balanced := func(t *testing.T, name, src string, wantExit int) {
		t.Helper()
		allocs, frees, live := counts(t, name, src, wantExit)
		if live != 0 {
			t.Errorf("%s: live_bytes=%d, want 0 — one unfreed value per round, so this "+
				"scales with the loop count", name, live)
		}
		if allocs != frees {
			t.Errorf("%s: allocs=%d frees=%d — must balance exactly", name, allocs, frees)
		}
	}

	for _, tc := range []struct {
		name string
		src  string
		want int
	}{
		{
			name: "arrarr_declared_in_loop",
			src: `function round(r: i32): i32 {
    let acc: i32 = 0;
    let i: i32 = 0;
    while (i < 4) {
        let g: i32[][] = [[i, i + 1], [i + 2]];
        acc = acc + g.len() + g[0][0];
        i = i + 1;
    }
    return acc + r;
}
function main(): i32 {
    let x: i32 = 0;
    let r: i32 = 0;
    while (r < 100) { x = x + round(r); r = r + 1; }
    return x % 83;
}`,
			want: 42,
		},
		{
			name: "strarr_declared_in_loop",
			src: `@noinline function ids(s: string): string { return s; }
function round(r: i32): i32 {
    let acc: i32 = 0;
    let i: i32 = 0;
    while (i < 4) {
        let xs: string[] = [ids("alpha"), "beta"];
        acc = acc + xs.len();
        i = i + 1;
    }
    return acc + r;
}
function main(): i32 {
    let x: i32 = 0;
    let r: i32 = 0;
    while (r < 100) { x = x + round(r); r = r + 1; }
    return x % 83;
}`,
			want: 23,
		},
		{
			name: "scalar_field_struct_array_declared_in_loop",
			src: `struct S { a: i32, b: i32 }
function round(r: i32): i32 {
    let acc: i32 = 0;
    let i: i32 = 0;
    while (i < 4) {
        let xs: S[] = [S { a: i, b: 1 }, S { a: i, b: 2 }];
        acc = acc + xs.len();
        i = i + 1;
    }
    return acc + r;
}
function main(): i32 {
    let x: i32 = 0;
    let r: i32 = 0;
    while (r < 100) { x = x + round(r); r = r + 1; }
    return x % 83;
}`,
			want: 23,
		},
		{
			name: "optaarr_declared_in_loop",
			src: `function round(r: i32): i32 {
    let acc: i32 = 0;
    let i: i32 = 0;
    while (i < 4) {
        let xs: Option[i32[]][] = [Some([i, i + 1]), None];
        acc = acc + xs.len();
        i = i + 1;
    }
    return acc + r;
}
function main(): i32 {
    let x: i32 = 0;
    let r: i32 = 0;
    while (r < 100) { x = x + round(r); r = r + 1; }
    return x % 83;
}`,
			want: 23,
		},
		{
			name: "rc_tuple_declared_in_loop",
			src: `function round(r: i32): i32 {
    let acc: i32 = 0;
    let i: i32 = 0;
    while (i < 4) {
        let t: (i32, i32[]) = (i, [i, i + 1]);
        acc = acc + t.0 + t.1.len();
        i = i + 1;
    }
    return acc + r;
}
function main(): i32 {
    let x: i32 = 0;
    let r: i32 = 0;
    while (r < 100) { x = x + round(r); r = r + 1; }
    return x % 83;
}`,
			want: 42,
		},
		{
			name: "rc_payload_enum_in_loop_stays_balanced",
			src: `enum E { Full(i32[]), Nil }
function round(r: i32): i32 {
    let acc: i32 = 0;
    let i: i32 = 0;
    while (i < 4) {
        let b: E = E.Full([i, i + 1]);
        match (b) { E.Full(xs) => { acc = acc + xs.len(); }, E.Nil => {} }
        i = i + 1;
    }
    return acc + r;
}
function main(): i32 {
    let x: i32 = 0;
    let r: i32 = 0;
    while (r < 100) { x = x + round(r); r = r + 1; }
    return x % 83;
}`,
			want: 23,
		},
		{
			name: "flat_option_in_loop_stays_balanced",
			src: `function round(r: i32): i32 {
    let acc: i32 = 0;
    let i: i32 = 0;
    while (i < 4) {
        let o: Option[i32[]] = Some([i, i + 1]);
        match (o) { Some(a) => { acc = acc + a.len() + a[0]; }, None => {} }
        i = i + 1;
    }
    return acc + r;
}
function main(): i32 {
    let x: i32 = 0;
    let r: i32 = 0;
    while (r < 100) { x = x + round(r); r = r + 1; }
    return x % 83;
}`,
			want: 42,
		},
		{
			name: "arrstruct_len_declared_in_loop",
			src: `struct P { xs: i32[], n: i32 }
function round(r: i32): i32 {
    let acc: i32 = 0;
    let i: i32 = 0;
    while (i < 4) {
        let ps: P[] = [P { xs: [i, i + 1], n: i }, P { xs: [i + 2], n: 1 }];
        acc = acc + ps.len();
        i = i + 1;
    }
    return acc + r;
}
function main(): i32 {
    let x: i32 = 0;
    let r: i32 = 0;
    while (r < 100) { x = x + round(r); r = r + 1; }
    return x % 83;
}`,
			want: 23,
		},
		{
			name: "arrtup_len_declared_in_loop",
			src: `function round(r: i32): i32 {
    let acc: i32 = 0;
    let i: i32 = 0;
    while (i < 4) {
        let ts: (i32[], i32)[] = [([i, i + 1], i), ([i + 2], 1)];
        acc = acc + ts.len();
        i = i + 1;
    }
    return acc + r;
}
function main(): i32 {
    let x: i32 = 0;
    let r: i32 = 0;
    while (r < 100) { x = x + round(r); r = r + 1; }
    return x % 83;
}`,
			want: 23,
		},
	} {
		t.Run(tc.name, func(t *testing.T) { balanced(t, tc.name, tc.src, tc.want) })
	}
}

func TestSelfHostBlockScopedClassesHazardsX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "drivers/asm_ir_run.fern", "driver")

	for _, tc := range []struct {
		name string
		src  string
		want int
	}{
		{
			name: "arr_escapes_into_an_outer_container",
			src: `function round(r: i32): i32 {
    let keep: i32[][] = [];
    let i: i32 = 0;
    while (i < 4) {
        let g: i32[] = [i, i + 1];
        keep = keep.append(g);
        i = i + 1;
    }
    let acc: i32 = 0;
    let j: i32 = 0;
    while (j < keep.len()) { acc = acc + keep[j][0]; j = j + 1; }
    return acc + r;
}
function main(): i32 {
    let x: i32 = 0;
    let r: i32 = 0;
    while (r < 100) { x = x + round(r); r = r + 1; }
    return x % 83;
}`,
			want: 72,
		},
		{
			name: "strarr_aliased_and_passed_to_a_call",
			src: `@noinline function ids(s: string): string { return s; }
function keepit(xs: string[]): i32 { return xs.len(); }
function round(r: i32): i32 {
    let acc: i32 = 0;
    let held: string[] = [];
    let i: i32 = 0;
    while (i < 4) {
        let xs: string[] = [ids("alpha"), "beta"];
        held = xs;
        acc = acc + keepit(xs);
        i = i + 1;
    }
    return acc + held.len() + r;
}
function main(): i32 {
    let x: i32 = 0;
    let r: i32 = 0;
    while (r < 100) { x = x + round(r); r = r + 1; }
    return x % 83;
}`,
			want: 57,
		},
		{
			name: "optaarr_aliased_to_an_outer_local",
			src: `function round(r: i32): i32 {
    let acc: i32 = 0;
    let held: Option[i32[]][] = [];
    let i: i32 = 0;
    while (i < 4) {
        let xs: Option[i32[]][] = [Some([i, i + 1]), None];
        held = xs;
        acc = acc + xs.len();
        i = i + 1;
    }
    return acc + held.len() + r;
}
function main(): i32 {
    let x: i32 = 0;
    let r: i32 = 0;
    while (r < 100) { x = x + round(r); r = r + 1; }
    return x % 83;
}`,
			want: 57,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			asm := hevCompile(t, runner, driverBin, tc.src, nil)
			progBin := buildBin(t, gcc, dir, "blc_hazard_"+tc.name, asm)
			_, exit := hevRun(t, runner, progBin)
			if exit != tc.want {
				t.Errorf("exited %d, want %d — a wrong answer or a crash here means a "+
					"retired slot was freed at function exit while something else still "+
					"held its value (use-after-free), not merely that it leaked",
					exit, tc.want)
			}
		})
	}
}
