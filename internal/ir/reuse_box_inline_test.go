package ir

import "testing"

// A constructor-reuse site decides between the donor's own box and a fresh
// one by asking is_unique, and then used to hand that answer to
// __alloc_reuse as a token for the callee to re-derive. The reuse branch —
// the whole point of the optimisation — therefore paid a call to be given
// its own argument back. emitReuseBox takes the branch in the IR and leaves
// the allocation call on the fresh arm alone (#8530).

const reuseBoxSrc = `struct Blk { buf: u8[], note: string, n: i32 }
enum Bag { Keep(i32[]), Swap(i32[]) }
function step(b: Blk, v: i32): Blk {
    var out: Blk = b;
    out = Blk { ...out, n: v };
    return out;
}
function eStep(n: i32): i32 {
    var b: Bag = Keep([0, 0]);
    var i: i32 = 0;
    while (i < n) { b = Swap([i, i]); i = i + 1; }
    return 0;
}
function main(): i32 { return 0; }`

func TestStructUpdateReuseTakesTheBoxWithoutAllocReuse(t *testing.T) {
	for _, fnName := range []string{"step", "eStep"} {
		for _, ptrW := range []int{4, 8} {
			p := lowerSourceWith(t, reuseBoxSrc, ptrW)
			fn := findFunc(p, fnName)
			if fn == nil {
				t.Fatalf("ptrW=%d: no %s in the lowered program", ptrW, fnName)
			}
			// The allocation call survives, on the fresh arm only: it is
			// still what marks a reuse-PAIRED site for verifyFipAllocs.
			sites := allocReuseSites(fn.Ops)
			if len(sites) != 1 {
				t.Fatalf("ptrW=%d: %s has %d __alloc_reuse calls, want 1; ops:\n%s",
					ptrW, fnName, len(sites), p)
			}
			if !onFreshArm(fn.Ops, sites[0]) {
				t.Errorf("ptrW=%d: %s reaches __alloc_reuse on the straight-line path — the "+
					"reuse branch is paying a call to re-derive the uniqueness answer it just "+
					"computed; ops:\n%s", ptrW, fnName, p)
			}
			// And the decision itself is still the runtime uniqueness test.
			if n := countCallDirect(fn.Ops, "__fern_rc_is_unique"); n < 1 {
				t.Errorf("ptrW=%d: %s reuses a box without asking whether it is uniquely "+
					"held; ops:\n%s", ptrW, fnName, p)
			}
		}
	}
}

// allocReuseSites returns the index of every __alloc_reuse call in ops.
func allocReuseSites(ops []Op) []int {
	var out []int
	for i, op := range ops {
		if op.Kind == OpCallDirect && op.Str == "__alloc_reuse" {
			out = append(out, i)
		}
	}
	return out
}

// onFreshArm reports whether the call at index i is the one emitReuseBox
// leaves in the else-arm of its inline reuse test, where the three
// arguments are a null token and the size twice.
func onFreshArm(ops []Op, i int) bool {
	if i < 3 {
		return false
	}
	for k := 1; k <= 3; k++ {
		if ops[i-k].Kind != OpConstI32 {
			return false
		}
	}
	if ops[i-3].I32 != 0 {
		return false
	}
	for j := i; j >= 0; j-- {
		switch ops[j].Kind {
		case OpElse:
			return true
		case OpIf, OpEnd:
			return false
		}
	}
	return false
}

func TestSameAllocClass(t *testing.T) {
	cases := []struct {
		a, b int32
		want bool
	}{
		{24, 24, true},
		{24, 32, true},  // both round to 32
		{24, 48, false}, // 32 vs 48
		{16, 17, false}, // 16 rounds to 16, 17 to 32
		{0, 1, false},   // 0 stays 0, 1 rounds to 16
	}
	for _, c := range cases {
		if got := sameAllocClass(c.a, c.b); got != c.want {
			t.Errorf("sameAllocClass(%d, %d) = %v, want %v — the predicate must match "+
				"__alloc_reuse's own 16-byte class rounding or a mismatched donor is "+
				"reused instead of freed", c.a, c.b, got, c.want)
		}
	}
}
