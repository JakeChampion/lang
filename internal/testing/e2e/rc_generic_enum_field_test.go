package e2e

import (
	"fmt"
	"testing"
)

const genericEnumFieldGrowSrc = `struct Inner { s: string }
struct Acc {
    buf: string,
    o_str: Option[string],
    o_i32: Option[i32],
    o_arr: Option[i32[]],
    o_struct: Option[Inner],
    o_nest: Option[Option[string]],
    r: Result[string, i32],
    n: i32,
}

function fresh(): Acc {
    return Acc {
        buf: "",
        o_str: Some("payload-string-long-enough-to-heap"),
        o_i32: Some(7),
        o_arr: Some([1, 2, 3]),
        o_struct: Some(Inner { s: "inner-string-long-enough-to-heap" }),
        o_nest: Some(Some("nested-string-long-enough-to-heap")),
        r: Ok("ok-string-long-enough-to-heap-allocate"),
        n: 0,
    };
}

function (a: Acc) grow(piece: string): Acc {
    return Acc { ...a, buf: a.buf + piece, n: a.n + 1 };
}

function main(): i32 {
    let a: Acc = fresh();
    let i: i32 = 0;
    while (i < 2000) {
        a = a.grow("ab");
        i = i + 1;
    }
    if (a.buf.len() != 4000) { return 1; }
    if (a.n != 2000) { return 2; }
    match (a.o_str) { Some(s) => { if (s.len() != 34) { return 3; } }, None => { return 4; } }
    match (a.o_i32) { Some(v) => { if (v != 7) { return 5; } }, None => { return 6; } }
    match (a.o_arr) { Some(xs) => { if (xs.len() != 3) { return 7; } }, None => { return 8; } }
    match (a.o_struct) { Some(v) => { if (v.s.len() != 32) { return 9; } }, None => { return 10; } }
    match (a.o_nest) {
        Some(o) => { match (o) { Some(s) => { if (s.len() != 33) { return 11; } }, None => { return 12; } } },
        None => { return 13; }
    }
    match (a.r) { Ok(s) => { if (s.len() != 38) { return 14; } }, Err(_) => { return 15; } }
    return 0;
}`

const genericEnumFieldAliasSrc = `struct B { buf: string, tag: Option[string], n: i32 }

function heap(s: string): string { return s + ""; }

function main(): i32 {
    let b: B = B { buf: heap("0123456789abcdefghij"), tag: Some("t"), n: 1 };
    let al: B = b;
    b = B { ...b, buf: b.buf + "X", n: b.n + 1 };
    print(al.buf);
    print(b.buf);
    al = B { ...al, buf: al.buf + "Y", n: al.n + 7 };
    print(al.buf);
    print(b.buf);
    if (al.n != 8) { return 1; }
    if (b.n != 2) { return 2; }
    return 0;
}`

const genericEnumFieldAliasWant = `0123456789abcdefghij
0123456789abcdefghijX
0123456789abcdefghijY
0123456789abcdefghijX`

func TestX86_64GenericEnumFieldAppendAllocsBounded(t *testing.T) {

	stdout, stderr, code := runLeakCheckX86_64(t, genericEnumFieldGrowSrc)
	if code != 0 {
		t.Fatalf("accumulator exited %d (a check inside it failed); stdout=%q stderr=%q", code, stdout, stderr)
	}
	allocs, frees, live := parseLeakCheckLine(t, stderr)
	if allocs > 3000 {
		t.Errorf("allocs = %d for 2000 field appends on a struct with generic-enum fields, "+
			"want well under 3000; the type is not reaching the owned model", allocs)
	}
	if allocs != frees || live != 0 {
		t.Errorf("heap unbalanced: allocs=%d frees=%d live_bytes=%d", allocs, frees, live)
	}
}

