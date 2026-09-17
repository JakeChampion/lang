package e2e

import (
	"testing"

	"github.com/jakechampion/lang/internal/ast"
)

// #8785 — the in-place string append through a STRUCT FIELD. The lowering
// decision is pinned in internal/ir/rc_str_field_append_test.go; these pin
// what the emitted runtime does, and above all that the uniqueness gate is
// the BOX's and not the string's.

// strFieldAppendAliasSrc is the test that separates a correct fix from the
// tempting wrong one.
//
// The struct's box is aliased (rc 2) while its buffer is not: only the box
// holds a reference to `buf`, so the STRING is at rc 1 and a gate on the
// string's uniqueness would grow it in place — mutating a value the
// second alias still reads through the old box, which the non-unique arm
// leaves pointing at it.
//
// Both alias and original then append, so the corruption is visible on the
// two-word ABI as well as the single-word one: on the single-word ABI the
// shared [rc][len] header makes the alias read the longer string straight
// away, and on the two-word one — where the alias carries its own length —
// the SECOND append overwrites the byte the first one wrote, so `b` reads
// back the alias's tail.
//
// Two details make the program discriminating rather than accidentally
// green, and both were found by mutating the fix and watching this test still
// pass. The accumulator starts as a HEAP string (`heap()`) because
// __fern_str_append refuses to grow a .rodata literal whatever the rc says.
// And twenty bytes plus one keeps the grown length inside the same allocator
// class, which is the other condition for growing in place: a growth that
// crossed a class boundary would take the copy path for a reason that has
// nothing to do with the gate under test.
const strFieldAppendAliasSrc = `struct B { buf: string, n: i32 }

function heap(s: string): string { return s + ""; }

function main(): i32 {
    var b: B = B { buf: heap("0123456789abcdefghij"), n: 1 };
    var al: B = b;
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
    var c: B = B { buf: heap("jihgfedcba9876543210"), n: 0 };
    var held: string = c.buf;
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

// strFieldAppendGrowSrc is the accumulator itself: 2000 two-byte appends into
// a struct field, the shape that was quadratic. It also exercises the pieces
// around the append — a second replaced pointer field (whose displaced value
// step 4 still deep-drops), a carried field, appending the empty string, and
// the field appended to itself.
const strFieldAppendGrowSrc = `struct Acc { buf: string, xs: i32[], n: i32 }

function grow(n: i32, piece: string): Acc {
    var a: Acc = Acc { buf: "", xs: [0], n: 0 };
    var i: i32 = 0;
    while (i < n) {
        a = Acc { ...a, buf: a.buf + piece, xs: [i], n: a.n + 1 };
        i = i + 1;
    }
    return a;
}

