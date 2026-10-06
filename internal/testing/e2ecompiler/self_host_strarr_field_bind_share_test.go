package e2ecompiler

import (
	"testing"
)

// --- A string[] FIELD READ bound to a LOCAL, then stored ----------------------
//
// `let tt: string[] = q.f; let p: P = P { f: tt, n: i }` — the hoisted spelling
// of the inline share self_host_strarr_field_share_read_test.go admitted, and
// the row that file pinned as `hoisted_bind_still_leaks` while it stayed open
// (#5338). It measured 800 allocs / 300 frees, 16800 live, against native's
// 600/600; the emit says why, and it is the same block the inline cell had —
// neither holder emitted __struct_drop_P or __field_reclaim_P at all, because
// strarrfld_scan marks `<T>.<field>` for any read and the bind is a read.
//
// WHAT THE BIND NEEDS THAT THE INLINE POSITION DID NOT. In `P { f: q.f }` the
// read is consumed where it is made, so admitting it proves itself: the
// construction retains an array field unconditionally, and the new holder
// co-owns a counted reference. Through a local that is no longer true — `tt` is
// an ordinary string[] local the field scan sees no further uses of, and an
// unchecked admission would let `return tt[0]` outlive the holder's deep free.
//
// So the bind is admitted only when `tt` reaches NOTHING but that store.
// strarr_unsafe_for_alias is the proof, reused rather than rewritten: it is the
// classifier the local "SARR:" credit already applies to the identical
// question, and its `sfld_ok` carve-out exists for exactly the struct-literal
// share this needs forgiven. Everything else is a hazard, which the
// `refused_*` rows below exercise.
//
// THE FAILURE MODE IS AN OVER-RELEASE, not a leak, so those rows are
// essential rather than decorative: each one escapes something (an element,
// the array, a bound element) or mutates `tt`, then reads every value back
// after 200 rounds of churn have recycled the freelist. They answer identically
// on native x86-64, `bin/fern -interp` and the self-host. On the typed
// lowering every row balances.
//
// `escaping_holder_now_clean` is the one row that moved further than the pin
// predicted: through the bind the returned literal's field value is a bare
// ident, which the strict-fresh return classifier already admitted, so the
// caller's binding earned its reclaim. The inline spelling reached the same
// classifier as a field read and was refused there until #5338 admitted it;
// self_host_strarr_field_share_read_test.go pins both. Both answer 8 on all
// three engines.
//
// The target was re-run under FERN_SANITIZE=1 with FERN_RC_UNDERFLOW_TRAP=1 and
// FERN_RC_FREE_DEBUG=1: clean, no trap, no quarantine hit.

const strarrBindShareDecl = `struct P { f: string[], n: i32 }
function w(a: string): string { return a + "-past-the-sso-inline-threshold"; }
function mkv(i: i32): string[] { let o: string[] = []; o = o.append(w("a")); o = o.append(w("b")); return o; }
function churn(i: i32): i32 { let a: string[] = mkv(i); let b: string[] = mkv(i + 1); return a[0].len() + b[1].len(); }
`

// strarrBindChurnMain drives 200 rounds and separates the three failure modes:
// a negative round result (an element read back wrong, i.e. an over-release)
// exits 100, a non-zero underflow counter exits 99, and reading freed memory
// segfaults on its own.
const strarrBindChurnMain = `
function main(): i32 {
    let t: i32 = 0; let i: i32 = 0; let bad: i32 = 0;
    while (i < 200) { let r: i32 = round(i); if (r < 0) { bad = bad + 1; } t = t + r; i = i + 1; }
    if (bad > 0) { return 100; }
    if (__rc_underflow_count() != 0) { return 99; }
    return t % 83;
}`

const strarrBindPlainMain = `
function main(): i32 {
    let t: i32 = 0; let i: i32 = 0;
    while (i < 100) { t = t + round(i); i = i + 1; }
    if (__rc_underflow_count() != 0) { return 99; }
    return t % 97;
}`

