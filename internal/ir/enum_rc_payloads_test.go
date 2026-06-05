package ir_test

import (
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/ast"
	"github.com/jakechampion/lang/internal/ir"
)

// Slice 1b: under EnumRcPayloads enum construction rc-counts its pointer
// payloads like StructLit — an ALIASED payload is inc'd so the box co-owns its
// reference (which is what lets enum boxes be deep-dropped precisely). Enums
// transitively containing a Map are EXCLUDED (their deep drop calls the
// not-everywhere-wired __map_drop_values) and keep the move model. Pinned at the
// IR layer; the byte-identical differential gate + the suite cover correctness.
func incCountInFn(ip *ir.Program, fn string) int {
	f := funcByName(ip, fn)
	n := 0
	for _, op := range f.Ops {
		if op.Kind == ir.OpCallDirect && strings.Contains(op.Str, "rc_inc") {
			n++
		}
	}
	return n
}

func TestEnumRcPayloadsInc(t *testing.T) {
	prev := ast.EnumRcPayloads
	defer func() { ast.EnumRcPayloads = prev }()

	// Aliased List payload (`C(0, t)`, t a borrowed param read again): inc'd
	// under the flag, not under the move model.
	const list = `enum L{C(i32,L),N}
function len(l:L):i32{match(l){C(h,x)=>{return 1+len(x);},N=>{return 0;}}}
function f(t:L):i32{var e:L=C(0,t);return len(t)+len(e);}
function main():i32{return 0;}`
	ast.EnumRcPayloads = false
	off := incCountInFn(lowerForTest(t, list), "f")
	ast.EnumRcPayloads = true
	on := incCountInFn(lowerForTest(t, list), "f")
	if on != off+1 {
		t.Errorf("aliased List payload: rc_inc off=%d on=%d, want on = off+1", off, on)
	}
	// (The Map-containing-enum exclusion — those keep the move model because
	// their deep drop calls the not-everywhere-wired __map_drop_values — is
	// covered end-to-end by e2e TestWASMArrayPushEnum passing under the flag.)
}
