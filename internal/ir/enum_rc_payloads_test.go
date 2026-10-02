package ir_test

import (
	"testing"

	"github.com/jakechampion/lang/internal/ast"
	"github.com/jakechampion/lang/internal/ir"
)

// Enum construction rc-counts its pointer payloads like StructLit — an ALIASED
// payload is inc'd so the box co-owns its reference (which is what lets enum
// boxes be deep-dropped precisely).
func TestEnumRcPayloadsInc(t *testing.T) {
	// Isolate the payload inc from Slice 2's call-site retain inc.
	pobd := ast.OwnedByDefault
	ast.OwnedByDefault = false
	defer func() { ast.OwnedByDefault = pobd }()

	// The aliased List payload (`C(0, t)`, t a borrowed param read again) is
	// inc'd straight off its load: t is slot 0.
	const list = `enum L{C(i32,L),N}
function len(l:L):i32{match(l){C(h,x)=>{return 1+len(x);},N=>{return 0;}}}
function f(t:L):i32{let e:L=C(0,t);return len(t)+len(e);}
function main():i32{return 0;}`
	ops := funcByName(lowerForTest(t, list), "f").Ops
	incOfT := false
	for i := 1; i < len(ops); i++ {
		if ops[i].Kind == ir.OpRcInc && ops[i-1].Kind == ir.OpLoadLocal && ops[i-1].I32 == 0 {
			incOfT = true
		}
	}
	if !incOfT {
		t.Errorf("aliased List payload: no rc_inc of t at the construction")
	}
}
