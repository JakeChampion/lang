// The string self-append written through the record-update idiom —
// `b = B { ...b, buf: b.buf + piece }` — is what a struct-held accumulator
// looks like, because E048 forbids field assignment. It used to refuse the
// struct-update reuse path outright (a replaced STRING field is not
// reusePlaceableField), so it lowered through the general StructLit path:
// a fresh box per update and an OpStrConcat that copied the whole
// accumulated buffer, i.e. quadratic (#8785).
//
// These pin the LOWERING decision; internal/e2e/rc_str_field_append_test.go
// pins what the emitted runtime does with it.
package ir_test

import (
	"testing"

	"github.com/jakechampion/lang/internal/ast"
	"github.com/jakechampion/lang/internal/checker"
	"github.com/jakechampion/lang/internal/constfold"
	"github.com/jakechampion/lang/internal/ir"
	"github.com/jakechampion/lang/internal/parser"
)

func strAppendCount(fn *ir.Func) int {
	n := 0
	for _, op := range fn.Ops {
		if op.Kind == ir.OpCallDirect && op.Str == "__fern_str_append" {
			n++
		}
	}
	return n
}

func strConcatCount(fn *ir.Func) int {
	n := 0
	for _, op := range fn.Ops {
		if op.Kind == ir.OpStrConcat {
			n++
		}
	}
	return n
}

// lowerForTestPtrW is lowerForTest at a chosen pointer width — ptrW==4 is the
// wasm shape, where strings are two-word unconditionally.
func lowerForTestPtrW(t *testing.T, src string, ptrW int) *ir.Program {
	t.Helper()
	prog, err := parser.Parse(src)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if err := constfold.Fold(prog, nil); err != nil {
		t.Fatalf("constfold: %v", err)
	}
	info, err := checker.Check(prog)
	if err != nil {
		t.Fatalf("check: %v", err)
	}
	prev := ast.RcFreeEnabled
	ast.RcFreeEnabled = true
	ip, err := ir.LowerWith(prog, info, ptrW)
	ast.RcFreeEnabled = prev
	if err != nil {
		t.Fatalf("lower: %v", err)
	}
	return ip
}

const strFieldAppendSelfOverwriteSrc = `struct B { buf: string, n: i32 }
function main(): i32 {
    var b: B = B { buf: "", n: 0 };
    var i: i32 = 0;
    while (i < 4) { b = B { ...b, buf: b.buf + "ab", n: b.n + 1 }; i = i + 1; }
    return b.buf.len() - 8;
}`

// Fires: the self-overwrite form takes the reuse path (one __alloc_reuse for
// the box) and the field's own append (__fern_str_append), with the plain
// OpStrConcat kept for the decline arm — where p's box is shared and the
// buffer must not move.
func TestStrFieldAppendSelfOverwriteGrowsInPlace(t *testing.T) {
	fn := funcByName(lowerForTest(t, strFieldAppendSelfOverwriteSrc), "main")
	if got := allocReuseCount(fn); got != 1 {
		t.Errorf("self-overwrite with an appended string field should reuse the box, got %d __alloc_reuse", got)
	}
	if got := strAppendCount(fn); got != 1 {
		t.Errorf("got %d __fern_str_append, want 1 — the appended field is still copying", got)
	}
	if got := strConcatCount(fn); got != 1 {
		t.Errorf("got %d OpStrConcat, want 1 (the decline arm's copy)", got)
	}
}

// The same on the two-word ABI: the field load fans out to (data, len) and
// the helper takes both words.
func TestStrFieldAppendSelfOverwriteGrowsInPlaceTwoWord(t *testing.T) {
	fn := funcByName(lowerForTestPtrW(t, strFieldAppendSelfOverwriteSrc, 4), "main")
	if got := strAppendCount(fn); got != 1 {
		t.Errorf("wasm: got %d __fern_str_append, want 1", got)
	}
	if got := allocReuseCount(fn); got != 1 {
		t.Errorf("wasm: got %d __alloc_reuse, want 1", got)
	}
}

// And on arm64 — ptrW 8 with the two-word override, the ABI that carries the
// pair in registers rather than a memory pair. It is its own leg because
// neither of the two above exercises that combination.
func TestStrFieldAppendSelfOverwriteGrowsInPlaceArm64(t *testing.T) {
	prev := ast.TwoWordOverride
	defer func() { ast.TwoWordOverride = prev }()
	ast.TwoWordOverride = true

	fn := funcByName(lowerForTest(t, strFieldAppendSelfOverwriteSrc), "main")
	if got := strAppendCount(fn); got != 1 {
		t.Errorf("arm64: got %d __fern_str_append, want 1", got)
	}
	if got := allocReuseCount(fn); got != 1 {
		t.Errorf("arm64: got %d __alloc_reuse, want 1", got)
	}
}

// Fires on the RETURN-position spread too — the state-threading shape, and
// the one BufWriter.write_string is written in. The base here is a LOCAL, so
// the frame owns it; an owned-by-default parameter qualifies equally.
func TestStrFieldAppendReturnSpreadGrowsInPlace(t *testing.T) {
	fn := funcByName(lowerForTest(t, `struct B { buf: string, n: i32 }
function grow(n: i32): B {
    var b: B = B { buf: "", n: 0 };
    var i: i32 = 0;
    while (i < n) { b = B { ...b, buf: b.buf + "z" }; i = i + 1; }
    return B { ...b, buf: b.buf + "!" };
}
function main(): i32 { return grow(3).buf.len() - 4; }`), "grow")
	if got := strAppendCount(fn); got != 2 {
		t.Errorf("got %d __fern_str_append, want 2 (the loop's overwrite and the return spread)", got)
	}
}

