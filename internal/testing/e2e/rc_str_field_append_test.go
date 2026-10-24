package e2e

import (
	"testing"
)

const strFieldAppendAliasSrc = `struct B { buf: string, n: i32 }

function heap(s: string): string { return s + ""; }

function main(): i32 {
    let b: B = B { buf: heap("0123456789abcdefghij"), n: 1 };
    let al: B = b;
    b = B { ...b, buf: b.buf + "X", n: b.n + 1 };
    print(al.buf);
    print(b.buf);
    al = B { ...al, buf: al.buf + "Y", n: al.n + 7 };
    print(al.buf);
    print(b.buf);
    if (al.n != 8) { return 1; }
    if (b.n != 2) { return 2; }

    // The BUFFER aliased while the box is unique: the box's own gate says
    // reuse, and the helper's rc test on the buffer is what must then decline
    // the in-place grow.
    let c: B = B { buf: heap("jihgfedcba9876543210"), n: 0 };
    let held: string = c.buf;
    c = B { ...c, buf: c.buf + "Z" };
    print(held);
    print(c.buf);
    return 0;
}`

const strFieldAppendAliasWant = `0123456789abcdefghij
0123456789abcdefghijX
0123456789abcdefghijY
0123456789abcdefghijX
jihgfedcba9876543210
jihgfedcba9876543210Z`

const strFieldAppendGrowSrc = `struct Acc { buf: string, xs: i32[], n: i32 }

function grow(n: i32, piece: string): Acc {
    let a: Acc = Acc { buf: "", xs: [0], n: 0 };
    let i: i32 = 0;
    while (i < n) {
        a = Acc { ...a, buf: a.buf + piece, xs: [i], n: a.n + 1 };
        i = i + 1;
    }
    return a;
}

function main(): i32 {
    let a: Acc = grow(2000, "ab");
    if (a.buf.len() != 4000) { return 1; }
    if (a.n != 2000) { return 2; }
    if (a.xs[0] != 1999) { return 3; }
    let e: Acc = grow(3, "");
    if (e.buf.len() != 0) { return 4; }
    let s: Acc = Acc { buf: "abcdefgh", xs: [1], n: 0 };
    s = Acc { ...s, buf: s.buf + s.buf };
    if (s.buf != "abcdefghabcdefgh") { return 5; }
    return 0;
}`

func TestX86_64StrFieldAppendAliasedBoxIsNotMutated(t *testing.T) {

	stdout, stderr, code := runLeakCheckX86_64(t, strFieldAppendAliasSrc)
	if code != 0 {
		t.Fatalf("exited %d, want 0; stderr=%q", code, stderr)
	}
	if stdout != strFieldAppendAliasWant+"\n" {
		t.Errorf("x86-64 aliased-box field append =\n%q\nwant\n%q", stdout, strFieldAppendAliasWant+"\n")
	}
	allocs, frees, live := parseLeakCheckLine(t, stderr)
	if allocs != frees || live != 0 {
		t.Errorf("heap unbalanced: allocs=%d frees=%d live_bytes=%d, want balanced at 0", allocs, frees, live)
	}
}

func TestWASMStrFieldAppendAliasedBoxIsNotMutated(t *testing.T) {

	if got := runWasmCapturingStdout(t, strFieldAppendAliasSrc); got != strFieldAppendAliasWant {
		t.Errorf("wasm aliased-box field append =\n%q\nwant\n%q", got, strFieldAppendAliasWant)
	}
}

func TestArm64StrFieldAppendAliasedBoxIsNotMutated(t *testing.T) {

	stdout, stderr, code := runLeakCheckArm64(t, strFieldAppendAliasSrc)
	if code != 0 {
		t.Fatalf("exited %d, want 0; stderr=%q", code, stderr)
	}
	if stdout != strFieldAppendAliasWant+"\n" {
		t.Errorf("arm64 aliased-box field append =\n%q\nwant\n%q", stdout, strFieldAppendAliasWant+"\n")
	}
	allocs, frees, live := parseLeakCheckLine(t, stderr)
	if allocs != frees || live != 0 {
		t.Errorf("heap unbalanced: allocs=%d frees=%d live_bytes=%d, want balanced at 0", allocs, frees, live)
	}
}

func TestX86_64StrFieldAppendAllocsBounded(t *testing.T) {

	stdout, stderr, code := runLeakCheckX86_64(t, strFieldAppendGrowSrc)
	if code != 0 {
		t.Fatalf("field-append accumulator exited %d (a check inside it failed); stdout=%q stderr=%q", code, stdout, stderr)
	}
	allocs, frees, live := parseLeakCheckLine(t, stderr)
	if allocs > 3000 {
		t.Errorf("allocs = %d for 2000 field appends, want well under 3000; the in-place field append is not firing", allocs)
	}
	if allocs != frees || live != 0 {
		t.Errorf("heap unbalanced after the field-append loop: allocs=%d frees=%d live_bytes=%d", allocs, frees, live)
	}
}

