package ir_test

import (
	"testing"

	"github.com/jakechampion/lang/internal/ir"
)

// A range pattern on a signed scrutinee must compare signed. The bound was
// read off `NumberType.Signed` rather than `IsSigned()`, and a plain `i32`
// carries the zero-value NumberType where that field is false while the
// width-0 default IS signed. Every range comparison came out unsigned, so
// `match (v) { -10..0 => … }` matched nothing: as unsigned, -5 is 4294967291,
// which is not below 0.
//
// Only a range straddling zero showed it. `-100..-10` and `0..10` give the
// same answer under either interpretation, which is why the corpus had not
// caught it.
func TestRangePatternComparesSigned(t *testing.T) {
	ip := lowerForTest(t, `function bucket(v: i32): i32 {
    match (v) {
        -10..0 => { return 1; },
        0..=10 => { return 2; },
        _ => { return 3; },
    }
}

function main(): i32 { return bucket(-5); }
`)
	fn := funcByName(ip, "bucket")
	if fn == nil {
		t.Fatal("bucket was not lowered")
	}
	for i, op := range fn.Ops {
		switch op.Kind {
		case ir.OpLtS, ir.OpLeS, ir.OpGtS, ir.OpGeS:
			if op.Unsigned {
				t.Errorf("op %d: a range comparison on an i32 scrutinee is unsigned", i)
			}
		}
	}
}

// A negated element in a tuple literal used to give the enclosing tuple a NIL
// element type, and the compiler then panicked in TupleType.String() when the
// call-argument temp's drop asked for its `__drop_tuple_<shape>` name. The
// same literal without the sign was fine, so the crash needed a `-` to appear.
//
// Lowering completing at all is the assertion; the panic was a nil-pointer
// dereference, not a returned error.
func TestUnaryElementDoesNotLoseItsTupleShape(t *testing.T) {
	ip := lowerForTest(t, `function sum(t: (i32, i32)): i32 { return t.0 + t.1; }

struct Pair { a: i32, b: i32 }

function main(): i32 {
    var n: i32 = 4;
    var x: i32 = sum((-1, 9));
    var y: i32 = sum((-n, -9));
    var z: i32 = sum((1, 9));
    var p: Pair = Pair { a: -1, b: 9 };
    return x + y + z + p.a + p.b;
}
`)
	if funcByName(ip, "main") == nil {
		t.Fatal("main was not lowered")
	}
}
