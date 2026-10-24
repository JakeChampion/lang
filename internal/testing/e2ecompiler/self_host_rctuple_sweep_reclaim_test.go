package e2ecompiler

import (
	"strings"
	"testing"
)

func TestSelfHostRcTupleSweepReclaimX86_64(t *testing.T) {
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

	for _, tc := range []struct {
		name string
		src  string
		want int
	}{
		{
			name: "array_elem_single_bind",
			src: `function round(i: i32): i32 {
    let t: (i32, i32[]) = (i, [i, i + 1]);
    return t.0 + t.1.len();
}
function main(): i32 {
    let x: i32 = 0;
    let r: i32 = 0;
    while (r < 100) { x = x + round(r); r = r + 1; }
    return x % 83;
}`,
			want: 4,
		},
		{
			name: "string_elem_single_bind",
			src: `function round(i: i32): i32 {
    let t: (i32, string) = (i, "abc");
    return t.0 + t.1.len();
}
function main(): i32 {
    let x: i32 = 0;
    let r: i32 = 0;
    while (r < 100) { x = x + round(r); r = r + 1; }
    return x % 83;
}`,
			want: 21,
		},
		{
			name: "rc_elem_never_read",
			src: `function round(i: i32): i32 {
    let t: (i32, string) = (i, "abc");
    return t.0;
}
function main(): i32 {
    let x: i32 = 0;
    let r: i32 = 0;
    while (r < 100) { x = x + round(r); r = r + 1; }
    return x % 83;
}`,
			want: 53,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			allocs, frees, live := counts(t, "rctupsweep_"+tc.name, tc.src, tc.want)
			if live != 0 {
				t.Errorf("live_bytes=%d, want 0 — the final rc-tuple value's children and "+
					"box must both be freed at scope exit; this leaks on every call, so "+
					"it is unbounded", live)
			}
			if allocs != frees {
				t.Errorf("allocs=%d frees=%d — must balance exactly", allocs, frees)
			}
		})
	}
}

func TestSelfHostRcTupleSweepHazardsX86_64(t *testing.T) {
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
			name: "ident_elem_aliases_live_local",
			src: `function round(i: i32): i32 {
    let xs: i32[] = [i, i + 1];
    let t: (i32[], i32[]) = (xs, [i, i + 2]);
    return t.0.len() + xs[0] + xs[1];
}
function main(): i32 {
    let x: i32 = 0;
    let r: i32 = 0;
    while (r < 60) { x = x + round(r); r = r + 1; }
    return x % 71;
}`,
			want: 28,
		},
		{
			name: "rc_elem_extracted_to_local",
			src: `function round(i: i32): i32 {
    let t: (i32, i32[]) = (i, [i, i + 1]);
    let keep: i32[] = t.1;
    return keep.len() + keep[0];
}
function main(): i32 {
    let x: i32 = 0;
    let r: i32 = 0;
    while (r < 60) { x = x + round(r); r = r + 1; }
    return x % 71;
}`,
			want: 44,
		},
		{
			name: "escaping_return",
			src: `function mk(i: i32): (i32, i32[]) { let t: (i32, i32[]) = (i, [i, i + 1]); return t; }
function round(i: i32): i32 { let p: (i32, i32[]) = mk(i); return p.0 + p.1.len(); }
function main(): i32 {
    let x: i32 = 0;
    let r: i32 = 0;
    while (r < 60) { x = x + round(r); r = r + 1; }
    return x % 71;
}`,
			want: 44,
		},
		{
			name: "rc_elem_escapes_into_call",
			src: `function take(a: i32[]): i32 { return a.len(); }
function round(i: i32): i32 {
    let t: (i32, i32[]) = (i, [i, i + 1]);
    return t.0 + take(t.1);
}
function main(): i32 {
    let x: i32 = 0;
    let r: i32 = 0;
    while (r < 60) { x = x + round(r); r = r + 1; }
    return x % 71;
}`,
			want: 44,
		},
		{
			name: "reassigned",
			src: `function round(i: i32): i32 {
    let t: (i32, string) = (i, "abc");
    let k: i32 = 0;
    while (k < 4) { t = (k, "xy"); k = k + 1; }
    return t.0 + t.1.len();
}
function main(): i32 {
    let x: i32 = 0;
    let r: i32 = 0;
    while (r < 100) { x = x + round(r); r = r + 1; }
    return x % 83;
}`,
			want: 2,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			asm := hevCompile(t, runner, driverBin, tc.src, []string{"FERN_LEAKCHECK=1"})
			progBin := buildBin(t, gcc, dir, "rctupsweep_hazard_"+tc.name, asm)
			stderr, exit := hevRun(t, runner, progBin)
			if exit != tc.want {
				t.Fatalf("exited %d, want %d — a wrong answer or a crash here means the "+
					"sweep freed memory something else still owns (use-after-free), not "+
					"merely that the shape leaked", exit, tc.want)
			}
			allocs, frees, live := parseLeakcheck(t, tc.name, stderr)
			if live != 0 || allocs != frees {
				t.Errorf("allocs=%d frees=%d live_bytes=%d, want a balanced census", allocs, frees, live)
			}
		})
	}
}
