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

// A struct-update spread whose base is a FRESH value — a call result or a
// nested literal — owns that base, so the construction has to release it.
// The lowering treated every base as borrowed, which is right for an Ident
// (its local releases at scope exit) and wrong for a temporary nobody holds:
// `T { ...mk(), f: v }` leaked one base box per evaluation, unbounded.
func TestStructUpdateReleasesAFreshBase(t *testing.T) {
	const src = `struct R { tag: string, n: i32 }

function mk(): R { return R { tag: "base", n: 0 }; }

function from_call(n: i32): i32 {
    var t: i32 = 0;
    for i in 0..n { var r: R = R { ...mk(), n: i }; t = t + r.n; }
    return t;
}

function from_local(n: i32): i32 {
    var b: R = mk();
    var t: i32 = 0;
    for i in 0..n { var r: R = R { ...b, n: i }; t = t + r.n; }
    return t;
}

function main(): i32 { return from_call(2) + from_local(2); }
`
	ip := lowerForTest(t, src)
	// The two functions release the same number of R values — one per loop
	// iteration plus one base. They differ only in where the base comes
	// from, and the Ident-base form was already right, so it is the yardstick.
	call := dropStructCalls(funcByName(ip, "from_call"), "R")
	local := dropStructCalls(funcByName(ip, "from_local"), "R")
	if call != local {
		t.Errorf("from_call releases %d R values and from_local %d — a fresh base is owned by the construction, so the counts must match", call, local)
	}
	if call == 0 {
		t.Error("neither function releases anything; the test no longer measures what it claims")
	}
}

func dropStructCalls(fn *ir.Func, structName string) int {
	if fn == nil {
		return 0
	}
	n := 0
	for _, op := range fn.Ops {
		if op.Kind == ir.OpCallDirect && op.Str == "__drop_struct_"+structName {
			n++
		}
	}
	return n
}