func strarrBindShareCases() []arrenumShareCase {
	return []arrenumShareCase{
		{
			// THE CELL. 800/300 before, 800/800 now — and the same 72 the
			// inline spelling answers, which is the point: the two programs
			// differ only in whether the read is named.
			name: "bind_then_store",
			src: strarrBindShareDecl + `function round(i: i32): i32 {
    let q: P = P { f: mkv(i), n: i };
    let tt: string[] = q.f;
    let p: P = P { f: tt, n: i };
    return (p.f.len() + p.f[0].len() + p.n + q.n) % 101;
}` + strarrBindPlainMain,
			want: 72,
		},
		{
			// The holder escapes with the bind inside the callee. The walk can
			// see `tt` reaches only the returned holder's field, so it is
			// admitted and balances. Reads every element back after churn.
			name: "escaping_holder_now_clean",
			src: strarrBindShareDecl + `function make(i: i32): P {
    let q: P = P { f: mkv(i), n: i };
    let tt: string[] = q.f;
    let p: P = P { f: tt, n: i };
    return p;
}
function round(i: i32): i32 {
    let want: i32 = w("a").len();
    let p: P = make(i);
    let junk: i32 = churn(i * 3 + 1);
    if (p.f.len() != 2) { return 0 - 1; }
    if (p.f[0].len() != want) { return 0 - 2; }
    return (p.f[1].len() + junk) % 101;
}` + strarrBindChurnMain,
			want: 8,
		},
		{
			// Control: a local-to-local bind of a FRESH array, clean before
			// this change. If it moves, the admission widened past the field
			// read it is scoped to.
			name: "fresh_local_bind_unchanged",
			src: strarrBindShareDecl + `function round(i: i32): i32 {
    let src: string[] = mkv(i);
    let tt: string[] = src;
    let p: P = P { f: tt, n: i };
    return (p.f.len() + p.f[0].len() + p.n) % 101;
}` + strarrBindPlainMain,
			want: 71,
		},
		{
			// An ELEMENT of `tt` escapes the frame. The holders' deep
			// free would dangle it, so the read stays marked.
			name: "refused_element_escapes",
			src: strarrBindShareDecl + `function grab(i: i32): string {
    let q: P = P { f: mkv(i), n: i };
    let tt: string[] = q.f;
    let p: P = P { f: tt, n: i };
    return tt[0];
}
function round(i: i32): i32 {
    let want: i32 = w("a").len();
    let s: string = grab(i);
    let junk: i32 = churn(i * 3 + 1);
    if (s.len() != want) { return 0 - 1; }
    return (s.len() + junk) % 101;
}` + strarrBindChurnMain,
			want: 8,
		},
		{
			// The ARRAY itself escapes, so a second holder outlives
			// both structs.
			name: "refused_array_escapes",
			src: strarrBindShareDecl + `function grab(i: i32): string[] {
    let q: P = P { f: mkv(i), n: i };
    let tt: string[] = q.f;
    let p: P = P { f: tt, n: i };
    return tt;
}
function round(i: i32): i32 {
    let want: i32 = w("a").len();
    let xs: string[] = grab(i);
    let junk: i32 = churn(i * 3 + 1);
    if (xs.len() != 2) { return 0 - 1; }
    if (xs[0].len() != want) { return 0 - 2; }
    return (xs[1].len() + junk) % 101;
}` + strarrBindChurnMain,
			want: 8,
		},
		{
			// An element is BOUND to a local and read after churn —
			// the lasting element alias strarr_expr_unsafe exists to catch.
			name: "refused_element_bound",
			src: strarrBindShareDecl + `function round(i: i32): i32 {
    let want: i32 = w("a").len();
    let q: P = P { f: mkv(i), n: i };
    let tt: string[] = q.f;
    let e: string = tt[0];
    let p: P = P { f: tt, n: i };
    let junk: i32 = churn(i * 3 + 1);
    if (e.len() != want) { return 0 - 1; }
    return (e.len() + p.n + junk) % 101;
}` + strarrBindChurnMain,
			want: 40,
		},
		{
			// Was REFUSED, and is not any more. `tt` is rebound by a
			// self-append, which copies the shared buffer; the copy now
			// retains the elements it shares and the superseded buffer is
			// released (#9209), so this balances.
			name: "appended_now_clean",
			src: strarrBindShareDecl + `function round(i: i32): i32 {
    let q: P = P { f: mkv(i), n: i };
    let tt: string[] = q.f;
    tt = tt.append(w("c"));
    let p: P = P { f: tt, n: i };
    return (p.f.len() + p.f[0].len() + q.f.len() + q.n) % 101;
}` + strarrBindPlainMain,
			want: 68,
		},
		{
			// Was REFUSED, and is not any more. `for s in tt` binds an element
			// per iteration, which the walker used to refuse outright; a
			// transient binder no longer takes the array's credit away (see
			// self_host_forin_strarr_binder_test.go), so the share behind it is
			// admitted too and this balances. The exit is 43 before and after —
			// only the accounting moved — and native and interp agree.
			name: "iterated_now_clean",
			src: strarrBindShareDecl + `function round(i: i32): i32 {
    let q: P = P { f: mkv(i), n: i };
    let tt: string[] = q.f;
    let p: P = P { f: tt, n: i };
    let acc: i32 = 0;
    for s in tt { acc = acc + s.len(); }
    return (acc + p.n + q.n) % 101;
}` + strarrBindPlainMain,
			want: 43,
		},
		{
			// `tt` is handed to a call. The borrowable registry is not
			// built at admission time, so every call argument is a hazard —
			// the direction that can only refuse.
			name: "refused_call_argument",
			src: strarrBindShareDecl + `function keep(xs: string[]): i32 { let k: string[] = xs; return k[0].len() + k.len(); }
function round(i: i32): i32 {
    let q: P = P { f: mkv(i), n: i };
    let tt: string[] = q.f;
    let p: P = P { f: tt, n: i };
    let k: i32 = keep(tt);
    return (k + p.n + q.n) % 101;
}` + strarrBindPlainMain,
			want: 72,
		},
	}
}

// TestSelfHostStrArrFieldBindShareX86_64 — a `q.f` string[] read BOUND to a
// local and stored into a string[] field is the same counted share as the
// inline spelling, and every other use of that local still refuses it.
func TestSelfHostStrArrFieldBindShareX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "drivers/asm_ir_run.fern", "driver")

	for _, tc := range strarrBindShareCases() {
		t.Run(tc.name, func(t *testing.T) {
			asm := hevCompile(t, runner, driverBin, tc.src, []string{"FERN_LEAKCHECK=1"})
			progBin := buildBin(t, gcc, dir, "strarrbindshare_"+tc.name, asm)
			stderr, exit := hevRun(t, runner, progBin)
			if exit != tc.want {
				t.Fatalf("%s exited %d, want %d (99 = rc underflow; 100 = a value read back "+
					"wrong, i.e. an over-release; 139 = it read freed memory)", tc.name, exit, tc.want)
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
				t.Errorf("%s: %s — must balance at live_bytes 0", tc.name, summary)
			}
		})
	}
}
