package e2ecompiler

import (
	"strings"
	"testing"
)

const structArrAppendChurnSrc = `struct P { x: i32, y: i32 }

function round(): i32 {
    let ps: P[] = [];
    let i: i32 = 0;
    while (i < 8) { ps = ps.append(P { x: i, y: i }); i = i + 1; }
    return ps.len();
}

function main(): i32 {
    let t: i32 = 0;
    let r: i32 = 0;
    while (r < 100) { t = t + round(); r = r + 1; }
    return t / 100;
}`

func TestSelfHostStructArrAppendReclaimX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "drivers/asm_ir_run.fern", "driver")

	asm := hevCompile(t, runner, driverBin, structArrAppendChurnSrc, []string{"FERN_LEAKCHECK=1"})
	progBin := buildBin(t, gcc, dir, "structarr_append", asm)
	stderr, exit := hevRun(t, runner, progBin)
	if exit != 8 {
		t.Fatalf("program exited %d, want 8", exit)
	}

	summary := ""
	for _, line := range strings.Split(stderr, "\n") {
		if strings.HasPrefix(line, "leakcheck: ") {
			summary = line
		}
	}
	if summary == "" {
		t.Fatal("no leakcheck summary")
	}
	var allocs, frees, live int64
	if _, err := fmtSscan(summary, &allocs, &frees, &live); err != nil {
		t.Fatalf("parse %q: %v", summary, err)
	}
	if allocs == 0 {
		t.Fatal("program allocated nothing — the probe is not exercising the path")
	}
	if live != 0 {
		t.Errorf("%s: live_bytes=%d, want 0 — an append-built struct array must reclaim "+
			"its element boxes; the leak scales with the iteration count, so any "+
			"nonzero here is unbounded in a loop", summary, live)
	}
}

const structArrAppendStrFieldSrc = `struct N { s: string, n: i32 }

function round(i: i32): i32 {
    let xs: N[] = [];
    let k: i32 = 0;
    while (k < 4) { xs = xs.append(N { s: "ab", n: i }); k = k + 1; }
    return xs.len();
}

function main(): i32 {
    let t: i32 = 0;
    let r: i32 = 0;
    while (r < 100) { t = t + round(r); r = r + 1; }
    return t / 100;
}`

const structArrLiteralStrFieldSrc = `struct N { s: string, n: i32 }

function round(i: i32): i32 {
    let xs: N[] = [N { s: "ab", n: i }, N { s: "cd", n: i }, N { s: "ef", n: i }, N { s: "gh", n: i }];
    return xs.len();
}

function main(): i32 {
    let t: i32 = 0;
    let r: i32 = 0;
    while (r < 100) { t = t + round(r); r = r + 1; }
    return t / 100;
}`

func TestSelfHostStructArrAppendStrFieldReclaimX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "drivers/asm_ir_run.fern", "driver")

	liveOf := func(t *testing.T, name, src string) int64 {
		t.Helper()
		asm := hevCompile(t, runner, driverBin, src, []string{"FERN_LEAKCHECK=1"})
		progBin := buildBin(t, gcc, dir, name, asm)
		stderr, exit := hevRun(t, runner, progBin)
		if exit != 4 {
			t.Fatalf("%s exited %d, want 4", name, exit)
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
		return live
	}

	appended := liveOf(t, "structarr_append_strfield", structArrAppendStrFieldSrc)
	literal := liveOf(t, "structarr_literal_strfield", structArrLiteralStrFieldSrc)
	if appended != literal {
		t.Errorf("append-built live_bytes=%d, literal-built live_bytes=%d — the two ways "+
			"of building the same array must reclaim alike; a larger append figure means "+
			"the element boxes are leaking again, and the gap scales with the iteration "+
			"count", appended, literal)
	}
}

const structArrAppendCallElemSrc = `struct P { x: i32, y: i32 }

function mk(i: i32): P { return P { x: i, y: i }; }

function round(): i32 {
    let ps: P[] = [];
    let i: i32 = 0;
    while (i < 8) { ps = ps.append(mk(i)); i = i + 1; }
    return ps.len();
}

function main(): i32 {
    let t: i32 = 0;
    let r: i32 = 0;
    while (r < 100) { t = t + round(); r = r + 1; }
    return t / 100;
}`

func TestSelfHostStructArrAppendCallElemReclaimX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "drivers/asm_ir_run.fern", "driver")

	asm := hevCompile(t, runner, driverBin, structArrAppendCallElemSrc, []string{"FERN_LEAKCHECK=1"})
	progBin := buildBin(t, gcc, dir, "structarr_append_callelem", asm)
	stderr, exit := hevRun(t, runner, progBin)
	if exit != 8 {
		t.Fatalf("program exited %d, want 8", exit)
	}

	summary := ""
	for _, line := range strings.Split(stderr, "\n") {
		if strings.HasPrefix(line, "leakcheck: ") {
			summary = line
		}
	}
	if summary == "" {
		t.Fatal("no leakcheck summary")
	}
	var allocs, frees, live int64
	if _, err := fmtSscan(summary, &allocs, &frees, &live); err != nil {
		t.Fatalf("parse %q: %v", summary, err)
	}
	if allocs == 0 {
		t.Fatal("program allocated nothing — the probe is not exercising the path")
	}
	if live != 0 {
		t.Errorf("%s: live_bytes=%d, want 0 — an element built by a strict-fresh-returning "+
			"call is as sole-owned as an inline literal, so its element boxes must be "+
			"reclaimed; the leak scales with the iteration count", summary, live)
	}
}

