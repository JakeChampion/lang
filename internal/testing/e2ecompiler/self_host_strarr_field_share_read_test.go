package e2ecompiler

import (
	"testing"
)

// --- A string[] FIELD READ handed into a string[] field ------------------------
//
// `let p: P = P { f: q.f, n: i }` — the RewriteCtx shape, string[] flavour, and
// a cell of the construction-retain matrix (#5338). The construction RETAINS an
// array field, so the new holder co-owns a COUNTED reference and its drop's dec
// balances against it, while the SOURCE holder keeps its own release — a share
// needs both ends alive to balance. `local_store_unchanged` is the same store
// from a local; the hoisted spelling (`let tt = q.f; P { f: tt }`) is covered
// with its own refusals in self_host_strarr_field_bind_share_test.go.
//
// THE FAILURE MODE HERE IS AN OVER-RELEASE, not a leak: one box under two
// rc-aware decs frees on the first and dangles on the second. So the
// `escaping_holder*` rows are the essential cases rather than a formality: each
// returns a holder that outlives the frame and reads every element back after
// 200 rounds of churn have recycled the freelist. Every row balances.
//
// Every want was confirmed against `bin/fern -interp`, and every row was run
// under FERN_SANITIZE=1 with FERN_RC_UNDERFLOW_TRAP=1 and FERN_RC_FREE_DEBUG=1:
// clean, no trap, no quarantine hit.

const strarrShareReadDecl = `struct P { f: string[], n: i32 }
function w(a: string): string { return a + "-past-the-sso-inline-threshold"; }
function mkv(i: i32): string[] { let o: string[] = []; o = o.append(w("a")); o = o.append(w("b")); return o; }
`

func strarrShareReadCases() []arrenumShareCase {
	loop := `
function main(): i32 {
    let t: i32 = 0; let i: i32 = 0;
    while (i < 100) { t = t + round(i); i = i + 1; }
    if (__rc_underflow_count() != 0) { return 99; }
    return t % 97;
}`
	return []arrenumShareCase{
		{
			// The cell. 800/300 before, 800/800 now.
			name: "inline_field_share",
			src: strarrShareReadDecl + `function round(i: i32): i32 {
    let q: P = P { f: mkv(i), n: i };
    let p: P = P { f: q.f, n: i };
    return (p.f.len() + p.f[0].len() + p.n + q.n) % 101;
}` + loop,
			want: 72,
		},
		{
			// Control: the bare-ident store, already clean before this change.
			// If this ever moves, the admission widened somewhere it should not.
			name: "local_store_unchanged",
			src: strarrShareReadDecl + `function round(i: i32): i32 {
    let src: string[] = mkv(i);
    let p: P = P { f: src, n: i };
    return (p.f.len() + p.f[0].len() + p.n) % 101;
}` + loop,
			want: 71,
		},
		{
			// The HOISTED spelling, closed by the local-BIND admission in
			// self_host_strarr_field_bind_share_test.go, which owns the shape
			// and the rows refusing everything around it. Kept here as the
			// pair: the two programs differ only in whether the read is named,
			// so they must agree — on the exit AND on the accounting.
			name: "hoisted_bind_now_clean",
			src: strarrShareReadDecl + `function round(i: i32): i32 {
    let q: P = P { f: mkv(i), n: i };
    let tt: string[] = q.f;
    let p: P = P { f: tt, n: i };
    return (p.f.len() + p.f[0].len() + p.n + q.n) % 101;
}` + loop,
			want: 72,
		},
		{
			// THE SOUNDNESS CASE. The source holder dies inside `make`
			// while the target is returned, and every element is read back
			// after churn has recycled the freelist. An over-release here
			// returns -1 or -2 (exit 100) or segfaults (139); native and
			// interp both exit 8.
			name: "escaping_holder",
			src: strarrShareReadDecl + strarrEscapingChurn + `function make(i: i32): P { let q: P = P { f: mkv(i), n: i }; let p: P = P { f: q.f, n: i }; return p; }
` + strarrEscapingRound + strarrEscapingMain,
			want: 8,
		},
		{
			// The field is read into TWO holders and one is returned. Three
			// counts on the buffer; the source's drop and the second literal's
			// drop each take one inside `make`, and the caller's deep drop is
			// the last owner. Balances, and reads back after churn.
			name: "escaping_holder_read_twice",
			src: strarrShareReadDecl + strarrEscapingChurn + `function make(i: i32): P {
    let q: P = P { f: mkv(i), n: i };
    let p2: P = P { f: q.f, n: i + 1 };
    let p: P = P { f: q.f, n: i + p2.f.len() - 2 };
    return p;
}
` + strarrEscapingRound + strarrEscapingMain,
			want: 8,
		},
		{
			// The SOURCE is returned on one path and the target on the
			// other, so both holders escape `make`. Exit 8 on every engine.
			name: "escaping_holder_source_returned",
			src: strarrShareReadDecl + strarrEscapingChurn + `function make(i: i32): P {
    let q: P = P { f: mkv(i), n: i };
    let p: P = P { f: q.f, n: i };
    if (i % 2 == 0) { return q; }
    return p;
}
` + strarrEscapingRound + strarrEscapingMain,
			want: 8,
		},
		{
			// The source is a PARAMETER the caller keeps reading after the
			// returned holder has been dropped. The read is admitted — the
			// literal retained it, so the caller's drop decs to the parameter's
			// own count, and the caller's `q` is reclaimed too. Exit 76 on every
			// engine, and `q.f[1]` reads back intact.
			name: "escaping_holder_param_source",
			src: strarrShareReadDecl + strarrEscapingChurn + `function make(q: P, i: i32): P { return P { f: q.f, n: i }; }
function round(i: i32): i32 {
    let want: i32 = w("a").len();
    let q: P = P { f: mkv(i), n: i };
    let p: P = make(q, i);
    let junk: i32 = churn(i * 3 + 1);
    if (p.f.len() != 2) { return 0 - 1; }
    if (p.f[0].len() != want) { return 0 - 2; }
    if (q.f[1].len() != want) { return 0 - 3; }
    return (p.f[1].len() + q.f.len() + junk) % 101;
}` + strarrEscapingMain,
			want: 76,
		},
		{
			// A local holder SHADOWS a parameter of the same name, and the
			// returned box is still reclaimed. Exit 76.
			name: "escaping_holder_shadowed_holder",
			src: strarrShareReadDecl + strarrEscapingChurn + `function make(q: P, i: i32): P {
    let q: P = P { f: mkv(i), n: i };
    let p: P = P { f: q.f, n: i };
    return p;
}
function round(i: i32): i32 {
    let want: i32 = w("a").len();
    let q0: P = P { f: mkv(i + 7), n: i };
    let p: P = make(q0, i);
    let junk: i32 = churn(i * 3 + 1);
    if (p.f.len() != 2) { return 0 - 1; }
    if (p.f[0].len() != want) { return 0 - 2; }
    return (p.f[1].len() + q0.f.len() + junk) % 101;
}` + strarrEscapingMain,
			want: 76,
		},
		{
			// An ELEMENT is bound inside `make` before the share, then read
			// back after churn.
			name: "escaping_holder_element_bound",
			src: strarrShareReadDecl + strarrEscapingChurn + `function make(i: i32): P {
    let q: P = P { f: mkv(i), n: i };
    let e: string = q.f[0];
    let p: P = P { f: q.f, n: e.len() };
    return p;
}
` + strarrEscapingRound + strarrEscapingMain,
			want: 8,
		},
	}
}

