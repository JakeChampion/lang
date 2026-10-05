package e2eselfhost

import (
	"os/exec"
	"testing"
)

// TestSelfHostStrArrElemReclaimIRX86_64 pins the #4355 string[] ELEMENT reclaim
// slice: a non-escaping string[] local the frame solely owns — every stored
// element provably fresh (a concat, a proven producer call) or a static
// literal, or the whole array handed over by a "STRARR:" producer — is credited
// "SARR:" by reclaimable_names_of, and the exit sweep frees it with
// __fern_str_arr_free —
// the element-walking sibling of the shallow array dec (rc==1: __fern_str_free
// every element box, then the buffer; rc>1: dec; rc<0: skip; rc==0: underflow
// detector). Anything the element-hazard walk cannot prove keeps the shallow
// buffer-only dec (elements leak — sound).
//
// The reclaim is proven by a BOUNDED HIGH-WATER assertion (__heap_bump_bytes()
// stays flat across a second 5000-iteration churn — element leaks grow it by
// ~150 B/iter; the only admitted slack is the churn frame's own literal box, a
// known pre-existing gap measured at 24 B/call), and a double-free by the
// over-release detector (__rc_underflow_count() → 99).
func TestSelfHostStrArrElemReclaimIRX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	l := newStdlibLoader(t)
	dir := t.TempDir()

	run := func(t *testing.T, prog, name string, want int) {
		t.Helper()
		asm := []byte(l.emit(t, prog))
		if len(asm) == 0 {
			t.Fatalf("%s: self-host compiler emitted 0 bytes", name)
		}
		bin := buildBin(t, gcc, dir, name, string(asm))
		var cmd *exec.Cmd
		if len(runner) == 0 {
			cmd = exec.Command(bin)
		} else {
			cmd = exec.Command(runner[0], append(runner[1:], bin)...)
		}
		_ = cmd.Run()
		if code := cmd.ProcessState.ExitCode(); code != want {
			t.Errorf("%s exited %d, want %d (98 = heap bump grew → elements not reclaimed; 99 = over-release; 97 = value corrupted)", name, code, want)
		}
	}

	// RECLAIM, BOUNDED HIGH-WATER: xs is built from a literal + fresh concats
	// (a literal element's box is freed, its .rodata data heap-guard-skipped)
	// and grown via the sanctioned self-append rebind; elements are only read
	// transiently (xs[j].len()). Every build() exit frees 3 element boxes +
	// their heap buffers + the array buffer, so after a 5000-iteration warmup a
	// second 5000-iteration churn re-serves every allocation from the freelist:
	// the bump high-water moves < 256 B (measured: 24 B — churn's own literal
	// box, a pre-existing gap). Without the element walk it grows ~750 KB → 98.
	// A double-free ticks the underflow detector → 99. acc value checked (97).
	run(t, `function build(pre: string): i32 { let xs: string[] = ["lit", pre + "c"]; xs = xs.append(pre + "de"); let tl: i32 = 0; let j: i32 = 0; while (j < xs.len()) { tl = tl + xs[j].len(); j = j + 1; } return tl; }
function churn(n: i32): i32 { let pre: string = "ab"; let acc: i32 = 0; let i: i32 = 0; while (i < n) { acc = (acc + build(pre)) % 251; i = i + 1; } return acc; }
function main(): i32 { let w: i32 = churn(5000); let b1: i32 = (__heap_bump_bytes() as i32); let x: i32 = churn(5000); let b2: i32 = (__heap_bump_bytes() as i32); if (__rc_underflow_count() != 0) { return 99; } if (b2 - b1 >= 256) { return 98; } if (w != x) { return 97; } return 0; }`,
		"strarr-elem-reclaim-flat", 0)

	// LOOP-BODY REINIT, BOUNDED HIGH-WATER (#4353 item 4): a string[] local
	// re-DECLARED each iteration of churn's own loop (not freed at a helper's
	// exit like the flat case above — freed at the loop REBIND). Pre-fix the
	// reinit store took the shallow buffer-only dec and leaked all 3 element
	// boxes + their buffers every iteration; the strarr reinit branch
	// (emit_strarr_reclaim_store) now frees the prior iteration's elements with
	// __fern_str_arr_free before the store, so the second churn re-serves from
	// the freelist and the bump high-water stays flat. Element leaks → 98; a
	// double-free (reinit + exit sweep both freeing the final box) → 99.
	run(t, `function churn(n: i32): i32 { let pre: string = "ab"; let acc: i32 = 0; let i: i32 = 0; while (i < n) { let xs: string[] = ["lit", pre + "x", pre + "yy"]; acc = (acc + xs[0].len() + xs[2].len()) % 251; i = i + 1; } return acc; }
function main(): i32 { let w: i32 = churn(5000); let b1: i32 = (__heap_bump_bytes() as i32); let x: i32 = churn(5000); let b2: i32 = (__heap_bump_bytes() as i32); if (__rc_underflow_count() != 0) { return 99; } if (b2 - b1 >= 256) { return 98; } if (w != x) { return 97; } return 0; }`,
		"strarr-elem-reinit-loop", 0)

	// ELEMENT ALIAS BINDING excludes: `let t = xs[0]` is a lasting element alias
	// the walk can't see through — xs must stay on the shallow buffer-only dec,
	// so t reads valid bytes after xs's sweep point and nothing double-frees.
	// lens 3+2 = 5 over 2000 calls, underflow 0 → exit 0.
	run(t, `function pick(pre: string): i32 { let xs: string[] = [pre + "x", "qq"]; let t: string = xs[0]; return t.len() + xs[1].len(); }
function churn(n: i32): i32 { let pre: string = "ab"; let bad: i32 = 0; let i: i32 = 0; while (i < n) { if (pick(pre) != 5) { bad = 1; } i = i + 1; } return bad; }
function main(): i32 { let v: i32 = churn(2000); if (__rc_underflow_count() != 0) { return 99; } return v; }`,
		"strarr-elem-alias-excluded", 0)

	// RETURNED ELEMENT excludes: `return xs[0]` hands an element box to the
	// caller — xs must not element-walk (the shallow dec frees only the buffer,
	// so the returned box stays valid). len 3 over 2000 calls, underflow 0.
	run(t, `function first(pre: string): string { let xs: string[] = [pre + "z"]; return xs[0]; }
function churn(n: i32): i32 { let pre: string = "ab"; let bad: i32 = 0; let i: i32 = 0; while (i < n) { if (first(pre).len() != 3) { bad = 1; } i = i + 1; } return bad; }
function main(): i32 { let v: i32 = churn(2000); if (__rc_underflow_count() != 0) { return 99; } return v; }`,
		"strarr-elem-return-excluded", 0)

	// PRODUCER-CALL ELEMENT, BOUNDED HIGH-WATER: the stored elements are calls
	// to `w`, a whole-program-proven fresh-string producer (str_fresh_ret_fns),
	// rather than inline concats. The credit's element proof is
	// strarr_value_is_fresh, the same question the "STRARR:" producer admission
	// asks, so the registry arm admits them and the exit sweep element-walks.
	// Before that the credit asked a registry-blind sibling that refused any
	// call, and all three element boxes leaked per build → 98.
	run(t, `function w(pre: string): string { return pre + "-a-wide-element-past-the-inline-threshold"; }
function build(pre: string): i32 { let xs: string[] = [w(pre), "lit"]; xs = xs.append(w(pre)); let tl: i32 = 0; let j: i32 = 0; while (j < xs.len()) { tl = tl + xs[j].len(); j = j + 1; } return tl; }
function churn(n: i32): i32 { let pre: string = "ab"; let acc: i32 = 0; let i: i32 = 0; while (i < n) { acc = (acc + build(pre)) % 251; i = i + 1; } return acc; }
function main(): i32 { let w0: i32 = churn(5000); let b1: i32 = (__heap_bump_bytes() as i32); let x: i32 = churn(5000); let b2: i32 = (__heap_bump_bytes() as i32); if (__rc_underflow_count() != 0) { return 99; } if (b2 - b1 >= 256) { return 98; } if (w0 != x) { return 97; } return 0; }`,
		"strarr-elem-producer-store-flat", 0)

	// LOCAL BOUND FROM A PRODUCER, BOUNDED HIGH-WATER: `let xs = mk(pre)` where
	// `mk` is a "STRARR:" registry function — its admission already proved
	// whole-program that every element of the returned array is a box `mk`
	// allocated and handed out at rc=1, so the frame owns them and the exit
	// sweep may element-walk. Previously only an array LITERAL initialiser
	// earned the credit, so this shape took the shallow buffer-only dec and
	// leaked every element → 98. `mk`'s own `out` still escapes by return and
	// keeps the shallow dec, so the elements are freed exactly once.
	run(t, `function w(pre: string): string { return pre + "-a-wide-element-past-the-inline-threshold"; }
function mk(pre: string): string[] { let out: string[] = []; let i: i32 = 0; while (i < 3) { out = out.append(w(pre)); i = i + 1; } return out; }
function build(pre: string): i32 { let xs: string[] = mk(pre); let tl: i32 = 0; let j: i32 = 0; while (j < xs.len()) { tl = tl + xs[j].len(); j = j + 1; } return tl; }
function churn(n: i32): i32 { let pre: string = "ab"; let acc: i32 = 0; let i: i32 = 0; while (i < n) { acc = (acc + build(pre)) % 251; i = i + 1; } return acc; }
function main(): i32 { let w0: i32 = churn(5000); let b1: i32 = (__heap_bump_bytes() as i32); let x: i32 = churn(5000); let b2: i32 = (__heap_bump_bytes() as i32); if (__rc_underflow_count() != 0) { return 99; } if (b2 - b1 >= 256) { return 98; } if (w0 != x) { return 97; } return 0; }`,
		"strarr-local-from-producer-flat", 0)

	// BORROWED CALL ARG stays admitted: `take` only reads its parameter, so it
	// is borrowable and the array does not escape — the credit survives the
	// call and both the callee's reads and the post-call `xs[2]` read see live
	// bytes. 3 + 43 + 43 = 89 over 2000 calls, underflow 0.
	run(t, `function w(pre: string): string { return pre + "-a-wide-element-past-the-inline-threshold"; }
function mk(pre: string): string[] { let out: string[] = []; let i: i32 = 0; while (i < 3) { out = out.append(w(pre)); i = i + 1; } return out; }
function take(xs: string[]): i32 { return xs.len() + xs[0].len(); }
function build(pre: string): i32 { let xs: string[] = mk(pre); let k: i32 = take(xs); return k + xs[2].len(); }
function churn(n: i32): i32 { let pre: string = "ab"; let bad: i32 = 0; let i: i32 = 0; while (i < n) { if (build(pre) != 89) { bad = 1; } i = i + 1; } return bad; }
function main(): i32 { let v: i32 = churn(2000); if (__rc_underflow_count() != 0) { return 99; } return v; }`,
		"strarr-local-borrowed-arg-flat", 0)

	// STORED BY THE CALLEE is ADMITTED, and this case used to pin the opposite.
	// Its premise was that `keep`'s parameter is not borrowable, which is still
	// true and is no longer the whole question: param_counted_of proves every
	// appearance of that parameter is a COUNTED store, so the construction incs
	// the buffer and the caller's claim survives the call. The "CNT:" tier now
	// carries that verdict to the escape walker.
	//
	// Granting the DEEP walk on top of a shallow-release justification is the
	// part that needs stating. Two rules close it from both ends, and neither is
	// this slice's invention: __fern_str_arr_free is rc-gated, so only the owner
	// that finds rc 1 walks the elements at all; and no element can be out
	// UNCOUNTED, because the tier refuses ExprIndex for array params (a callee
	// cannot extract one, nor pass the array onward to a callee that does) while
	// the caller's own element-hazard rules still exclude `let t = xs[0]` — the
	// alias case above.
	//
	// Both the struct read and the direct `xs[2]` read stay valid. 89 over 2000
	// calls, underflow 0.
	run(t, `struct Box { rows: string[] }
function w(pre: string): string { return pre + "-a-wide-element-past-the-inline-threshold"; }
function mk(pre: string): string[] { let out: string[] = []; let i: i32 = 0; while (i < 3) { out = out.append(w(pre)); i = i + 1; } return out; }
function keep(xs: string[]): Box { return Box { rows: xs }; }
function build(pre: string): i32 { let xs: string[] = mk(pre); let b: Box = keep(xs); return b.rows.len() + b.rows[0].len() + xs[2].len(); }
function churn(n: i32): i32 { let pre: string = "ab"; let bad: i32 = 0; let i: i32 = 0; while (i < n) { if (build(pre) != 89) { bad = 1; } i = i + 1; } return bad; }
function main(): i32 { let v: i32 = churn(2000); if (__rc_underflow_count() != 0) { return 99; } return v; }`,
		"strarr-local-stored-by-callee-counted", 0)

	// The same store where the holder ESCAPES the frame that owns the array —
	// the shape the case above was written against, and the one that can
	// actually fail. `build` returns the Box, so the retain is still live when
	// `xs` sweeps: the walk runs, finds rc 2, and decs without touching an
	// element. 500 rounds each read every element back AFTER 20 churn frames
	// have recycled the freelist; a wrong walk returns 100, a double free 99. One
	// junk element goes through ids so the churn's array is built on the heap
	// rather than placed as a constant.
	run(t, `struct Box { rows: string[] }
function w(pre: string): string { return pre + "-a-wide-element-past-the-inline-threshold"; }
function mk(pre: string): string[] { let out: string[] = []; let i: i32 = 0; while (i < 3) { out = out.append(w(pre)); i = i + 1; } return out; }
function keep(xs: string[]): Box { return Box { rows: xs }; }
function build(pre: string): Box { let xs: string[] = mk(pre); let b: Box = keep(xs); return b; }
function ids(s: string): string { return s; }
function churnjunk(i: i32): i32 { let a: string[] = ["zzzz", "yyyy", ids("xxxx")]; return a[0].len() + a[2].len(); }
function round(i: i32): i32 {
    let pre: string = "ab";
    let b: Box = build(pre);
    let j: i32 = 0; let t: i32 = 0;
    while (j < 20) { t = t + churnjunk(j); j = j + 1; }
    let s: i32 = 0; let k: i32 = 0;
    while (k < b.rows.len()) { s = s + b.rows[k].len(); k = k + 1; }
    if (s != 129) { return 0 - 1; }
    return (t + s) % 101;
}
function main(): i32 {
    let t: i32 = 0; let i: i32 = 0; let bad: i32 = 0;
    while (i < 500) { let r: i32 = round(i); if (r < 0) { bad = bad + 1; } t = t + r; i = i + 1; }
    if (bad > 0) { return 100; }
    if (__rc_underflow_count() != 0) { return 99; }
    return t % 83;
}`,
		"strarr-local-callee-holder-escapes", 8)

	// Forwarding transfers the claim: fwd must not free its returned array,
	// while churn must deep-free that result. fwd's `return xs` moves the array
	// out, so a free there would reach churn's release as an underflow (99).
	// Pin churn's release and flat high-water over two identical churns, as
	// well as the returned value and underflows.
	run(t, `function w(pre: string): string { return pre + "-a-wide-element-past-the-inline-threshold"; }
function mk(pre: string): string[] { let out: string[] = []; let i: i32 = 0; while (i < 3) { out = out.append(w(pre)); i = i + 1; } return out; }
function fwd(pre: string): string[] { let xs: string[] = mk(pre); return xs; }
function churn(n: i32): i32 { let pre: string = "ab"; let bad: i32 = 0; let i: i32 = 0; while (i < n) { let r: string[] = fwd(pre); if (r.len() + r[1].len() != 46) { bad = 1; } i = i + 1; } return bad; }
function main(): i32 { let v: i32 = churn(2000); let before = __heap_bump_bytes(); let v2: i32 = churn(2000); let after = __heap_bump_bytes(); if (after != before) { return 98; } if (__rc_underflow_count() != 0) { return 99; } return v + v2; }`,
		"strarr-local-forwarded-return-owned", 0)

	// SELF-`.with` REBIND, BOUNDED HIGH-WATER (#6407): `a = a.with(i, v)` on an
	// owned string[] lowers to an in-place arr_set, which used to drop the
	// overwritten element pointer without releasing it — and, because the rebind was a
	// hazard, cost the array its credit as well, so ALL eight element boxes
	// leaked per round (380 B/round measured). lower_strarr_with_store now
	// releases the superseded box and retains the stored value, which makes the
	// rebind admissible: the sweep element-walks and the loop is flat. `v` is
	// another ELEMENT here, so without the retain the walk would free one box
	// through two slots → 99.
	run(t, `import "std/i32";
function mks(pre: string): string[] { let out: string[] = []; let i: i32 = 0; while (i < 8) { out = out.append(pre + "kkkkkkkkkkkkkkkkkkkk" + i.to_string()); i = i + 1; } return out; }
function build(pre: string): i32 { let a: string[] = mks(pre); a = a.with(3, a[5]); return a.len() + a[3].len() + a[5].len(); }
function churn(n: i32): i32 { let pre: string = "ab"; let acc: i32 = 0; let i: i32 = 0; while (i < n) { acc = (acc + build(pre)) % 251; i = i + 1; } return acc; }
function main(): i32 { let w0: i32 = churn(5000); let b1: i32 = (__heap_bump_bytes() as i32); let x: i32 = churn(5000); let b2: i32 = (__heap_bump_bytes() as i32); if (__rc_underflow_count() != 0) { return 99; } if (b2 - b1 >= 256) { return 98; } if (w0 != x) { return 97; } return 0; }`,
		"strarr-with-rebind-flat", 0)

	// `.with` VALUE IS A LIVE LOCAL: the store retains it, so the array and the
	// local each hold a counted reference — the local reads valid bytes after
	// the store and the element walk decs rather than frees. A long string is
	// built between the store and the reads so a wrongly freed block is really
	// recycled first. 37 + 37 + 23 correct, underflow 0.
	run(t, `import "std/i32";
function mks(pre: string): string[] { let out: string[] = []; let i: i32 = 0; while (i < 8) { out = out.append(pre + "kkkkkkkkkkkkkkkkkkkk" + i.to_string()); i = i + 1; } return out; }
function churn(n: i32): string { let s: string = ""; let i: i32 = 0; while (i < n) { s = s + "0123456789012345678901234567890123456789"; i = i + 1; } return s; }
function build(pre: string): i32 { let xs: string[] = mks(pre); let nm: string = pre + "-a-distinct-live-local-string-value"; xs = xs.with(1, nm); let junk: string = churn(20); if (junk.len() < 0) { return 0; } return nm.len() + xs[1].len() + xs[0].len(); }
function run2(n: i32): i32 { let pre: string = "ab"; let bad: i32 = 0; let i: i32 = 0; while (i < n) { if (build(pre) != 97) { bad = 1; } i = i + 1; } return bad; }
function main(): i32 { let v: i32 = run2(3000); if (__rc_underflow_count() != 0) { return 99; } return v; }`,
		"strarr-with-live-local-value-safe", 0)

	// `.with` SELF-STORE `a.with(i, a[i])`: the release is cow-guarded on the
	// old element differing from the stored value, so the store never frees the
	// pointer it is about to write. 23 correct over 3000 rounds, underflow 0.
	run(t, `import "std/i32";
function mks(pre: string): string[] { let out: string[] = []; let i: i32 = 0; while (i < 8) { out = out.append(pre + "kkkkkkkkkkkkkkkkkkkk" + i.to_string()); i = i + 1; } return out; }
function churn(n: i32): string { let s: string = ""; let i: i32 = 0; while (i < n) { s = s + "0123456789012345678901234567890123456789"; i = i + 1; } return s; }
function build(pre: string): i32 { let xs: string[] = mks(pre); xs = xs.with(3, xs[3]); let junk: string = churn(20); if (junk.len() < 0) { return 0; } return xs[3].len(); }
function run2(n: i32): i32 { let pre: string = "ab"; let bad: i32 = 0; let i: i32 = 0; while (i < n) { if (build(pre) != 23) { bad = 1; } i = i + 1; } return bad; }
function main(): i32 { let v: i32 = run2(3000); if (__rc_underflow_count() != 0) { return 99; } return v; }`,
		"strarr-with-self-store-safe", 0)

	// (A borrowed-element STORE case — `let xs: string[] = [nm]` — cannot be
	// exercised here: a bare-ident element in a string[] literal/append bails
	// the whole module today, so the IR-path collector's
	// element-freshness gate is defence-in-depth for when that subset widens,
	// not a reachable shape.)
}
