package ir

import (
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/ast"
)

// `m.without(k).0` deep-drops the delete tuple it projects out of, and the
// helper it names is derived from the call's result type. A Map method's
// FuncSig result is the GENERIC one — `(Map[K, V], boolean)` — so without
// the call's TypeArgs substituted in, the site named a helper the worklist
// could not plan and never generated. With reclamation OFF the worklist
// generates no drop helper at all, so the site must not name one either;
// it did, and every backend failed to link `map_str_delete` on the
// free-off leg of the free-toggle gate (#8707).
func TestMapDeleteProjectionNamesAGeneratedTupleDrop(t *testing.T) {
	for _, free := range []bool{true, false} {
		t.Run(map[bool]string{true: "free-on", false: "free-off"}[free], func(t *testing.T) {
			prev := ast.RcFreeEnabled
			ast.RcFreeEnabled = free
			t.Cleanup(func() { ast.RcFreeEnabled = prev })
			checkMapDeleteProjectionDrop(t, free)
		})
	}
}

func checkMapDeleteProjectionDrop(t *testing.T, free bool) {
	p := lowerSourceWith(t, `import "core/map";
function main(): i32 {
    var m: Map[string, i32] = map_new(4);
    m = m.insert("a", 1);
    m = m.insert("b", 2);
    m = m.without("b").0;
    return m.len();
}`, 8)
	defined := map[string]bool{}
	for _, fn := range p.Funcs {
		defined[fn.Name] = true
	}
	var named int
	for _, fn := range p.Funcs {
		for _, op := range fn.Ops {
			if op.Kind != OpCallDirect || !strings.HasPrefix(op.Str, "__drop_tuple_") {
				continue
			}
			named++
			if strings.Contains(op.Str, "_K_") || strings.Contains(op.Str, "_V_") {
				t.Errorf("%s names the tuple drop with the type params unsubstituted: %s", fn.Name, op.Str)
			}
			if !defined[op.Str] {
				t.Errorf("%s calls %s, which no function in the program defines", fn.Name, op.Str)
			}
		}
	}
	switch {
	case free && named == 0:
		t.Fatalf("expected the projected delete tuple to be dropped through a __drop_tuple_ helper; none named:\n%s", p)
	case !free && named != 0:
		t.Fatalf("reclamation is off, so no drop helper exists, yet %d __drop_tuple_ call(s) were emitted:\n%s", named, p)
	}
}
