package e2ecompiler

import (
	"testing"
)

// --- An array-of-structs from a producer that returns a LOCAL ----------------
//
// The arrstruct twin of #7335. `let g: Val[] = mk(..)` takes the reclaim credit
// a fresh array is owed whether `mk` returns a literal or a local it built by
// self-append — the form a producer that computes its elements has to take:
//
//	function mk(i: i32): Val[] { return [Val { .. }, Val { .. }]; }
//	function mk(i: i32): Val[] { let vals: Val[] = [];
//	                             vals = vals.append(Val { .. });
//	                             return vals; }
//
// Uncredited, the consumer's exit release takes the shallow buffer dec and
// strands every element box and every element ARRAY field. No struct literal at
// the call site is needed — `let src: Val[] = mk(i); return src.len() +
// src[0].k;` is enough.
//
// A RETURNED container's co-owner would be a local of the frame being left, so
// the producer must append fresh struct LITERALS, never a bare ident;
// producer_bare_ident_elem pins that refusal: it stays a safe leak rather than
// becoming an over-release.
//
// Every want below was confirmed against bin/fern -interp, never read off the
// self-host run under test.

type arrstructProdCase struct {
	name    string
	src     string
	want    int
	balance bool // assert allocs == frees at live_bytes 0
}

const arrstructProdMain = "\nfunction main(): i32 { let t: i32 = 0; let i: i32 = 0; " +
	"while (i < 200) { t = t + round(i); i = i + 1; } " +
	"if (__rc_underflow_count() != 0) { return 99; } return t % 83; }"

const arrstructProdDecl = "struct Val { kids: i32[], k: i32 }\n"

func arrstructProdCases() []arrstructProdCase {
	return []arrstructProdCase{
		{
			// The repro: the producer builds by self-append and returns the local.
			name: "producer_returns_local",
			src: arrstructProdDecl + `function mk(i: i32): Val[] { let vals: Val[] = []; vals = vals.append(Val { kids: [i, i + 1], k: i }); vals = vals.append(Val { kids: [i + 2], k: i }); return vals; }
function round(i: i32): i32 { let v: Val[] = mk(i); return v.len() + v[0].k; }` + arrstructProdMain,
			want: 48, balance: true,
		},
		{
			// The same producer returning the literal directly — admitted before
			// this change, and the diff that isolated the cause.
			name: "producer_returns_literal",
			src: arrstructProdDecl + `function mk(i: i32): Val[] { return [Val { kids: [i, i + 1], k: i }, Val { kids: [i + 2], k: i }]; }
function round(i: i32): i32 { let v: Val[] = mk(i); return v.len() + v[0].k; }` + arrstructProdMain,
			want: 48, balance: true,
		},
		{
			// No producer at all: the literal bound straight into the local. Always
			// credited; must stay so.
			name: "literal_init",
			src: arrstructProdDecl + `function round(i: i32): i32 { let v: Val[] = [Val { kids: [i, i + 1], k: i }, Val { kids: [i + 2], k: i }]; return v.len() + v[0].k; }` +
				arrstructProdMain,
			want: 48, balance: true,
		},
		{
			// THE OVER-RELEASE GUARD, the shape #7335 recorded as the one a careless
			// widening breaks: two same-named `v`, one from the producer and one a
			// bare alias of a parameter. The arrstruct credit is site-keyed already,
			// so the alias cannot inherit it — asserted, not assumed. 99 here would
			// be main's `b` freed under it, and no byte count would say so.
			name: "sibling_alias",
			src: arrstructProdDecl + `function mk(i: i32): Val[] { let vals: Val[] = []; vals = vals.append(Val { kids: [i, i + 1], k: i }); return vals; }
function round(base: Val[], i: i32): i32 {
    let t: i32 = 0;
    if (i % 2 == 0) { let v: Val[] = mk(i);  t = t + v.len() + v[0].k; }
    if (i % 2 == 1) { let v: Val[] = base;   t = t + v.len() + v[0].k; }
    return t;
}
function main(): i32 { let b: Val[] = [Val { kids: [7, 8], k: 9 }]; let t: i32 = 0; let i: i32 = 0; while (i < 100) { t = t + round(b, i); i = i + 1; } if (__rc_underflow_count() != 0) { return 99; } return t % 83; }`,
			want: 12,
		},
		{
			// STRICTNESS, refused deliberately: the appended element is a bare IDENT,
			// so the returned container's counted co-owner is a local of the frame
			// being left. Admitting it would free `e`'s box twice. Stays a safe leak.
			name: "producer_bare_ident_elem",
			src: arrstructProdDecl + `function mk(i: i32): Val[] { let e: Val = Val { kids: [i, i + 1], k: i }; let vals: Val[] = []; vals = vals.append(e); return vals; }
function round(i: i32): i32 { let v: Val[] = mk(i); return v.len() + v[0].k; }` + arrstructProdMain,
			want: 14,
		},
		{
			// Refused: a reassignment that is not a self-store. `vals` may hold
			// another producer's structure by the return, which this frame does not
			// own outright.
			name: "producer_foreign_rebind",
			src: arrstructProdDecl + `function other(i: i32): Val[] { return [Val { kids: [i], k: i }]; }
function mk(i: i32): Val[] { let vals: Val[] = []; vals = vals.append(Val { kids: [i, i + 1], k: i }); if (i % 3 == 0) { vals = other(i); } return vals; }
function round(i: i32): i32 { let v: Val[] = mk(i); return v.len() + v[0].k; }` + arrstructProdMain,
			want: 14,
		},
		{
			// Refused: the local escapes by a route other than the return, so the
			// callee cannot promise the caller owns it outright.
			name: "producer_local_escapes",
			src: arrstructProdDecl + `function sink(vs: Val[]): i32 { return vs.len(); }
function mk(i: i32): Val[] { let vals: Val[] = []; vals = vals.append(Val { kids: [i, i + 1], k: i }); let n: i32 = sink(vals); return vals; }
function round(i: i32): i32 { let v: Val[] = mk(i); return v.len() + v[0].k; }` + arrstructProdMain,
			want: 14,
		},
	}
}

