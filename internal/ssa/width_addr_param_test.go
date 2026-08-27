package ssa

import "testing"

// ResolveWidths marks values that hold a machine address so the backend skips
// the sign-extension that would corrupt a pointer above 0x7fffffff. The marking
// is deliberately conservative — over-marking protects pointers — but it must
// stay inside the function that made the guess.
//
// It did not. Pass an address-marked i32 to a function and mark() overwrote the
// callee's DECLARED ParamAddrs[i], which then marked every OTHER caller's
// argument at the same position — stamping Width 64 on the defining op, which is how memLoadSeq is
// told to skip its maskFix. A negative i32 loaded through one of those stayed
// zero-extended, and arm64ssa compares at 64 bits, so every signed test on it
// read positive.
//
// In the self-hosted compiler that turned `util.i32_to_string(-1)` into
// "4294967295", so the SSA-built build emitted `movq $4294967295, %rax` where
// the stack-machine build emits `movq $-1, %rax` — a different encoding holding
// a different value. 588 loads across 201 functions carried the contradiction.
func TestAddrInferenceDoesNotCrossIntoAnotherCallersArgument(t *testing.T) {
	// callee(n: i32) — what lift.go records for a declared scalar parameter.
	callee := NewFunc("callee")
	cb := callee.NewBlock()
	p := callee.AddParam()
	callee.SetRet(cb, p)
	callee.ParamAddrs = []bool{false}

	// One caller passes a pointer DIFFERENCE — an integer standing on an address.
	// The OpSub case marks it, since Args[0] is an address; that over-approximation
	// is a convenient way to obtain a marked scalar here, not the seed observed in
	// the self-host, where no OpSub of two addresses is marked at all.
	diffCaller := NewFunc("diffCaller")
	db := diffCaller.NewBlock()
	size := diffCaller.AddOp(db, OpConstInt)
	db.Ops[len(db.Ops)-1].Imm = 16
	a := diffCaller.AddOp(db, OpAlloc, size)
	b := diffCaller.AddOp(db, OpAlloc, size)
	call1 := diffCaller.AddOp(db, OpCall, diffCaller.AddOp(db, OpSub, a, b))
	db.Ops[len(db.Ops)-1].Str = "callee"
	diffCaller.SetRet(db, call1)

	// Another passes a plain i32 read from four bytes of memory.
	intCaller := NewFunc("intCaller")
	ib := intCaller.NewBlock()
	base := intCaller.AddOp(ib, OpConstInt)
	ib.Ops[len(ib.Ops)-1].Imm = 4096
	load := intCaller.AddOp(ib, OpLoad32U, base)
	loadOp := ib.Ops[len(ib.Ops)-1]
	call2 := intCaller.AddOp(ib, OpCall, load)
	ib.Ops[len(ib.Ops)-1].Str = "callee"
	intCaller.SetRet(ib, call2)

	ResolveWidths(map[string]*Func{
		"callee": callee, "diffCaller": diffCaller, "intCaller": intCaller,
	})

	if callee.ParamAddrs[0] {
		t.Errorf("callee's declared scalar parameter was promoted to an address; " +
			"that promotion is what reaches back into every other caller")
	}
	if loadOp.Addr || loadOp.Width == 64 {
		t.Errorf("the unrelated i32 load is Addr=%v Width=%d, want Addr=false Width!=64: "+
			"Width 64 makes the backend skip its sign-extension, so a negative read "+
			"through this load stays zero-extended", loadOp.Addr, loadOp.Width)
	}
}

// A four-byte load cannot hold a machine address on the target this pass runs
// for: ResolveWidths is called only by the arm64 backend, whose lift renders
// every pointer-width load as the eight-byte OpLoad. Marking one anyway is the
// same category of impossible answer that mark() already refuses for integer
// constants.
func TestNarrowLoadIsNeverAnAddress(t *testing.T) {
	for _, kind := range []OpKind{OpLoad8U, OpLoad8S, OpLoad16U, OpLoad16S, OpLoad32U} {
		f := NewFunc("f")
		blk := f.NewBlock()
		base := f.AddOp(blk, OpConstInt)
		blk.Ops[len(blk.Ops)-1].Imm = 4096
		v := f.AddOp(blk, kind, base)
		narrow := blk.Ops[len(blk.Ops)-1]
		// Use the loaded value as the base of another load — the shape that
		// says "this is a pointer" to the propagation.
		f.AddOp(blk, OpLoad, v)
		f.SetRet(blk, v)

		ResolveWidths(map[string]*Func{"f": f})

		if narrow.Addr || narrow.Width == 64 {
			t.Errorf("%v: Addr=%v Width=%d, want Addr=false Width!=64", kind, narrow.Addr, narrow.Width)
		}
	}
}