function main(): i32 {
    var a: Acc = grow(2000, "ab");
    if (a.buf.len() != 4000) { return 1; }
    if (a.n != 2000) { return 2; }
    if (a.xs[0] != 1999) { return 3; }
    var e: Acc = grow(3, "");
    if (e.buf.len() != 0) { return 4; }
    var s: Acc = Acc { buf: "abcdefgh", xs: [1], n: 0 };
    s = Acc { ...s, buf: s.buf + s.buf };
    if (s.buf != "abcdefghabcdefgh") { return 5; }
    return 0;
}`

// TestX86_64StrFieldAppendAliasedBoxIsNotMutated is the important one: it
// fails if the in-place field append is gated on anything but the box's own
// runtime uniqueness.
func TestX86_64StrFieldAppendAliasedBoxIsNotMutated(t *testing.T) {
	prev := ast.RcFreeEnabled
	ast.RcFreeEnabled = true
	defer func() { ast.RcFreeEnabled = prev }()

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

// The two-word (wasm) sibling. strAppendAvailable covers ptrW==4, so the
// field load fans out to (data, len) and the helper consumes both words.
func TestWASMStrFieldAppendAliasedBoxIsNotMutated(t *testing.T) {
	prev := ast.RcFreeEnabled
	ast.RcFreeEnabled = true
	defer func() { ast.RcFreeEnabled = prev }()

	if got := runWasmCapturingStdout(t, strFieldAppendAliasSrc); got != strFieldAppendAliasWant {
		t.Errorf("wasm aliased-box field append =\n%q\nwant\n%q", got, strFieldAppendAliasWant)
	}
}

// The two-word NATIVE sibling, where the pair is carried in registers.
func TestArm64StrFieldAppendAliasedBoxIsNotMutated(t *testing.T) {
	prev := ast.RcFreeEnabled
	ast.RcFreeEnabled = true
	defer func() { ast.RcFreeEnabled = prev }()

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

// TestX86_64StrFieldAppendAllocsBounded pins the collapse. Measured on this
// program: 8022 allocations before, 275 after — the before figure is one box
// and one whole-buffer copy per append, which is the quadratic.
//
// The assertions are the invariants rather than the exact numbers: well under
// one allocation per append, and a balanced heap at exit, which catches an
// over-release as firmly as a leak.
func TestX86_64StrFieldAppendAllocsBounded(t *testing.T) {
	prev := ast.RcFreeEnabled
	ast.RcFreeEnabled = true
	defer func() { ast.RcFreeEnabled = prev }()

	stdout, stderr, code := runLeakCheckX86_64(t, strFieldAppendGrowSrc)
	if code != 0 {
		t.Fatalf("field-append accumulator exited %d (a check inside it failed); stdout=%q stderr=%q", code, stdout, stderr)
	}
	allocs, frees, live := parseLeakCheckLine(t, stderr)
	// 2000 appends over 4000 bytes cross ~250 16-byte classes; the 2000
	// boxes collapse to one reused box. Anything near 4000 means the site
	// went back to a fresh box and a full copy per update.
	if allocs > 3000 {
		t.Errorf("allocs = %d for 2000 field appends, want well under 3000; the in-place field append is not firing", allocs)
	}
	if allocs != frees || live != 0 {
		t.Errorf("heap unbalanced after the field-append loop: allocs=%d frees=%d live_bytes=%d", allocs, frees, live)
	}
}

// The arm64 half of the collapse, on the same program: without the helper the
// site was not placeable at all, so every update allocated a fresh box AND
// copied the whole buffer.
func TestArm64StrFieldAppendAllocsBounded(t *testing.T) {
	prev := ast.RcFreeEnabled
	ast.RcFreeEnabled = true
	defer func() { ast.RcFreeEnabled = prev }()

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

// The same program on wasm, for the answers rather than the counts.
func TestWASMStrFieldAppendCorrect(t *testing.T) {
	prev := ast.RcFreeEnabled
	ast.RcFreeEnabled = true
	defer func() { ast.RcFreeEnabled = prev }()

	if code := runWasm(t, strFieldAppendGrowSrc); code != 0 {
		t.Errorf("wasm field-append accumulator exited %d, want 0", code)
	}
}

// strFieldAppendChainSrc is the field accumulator written as a CHAIN — the
// shape a record-update builder takes when it emits a separator in the same
// statement. The pieces here cover what the fold has to get right: a box
// aliased across a chained update (the buffer must not move), a chain whose
// operands are owned temps, a chain reading the field again from the right
// (which must decline to fuse and still answer correctly), and the empty
// piece.
const strFieldAppendChainSrc = `struct Acc { buf: string, xs: i32[], n: i32 }

function heap(s: string): string { return s + ""; }

function grow(n: i32, piece: string): Acc {
    var a: Acc = Acc { buf: "", xs: [0], n: 0 };
    var i: i32 = 0;
    while (i < n) {
        a = Acc { ...a, buf: a.buf + piece + ",", xs: [i], n: a.n + 1 };
        i = i + 1;
    }
    return a;
}