// TestSelfHostArrStructProducerX86_64 — an array-of-structs from a local-returning
// producer is reclaimed, and neither a same-named sibling nor a refused producer
// shape starts over-releasing.
func TestSelfHostArrStructProducerX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "drivers/asm_ir_run.fern", "driver")

	for _, tc := range arrstructProdCases() {
		t.Run(tc.name, func(t *testing.T) {
			asm := hevCompile(t, runner, driverBin, tc.src, []string{"FERN_LEAKCHECK=1"})
			progBin := buildBin(t, gcc, dir, "arrstructprod_"+tc.name, asm)
			stderr, exit := hevRun(t, runner, progBin)
			if exit != tc.want {
				t.Fatalf("%s exited %d, want %d (99 = rc underflow: a share the "+
					"producer registry admitted without owning it)", tc.name, exit, tc.want)
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
			if tc.balance && (live != 0 || allocs != frees) {
				t.Errorf("%s: %s — must balance at live_bytes 0. Each element box and "+
					"its kids array are two thirds of the allocations, so a withheld "+
					"deep walk shows as frees at one third of allocs", tc.name, summary)
			}
		})
	}
}

// TestSelfHostArrStructProducerWasmIR — the wasm sibling. Exit codes only: an
// over-release moves no byte count on any backend.
func TestSelfHostArrStructProducerWasmIR(t *testing.T) {
	cli := buildSelfHostCLI(t)
	for _, tc := range arrstructProdCases() {
		t.Run(tc.name, func(t *testing.T) {
			src := tc.src
			for _, target := range []string{"wasm32-wasi"} {
				if stderr, code := cli.exitOf(t, src, target); code != tc.want {
					t.Errorf("%s on %s exited %d, want %d\n%s", tc.name, target, code, tc.want, stderr)
				}
			}
		})
	}
}
