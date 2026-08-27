package e2eselfhost

import (
	"testing"
)

// --- A COUNTED param position is not a move ---------------------------------
//
// param_counted_of already answers the question these five construction-retain
// `__param` cells turn on: is every appearance of the callee's parameter a
// COUNTED store or a non-retaining read? Its verdict was reachable only from the
// argument-temp stash, which holds the sigs. The ESCAPE walker threads the
// borrowability registry and nothing else, so a NAMED local passed at a counted
// position read as a plain escape, earned no reclaim credit, and was never
// released at all — a constant 2-object leak whatever the loop count.
//
// The asm says it without inference. In `round(src: string, i: i32)` the callee
// emits __fern_rc_inc on src before boxing the holder and __struct_drop_P at
// exit; `main` emits no release of `keep` on any path. Both sides are internally
// consistent and neither owns the caller's original reference.
//
// The fix folds the verdict into the borrow registry under a "CNT:" key prefix.
// It is the only tier on that registry that ADMITS where the box flag refuses —
// "TUPB:" and "ELB:" both narrow it — because it asks a different question: not
// whether the callee keeps a reference, but whether a reference it does keep was
// RETAINED. A retained one leaves the caller's claim intact.
//
// SCOPE, stated rather than implied: this closes str__param and str_arr__param.
// The enum, enum-array and struct-array `__param` cells are not counted by any
// tier — param_counted_of admits `string` and scalar-element arrays plus
// `string[]`, by type — and widening it to a class whose release WALKS element
// boxes is the deep-release question "ELB:" exists for, with its own proof to
// write. Those three stay pinned as leaks.
//
// ON THE LOAD-BEARING CHECK, measured rather than asserted: replacing the "CNT:"
// lookup with a blanket admission of every bare-ident call argument does NOT
// break any case below, nor the rc suites — it merely closes enum__param too.
// So these probes do NOT separate the tier from the blanket. The lookup is kept
// because the blanket asserts something no analysis established, and because
// refusing is the leak-safe floor for this family; the silence below is not a
// proof that the blanket is sound.
//
// Every want was confirmed against BOTH oracles — bin/fern -interp and the
// native x86-64 backend agreed on each — never read off the self-host run.

type countedParamCase struct {
	name    string
	src     string
	want    int
	balance bool // assert allocs == frees at live_bytes 0
}

