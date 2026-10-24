package e2ecompiler

import (
	"strings"
	"testing"
)

type ownParamReleaseCase struct {
	name string
	src  string
	want int
}

const ownParamReleaseHead = `import "std/i32";
struct P { s: string, n: i32 }
@noinline
function w(i: i32): string { return "s-a-wide-payload-past-any-inline-threshold-" + i.to_string(); }
`

func ownParamReleaseMain(body string) string {
	return "\nfunction main(): i32 { let x: i32 = 0; let i: i32 = 0; " +
		"while (i < 100) { " + body + " i = i + 1; } " +
		"if (__rc_underflow_count() != 0) { return 99; } return x % 83; }"
}

func ownParamReleaseCases() []ownParamReleaseCase {
	return []ownParamReleaseCase{
		{
			name: "own_bare_ret_sibling_supersedes",
			src: ownParamReleaseHead + `@noinline
function flush(own p: P, k: i32): P {
    if (k < 2) { return p; }
    return P { ...p, s: w(k), n: 0 };
}` +
				ownParamReleaseMain(`let q: P = flush(P { s: w(i), n: i }, i % 4); x = x + q.n + q.s.len();`),
			want: 63,
		},
		{
			name: "borrowed_spread_string",
			src: ownParamReleaseHead + `@noinline
function bump(p: P): P { return P { ...p, n: p.n + 1 }; }` +
				ownParamReleaseMain(`let q: P = bump(P { s: w(i), n: i }); x = x + q.n + q.s.len();`),
			want: 78,
		},
		{
			name: "own_spread_string",
			src: ownParamReleaseHead + `@noinline
function bump(own p: P): P { return P { ...p, n: p.n + 1 }; }` +
				ownParamReleaseMain(`let q: P = bump(P { s: w(i), n: i }); x = x + q.n + q.s.len();`),
			want: 78,
		},
		{
			name: "own_spread_enum",
			src: `enum E { A(i32), B(i32) }
struct Q { e: E, n: i32 }
@noinline
function bumpq(own p: Q): Q { return Q { ...p, n: p.n + 1 }; }` +
				ownParamReleaseMain(`let q: Q = bumpq(Q { e: A(i), n: i }); x = x + q.n; match (q.e) { A(v) => { x = x + v; }, B(v) => { x = x + v * 2; } }`),
			want: 40,
		},
		{
			name: "own_scalar_control",
			src: `struct N { m: i32, n: i32 }
@noinline
function bump(own p: N): N { return N { ...p, n: p.n + 1 }; }` +
				ownParamReleaseMain(`let q: N = bump(N { m: i, n: i }); x = x + q.n + q.m;`),
			want: 40,
		},
		{
			name: "borrowed_scalar_control",
			src: `struct N { m: i32, n: i32 }
@noinline
function bump(p: N): N { return N { ...p, n: p.n + 1 }; }` +
				ownParamReleaseMain(`let q: N = bump(N { m: i, n: i }); x = x + q.n + q.m;`),
			want: 40,
		},
		{
			name: "own_read_only",
			src: ownParamReleaseHead + `@noinline
function sink(own p: P): i32 { return p.n + p.s.len(); }` +
				ownParamReleaseMain(`x = x + sink(P { s: w(i), n: i });`),
			want: 61,
		},
		{
			name: "own_passed_on",
			src: ownParamReleaseHead + `@noinline
function sink(own p: P): i32 { return p.n + p.s.len(); }
@noinline
function pass_on(own p: P): i32 { let k: i32 = p.n; return sink(p) + k; }` +
				ownParamReleaseMain(`x = x + pass_on(P { s: w(i), n: i });`),
			want: 31,
		},
		{
			name: "own_void_fallthrough",
			src: ownParamReleaseHead + `@noinline
function sink_void(own p: P): void { let k: i32 = p.n; if (k < 0) { return; } }` +
				ownParamReleaseMain(`sink_void(P { s: w(i), n: i }); x = x + i;`),
			want: 53,
		},
		{
			name: "own_returned_bare",
			src: ownParamReleaseHead + `@noinline
function id(own p: P): P { if (p.n < 0) { return P { ...p, n: 0 }; } return p; }` +
				ownParamReleaseMain(`let q: P = id(P { s: w(i), n: i }); x = x + q.n + q.s.len();`),
			want: 61,
		},
		{
			name: "own_array_spread_refused_reuse",
			src: `import "std/i32";
struct A { xs: i32[], s: string, n: i32 }
@noinline
function w(i: i32): string { return "s-a-wide-payload-past-any-inline-threshold-" + i.to_string(); }
@noinline
function relabel(own p: A, t: string): A { return A { ...p, s: t }; }` +
				ownParamReleaseMain(`let t: string = w(i + 1); let q: A = relabel(A { xs: [i, i + 1], s: w(i), n: i }, t); x = x + q.n + q.s.len() + q.xs[1] + t.len();`),
			want: 60,
		},
		{
			name: "field_read_share_own_override",
			src: ownParamReleaseHead + `struct H { s: string, k: i32 }
@noinline
function bump(own p: P): P { return P { ...p, s: "override-payload-wide-enough-to-heap-" + w(p.n), n: p.n + 1 }; }` +
				ownParamReleaseMain(`let h: H = H { s: w(i), k: i }; let q: P = bump(P { s: h.s, n: i }); x = x + q.n + q.s.len() + h.s.len();`),
			want: 51,
		},
		{
			name: "call_result_spread_again",
			src: ownParamReleaseHead + `struct H { s: string, k: i32 }
@noinline
function bump(own p: P): P { return P { ...p, s: "override-payload-wide-enough-to-heap-" + w(p.n), n: p.n + 1 }; }` +
				ownParamReleaseMain(`let h: H = H { s: w(i), k: i }; let q: P = bump(P { s: h.s, n: i }); let z: P = P { ...q, n: 0 }; x = x + q.n + q.s.len() + h.s.len() + z.n;`),
			want: 51,
		},
		{
			name: "unrouted_string_own_update_refused",
			src: ownParamReleaseHead + `struct H { s: string, k: i32 }
@noinline
function bump(own p: P): P { return P { ...p, s: "override-payload-wide-enough-to-heap-" + w(p.n), n: p.n + 1 }; }
@noinline
function get(x: P): string { return x.s; }
@noinline
function churn(i: i32): i32 { let a: string = w(i) + w(i + 1); let b: string = w(i + 2) + w(i + 3); return a.len() + b.len(); }` +
				ownParamReleaseMain(`let h: H = H { s: w(i), k: i }; let want: i32 = h.s.len(); let q: P = bump(P { s: h.s, n: i }); x = x + churn(i); if (h.s.len() != want) { return 77; } if (h.s[0] != b's') { return 78; } let g: string = get(q); x = x + q.n + g.len() + h.s.len();`),
			want: 12,
		},
		{
			name: "self_update_then_return_update",
			src: ownParamReleaseHead + `@noinline
function bump(own p: P): P {
    let s2: string = p.s;
    p = P { ...p, s: "override-payload-wide-enough-to-heap-" + w(p.n) };
    return P { ...p, n: p.n + s2.len() };
}` +
				ownParamReleaseMain(`let q: P = bump(P { s: w(i), n: i }); x = x + q.n + q.s.len();`),
			want: 34,
		},
		{
			name: "enum_field_bind_then_local_override",
			src: `enum E { A(i32), B(i32) }
struct Q { e: E, n: i32 }
@noinline
function round(i: i32): i32 {
    let d: Q = Q { e: A(i), n: i };
    let e2: E = d.e;
    let c: Q = Q { ...d, e: B(i + 1) };
    let r: i32 = c.n;
    match (e2) { A(v) => { r = r + v; }, B(v) => { r = r + v * 2; } }
    match (c.e) { A(v) => { r = r + v; }, B(v) => { r = r + v * 3; } }
    return r;
}` +
				ownParamReleaseMain(`x = x + round(i);`),
			want: 67,
		},
		{
			name: "counted_position_array_field_temp",
			src: `struct H { id: i32, xs: i32[] }
struct Hold { items: H[], n: i32 }
@noinline
function keep(h: H, k: i32): Hold { let items: H[] = []; items = items.append(h); return Hold { items: items, n: k }; }
@noinline
function churn(i: i32): H { return H { id: i, xs: [900, 901, 902] }; }` +
				ownParamReleaseMain(`let hd: Hold = keep(H { id: i, xs: [i, i + 1, i + 2] }, i); let c: H = churn(i); if (hd.items[0].xs[2] != i + 2) { return 77; } x = x + hd.n + hd.items[0].xs.len() + hd.items[0].xs[2] + c.xs.len();`),
			want: 76,
		},
		{
			name: "borrowed_field_returning_callee_temp",
			src: ownParamReleaseHead + `@noinline
function get(x: P): string { return x.s; }
@noinline
function churn(i: i32): i32 { let a: string = w(i) + w(i + 1); let b: string = w(i + 2) + w(i + 3); return a.len() + b.len(); }` +
				ownParamReleaseMain(`let g: string = get(P { s: w(i), n: i }); x = x + churn(i); if (g[0] != b's') { return 78; } x = x + g.len();`),
			want: 52,
		},
	}
}

