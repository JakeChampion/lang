package ir_test

import (
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/ir"
)

// Releasing an array-typed struct FIELD must drop the elements, not just free
// the buffer. `emitFieldDropOnStack` emitted the flat `__fern_arr_dec` for
// every array field regardless of element type, so replacing a struct's array
// field leaked one element box per call, unbounded, on all three compiled
// backends — while the same `.with` on a bare local was fine, because that
// receiver is a move and takes the in-place branch instead of the copy branch
// that retains the elements.
func TestStructFieldArrayReleaseDropsElements(t *testing.T) {
	ip := lowerForTest(t, `struct P { a: i32, b: i32 }
struct Box { items: P[], tag: i32 }

function churn(n: i32): i32 {
    var b: Box = Box { items: [P { a: 0, b: 0 }], tag: 0 };
    for i in 0..n { b = Box { ...b, items: b.items.with(0, P { a: i, b: i }) }; }
    return b.items[0].a;
}

function main(): i32 { return churn(3); }
`)
	fn := funcByName(ip, "churn")
	if fn == nil {
		t.Fatal("churn was not lowered")
	}
	var flat, deep bool
	for _, op := range fn.Ops {
		if op.Kind != ir.OpCallDirect {
			continue
		}
		if op.Str == "__fern_arr_dec" {
			flat = true
		}
		if strings.HasPrefix(op.Str, "__drop_arr_struct_") {
			deep = true
		}
	}
	if !deep {
		t.Error("the array field is released without dropping its elements (no __drop_arr_struct_* call)")
	}
	if flat {
		t.Error("the array field still takes the buffer-only __fern_arr_dec release")
	}
}
