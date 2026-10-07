package e2ecompiler

import (
	"testing"
)

// TestSelfHostStrArrElemReclaimIRArm64 is the arm64 port of the #4355 string[]
// ELEMENT reclaim (x86 sibling: TestSelfHostStrArrElemReclaimIRX86_64). Under
// qemu the reclaim is proven by CORRECTNESS (a wrong free of a live element box
// corrupts the read-back / ticks the underflow detector → 99) plus a balanced
// census. Heavy heap-exhaustion churn is left to the x86 path (too slow under
// qemu).
func TestSelfHostStrArrElemReclaimIRArm64(t *testing.T) {
	gcc, qemu := arm64Tooling(t)
	cli := newStrictCLI(t)

	run := func(t *testing.T, prog, name string, want int) {
		t.Helper()
		asm := cli.emit(t, "arm64-linux", "import \"std/i32\";\n"+prog, "FERN_LEAKCHECK=1")
		if code := arm64Census(t, gcc, qemu, name, asm); code != want {
			t.Errorf("%s exited %d, want %d (99 = over-release)", name, code, want)
		}
	}

	// RECLAIM SHAPE + VALUE: fresh-element string[] (literal + concats, sanctioned
	// self-append) over 5000 build/drop cycles — the element walk must free each
	// element exactly once (a double free ticks the underflow detector → 99) and
	// the transient reads stay correct. lens 3+3+4 = 10.
	run(t, `function build(pre: string): i32 { let xs: string[] = ["lit", pre + "c"]; xs = xs.append(pre + "de"); let tl: i32 = 0; let j: i32 = 0; while (j < xs.len()) { tl = tl + xs[j].len(); j = j + 1; } return tl; }
function churn(n: i32): i32 { let pre: string = "ab"; let bad: i32 = 0; let i: i32 = 0; while (i < n) { if (build(pre) != 10) { bad = 1; } i = i + 1; } return bad; }
function main(): i32 { let v: i32 = churn(5000); if (__rc_underflow_count() != 0) { return 99; } return v; }`,
		"strarr-elem-reclaim-arm64", 0)

	// LOOP-BODY REINIT (#4353 item 4): a string[] re-DECLARED each iteration is
	// freed at the loop REBIND, not at a helper exit. Correctness +
	// over-release proof under qemu (the x86 sibling carries the
	// heap-exhaustion / bounded-high-water leg). xs[1]="abx" (3) + xs[2]="abyy"
	// (4) = 7 each iteration; a UAF from an early element free would read garbage
	// (bad=1) or tick the underflow detector (99). underflow 0 + value 7 → 0.
	run(t, `function churn(n: i32): i32 { let pre: string = "ab"; let bad: i32 = 0; let i: i32 = 0; while (i < n) { let xs: string[] = ["lit", pre + "x", pre + "yy"]; if (xs[1].len() + xs[2].len() != 7) { bad = 1; } i = i + 1; } return bad; }
function main(): i32 { let v: i32 = churn(1000); if (__rc_underflow_count() != 0) { return 99; } return v; }`,
		"strarr-elem-reinit-loop-arm64", 0)

	// ELEMENT ALIAS BINDING: `let t = xs[0]` must read valid bytes and nothing
	// double-frees. 3+2 = 5.
	run(t, `function pick(pre: string): i32 { let xs: string[] = [pre + "x", "qq"]; let t: string = xs[0]; return t.len() + xs[1].len(); }
function churn(n: i32): i32 { let pre: string = "ab"; let bad: i32 = 0; let i: i32 = 0; while (i < n) { if (pick(pre) != 5) { bad = 1; } i = i + 1; } return bad; }
function main(): i32 { let v: i32 = churn(1000); if (__rc_underflow_count() != 0) { return 99; } return v; }`,
		"strarr-elem-alias-excluded-arm64", 0)

	// PRODUCER-CALL ELEMENT: the stored elements are calls to a fresh-string
	// producer rather than inline concats, and the array owns each one.
	// Correctness + over-release under qemu (the x86 sibling carries the
	// flatness leg). 43 + 3 + 43 = 89 each build.
	run(t, `function w(pre: string): string { return pre + "-a-wide-element-past-the-inline-threshold"; }
function build(pre: string): i32 { let xs: string[] = [w(pre), "lit"]; xs = xs.append(w(pre)); let tl: i32 = 0; let j: i32 = 0; while (j < xs.len()) { tl = tl + xs[j].len(); j = j + 1; } return tl; }
function churn(n: i32): i32 { let pre: string = "ab"; let bad: i32 = 0; let i: i32 = 0; while (i < n) { if (build(pre) != 89) { bad = 1; } i = i + 1; } return bad; }
function main(): i32 { let v: i32 = churn(1000); if (__rc_underflow_count() != 0) { return 99; } return v; }`,
		"strarr-elem-producer-store-arm64", 0)

	// LOCAL BOUND FROM A PRODUCER: `let xs = mk(pre)` where `mk` is a "STRARR:"
	// registry function — the frame owns every element the callee handed it, so
	// the exit sweep may element-walk. `mk`'s own `out` escapes by return and
	// keeps the shallow dec, so each element is freed exactly once. 3 + 43 = 46.
	run(t, `function w(pre: string): string { return pre + "-a-wide-element-past-the-inline-threshold"; }
function mk(pre: string): string[] { let out: string[] = []; let i: i32 = 0; while (i < 3) { out = out.append(w(pre)); i = i + 1; } return out; }
function build(pre: string): i32 { let xs: string[] = mk(pre); return xs.len() + xs[1].len(); }
function churn(n: i32): i32 { let pre: string = "ab"; let bad: i32 = 0; let i: i32 = 0; while (i < n) { if (build(pre) != 46) { bad = 1; } i = i + 1; } return bad; }
function main(): i32 { let v: i32 = churn(1000); if (__rc_underflow_count() != 0) { return 99; } return v; }`,
		"strarr-local-from-producer-arm64", 0)

	// STORED BY THE CALLEE: `keep` stores its array parameter into a struct, so
	// the construction counts the store and the caller's claim survives the
	// call; only the owner that finds rc 1 walks the elements. Both reads stay
	// valid. 3 + 43 + 43 = 89.
	run(t, `struct Box { rows: string[] }
function w(pre: string): string { return pre + "-a-wide-element-past-the-inline-threshold"; }
function mk(pre: string): string[] { let out: string[] = []; let i: i32 = 0; while (i < 3) { out = out.append(w(pre)); i = i + 1; } return out; }
function keep(xs: string[]): Box { return Box { rows: xs }; }
function build(pre: string): i32 { let xs: string[] = mk(pre); let b: Box = keep(xs); return b.rows.len() + b.rows[0].len() + xs[2].len(); }
function churn(n: i32): i32 { let pre: string = "ab"; let bad: i32 = 0; let i: i32 = 0; while (i < n) { if (build(pre) != 89) { bad = 1; } i = i + 1; } return bad; }
function main(): i32 { let v: i32 = churn(1000); if (__rc_underflow_count() != 0) { return 99; } return v; }`,
		"strarr-local-stored-by-callee-counted-arm64", 0)

	// The same store where the holder ESCAPES the frame that owns the array —
	// the shape the case above was written against, and the one that can
	// actually fail. `build` returns the Box, so the retain is still live when
	// `xs` sweeps: the walk runs, finds rc 2, and decs without touching an
	// element. Every element is read back AFTER 20 churn frames have recycled
	// the freelist; a wrong walk returns 100, a double free 99. One junk element
	// goes through ids so the churn's array is built on the heap rather than
	// placed as a constant.
	run(t, `struct Box { rows: string[] }
function w(pre: string): string { return pre + "-a-wide-element-past-the-inline-threshold"; }
function mk(pre: string): string[] { let out: string[] = []; let i: i32 = 0; while (i < 3) { out = out.append(w(pre)); i = i + 1; } return out; }
function keep(xs: string[]): Box { return Box { rows: xs }; }
function build(pre: string): Box { let xs: string[] = mk(pre); let b: Box = keep(xs); return b; }
@noinline function ids(s: string): string { return s; }
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
		"strarr-local-callee-holder-escapes-arm64", 8)

	// SELF-`.with` REBIND (#6407): the in-place element store releases the
	// superseded box and retains the stored value, which makes the rebind
	// admissible to the credit. `v` is another ELEMENT, so without the retain
	// the exit walk would free one box through two slots → 99. 8 + 23 + 23 = 54.
	run(t, `function mks(pre: string): string[] { let out: string[] = []; let i: i32 = 0; while (i < 8) { out = out.append(pre + "kkkkkkkkkkkkkkkkkkkk" + i.to_string()); i = i + 1; } return out; }
function build(pre: string): i32 { let a: string[] = mks(pre); a = a.with(3, a[5]); return a.len() + a[3].len() + a[5].len(); }
function churn(n: i32): i32 { let pre: string = "ab"; let bad: i32 = 0; let i: i32 = 0; while (i < n) { if (build(pre) != 54) { bad = 1; } i = i + 1; } return bad; }
function main(): i32 { let v: i32 = churn(1000); if (__rc_underflow_count() != 0) { return 99; } return v; }`,
		"strarr-with-rebind-arm64", 0)
}