func TestSelfHostOwnParamReleaseX86_64(t *testing.T) {
	boxedProbes(t)
	cli := newStrictCLI(t)
	dir := t.TempDir()
	for _, tc := range ownParamReleaseCases() {
		t.Run(tc.name, func(t *testing.T) {
			asm := cli.emit(t, "x86-64-linux", tc.src, "FERN_LEAKCHECK=1")
			progBin := buildBin(t, cli.gcc, dir, "ownrel_"+tc.name, asm)
			stderr, exit := hevRun(t, cli.runner, progBin)
			if exit != tc.want {
				t.Fatalf("%s exited %d, want %d (99 = rc underflow; 77/78 = a read "+
					"through a freed box)", tc.name, exit, tc.want)
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
			if live != 0 || allocs != frees {
				t.Errorf("%s: %s — must balance at live_bytes 0 (native does)", tc.name, summary)
			}

			sanAsm := cli.emit(t, "x86-64-linux", tc.src, "FERN_SANITIZE=1")
			sanBin := buildBin(t, cli.gcc, dir, "ownrel_san_"+tc.name, sanAsm)
			sanErr, sanExit := hevRun(t, cli.runner, sanBin)
			if sanExit != tc.want {
				t.Fatalf("%s sanitize leg exited %d, want %d (124 = fatal sanitizer check)", tc.name, sanExit, tc.want)
			}
			if strings.Contains(sanErr, "rc over-release") || strings.Contains(sanErr, "use-after-free") {
				t.Fatalf("%s sanitize leg reported:\n%s", tc.name, sanErr)
			}
		})
	}
}

func TestSelfHostOwnParamReleaseWasmIR(t *testing.T) {
	cli := newStrictCLI(t)
	for _, tc := range ownParamReleaseCases() {
		t.Run(tc.name, func(t *testing.T) {
			if got, _ := runWasm(t, cli.emit(t, "wasm32-wasi", tc.src)); got != tc.want {
				t.Errorf("own-param release wasm IR %q = %d, want %d", tc.name, got, tc.want)
			}
		})
	}
}

func TestSelfHostOwnParamReleaseIRArm64(t *testing.T) {
	gcc, qemu := arm64Tooling(t)
	cli := newStrictCLI(t)
	for _, tc := range ownParamReleaseCases() {
		t.Run(tc.name, func(t *testing.T) {
			if code, _ := runArm64(t, gcc, qemu, cli.emit(t, "arm64-linux", tc.src)); code != tc.want {
				t.Errorf("own-param release arm64 IR %q = %d, want %d", tc.name, code, tc.want)
			}
		})
	}
}

const ownHandbackReturnHead = `@noinline
function pass(own xs: string[]): string[] {
    let n: i32 = 0;
    let k: i32 = 0;
    while (k < xs.len()) { n = n + xs[k].len(); k = k + 1; }
    if (n > 100000) { print("big"); }
    return xs;
}
@noinline
function swap(own xs: string[], i: i32): string[] {
    if (i % 2 == 0) { return xs; }
    return ["cd", "cd", "cd"];
}
@noinline
function churn(i: i32): string[] { let c: string[] = []; c = c.append("zz"); c = c.append("zzz"); return c; }
`

func ownHandbackBuild(ret string) string {
	return `@noinline
function build(i: i32): string[] {
    let xs: string[] = [];
    let k: i32 = 0;
    while (k < i % 5 + 2) { xs = xs.append("ab"); k = k + 1; }
    return ` + ret + `;
}
`
}

const ownHandbackReturnMain = `let ys: string[] = build(i); let c: string[] = churn(i); if (ys[1].len() != 2) { return 77; } x = x + ys[1].len() + ys.len() + c.len();`

func ownHandbackReturnCases() []ownParamReleaseCase {
	return []ownParamReleaseCase{
		{
			name: "local_handed_back",
			src:  ownHandbackReturnHead + ownHandbackBuild("pass(xs)") + ownParamReleaseMain(ownHandbackReturnMain),
			want: 53,
		},
		{
			name: "local_handed_back_inlined",
			src: ownHandbackReturnHead + "@noinline function same(own xs: string[]): string[] { return xs; }\n" +
				ownHandbackBuild("same(xs)") + ownParamReleaseMain(ownHandbackReturnMain),
			want: 53,
		},
		{
			name: "local_replaced",
			src:  ownHandbackReturnHead + ownHandbackBuild("swap(xs, i)") + ownParamReleaseMain(ownHandbackReturnMain),
			want: 3,
		},
		{
			name: "flagged_param_replaced",
			src: ownHandbackReturnHead + `@noinline
function mid(own xs: string[], i: i32): string[] {
    xs = xs.append("ef");
    return swap(xs, i);
}
` + ownHandbackBuild("mid(xs, i)") + ownParamReleaseMain(ownHandbackReturnMain),
			want: 53,
		},
		{
			name: "arrarr_local_handed_back",
			src: `@noinline
function pass(own xs: i32[][]): i32[][] {
    if (xs.len() > 100000) { print("big"); }
    return xs;
}
@noinline
function churn(i: i32): i32[][] { let c: i32[][] = [[i]]; return c; }
@noinline
function build(i: i32): i32[][] {
    let xs: i32[][] = [];
    return pass(xs);
}
` + ownParamReleaseMain(`let ys: i32[][] = build(i); let c: i32[][] = churn(i); if (ys.len() != 0) { return 77; } x = x + ys.len() + c.len() + 1;`),
			want: 34,
		},
	}
}

const ownHandbackHeapHead = ownParamReleaseHead + `@noinline
function spass(own xs: string[]): string[] { return xs; }
@noinline
function spick(own xs: string[], i: i32): string[] {
    if (i % 2 == 0) { return xs; }
    return [w(i), w(i + 1)];
}
@noinline
function smk(i: i32): string[] { return [w(i), w(i + 2)]; }
@noinline
function rpass(own xs: i32[][]): i32[][] { return xs; }
@noinline
function rpick(own xs: i32[][], i: i32): i32[][] {
    if (i % 2 == 0) { return xs; }
    return [[i, i], [i]];
}
`

func ownHandbackHeapStrBuild(tail string) string {
	return `@noinline
function build(i: i32): string[] {
    let xs: string[] = [];
    let k: i32 = 0;
    while (k < i % 3 + 2) { xs = xs.append(w(i + k)); k = k + 1; }
    ` + tail + `
}
`
}

func ownHandbackHeapRowsBuild(tail string) string {
	return `@noinline
function build(i: i32): i32[][] {
    let xs: i32[][] = [];
    let k: i32 = 0;
    while (k < i % 3 + 2) { xs = xs.append([k, k + i]); k = k + 1; }
    ` + tail + `
}
`
}

const ownHandbackHeapStrMain = `let ys: string[] = build(i); if (ys[1].len() < 40) { return 77; } x = x + ys.len() + ys[1].len();`

const ownHandbackHeapRowsMain = `let ys: i32[][] = build(i); if (ys[1].len() == 0) { return 77; } x = x + ys.len() + ys[1][0];`

func ownHandbackHeapCases() []ownParamReleaseCase {
	return []ownParamReleaseCase{
		{name: "strings_handed_back", src: ownHandbackHeapHead + ownHandbackHeapStrBuild("return spass(xs);") + ownParamReleaseMain(ownHandbackHeapStrMain), want: 60},
		{name: "strings_replaced", src: ownHandbackHeapHead + ownHandbackHeapStrBuild("return spick(xs, i);") + ownParamReleaseMain(ownHandbackHeapStrMain), want: 11},
		{name: "strings_rebound_through_handback", src: ownHandbackHeapHead + ownHandbackHeapStrBuild("xs = spick(xs, i); return xs;") + ownParamReleaseMain(ownHandbackHeapStrMain), want: 11},
		{name: "strings_rebound_from_producer", src: ownHandbackHeapHead + ownHandbackHeapStrBuild("if (i % 2 == 1) { xs = smk(i); } return xs;") + ownParamReleaseMain(ownHandbackHeapStrMain), want: 11},
		{name: "rows_literal_handed_back", src: ownHandbackHeapHead + `@noinline
function build(i: i32): i32[][] {
    let xs: i32[][] = [[i, 1], [2, i], [3, 3]];
    return rpass(xs);
}
` + ownParamReleaseMain(ownHandbackHeapRowsMain), want: 2},
		{name: "rows_appended_returned", src: ownHandbackHeapHead + ownHandbackHeapRowsBuild("return xs;") + ownParamReleaseMain(ownHandbackHeapRowsMain), want: 67},
		{name: "rows_appended_handed_back", src: ownHandbackHeapHead + ownHandbackHeapRowsBuild("return rpass(xs);") + ownParamReleaseMain(ownHandbackHeapRowsMain), want: 67},
		{name: "rows_appended_replaced", src: ownHandbackHeapHead + ownHandbackHeapRowsBuild("return rpick(xs, i);") + ownParamReleaseMain(ownHandbackHeapRowsMain), want: 61},
		{name: "rows_appended_rebound_through_handback", src: ownHandbackHeapHead + ownHandbackHeapRowsBuild("xs = rpick(xs, i); return xs;") + ownParamReleaseMain(ownHandbackHeapRowsMain), want: 61},
	}
}

func closureCaptureReturnCases() []ownParamReleaseCase {
	return []ownParamReleaseCase{
		{
			name: "closure_returns_captured_arrarr",
			src: `@noinline
function usr(f: (i32) => i32[][], i: i32): i32 {
    let g: i32[][] = f(i);
    return g.len() + g[0][0];
}
@noinline
function round(keep: i32[][], i: i32): i32 {
    let f = (j: i32): i32[][] => keep;
    return usr(f, i);
}
function main(): i32 {
    let keep: i32[][] = [[5], [6], [7]];
    let x: i32 = 0;
    let i: i32 = 0;
    while (i < 100) { x = x + round(keep, i); i = i + 1; }
    if (keep[1][0] != 6) { return 77; }
    if (__rc_underflow_count() != 0) { return 99; }
    return x % 83;
}`,
			want: 53,
		},
		{
			name: "closure_hands_back_captured_strarr",
			src: `import "std/i32";
@noinline
function w(i: i32): string { return "s-a-wide-payload-past-any-inline-threshold-" + i.to_string(); }
@noinline
function usr(hb: (string[]) => string[], i: i32): i32 {
    let xs: string[] = ["ab", "cd"];
    xs = hb(xs);
    return xs.len() + xs[1].len();
}
@noinline
function round(keep: string[], i: i32): i32 {
    let hb = (a: string[]): string[] => keep;
    return usr(hb, i);
}
function main(): i32 {
    let keep: string[] = [w(1), w(2)];
    let x: i32 = 0;
    let i: i32 = 0;
    while (i < 100) { x = x + round(keep, i); i = i + 1; }
    if (keep[1].len() != 44) { return 77; }
    if (__rc_underflow_count() != 0) { return 99; }
    return x % 83;
}`,
			want: 35,
		},
		{
			name: "closure_returns_captured_own_arrarr",
			src: `@noinline
function usr(f: (i32) => i32[][], i: i32): i32 {
    let g: i32[][] = f(i);
    return g.len() + g[0][0];
}
@noinline
function round(own keep: i32[][], i: i32): i32 {
    let f = (j: i32): i32[][] => keep;
    return usr(f, i);
}
function main(): i32 {
    let x: i32 = 0;
    let i: i32 = 0;
    while (i < 100) {
        x = x + round([[5], [6]], i);
        i = i + 1;
    }
    if (__rc_underflow_count() != 0) { return 99; }
    return x % 83;
}`,
			want: 36,
		},
		{
			name: "closure_returns_captured_own_strarr",
			src: `import "std/i32";
@noinline
function w(i: i32): string { return "s-a-wide-payload-past-any-inline-threshold-" + i.to_string(); }
@noinline
function usr(f: (i32) => string[], i: i32): i32 {
    let g: string[] = f(i);
    return g.len() + g[1].len();
}
@noinline
function round(own keep: string[], i: i32): i32 {
    let f = (j: i32): string[] => keep;
    return usr(f, i);
}
@noinline
function plain(own keep: string[]): i32 { return keep[0].len(); }
function main(): i32 {
    let x: i32 = 0;
    let i: i32 = 0;
    while (i < 100) { x = x + round([w(i), w(i + 1)], i) + plain([w(i)]); i = i + 1; }
    if (__rc_underflow_count() != 0) { return 99; }
    return x % 83;
}`,
			want: 52,
		},
	}
}

func TestSelfHostOwnHandbackReturnX86_64(t *testing.T) {
	runBalancedRows(t, "ownhb", append(append(ownHandbackReturnCases(), ownHandbackHeapCases()...), closureCaptureReturnCases()...))
}

func runBalancedRows(t *testing.T, prefix string, cases []ownParamReleaseCase) {
	cli := newStrictCLI(t)
	dir := t.TempDir()
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			asm := cli.emit(t, "x86-64-linux", tc.src, "FERN_LEAKCHECK=1")
			progBin := buildBin(t, cli.gcc, dir, prefix+"_"+tc.name, asm)
			stderr, exit := hevRun(t, cli.runner, progBin)
			if exit != tc.want {
				t.Fatalf("exited %d, want %d (99 = rc underflow; 77 = a read through a freed buffer)", exit, tc.want)
			}
			summary := leakSummaryLine(stderr)
			if summary == "" {
				t.Fatalf("no leakcheck summary")
			}
			var allocs, frees, live int64
			if _, err := fmtSscan(summary, &allocs, &frees, &live); err != nil {
				t.Fatalf("parse %q: %v", summary, err)
			}
			if allocs == 0 {
				t.Fatalf("allocated nothing — the probe is not exercising the path")
			}
			if live != 0 || allocs != frees {
				t.Errorf("%s — must balance at live_bytes 0", summary)
			}

			sanAsm := cli.emit(t, "x86-64-linux", tc.src, "FERN_SANITIZE=1")
			sanBin := buildBin(t, cli.gcc, dir, prefix+"_san_"+tc.name, sanAsm)
			sanErr, sanExit := hevRun(t, cli.runner, sanBin)
			if sanExit != tc.want {
				t.Fatalf("sanitize leg exited %d, want %d (124 = fatal sanitizer check)", sanExit, tc.want)
			}
			if strings.Contains(sanErr, "rc over-release") || strings.Contains(sanErr, "use-after-free") {
				t.Fatalf("sanitize leg reported:\n%s", sanErr)
			}
		})
	}
}
