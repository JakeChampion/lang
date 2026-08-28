package e2eselfhost

import (
	"strings"
	"testing"
)

// The rc-tuple credit learns the string-fresh registry at a CALL element
// (#7374): `var v: (i32, string) = (1, w("p"))` with w a
// str_fresh_ret_fns_of-registered producer now earns the "TUPRC:"/"TUPRCS:"
// credits (tuple_str_elem_fresh_reg at both gates — tuple_lit_has_rc_child
// and tuple_arg_payload_retained), so the scope-exit sweep frees the string
// box AND the tuple box where before NOTHING was freed (600/0, 72 B/round
// unbounded). `.to_string()` was the one call form the syntactic predicate
// accepted, which is what proved the whole downstream path already worked.
//
// The registry's own fixpoint keeps an aliased producer (`id(s) { return s; }`
// and every param/field return) out — the refused pin below asserts that shape
// stays a safe leak, never a release. The sole-owner flavour
// (tuple_arg_payload_fresh: an Option's tuple payload, an array-of-tuples
// element) deliberately keeps the syntactic admission; #7374 records the
// scope split.
//
// Exits confirmed against native x86-64. Each case re-runs under
// FERN_SANITIZE=1 (identical exit, no over-release / use-after-free).

func tupleStrCallElemCases() []tupleAliasParamCase {
	return []tupleAliasParamCase{
		{
			// The issue's repro verbatim: 200 rounds, was allocs=600 frees=0
			// live=14400 (native 200/200/0), now balanced.
			name: "inline_call_string_elem",
			src: `function w(a: string): string { return a + "!"; }
function round(i: i32): i32 { var v: (i32, string) = (1, w("p")); return v.1.len(); }
function main(): i32 {
    var t: i32 = 0; var i: i32 = 0;
    while (i < 200) { t = t + round(i); i = i + 1; }
    if (__rc_underflow() != 0) { return 99; }
    return t % 83;
}`,
			want: 68, balance: true,
		},
		{
			// Freed-block-reuse net: same-size string churn between sweeps and
			// a kept string read back by CONTENT at the end — a sweep that
			// freed the wrong buffer surfaces here or in the sanitize leg.
			name: "churn_read_back",
			src: `function w(a: string): string { return a + "!"; }
function round(i: i32): i32 {
    var v: (i32, string) = (1, w("abcdefgh"));
    var churn: string = w("zzzzzzzz");
    return v.1.len() + churn.len() + i % 3;
}
function main(): i32 {
    var keep: string = w("keepmeeee");
    var t: i32 = 0; var r: i32 = 0;
    while (r < 200) { t = t + round(r); r = r + 1; }
    var ok: i32 = 0;
    if (keep == "keepmeeee!") { ok = 1; }
    if (__rc_underflow() != 0) { return 99; }
    return (t + ok) % 97;
}`,
			want: 17, balance: true,
		},
		{
			// The rebind flavour: every assignment rebuilds the same shape
			// (all_assigns_fresh_rc_tuple through the widened predicates), so
			// the assign-site deep drop releases each superseded chain and the
			// sweep takes the last — exercising the emit-side
			// tuple_str_elem_fresh_reg arm in emit_tuple_child_drops too.
			name: "rebind_call_string_elem",
			src: `function w(a: string): string { return a + "!"; }
function round(i: i32): i32 {
    var v: (i32, string) = (1, w("p"));
    var j: i32 = 0;
    while (j < 3) { v = (j, w("qr")); j = j + 1; }
    return v.1.len() + i % 3;
}
function main(): i32 {
    var t: i32 = 0; var r: i32 = 0;
    while (r < 100) { t = t + round(r); r = r + 1; }
    if (__rc_underflow() != 0) { return 99; }
    return t % 97;
}`,
			want: 11, balance: true,
		},
		{
			// REFUSED: a producer that returns its parameter is exactly what
			// the registry's fixpoint exists to exclude — `id(q)` at the
			// element aliases the live local `q`, so crediting the tuple
			// would put q's box under the blind type-driven str_free. The
			// shape must stay a leak (native reclaims it via dup-at-extract;
			// the self-host's safe floor is recorded on #7374), and the
			// sanitize leg must stay silent.
			name: "aliased_producer_refused",
			src: `function id(s: string): string { return s; }
function w(a: string): string { return a + "!"; }
function round(i: i32): i32 { var q: string = w("q"); var v: (i32, string) = (1, id(q)); return v.1.len() + q.len(); }
function main(): i32 {
    var t: i32 = 0; var i: i32 = 0;
    while (i < 200) { t = t + round(i); i = i + 1; }
    if (__rc_underflow() != 0) { return 99; }
    return t % 83;
}`,
			want: 53, balance: false, wantFrees: 0,
		},
	}
}

func TestSelfHostTupleStrCallElemX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_ir_run.fern", "driver")

	for _, tc := range tupleStrCallElemCases() {
		t.Run(tc.name, func(t *testing.T) {
			asm := hevCompile(t, runner, driverBin, tc.src, []string{"FERN_LEAKCHECK=1"})
			progBin := buildBin(t, gcc, dir, "tupstrcall_"+tc.name, asm)
			stderr, exit := hevRun(t, runner, progBin)
			if exit != tc.want {
				t.Fatalf("%s exited %d, want %d (99 = rc underflow; 139 = read freed memory)", tc.name, exit, tc.want)
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
			if tc.balance {
				if live != 0 || allocs != frees {
					t.Errorf("%s: %s — must balance at live_bytes 0", tc.name, summary)
				}
			} else {
				if live == 0 {
					t.Errorf("%s: %s — a refused alias shape came back clean; the admission widened past the registry's fixpoint, which is the double-free direction", tc.name, summary)
				}
				if frees != tc.wantFrees {
					t.Errorf("%s: frees=%d, want %d — a moved count on a refused row is a silent widening or regression", tc.name, frees, tc.wantFrees)
				}
			}

			sanAsm := hevCompile(t, runner, driverBin, tc.src, []string{"FERN_SANITIZE=1"})
			sanBin := buildBin(t, gcc, dir, "tupstrcall_san_"+tc.name, sanAsm)
			sanErr, sanExit := hevRun(t, runner, sanBin)
			if sanExit != tc.want {
				t.Fatalf("%s sanitize leg exited %d, want %d (124 = fatal sanitizer check)", tc.name, sanExit, tc.want)
			}
			if strings.Contains(sanErr, "rc over-release") || strings.Contains(sanErr, "use-after-free") {
				t.Fatalf("%s sanitize leg reported:\n%s", tc.name, sanErr)
			}
		})
	}
}
