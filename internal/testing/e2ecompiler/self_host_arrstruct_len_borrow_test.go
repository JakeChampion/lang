package e2ecompiler

import (
	"strings"
	"testing"
)

func TestSelfHostArrStructLenBorrowX86_64(t *testing.T) {
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

	for _, tc := range []struct {
		name string
		src  string
		want int
	}{
		{
			name: "arrstruct_len_is_the_only_use",
			src: `struct P { xs: i32[] }
function round(r: i32): i32 {
    let ps: P[] = [P { xs: [r, r + 1] }];
    return ps.len();
}
function main(): i32 {
    let x: i32 = 0;
    let r: i32 = 0;
    while (r < 100) { x = x + round(r); r = r + 1; }
    return x % 83;
}`,
			want: 17,
		},
		{
			name: "arrstruct_len_alongside_an_element_read",
			src: `struct P { xs: i32[] }
function round(r: i32): i32 {
    let ps: P[] = [P { xs: [r, r + 1] }];
    return ps.len() + ps[0].xs[0];
}
function main(): i32 {
    let x: i32 = 0;
    let r: i32 = 0;
    while (r < 100) { x = x + round(r); r = r + 1; }
    return x % 83;
}`,
			want: 70,
		},
		{
			name: "arrstruct_len_bound_to_a_local",
			src: `struct P { xs: i32[] }
function round(r: i32): i32 {
    let ps: P[] = [P { xs: [r, r + 1] }];
    let n: i32 = ps.len();
    return n;
}
function main(): i32 {
    let x: i32 = 0;
    let r: i32 = 0;
    while (r < 100) { x = x + round(r); r = r + 1; }
    return x % 83;
}`,
			want: 17,
		},
		{
			name: "arrtup_len_is_the_only_use",
			src: `function round(r: i32): i32 {
    let ts: (i32, i32[])[] = [(r, [r, r + 1])];
    return ts.len();
}
function main(): i32 {
    let x: i32 = 0;
    let r: i32 = 0;
    while (r < 100) { x = x + round(r); r = r + 1; }
    return x % 83;
}`,
			want: 17,
		},
		{
			name: "arrstruct_len_declared_in_a_loop",
			src: `struct P { xs: i32[], n: i32 }
function round(r: i32): i32 {
    let acc: i32 = 0;
    let i: i32 = 0;
    while (i < 4) {
        let ps: P[] = [P { xs: [i, i + 1], n: i }];
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
			want: 38,
		},
		{
			name: "arrtup_len_declared_in_a_loop",
			src: `function round(r: i32): i32 {
    let acc: i32 = 0;
    let i: i32 = 0;
    while (i < 4) {
        let ts: (i32, i32[])[] = [(i, [i, i + 1])];
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
			want: 38,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			allocs, frees, live := counts(t, tc.name, tc.src, tc.want)
			if live != 0 {
				t.Errorf("live_bytes=%d, want 0 — the element boxes and their array "+
					"fields leak once per round, so this scales with the loop count", live)
			}
			if allocs != frees {
				t.Errorf("allocs=%d frees=%d — must balance exactly", allocs, frees)
			}
		})
	}
}

func TestSelfHostArrStructLenBorrowHazardsX86_64(t *testing.T) {
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
			name: "len_plus_alias_to_an_outer_local",
			src: `struct P { xs: i32[] }
function round(r: i32): i32 {
    let keep: P[] = [];
    let ps: P[] = [P { xs: [r, r + 1] }];
    keep = ps;
    return ps.len() + keep[0].xs[0];
}
function main(): i32 {
    let x: i32 = 0;
    let r: i32 = 0;
    while (r < 100) { x = x + round(r); r = r + 1; }
    return x % 83;
}`,
			want: 70,
		},
		{
			name: "len_plus_return_to_the_caller",
			src: `struct P { xs: i32[] }
function build(r: i32): P[] {
    let ps: P[] = [P { xs: [r, r + 1] }];
    if (ps.len() > 0) { return ps; }
    return [];
}
function round(r: i32): i32 {
    let got: P[] = build(r);
    if (got.len() == 0) { return 0; }
    return got[0].xs[0];
}
function main(): i32 {
    let x: i32 = 0;
    let r: i32 = 0;
    while (r < 100) { x = x + round(r); r = r + 1; }
    return x % 83;
}`,
			want: 53,
		},
		{
			name: "len_plus_element_field_extracted",
			src: `struct P { xs: i32[] }
function round(r: i32): i32 {
    let held: i32[] = [];
    let ps: P[] = [P { xs: [r, r + 1] }];
    held = ps[0].xs;
    return ps.len() + held[0];
}
function main(): i32 {
    let x: i32 = 0;
    let r: i32 = 0;
    while (r < 100) { x = x + round(r); r = r + 1; }
    return x % 83;
}`,
			want: 70,
		},
		{
			name: "len_plus_call_arg_the_callee_keeps",
			src: `struct P { xs: i32[] }
@noinline function keepit(ps: P[]): P[] { return ps; }
function round(r: i32): i32 {
    let ps: P[] = [P { xs: [r, r + 1] }];
    let held: P[] = keepit(ps);
    return ps.len() + held[0].xs[0];
}
function main(): i32 {
    let x: i32 = 0;
    let r: i32 = 0;
    while (r < 100) { x = x + round(r); r = r + 1; }
    return x % 83;
}`,
			want: 70,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			asm := hevCompile(t, runner, driverBin, tc.src, nil)
			progBin := buildBin(t, gcc, dir, "aslen_hazard_"+tc.name, asm)
			_, exit := hevRun(t, runner, progBin)
			if exit != tc.want {
				t.Errorf("exited %d, want %d — a wrong answer or a crash here means the "+
					"`.len()` borrow laundered a genuine escape and the reclaim freed a "+
					"value something else still holds", exit, tc.want)
			}
		})
	}
}