func TestSelfHostStructArrAppendHazardsX86_64(t *testing.T) {
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
			name: "element_alias",
			src: `struct P { x: i32, y: i32 }
function main(): i32 {
    let ps: P[] = [];
    let i: i32 = 0;
    while (i < 3) { ps = ps.append(P { x: i, y: i }); i = i + 1; }
    let q: P = ps[1];
    return q.x + ps.len();
}`,
			want: 4,
		},
		{
			name: "ident_element",
			src: `struct P { x: i32, y: i32 }
function main(): i32 {
    let shared: P = P { x: 5, y: 6 };
    let ps: P[] = [];
    let i: i32 = 0;
    while (i < 3) { ps = ps.append(shared); i = i + 1; }
    return ps[0].x + shared.y;
}`,
			want: 11,
		},
		{
			name: "base_copy_element",
			src: `struct P { x: i32, y: i32 }
function main(): i32 {
    let base: P = P { x: 2, y: 3 };
    let ps: P[] = [];
    let i: i32 = 0;
    while (i < 3) { ps = ps.append(P { ...base, x: i }); i = i + 1; }
    return ps[2].x + base.y;
}`,
			want: 5,
		},
		{
			name: "rebound_to_other",
			src: `struct P { x: i32, y: i32 }
function main(): i32 {
    let ps: P[] = [];
    let i: i32 = 0;
    while (i < 2) { ps = ps.append(P { x: 4, y: 4 }); i = i + 1; }
    let qs: P[] = [P { x: 9, y: 9 }];
    ps = qs;
    return ps[0].x + qs.len();
}`,
			want: 10,
		},
		{
			name: "escaping_return",
			src: `struct P { x: i32, y: i32 }
function build(): P[] {
    let ps: P[] = [];
    let i: i32 = 0;
    while (i < 3) { ps = ps.append(P { x: i, y: i }); i = i + 1; }
    return ps;
}
function main(): i32 { let r: P[] = build(); return r[2].x + r.len(); }`,
			want: 5,
		},
		{
			name: "string_field_admitted",
			src: `struct N { name: string, n: i32 }
function main(): i32 {
    let ns: N[] = [];
    let i: i32 = 0;
    while (i < 3) { ns = ns.append(N { name: "abc", n: i }); i = i + 1; }
    return ns[2].n + ns[0].name.len();
}`,
			want: 5,
		},
		{
			name: "string_field_element_alias",
			src: `struct N { name: string, n: i32 }
function main(): i32 {
    let ns: N[] = [];
    let i: i32 = 0;
    while (i < 3) { ns = ns.append(N { name: "wxyz", n: i }); i = i + 1; }
    let q: N = ns[1];
    return q.n + q.name.len() + ns.len();
}`,
			want: 8,
		},
		{
			name: "passthrough_call_element",
			src: `struct N { name: string, n: i32 }
@noinline function passthru(p: N): N { return p; }
function main(): i32 {
    let shared: N = N { name: "pq", n: 5 };
    let ns: N[] = [];
    let i: i32 = 0;
    while (i < 3) { ns = ns.append(passthru(shared)); i = i + 1; }
    return ns[0].n + shared.name.len();
}`,
			want: 7,
		},
		{
			name: "method_call_element",
			src: `struct N { name: string, n: i32 }
struct Src { base: i32 }
function (s: Src) make(i: i32): N { return N { name: "zz", n: s.base + i }; }
function main(): i32 {
    let src: Src = Src { base: 10 };
    let ns: N[] = [];
    let i: i32 = 0;
    while (i < 3) { ns = ns.append(src.make(i)); i = i + 1; }
    return ns[2].n + ns[0].name.len();
}`,
			want: 14,
		},
		{
			name: "closure_field_admitted",
			src: `struct C { name: string, f: (i32) => i32 }
function main(): i32 {
    let cs: C[] = [];
    let i: i32 = 0;
    while (i < 3) { cs = cs.append(C { name: "ab", f: (x: i32) => x + 1 }); i = i + 1; }
    return (cs[2].f)(3) + cs[0].name.len();
}`,
			want: 6,
		},
		{
			name: "string_field_ident_element",
			src: `struct N { name: string, n: i32 }
function main(): i32 {
    let shared: N = N { name: "pq", n: 5 };
    let ns: N[] = [];
    let i: i32 = 0;
    while (i < 3) { ns = ns.append(shared); i = i + 1; }
    return ns[0].n + shared.name.len();
}`,
			want: 7,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			asm := hevCompile(t, runner, driverBin, tc.src, nil)
			progBin := buildBin(t, gcc, dir, "structarr_hazard_"+tc.name, asm)
			_, exit := hevRun(t, runner, progBin)
			if exit != tc.want {
				t.Errorf("exited %d, want %d — a wrong answer or a crash here means the "+
					"reclaim credit was granted to a shape that still holds a live "+
					"reference (use-after-free), not merely that it leaked", exit, tc.want)
			}
		})
	}
}