// The escaping-holder rows share a caller that drops the returned holder only
// after churn has recycled the freelist, then reads every element back.
const strarrEscapingChurn = `function churn(i: i32): i32 { let a: string[] = mkv(i); let b: string[] = mkv(i + 1); return a[0].len() + b[1].len(); }
`

const strarrEscapingRound = `function round(i: i32): i32 {
    let want: i32 = w("a").len();
    let p: P = make(i);
    let junk: i32 = churn(i * 3 + 1);
    if (p.f.len() != 2) { return 0 - 1; }
    if (p.f[0].len() != want) { return 0 - 2; }
    return (p.f[1].len() + junk) % 101;
}`

const strarrEscapingMain = `
function main(): i32 {
    let t: i32 = 0; let i: i32 = 0; let bad: i32 = 0;
    while (i < 200) { let r: i32 = round(i); if (r < 0) { bad = bad + 1; } t = t + r; i = i + 1; }
    if (bad > 0) { return 100; }
    if (__rc_underflow_count() != 0) { return 99; }
    return t % 83;
}`

// TestSelfHostStrArrFieldShareReadX86_64 — a bare `q.f` string[] read handed into
// a string[] field is a counted share, and both holders keep their reclaim.
func TestSelfHostStrArrFieldShareReadX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "drivers/asm_ir_run.fern", "driver")

	for _, tc := range strarrShareReadCases() {
		t.Run(tc.name, func(t *testing.T) {
			asm := hevCompile(t, runner, driverBin, tc.src, []string{"FERN_LEAKCHECK=1"})
			progBin := buildBin(t, gcc, dir, "strarrshareread_"+tc.name, asm)
			stderr, exit := hevRun(t, runner, progBin)
			if exit != tc.want {
				t.Fatalf("%s exited %d, want %d (99 = rc underflow; 100 = an element read back "+
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