function main(): i32 {
    var a: Acc = grow(1000, "ab");
    if (a.buf.len() != 3000) { return 1; }
    if (a.n != 1000) { return 2; }
    if (a.xs[0] != 999) { return 3; }
    var e: Acc = grow(3, "");
    if (e.buf.len() != 3) { return 4; }

    var b: Acc = Acc { buf: heap("0123456789abcdefghij"), xs: [0], n: 0 };
    var al: Acc = b;
    b = Acc { ...b, buf: b.buf + "X" + "Y" };
    print(al.buf);
    print(b.buf);

    var c: Acc = Acc { buf: heap("jihgfedcba9876543210"), xs: [0], n: 0 };
    var held: string = c.buf;
    c = Acc { ...c, buf: c.buf + "Z" + "W" };
    print(held);
    print(c.buf);

    var d: Acc = Acc { buf: "ab", xs: [0], n: 0 };
    d = Acc { ...d, buf: d.buf + "-" + d.buf };
    print(d.buf);
    return 0;
}`

const strFieldAppendChainWant = `0123456789abcdefghij
0123456789abcdefghijXY
jihgfedcba9876543210
jihgfedcba9876543210ZW
ab-ab`

// TestX86_64StrFieldAppendChainCorrect covers all four shapes at once on the
// single-word ABI, under the leak detector so an over-release shows as
// frees > allocs. The aliased-box and aliased-buffer lines are the ones that
// separate a correct fold from one that grew a buffer someone else reads.
func TestX86_64StrFieldAppendChainCorrect(t *testing.T) {
	prev := ast.RcFreeEnabled
	ast.RcFreeEnabled = true
	defer func() { ast.RcFreeEnabled = prev }()

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

// TestArm64StrFieldAppendChainCorrect is the two-word NATIVE sibling.
func TestArm64StrFieldAppendChainCorrect(t *testing.T) {
	prev := ast.RcFreeEnabled
	ast.RcFreeEnabled = true
	defer func() { ast.RcFreeEnabled = prev }()

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

// TestWASMStrFieldAppendChainCorrect is the wasm sibling.
func TestWASMStrFieldAppendChainCorrect(t *testing.T) {
	prev := ast.RcFreeEnabled
	ast.RcFreeEnabled = true
	defer func() { ast.RcFreeEnabled = prev }()

	want := strFieldAppendChainWant
	if got := runWasmCapturingStdout(t, strFieldAppendChainSrc); got != want {
		t.Errorf("wasm chained field append =\n%q\nwant\n%q", got, want)
	}
}

// strFieldAppendChainAllocSrc is the chain alone, sized so the allocation
// count is the whole signal: 500 iterations of three joins growing to 3000
// bytes.
const strFieldAppendChainAllocSrc = `struct B { buf: string, n: i32 }
function main(): i32 {
    var b: B = B { buf: "", n: 0 };
    var i: i32 = 0;
    while (i < 500) {
        b = B { ...b, buf: b.buf + "a" + "bb" + "ccc", n: b.n + 1 };
        i = i + 1;
    }
    if (b.buf.len() != 3000) { return 1; }
    return 0;
}`

// TestX86_64StrFieldAppendChainAllocsBounded pins the collapse: allocs=130
// frees=130 live_bytes=0, the same as the single-join form over the same 3000
// bytes. Only the leftmost join used to grow the field, so the two above it
// allocated and copied the accumulator every iteration and cost 1128 here.
func TestX86_64StrFieldAppendChainAllocsBounded(t *testing.T) {
	prev := ast.RcFreeEnabled
	ast.RcFreeEnabled = true
	defer func() { ast.RcFreeEnabled = prev }()

	stdout, stderr, code := runLeakCheckX86_64(t, strFieldAppendChainAllocSrc)
	if code != 0 {
		t.Fatalf("chained field append loop exited %d (want 0 — the accumulated length was wrong); stdout=%q stderr=%q", code, stdout, stderr)
	}
	allocs, frees, live := parseLeakCheckLine(t, stderr)
	if allocs > 250 {
		t.Errorf("allocs = %d for 500 three-join iterations, want <= 250 (~one per size-class step); a join is allocating and copying instead of growing", allocs)
	}
	if allocs != frees || live != 0 {
		t.Errorf("heap unbalanced after the chained field append: allocs=%d frees=%d live_bytes=%d, want allocs==frees and live_bytes==0", allocs, frees, live)
	}
}
