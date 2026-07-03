package ir_test

import (
	"testing"

	"github.com/jakechampion/lang/internal/ir"
)

// The consumed-threaded param entry-inc is the first RC insertion converted
// to a true post-lowering []Op splice (insertConsumedParamEntryIncs, #4393
// slice 4). Pin both halves of its contract: the inc still exists for a
// threaded string-bearing struct param, and it still lands at the PROLOGUE
// boundary — nothing but the entry zero-init (consts + local stores) may
// precede it, exactly as the old in-build emission placed it.
func TestConsumedParamEntryIncSplicedAtPrologue(t *testing.T) {
	ip := lowerForTest(t, `struct Ctx { name: string, n: i32 }
function thread(c: Ctx): i32 {
	c = Ctx { name: "x", n: c.n + 1 };
	return c.n;
}
function main(): i32 { return thread(Ctx { name: "a", n: 1 }); }`)
	f := funcByName(ip, "thread")
	if f == nil {
		t.Fatal("thread not lowered")
	}
	idx := -1
	for i, op := range f.Ops {
		if op.Kind == ir.OpCallDirect && op.Str == "__fern_rc_inc" {
			idx = i
			break
		}
	}
	if idx < 1 {
		t.Fatal("expected a consumed-param entry retain-inc in thread, found no __fern_rc_inc")
	}
	if f.Ops[idx-1].Kind != ir.OpLoadLocal || f.Ops[idx-1].I32 != 0 {
		t.Fatalf("entry-inc operand: want OpLoadLocal slot 0 (the param) before the inc, got %+v", f.Ops[idx-1])
	}
	if f.Ops[idx+1].Kind != ir.OpDrop {
		t.Fatalf("entry-inc result: want OpDrop after the inc, got %+v", f.Ops[idx+1])
	}
	for i := 0; i < idx-1; i++ {
		if k := f.Ops[i].Kind; k != ir.OpConstI32 && k != ir.OpStoreLocal {
			t.Fatalf("op %d before the spliced entry-inc has kind %v — the inc is no longer at the prologue boundary", i, k)
		}
	}
}
