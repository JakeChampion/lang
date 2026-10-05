package e2eselfhost

import (
	"strings"
	"testing"
)

// --- `own` struct params: released at exit, donated with string / enum fields
// (#5342) -------------------------------------------------------------------
//
// Four leaks on one shape family, measured on x86-64 at 100 rounds
// (allocs/frees/live_bytes; native was clean on every row):
//
//	bump(p: P): P { return P { ...p, n: p.n + 1 }; }     P { s: string, n: i32 }
//	  called as bump(P { s: w(i), n: i })                 400/100  live 16800
//	bump(own p: P): P { … the same … }                    300/100  live 12000
//	bumpq(own p: Q): Q { … }, Q { e: E, n: i32 }         300/0    live 13600
//	bump(own p: N): N { … }, N { m: i32, n: i32 }        100/100  (reuse fired)
//
// Every row was the caller never releasing `q`: a call result built by a
// functional update `T { ...base, … }` earned no strict-fresh credit, though
// every field the spread CARRIES reaches the new box counted (the base copy
// retains a nested-struct or enum field, and a string field where the type
// ROUTES field reclaim; a scalar carries nothing).
// return_value_is_strictfresh_struct now admits it on exactly that condition
// (spread_carried_fields_counted), and a bare `return p` of an `own` param on
// the frame-fresh terms (own_param_ret_is_frame_fresh).
//
// The enum row's callee never released `p` at all: reuse was refused for the
// enum field and a param was never exit-swept. own_struct_param_release_rows_of
// now credits an `own` struct param the frame still holds at exit ("OWNREL:" —
// deep; "OWNRELB:" — box-only where a field may have been copied out
// uncounted), and the own-update family admits enum fields.
//
// Two bugs surfaced under the new coverage and are pinned here too:
//
//   - borrowable_params_of marked an `own` param borrowable whenever the body
//     did not "consume" it, so the caller stashed and FREED a fresh argument the
//     callee had taken — a double ownership the scalar control masked only
//     because `q` was never released. An `own` position is a move.
//   - the own-update family admitted a string override on a type that does
//     not ROUTE field reclaim; the reuse arm then freed the old string while
//     the caller's construction — whose retain is gated on routing — had
//     taken no share. `unrouted_string_own_update` exited 77 on the parent
//     with allocation churn between the free and the read. FERN_SANITIZE
//     reported nothing: the quarantine stops the recycling that exposes it.
//
// Every row is gated on `__rc_underflow_count()` and runs a second leg under
// FERN_SANITIZE=1. Every want was confirmed against BOTH oracles: `bin/fern
// -interp` and the native x86-64 backend agreed on each.
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
			// #8628: one exit returns the param bare, a sibling exit supersedes
			// it with a spread that OVERRIDES an rc field. The bare return used
			// to disqualify the whole param from an exit-release row
			// (struct_returned_bare), so the superseding exit released nothing
			// and the overridden field's old buffer leaked once per call —
			// 10002/5000, 360,080 bytes on the issue's own reproducer.
			//
			// The row is granted now and the BARE exit alone elides its release
			// (returned_own_struct_param_slot), which is the move: there is no
			// return-transfer retain for structs, so eliding the dec is the
			// whole hand-off. A LOWER free count here is that elision spreading
			// to the superseding exit; a HIGHER one is the bare exit releasing a
			// box it just handed the caller, which the underflow check catches
			// as exit 99.
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
			// THE BORROWED ROW: the argument temp and the call result are both
			// released.
			name: "borrowed_spread_string",
			src: ownParamReleaseHead + `@noinline
function bump(p: P): P { return P { ...p, n: p.n + 1 }; }` +
				ownParamReleaseMain(`let q: P = bump(P { s: w(i), n: i }); x = x + q.n + q.s.len();`),
			want: 78,
		},
		{
			// THE OWN STRING ROW. Was 300/100 live 12000. The callee reused
			// the param's box already (the own-update family); `q` leaked.
			name: "own_spread_string",
			src: ownParamReleaseHead + `@noinline
function bump(own p: P): P { return P { ...p, n: p.n + 1 }; }` +
				ownParamReleaseMain(`let q: P = bump(P { s: w(i), n: i }); x = x + q.n + q.s.len();`),
			want: 78,
		},
		{
			// THE OWN ENUM ROW. Was 300/0 live 13600: reuse refused for the
			// enum field, the param never released, `q` never released.
			name: "own_spread_enum",
			src: `enum E { A(i32), B(i32) }
struct Q { e: E, n: i32 }
@noinline
function bumpq(own p: Q): Q { return Q { ...p, n: p.n + 1 }; }` +
				ownParamReleaseMain(`let q: Q = bumpq(Q { e: A(i), n: i }); x = x + q.n; match (q.e) { A(v) => { x = x + v; }, B(v) => { x = x + v * 2; } }`),
			want: 40,
		},
		{
			// The scalar control, which read 100/100 on the parent by accident:
			// the caller freed the argument the callee had reused as its
			// result, and the result was then read after the free.
			name: "own_scalar_control",
			src: `struct N { m: i32, n: i32 }
@noinline
function bump(own p: N): N { return N { ...p, n: p.n + 1 }; }` +
				ownParamReleaseMain(`let q: N = bump(N { m: i, n: i }); x = x + q.n + q.m;`),
			want: 40,
		},
		{
			// The borrowed scalar control. Was 200/100: the argument temp was
			// released, `q` was not.
			name: "borrowed_scalar_control",
			src: `struct N { m: i32, n: i32 }
@noinline
function bump(p: N): N { return N { ...p, n: p.n + 1 }; }` +
				ownParamReleaseMain(`let q: N = bump(N { m: i, n: i }); x = x + q.n + q.m;`),
			want: 40,
		},
		{
			// An `own` param the callee only READS: nothing donated, so the
			// exit release is the only thing that frees it.
			name: "own_read_only",
			src: ownParamReleaseHead + `@noinline
function sink(own p: P): i32 { return p.n + p.s.len(); }` +
				ownParamReleaseMain(`x = x + sink(P { s: w(i), n: i });`),
			want: 61,
		},
		{
			// Passed on to another `own` position: the first frame's credit is
			// refused (a non-borrowable argument is a move) and the second
			// frame releases. One owner at every point.
			name: "own_passed_on",
			src: ownParamReleaseHead + `@noinline
function sink(own p: P): i32 { return p.n + p.s.len(); }
@noinline
function pass_on(own p: P): i32 { let k: i32 = p.n; return sink(p) + k; }` +
				ownParamReleaseMain(`x = x + pass_on(P { s: w(i), n: i });`),
			want: 31,
		},
		{
			// A VOID callee that falls off its end. The implicit exit went
			// through the backend's default epilogue with no sweep at all —
			// void functions leaked every local there, own params included.
			name: "own_void_fallthrough",
			src: ownParamReleaseHead + `@noinline
function sink_void(own p: P): void { let k: i32 = p.n; if (k < 0) { return; } }` +
				ownParamReleaseMain(`sink_void(P { s: w(i), n: i }); x = x + i;`),
			want: 53,
		},
		{
			// `return p` hands the moved-in box straight back: the callee's
			// release is refused (struct_returned_bare) and the caller's
			// binding earns the strict-fresh credit instead.
			name: "own_returned_bare",
			src: ownParamReleaseHead + `@noinline
function id(own p: P): P { if (p.n < 0) { return P { ...p, n: 0 }; } return p; }` +
				ownParamReleaseMain(`let q: P = id(P { s: w(i), n: i }); x = x + q.n + q.s.len();`),
			want: 61,
		},
		{
			// REFUSED reuse (a borrowed string as the override) over a type
			// with an ARRAY field: the spread copies `xs` into the result
			// COUNTED, so the param's exit release stays deep ("OWNREL:") and
			// the result's own drop releases its share — every box balances.
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
			// The own-update string override over a COUNTED share: the
			// argument literal took `h.s` with a retain, so the reuse arm's
			// rc-aware free only decs and `h` keeps its string.
			name: "field_read_share_own_override",
			src: ownParamReleaseHead + `struct H { s: string, k: i32 }
@noinline
function bump(own p: P): P { return P { ...p, s: "override-payload-wide-enough-to-heap-" + w(p.n), n: p.n + 1 }; }` +
				ownParamReleaseMain(`let h: H = H { s: w(i), k: i }; let q: P = bump(P { s: h.s, n: i }); x = x + q.n + q.s.len() + h.s.len();`),
			want: 51,
		},
		{
			// The same with the call result SPREAD again in the caller —
			// the shape that first exposed the bogus post-call free of an
			// `own` argument (exit 99 with the type gate alone widened). The
			// spread copy `z` holds its own count of `s`.
			name: "call_result_spread_again",
			src: ownParamReleaseHead + `struct H { s: string, k: i32 }
@noinline
function bump(own p: P): P { return P { ...p, s: "override-payload-wide-enough-to-heap-" + w(p.n), n: p.n + 1 }; }` +
				ownParamReleaseMain(`let h: H = H { s: w(i), k: i }; let q: P = bump(P { s: h.s, n: i }); let z: P = P { ...q, n: 0 }; x = x + q.n + q.s.len() + h.s.len() + z.n;`),
			want: 51,
		},
		{
			// THE USE-AFTER-FREE. `get` returns a field named `s`, which the
			// routing scan reads as unsafe for EVERY `s` field, so neither P
			// nor H routes and the argument literal takes no retain on `h.s`.
			// The parent still admitted `bump` to the own-update family and
			// freed `h.s` under `h`; `churn` recycles the box before the read.
			// Parent: exit 77 (h.s.len() moved). Native: 12.
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
			// A self-update followed by a return-position update, with a
			// field bound out before either: the bind is counted, so the
			// first update's free only decs, and `s2` releases its own
			// count (#10371).
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
			// The LOCAL self-overwrite family with an enum field bound out
			// first: the same counted-bind argument the own admission rests
			// on, witnessed on the family that already shipped.
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
			// A fresh literal at a COUNTED-RETAIN position: the callee appends
			// it into a container it returns, so the box is shared after the
			// call and the temp's release must skip the field walk (the
			// rc==1 gate) and dec the box only. `churn` recycles any block
			// freed early, so a premature release reads back as exit 77 or
			// a crash rather than as a moved count.
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
			// A callee that RETURNS a field of its param is not borrowable,
			// so the widened temp stash must not fire on its argument: `g`
			// aliases the literal's string and reads it after churn.
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

// TestSelfHostOwnParamReleaseX86_64 — every row balances at live_bytes 0 with
// no rc underflow, on the census leg and again under the quarantining allocator.
func TestSelfHostOwnParamReleaseX86_64(t *testing.T) {
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

// TestSelfHostOwnParamReleaseWasmIR — the wasm sibling. Exit codes only: an
// over-release moves no byte count on any backend.
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

// TestSelfHostOwnParamReleaseIRArm64 — the arm64 sibling under qemu.
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

// --- `return f(xs)` handing a pointer-element array back through an `own`
// position (#10718) ----------------------------------------------------------
//
// A callee that does not consume a pointer-element `own` array hands an
// identical buffer back uncounted, so `return f(xs)` is `xs = f(xs); return
// xs`. The AST lowering's exit sweep released `xs` whatever the call returned,
// freeing the buffer the result still held: exit 77 or a sanitizer
// use-after-free on every row below before the fix.
//
// The elements are static strings, and the array-of-arrays row has no rows,
// so the only heap unit in play is the buffer the handback moves.
// Expected values agree with `bin/fern -interp`.
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
			// The issue's shape: an owned local handed back unchanged.
			name: "local_handed_back",
			src:  ownHandbackReturnHead + ownHandbackBuild("pass(xs)") + ownParamReleaseMain(ownHandbackReturnMain),
			want: 53,
		},
		{
			// The same through an identity callee the inliner takes.
			name: "local_handed_back_inlined",
			src: ownHandbackReturnHead + "function same(own xs: string[]): string[] { return xs; }\n" +
				ownHandbackBuild("same(xs)") + ownParamReleaseMain(ownHandbackReturnMain),
			want: 53,
		},
		{
			// Every other round the callee returns a different buffer, and
			// the local it was handed is released at exit as a rebind would.
			name: "local_replaced",
			src:  ownHandbackReturnHead + ownHandbackBuild("swap(xs, i)") + ownParamReleaseMain(ownHandbackReturnMain),
			want: 3,
		},
		{
			// A flagged `own` parameter that owns a grown buffer when it
			// reaches the return: kept when handed back, released when the
			// callee replaced it.
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

// Pointer-element arrays whose elements the heap holds (#10721): strings built
// at run time and rows appended in a loop. A callee that hands its `own`
// argument back, or returns a fresh array in its place, gives the caller the
// release of every element; so does a local built by appends and returned bare.
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

// A closure handing back a value it captured (#10740). The lifted body reads
// the capture out of the env box as a borrow of the captured local, so
// returning it retains: the caller binds a closure call's array result as a
// count it owns. Without the retain the caller's release took the local's
// count, and the owner's own release underflowed.
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
			// An `own` capture: the callee does not consume it, so the caller releases
			// the literal it passed after the call, and the closure's return retains.
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
			// The same with string[], beside a plain `own` callee.
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

// TestSelfHostOwnHandbackReturnX86_64 — every row on both lowerings, balanced
// at live_bytes 0 with no rc underflow, and clean under the quarantining
// allocator.
func TestSelfHostOwnHandbackReturnX86_64(t *testing.T) {
	runBalancedRows(t, "ownhb", append(append(ownHandbackReturnCases(), ownHandbackHeapCases()...), closureCaptureReturnCases()...))
}

// runBalancedRows runs every case on both lowerings: the exit code is the
// value check (99 is the rc underflow gate), the leakcheck must balance at
// live_bytes 0, and the sanitize leg must be clean.
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