func TestX86_64GenericEnumFieldAliasedBoxIsNotMutated(t *testing.T) {

	stdout, stderr, code := runLeakCheckX86_64(t, genericEnumFieldAliasSrc)
	if code != 0 {
		t.Fatalf("exited %d, want 0; stderr=%q", code, stderr)
	}
	if stdout != genericEnumFieldAliasWant+"\n" {
		t.Errorf("x86-64 aliased-box append =\n%q\nwant\n%q", stdout, genericEnumFieldAliasWant+"\n")
	}
	allocs, frees, live := parseLeakCheckLine(t, stderr)
	if allocs != frees || live != 0 {
		t.Errorf("heap unbalanced: allocs=%d frees=%d live_bytes=%d", allocs, frees, live)
	}
}

func TestArm64GenericEnumFieldDropsAreBalanced(t *testing.T) {

	stdout, stderr, code := runLeakCheckArm64(t, genericEnumFieldGrowSrc)
	if code != 0 {
		t.Fatalf("accumulator exited %d; stdout=%q stderr=%q", code, stdout, stderr)
	}
	allocs, frees, live := parseLeakCheckLine(t, stderr)
	if allocs != frees || live != 0 {
		t.Errorf("heap unbalanced: allocs=%d frees=%d live_bytes=%d", allocs, frees, live)
	}
}

func TestArm64GenericEnumFieldAliasedBoxIsNotMutated(t *testing.T) {

	stdout, stderr, code := runLeakCheckArm64(t, genericEnumFieldAliasSrc)
	if code != 0 {
		t.Fatalf("exited %d, want 0; stderr=%q", code, stderr)
	}
	if stdout != genericEnumFieldAliasWant+"\n" {
		t.Errorf("arm64 aliased-box append =\n%q\nwant\n%q", stdout, genericEnumFieldAliasWant+"\n")
	}
}

func TestWASMGenericEnumFieldAppendCorrect(t *testing.T) {

	if code := runWasm(t, genericEnumFieldGrowSrc); code != 0 {
		t.Errorf("wasm accumulator exited %d, want 0", code)
	}
}

func TestWASMGenericEnumFieldAliasedBoxIsNotMutated(t *testing.T) {

	if got := runWasmCapturingStdout(t, genericEnumFieldAliasSrc); got != genericEnumFieldAliasWant {
		t.Errorf("wasm aliased-box append =\n%q\nwant\n%q", got, genericEnumFieldAliasWant)
	}
}

func TestOptionOfMapStaysOutOfTheOwnedModel(t *testing.T) {

	src := func(n int) string {
		return fmt.Sprintf(`import "core/map";

struct H { buf: string, m: Option[Map[string, i32]], n: i32 }

function (h: H) grow(s: string): H {
    return H { ...h, buf: h.buf + s, n: h.n + 1 };
}

function main(): i32 {
    let mm: Map[string, i32] = map_new(8);
    mm = mm.insert("k", 3);
    let h: H = H { buf: "", m: Some(mm), n: 0 };
    let i: i32 = 0;
    while (i < %d) { h = h.grow("cccc"); i = i + 1; }
    if (h.buf.len() != %d) { return 1; }
    if (h.n != %d) { return 2; }
    match (h.m) { Some(m) => { if (m.len() != 1) { return 3; } }, None => { return 4; } }
    return 0;
}`, n, n*4, n)
	}

	measure := func(n int) (unpaired int64, live int64) {
		t.Helper()
		_, stderr, code := runLeakCheckX86_64(t, src(n))
		if code != 0 {
			t.Fatalf("Option[Map] program (%d appends) exited %d, want 0; stderr=%q", n, code, stderr)
		}
		allocs, frees, liveBytes := parseLeakCheckLine(t, stderr)
		return allocs - frees, liveBytes
	}

	shortUnpaired, shortLive := measure(50)
	longUnpaired, longLive := measure(200)
	if shortUnpaired != longUnpaired || shortLive != longLive {
		t.Errorf("loss scales with the append count: 50 appends stranded %d allocations / %d bytes, "+
			"200 stranded %d / %d — Option[Map] has reached the owned model",
			shortUnpaired, shortLive, longUnpaired, longLive)
	}
}
