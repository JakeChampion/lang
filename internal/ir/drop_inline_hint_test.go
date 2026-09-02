package ir_test

import (
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/ast"
)

// A generated drop helper that walks elements is never inlined — its loop
// would land at every exit sweep owning the type — while a flat drop stays
// under the inliner's size caps, because in a hot loop the call alone costs
// 3-10% of retired instructions (`enum_match`, `struct_drop`).
func TestGeneratedDropHelpersInlineUnlessTheyWalk(t *testing.T) {
	prog := lowerForTest(t, `enum E { A(i32), B(string) }
struct H { items: E[] }
function weigh(e: E): i32 {
	match (e) {
		A(v) => { return v; },
		B(s) => { return s.len(); }
	}
}
function total(h: H): i32 {
	var n: i32 = 0;
	var i: i32 = 0;
	while (i < h.items.len()) { n = n + weigh(h.items[i]); i = i + 1; }
	return n;
}
function main(): i32 {
	var h: H = H { items: [A(1), B("xy")] };
	return total(h) + weigh(B("q"));
}`)
	hints := map[string]ast.InlineHint{}
	var names []string
	for _, fn := range prog.Funcs {
		if strings.HasPrefix(fn.Name, "__drop_") {
			hints[fn.Name] = fn.InlineHint
			names = append(names, fn.Name)
		}
	}
	flat, ok := hints["__drop_enum_E"]
	if !ok {
		t.Fatalf("no __drop_enum_E generated; drop helpers: %v", names)
	}
	if flat == ast.InlineHintNever {
		t.Errorf("__drop_enum_E is a flat drop and must stay inlineable, got InlineHintNever")
	}
	walk, ok := hints["__drop_arr_enum_E"]
	if !ok {
		t.Fatalf("no __drop_arr_enum_E generated; drop helpers: %v", names)
	}
	if walk != ast.InlineHintNever {
		t.Errorf("__drop_arr_enum_E walks elements and must carry InlineHintNever, got %v", walk)
	}
}
