package ir_test

import (
	"testing"

	"github.com/jakechampion/lang/internal/ir"
)

func TestBorrowedArrayOwnForwardTracksOwnership(t *testing.T) {
	ip := lowerForTest(t, `function update(own xs: i32[]): i32[] { return xs.with(0, 9); }
function forward(xs: i32[]): i32[] { xs = update(xs); return xs; }
function main(): i32 { var xs = [1, 2, 3]; var next = forward(xs); return xs[0] + next[0]; }`)
	fn := fnNamed(t, ip, "forward")
	flag := int32(-1)
	for i := 0; i+6 < len(fn.Ops); i++ {
		ops := fn.Ops[i:]
		if ops[0].Kind == ir.OpLoadLocal && ops[1].Kind == ir.OpConstI32 && ops[1].I32 == 0 &&
			ops[2].Kind == ir.OpEq && ops[3].Kind == ir.OpIf &&
			ops[4].Kind == ir.OpLoadLocal && ops[5].Kind == ir.OpRcInc && ops[6].Kind == ir.OpDrop {
			flag = ops[0].I32
			break
		}
	}
	if flag < 0 {
		t.Fatalf("borrowed-to-own transfer lacks an ownership-flag-gated retain:\n%s", ip)
	}
	called := false
	for i, op := range fn.Ops {
		if op.Kind == ir.OpCallDirect && op.Str == "update" {
			called = true
		}
		if called && i > 0 && op.Kind == ir.OpStoreLocal && op.I32 == flag &&
			fn.Ops[i-1].Kind == ir.OpConstI32 && fn.Ops[i-1].I32 == 1 {
			return
		}
	}
	t.Fatalf("own-call result never becomes owned by the forwarding frame:\n%s", ip)
}