func countedParamCases() []countedParamCase {
	return []countedParamCase{
		{
			// The repro. The callee stores the param into a struct-literal field, which
			// RETAINS it and gives the retain back at the holder's drop; the caller's
			// own claim was never released. 102 allocs / 100 frees before, native 101/101.
			name: "counted_arg_string",
			src: `struct P { f: string, n: i32 }
function w(a: string): string { return a + "-past-the-sso-inline-threshold"; }
function mkv(i: i32): string { var s: string = w("k"); return s; }

function round(src: string, i: i32): i32 {
    var p: P = P { f: src, n: i };
    return (p.f.len() + p.n) % 101;
}
function main(): i32 {
    var keep: string = mkv(7);
    var t: i32 = 0;
    var r: i32 = 0;
    while (r < 100) { t = t + round(keep, r); t = t + 0; r = r + 1; }
    t = (t + 0) % 97;
    if (__rc_underflow_count() != 0) { return 99; }
    return t % 97;
}`,
			want: 73, balance: true,
		},
		{
			// The same at the DEEP class. The callee's store retains the BUFFER, so
			// __fern_str_arr_free's rc gate leaves the element walk to whichever owner
			// reaches rc 1. 106/102 before, native 104/104.
			name: "counted_arg_strarr",
			src: `struct P { f: string[], n: i32 }
function w(a: string): string { return a + "-past-the-sso-inline-threshold"; }
function mkv(i: i32): string[] { var o: string[] = []; o = o.append(w("a")); o = o.append(w("b")); return o; }

function round(src: string[], i: i32): i32 {
    var p: P = P { f: src, n: i };
    return (p.f.len() + p.f[0].len() + p.n) % 101;
}
function main(): i32 {
    var keep: string[] = mkv(7);
    var t: i32 = 0;
    var r: i32 = 0;
    while (r < 100) { t = t + round(keep, r); t = t + 0; r = r + 1; }
    t = (t + 0) % 97;
    if (__rc_underflow_count() != 0) { return 99; }
    return t % 97;
}`,
			want: 71, balance: true,
		},
		{
			// REFUSED: a callee that hands the param back is not counted
			// (arrparam_use_ok's ExprIdent arm credits only a counted position, and
			// str_result_cannot_alias refuses a string result outright).
			name: "callee_returns_param",
			src: `function w(a: string): string { return a + "-past-the-sso-inline-threshold"; }
function mkv(i: i32): string { var s: string = w("k"); return s; }
// hands the param straight back — the caller must NOT get a release credit
function esc(src: string, i: i32): string { return src; }
function main(): i32 {
    var keep: string = mkv(7);
    var t: i32 = 0;
    var r: i32 = 0;
    while (r < 100) { var o: string = esc(keep, r); t = t + o.len(); r = r + 1; }
    // churn: overwrite anything freed under us
    var c: i32 = 0;
    while (c < 200) { var junk: string = w("zzzz"); t = t + junk.len(); c = c + 1; }
    // read keep back AFTER the churn — a dangling keep shows up here
    t = t + keep.len() * 1000;
    if (__rc_underflow_count() != 0) { return 99; }
    return t % 97;
}`,
			want: 63, balance: false,
		},
		{
			// The same callee, read back BY BYTES after 200 churn allocations. The
			// census cannot see a use-after-free; this returns 88 if keep's bytes moved.
			name: "callee_returns_param_readback",
			src: `function w(a: string): string { return a + "-past-the-sso-inline-threshold"; }
function mkv(i: i32): string { var s: string = w("k"); return s; }
function esc(src: string, i: i32): string { return src; }
function bytesum(s: string): i32 {
    var acc: i32 = 0;
    var i: i32 = 0;
    while (i < s.len()) { acc = acc + (s[i] as i32); i = i + 1; }
    return acc;
}
function main(): i32 {
    var keep: string = mkv(7);
    var before: i32 = bytesum(keep);
    var t: i32 = 0;
    var r: i32 = 0;
    while (r < 100) { var o: string = esc(keep, r); t = t + o.len(); r = r + 1; }
    var c: i32 = 0;
    while (c < 200) { var junk: string = w("zzzz"); t = t + junk.len(); c = c + 1; }
    var after: i32 = bytesum(keep);
    if (after != before) { return 88; }
    if (__rc_underflow_count() != 0) { return 99; }
    return (t + before) % 97;
}`,
			want: 45, balance: false,
		},
		{
			// REFUSED: the param is stored into a struct the callee RETURNS, so the
			// share outlives the call. The caller keeps leaking rather than releasing
			// under the holder.
			name: "param_in_returned_struct",
			src: `struct P { f: string, n: i32 }
function w(a: string): string { return a + "-past-the-sso-inline-threshold"; }
function mkv(i: i32): string { var s: string = w("k"); return s; }
// stores the param into a RETURNED struct: the counted-store escaping path
function mk(src: string, i: i32): P { return P { f: src, n: i }; }
function main(): i32 {
    var keep: string = mkv(7);
    var t: i32 = 0;
    var r: i32 = 0;
    while (r < 100) { var h: P = mk(keep, r); t = t + h.f.len() + h.n; r = r + 1; }
    var c: i32 = 0;
    while (c < 200) { var junk: string = w("zzzz"); t = t + junk.len(); c = c + 1; }
    t = t + keep.len() * 1000;
    if (__rc_underflow_count() != 0) { return 99; }
    return t % 97;
}`,
			want: 66, balance: false,
		},
		{
			// REFUSED through the onward position: arrparam_use_ok credits an argument
			// only when the callee's own parameter is counted, and esc2 hands it back.
			name: "onward_pass_to_handback",
			src: `function w(a: string): string { return a + "-past-the-sso-inline-threshold"; }
function mkv(i: i32): string { var s: string = w("k"); return s; }
function esc2(s: string): string { return s; }
// passes the param ONWARD to a callee that hands it back
function outer(src: string, i: i32): i32 { var o: string = esc2(src); return o.len(); }
function main(): i32 {
    var keep: string = mkv(7);
    var t: i32 = 0;
    var r: i32 = 0;
    while (r < 100) { t = t + outer(keep, r); r = r + 1; }
    var c: i32 = 0;
    while (c < 200) { var junk: string = w("zzzz"); t = t + junk.len(); c = c + 1; }
    t = t + keep.len() * 1000;
    if (__rc_underflow_count() != 0) { return 99; }
    return t % 97;
}`,
			want: 63, balance: false,
		},
		{
			// REFUSED for string[]: an element read is not a counted use — the tier
			// declines ExprIndex for arrays precisely because the element may BE a
			// reference handed out uncounted. Reads the element back after churn.
			name: "callee_extracts_element",
			src: `struct P { f: string[], n: i32 }
function w(a: string): string { return a + "-past-the-sso-inline-threshold"; }
function mkv(i: i32): string[] { var o: string[] = []; o = o.append(w("a")); o = o.append(w("b")); return o; }
// hands an ELEMENT out — the caller's deep walk must not free it under the result
function el(src: string[], i: i32): string { return src[0]; }
function main(): i32 {
    var keep: string[] = mkv(7);
    var t: i32 = 0;
    var r: i32 = 0;
    while (r < 100) { var e: string = el(keep, r); t = t + e.len(); r = r + 1; }
    var c: i32 = 0;
    while (c < 200) { var junk: string = w("zzzz"); t = t + junk.len(); c = c + 1; }
    t = t + keep.len() * 1000 + keep[0].len() * 10;
    if (__rc_underflow_count() != 0) { return 99; }
    return t % 97;
}`,
			want: 85, balance: false,
		},
		{
			// REFUSED: the string[] reaches a returned struct. Reads both the array
			// header and element 0 back after churn.
			name: "strarr_in_returned_struct",
			src: `struct P { f: string[], n: i32 }
function w(a: string): string { return a + "-past-the-sso-inline-threshold"; }
function mkv(i: i32): string[] { var o: string[] = []; o = o.append(w("a")); o = o.append(w("b")); return o; }
// string[] stored into a RETURNED struct, then read back after churn
function mk(src: string[], i: i32): P { return P { f: src, n: i }; }
function main(): i32 {
    var keep: string[] = mkv(7);
    var t: i32 = 0;
    var r: i32 = 0;
    while (r < 100) { var h: P = mk(keep, r); t = t + h.f.len() + h.f[0].len() + h.n; r = r + 1; }
    var c: i32 = 0;
    while (c < 200) { var junk: string = w("zzzz"); t = t + junk.len(); c = c + 1; }
    t = t + keep.len() * 1000 + keep[0].len() * 10;
    if (__rc_underflow_count() != 0) { return 99; }
    return t % 97;
}`,
			want: 94, balance: false,
		},
	}
}

// TestSelfHostCountedParamReleaseX86_64 — a local passed at a counted parameter
// position keeps the scope-exit release its own creation owes, and every callee
// that could let the value outlive the call keeps refusing it.
func TestSelfHostCountedParamReleaseX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_ir_run.fern", "driver")

	for _, tc := range countedParamCases() {
		t.Run(tc.name, func(t *testing.T) {
			asm := hevCompile(t, runner, driverBin, tc.src, []string{"FERN_LEAKCHECK=1"})
			progBin := buildBin(t, gcc, dir, "countedparam_"+tc.name, asm)
			stderr, exit := hevRun(t, runner, progBin)
			if exit != tc.want {
				t.Fatalf("%s exited %d, want %d (88 = the value read back wrong after "+
					"churn; 99 = rc underflow; 139 = it read freed memory)", tc.name, exit, tc.want)
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
			if frees > allocs {
				t.Errorf("%s: %s — more frees than allocs is a double free", tc.name, summary)
			}
			if tc.balance && (live != 0 || allocs != frees) {
				t.Errorf("%s: %s — must balance at live_bytes 0", tc.name, summary)
			}
		})
	}
}