func TestArm64StrFieldAppendAllocsBounded(t *testing.T) {

	stdout, stderr, code := runLeakCheckArm64(t, strFieldAppendGrowSrc)
	if code != 0 {
		t.Fatalf("field-append accumulator exited %d (a check inside it failed); stdout=%q stderr=%q", code, stdout, stderr)
	}
	allocs, frees, live := parseLeakCheckLine(t, stderr)
	if allocs > 3000 {
		t.Errorf("allocs = %d for 2000 field appends, want well under 3000; the in-place field append is not firing", allocs)
	}
	if allocs != frees || live != 0 {
		t.Errorf("heap unbalanced after the field-append loop: allocs=%d frees=%d live_bytes=%d", allocs, frees, live)
	}
}

func TestWASMStrFieldAppendCorrect(t *testing.T) {

	if code := runWasm(t, strFieldAppendGrowSrc); code != 0 {
		t.Errorf("wasm field-append accumulator exited %d, want 0", code)
	}
}

const strFieldAppendChainSrc = `struct Acc { buf: string, xs: i32[], n: i32 }

function heap(s: string): string { return s + ""; }

function grow(n: i32, piece: string): Acc {
    let a: Acc = Acc { buf: "", xs: [0], n: 0 };
    let i: i32 = 0;
    while (i < n) {
        a = Acc { ...a, buf: a.buf + piece + ",", xs: [i], n: a.n + 1 };
        i = i + 1;
    }
    return a;
}

function main(): i32 {
    let a: Acc = grow(1000, "ab");
    if (a.buf.len() != 3000) { return 1; }
    if (a.n != 1000) { return 2; }
    if (a.xs[0] != 999) { return 3; }
    let e: Acc = grow(3, "");
    if (e.buf.len() != 3) { return 4; }

    let b: Acc = Acc { buf: heap("0123456789abcdefghij"), xs: [0], n: 0 };
    let al: Acc = b;
    b = Acc { ...b, buf: b.buf + "X" + "Y" };
    print(al.buf);
    print(b.buf);

    let c: Acc = Acc { buf: heap("jihgfedcba9876543210"), xs: [0], n: 0 };
    let held: string = c.buf;
    c = Acc { ...c, buf: c.buf + "Z" + "W" };
    print(held);
    print(c.buf);

    let d: Acc = Acc { buf: "ab", xs: [0], n: 0 };
    d = Acc { ...d, buf: d.buf + "-" + d.buf };
    print(d.buf);
    return 0;
}`

const strFieldAppendChainWant = `0123456789abcdefghij
0123456789abcdefghijXY
jihgfedcba9876543210
jihgfedcba9876543210ZW
ab-ab`

func TestX86_64StrFieldAppendChainCorrect(t *testing.T) {

	stdout, stderr, code := runLeakCheckX86_64(t, strFieldAppendChainSrc)
	if code != 0 {
		t.Fatalf("exited %d, want 0; stdout=%q stderr=%q", code, stdout, stderr)
	}
	if stdout != strFieldAppendChainWant+"\n" {
		t.Errorf("x86-64 chained field append =\n%q\nwant\n%q", stdout, strFieldAppendChainWant+"\n")
	}
	if allocs, frees, _ := parseLeakCheckLine(t, stderr); frees > allocs {
		t.Errorf("frees=%d > allocs=%d — the chained field append over-released a buffer", frees, allocs)
	}
}

func TestArm64StrFieldAppendChainCorrect(t *testing.T) {

	stdout, stderr, code := runLeakCheckArm64(t, strFieldAppendChainSrc)
	if code != 0 {
		t.Fatalf("exited %d, want 0; stdout=%q stderr=%q", code, stdout, stderr)
	}
	if stdout != strFieldAppendChainWant+"\n" {
		t.Errorf("arm64 chained field append =\n%q\nwant\n%q", stdout, strFieldAppendChainWant+"\n")
	}
	if allocs, frees, _ := parseLeakCheckLine(t, stderr); frees > allocs {
		t.Errorf("frees=%d > allocs=%d — the chained field append over-released a buffer", frees, allocs)
	}
}

func TestWASMStrFieldAppendChainCorrect(t *testing.T) {

	want := strFieldAppendChainWant
	if got := runWasmCapturingStdout(t, strFieldAppendChainSrc); got != want {
		t.Errorf("wasm chained field append =\n%q\nwant\n%q", got, want)
	}
}

const strFieldAppendChainAllocSrc = `struct B { buf: string, n: i32 }
function main(): i32 {
    let b: B = B { buf: "", n: 0 };
    let i: i32 = 0;
    while (i < 500) {
        b = B { ...b, buf: b.buf + "a" + "bb" + "ccc", n: b.n + 1 };
        i = i + 1;
    }
    if (b.buf.len() != 3000) { return 1; }
    return 0;
}`

func TestX86_64StrFieldAppendChainAllocsBounded(t *testing.T) {

	stdout, stderr, code := runLeakCheckX86_64(t, strFieldAppendChainAllocSrc)
	if code != 0 {
		t.Fatalf("chained field append loop exited %d (want 0 — the accumulated length was wrong); stdout=%q stderr=%q", code, stdout, stderr)
	}
	allocs, frees, live := parseLeakCheckLine(t, stderr)
	if allocs > 300 {
		t.Errorf("allocs = %d for 500 three-join iterations, want <= 300 (~one per size-class step); a join is allocating and copying instead of growing", allocs)
	}
	if allocs != frees || live != 0 {
		t.Errorf("heap unbalanced after the chained field append: allocs=%d frees=%d live_bytes=%d, want allocs==frees and live_bytes==0", allocs, frees, live)
	}
}