// Does NOT fire: the appended field reads a DIFFERENT field. `buf: b.other +
// s` would have the helper consume the reference `other` still holds — the
// field it replaces is not the one it reads, so its old value must be
// released and `other`'s must not be touched.
func TestStrFieldAppendRefusesCrossFieldRead(t *testing.T) {
	fn := funcByName(lowerForTest(t, `struct B { buf: string, other: string }
function main(): i32 {
    var b: B = B { buf: "", other: "xy" };
    b = B { ...b, buf: b.other + "z" };
    return b.buf.len() - 3;
}`), "main")
	if got := strAppendCount(fn); got != 0 {
		t.Errorf("a cross-field read must not take the in-place append, got %d", got)
	}
	if got := allocReuseCount(fn); got != 0 {
		t.Errorf("a replaced string field that is not the self-append is still unplaceable, got %d __alloc_reuse", got)
	}
}

// Does NOT fire when the spread base is a DIFFERENT struct: the un-listed
// fields come from another box, and `q.buf`'s reference is q's, not p's.
func TestStrFieldAppendRefusesForeignBase(t *testing.T) {
	fn := funcByName(lowerForTest(t, `struct B { buf: string, n: i32 }
function main(): i32 {
    var p: B = B { buf: "a", n: 0 };
    var q: B = B { buf: "b", n: 1 };
    p = B { ...q, buf: q.buf + "c" };
    return p.buf.len() - 2;
}`), "main")
	if got := strAppendCount(fn); got != 0 {
		t.Errorf("a foreign-base spread must not take the in-place append, got %d", got)
	}
}

const strFieldAppendChainSrc = `struct B { buf: string, n: i32 }
function main(): i32 {
    var b: B = B { buf: "", n: 0 };
    var i: i32 = 0;
    while (i < 4) { b = B { ...b, buf: b.buf + "a" + "bb", n: b.n + 1 }; i = i + 1; }
    return b.buf.len() - 12;
}`

// A CHAIN of joins into the field grows one buffer, the way the bare-local
// form does. `hdr = H { ...hdr, buf: hdr.buf + name + ": " + value }` is the
// shape, and requiring the field access to be the whole init's own left
// operand left every join but the outermost copying — so a three-join
// iteration copied the accumulator twice.
//
// The reuse arm appends once per join; the decline arm, which must not move
// p's buffer, concats the first and then grows THAT intermediate, so it keeps
// exactly one OpStrConcat however long the chain is.
func TestStrFieldAppendChainGrowsOneBuffer(t *testing.T) {
	fn := funcByName(lowerForTest(t, strFieldAppendChainSrc), "main")
	if got := allocReuseCount(fn); got != 1 {
		t.Errorf("chained field append should still reuse the box, got %d __alloc_reuse", got)
	}
	if got := strAppendCount(fn); got != 3 {
		t.Errorf("got %d __fern_str_append, want 3 (two joins on the reuse arm, one growing the decline arm's concat)", got)
	}
	if got := strConcatCount(fn); got != 1 {
		t.Errorf("got %d OpStrConcat, want 1 (only the decline arm's first join copies)", got)
	}
}

// The two-word ABI legs, as for the single-join form: the field load fans out
// to (data, len) and each helper call takes both words.
func TestStrFieldAppendChainGrowsOneBufferTwoWord(t *testing.T) {
	fn := funcByName(lowerForTestPtrW(t, strFieldAppendChainSrc, 4), "main")
	if got := strAppendCount(fn); got != 3 {
		t.Errorf("wasm: got %d __fern_str_append, want 3", got)
	}
	if got := strConcatCount(fn); got != 1 {
		t.Errorf("wasm: got %d OpStrConcat, want 1", got)
	}
}

func TestStrFieldAppendChainGrowsOneBufferArm64(t *testing.T) {
	prev := ast.TwoWordOverride
	defer func() { ast.TwoWordOverride = prev }()
	ast.TwoWordOverride = true

	fn := funcByName(lowerForTest(t, strFieldAppendChainSrc), "main")
	if got := strAppendCount(fn); got != 3 {
		t.Errorf("arm64: got %d __fern_str_append, want 3", got)
	}
	if got := strConcatCount(fn); got != 1 {
		t.Errorf("arm64: got %d OpStrConcat, want 1", got)
	}
}

// Does NOT fire past an operand that names the base. Every right is evaluated
// before the gate, but a spilled slot on the single-word ABI carries only the
// data pointer and re-reads its length from [data-4] at the append — so a
// join that already grew the buffer in place would change what `b.buf` in a
// later operand means. The spine stops, and the whole init copies.
func TestStrFieldAppendChainStopsAtAReadOfTheBase(t *testing.T) {
	fn := funcByName(lowerForTest(t, `struct B { buf: string, n: i32 }
function main(): i32 {
    var b: B = B { buf: "ab", n: 0 };
    var i: i32 = 0;
    while (i < 3) { b = B { ...b, buf: b.buf + "-" + b.buf, n: b.n + 1 }; i = i + 1; }
    return b.buf.len() - 23;
}`), "main")
	if got := strAppendCount(fn); got != 1 {
		t.Errorf("got %d __fern_str_append, want 1 (only the outer join's own concat temp may be grown; fusing to b.buf would consume the buffer the third operand reads)", got)
	}
}
