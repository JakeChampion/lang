package fernrt

import (
	"testing"

	"github.com/jakechampion/lang/internal/ir"
)

// The source must front-end cleanly and lower for every pointer width a
// backend asks with; a helper that fails here would fail inside every Emit
// that needs it.
func TestEveryHelperLowersForEveryPointerWidth(t *testing.T) {
	names := Names()
	if len(names) == 0 {
		t.Fatal("runtime.fern defines no helpers")
	}
	for _, ptrW := range []int{4, 8} {
		for _, name := range names {
			decl, fn, err := Func(name, ptrW)
			if err != nil {
				t.Fatalf("Func(%q, %d): %v", name, ptrW, err)
			}
			if decl.Name != name || fn.Name != name {
				t.Errorf("Func(%q, %d) returned %q / %q", name, ptrW, decl.Name, fn.Name)
			}
			if fn.PtrW != ptrW {
				t.Errorf("%s at ptrW=%d lowered with PtrW=%d", name, ptrW, fn.PtrW)
			}
			if len(fn.Ops) == 0 {
				t.Errorf("%s at ptrW=%d lowered to no ops", name, ptrW)
			}
		}
	}
	if _, _, err := Func("__fern_no_such_helper", 8); err == nil {
		t.Error("Func on an undefined helper returned no error")
	}
	if Has("__fern_no_such_helper") || !Has("__fern_utf8_valid") {
		t.Error("Has disagrees with the source")
	}
}

// A helper may only reach the provided-callee floor. A call to a function
// defined in runtime.fern is fine too (it is emitted alongside); a call to
// anything else is a link failure on every backend, and a call to the
// operation the helper implements would be the circularity the floor exists
// to avoid.
func TestHelpersCallOnlyTheFloorOrEachOther(t *testing.T) {
	for _, name := range Names() {
		_, fn, err := Func(name, 8)
		if err != nil {
			t.Fatal(err)
		}
		for _, op := range fn.Ops {
			switch op.Kind {
			case ir.OpCallDirect:
				if Has(op.Str) {
					continue
				}
				if _, _, ok := ir.ProvidedCallee(op.Str, false); !ok {
					t.Errorf("%s calls %q, which is neither a runtime.fern helper nor a provided callee", name, op.Str)
				}
			case ir.OpCallIndirect, ir.OpCallClosureDirect, ir.OpMakeClosure, ir.OpMakeEnv, ir.OpStrConcat, ir.OpStrEq, ir.OpStrCmp, ir.OpAlloc:
				t.Errorf("%s uses %v, which needs a runtime helper of its own", name, op.Kind)
			}
		}
	}
}
