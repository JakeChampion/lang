package ssa

import (
	"testing"

	"github.com/jakechampion/lang/internal/ast"
)

// ResolveWidths marks a call whose result occupies the full 64-bit register, so
// the backend skips the i32 sign-extension that would truncate it. An i64
// return is the obvious case; a FLOAT return is the subtle one, and it is not
// covered by the width check alone. Floats live in a general register as their
// f64 bit pattern, so an f32 return is 32 bits of TYPE but 64 bits of REGISTER
// — masking it to 32 keeps the low mantissa half and discards the sign and
// exponent, which reads back as a denormal ≈ 0. Before ReturnFloat was consulted
// here, every f32 crossing a call arrived as 0.0 on arm64-ssa.
func TestResolveWidthsCallResult(t *testing.T) {
	cases := []struct {
		name        string
		retWidth    int8
		returnFloat bool
		returnAddr  bool
		want        int8
	}{
		{"i32 return is left narrow", 32, false, false, 0},
		{"i64 return is widened", 64, false, false, 64},
		{"f64 return is widened", 64, true, false, 64},
		{"f32 return is widened despite width 32", 32, true, false, 64},
		{"pointer return is widened despite width 32", 32, false, true, 64},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			callee := NewFunc("callee")
			cb := callee.NewBlock()
			callee.SetRet(cb, zeroConst(callee, cb))
			callee.ReturnWidth = c.retWidth
			callee.ReturnFloat = c.returnFloat
			callee.ReturnAddr = c.returnAddr

			caller := NewFunc("caller")
			eb := caller.NewBlock()
			r := caller.AddOp(eb, OpCall)
			eb.Ops[len(eb.Ops)-1].Str = "callee"
			caller.SetRet(eb, r)

			ResolveWidths(map[string]*Func{"caller": caller, "callee": callee})

			if got := eb.Ops[0].Width; got != c.want {
				t.Errorf("call Width = %d, want %d", got, c.want)
			}
		})
	}
}

// A callee absent from the map is a backend-provided builtin or runtime
// helper: there is no ssa.Func to read a signature off, so the result width
// arrives already stamped on the op by the lift (from ir.ResAddr / ResWide /
// ResNarrow at the site that emitted the call). This pass must leave that
// stamp alone rather than re-deriving it from the name.
func TestResolveWidthsKeepsTheStampOnAnUnresolvableCallee(t *testing.T) {
	cases := []struct {
		name     string
		width    int8
		addr     bool
		wantWide int8
	}{
		{"unstamped stays narrow", 0, false, 0},
		{"wide value stays wide", 64, false, 64},
		{"address stays wide", 64, true, 64},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			caller := NewFunc("caller")
			eb := caller.NewBlock()
			r := caller.AddOp(eb, OpCall)
			eb.Ops[len(eb.Ops)-1].Str = "__some_helper"
			eb.Ops[len(eb.Ops)-1].Width = c.width
			eb.Ops[len(eb.Ops)-1].Addr = c.addr
			caller.SetRet(eb, r)

			ResolveWidths(map[string]*Func{"caller": caller})

			if got := eb.Ops[0].Width; got != c.wantWide {
				t.Errorf("call Width = %d, want %d", got, c.wantWide)
			}
			if got := eb.Ops[0].Addr; got != c.addr {
				t.Errorf("call Addr = %v, want %v", got, c.addr)
			}
		})
	}
}

// An address a helper returns has to propagate into the arithmetic that
// offsets it, exactly as an OpAlloc's does — that propagation is the whole
// reason the stamp distinguishes an address from a plain 64-bit value.
func TestResolveWidthsPropagatesAStampedHelperAddress(t *testing.T) {
	f := NewFunc("f")
	b := f.NewBlock()
	base := f.AddOp(b, OpCall)
	b.Ops[len(b.Ops)-1].Str = "__some_helper"
	b.Ops[len(b.Ops)-1].Width, b.Ops[len(b.Ops)-1].Addr = 64, true
	off := f.AddOp(b, OpConstInt)
	b.Ops[len(b.Ops)-1].Imm = 8
	elem := f.AddOp(b, OpAdd, base, off)
	f.SetRet(b, elem)

	ResolveWidths(map[string]*Func{"f": f})

	for _, op := range b.Ops {
		if op.Result.IsValid() && op.Result.ID == elem.ID && op.Width != 64 {
			t.Errorf("helper_result + 8 has Width %d, want 64 — the offset address is "+
				"truncated back to 32 bits", op.Width)
		}
	}
}

// The address a load or store is given is almost never the raw allocation: it
// is the allocation plus a field offset, often merged through a loop phi first.
// Every step of that chain has to stay 64-bit, or the last one truncates a heap
// address above 0x7fffffff back to a negative number.
func TestResolveWidthsPropagatesThroughAddressArithmetic(t *testing.T) {
	f := NewFunc("f")
	b := f.NewBlock()
	size := f.AddOp(b, OpConstInt)
	b.Ops[len(b.Ops)-1].Imm = 16
	base := f.AddOp(b, OpAlloc, size)
	off := f.AddOp(b, OpConstInt)
	b.Ops[len(b.Ops)-1].Imm = 8
	elem := f.AddOp(b, OpAdd, base, off)
	hdr := f.AddOp(b, OpSub, elem, off)
	sum := f.AddOp(b, OpAdd, off, off) // genuine i32 arithmetic, no address in it
	f.SetRet(b, sum)

	ResolveWidths(map[string]*Func{"f": f})

	byResult := map[int32]*Op{}
	for _, op := range b.Ops {
		if op.Result.IsValid() {
			byResult[op.Result.ID] = op
		}
	}
	for _, v := range []Value{base, elem, hdr} {
		if got := byResult[v.ID].Width; got != 64 {
			t.Errorf("v%d (address) Width = %d, want 64", v.ID, got)
		}
	}
	if got := byResult[sum.ID].Width; got != 0 {
		t.Errorf("v%d (i32 value) Width = %d, want 0 — widening it drops i32 wraparound",
			sum.ID, got)
	}
}

// A loop that walks an array carries the cursor round a phi, so the phi is the
// one merge point where address-ness has to survive a fixpoint rather than a
// single forward pass.
func TestResolveWidthsPropagatesThroughPhi(t *testing.T) {
	f := NewFunc("f")
	entry := f.NewBlock()
	body := f.NewBlock()
	size := f.AddOp(entry, OpConstInt)
	entry.Ops[len(entry.Ops)-1].Imm = 16
	base := f.AddOp(entry, OpAlloc, size)
	f.SetBr(entry, body)

	cur := f.AddOp(body, OpPhi, base, Value{})
	step := f.AddOp(body, OpConstInt)
	body.Ops[len(body.Ops)-1].Imm = 4
	next := f.AddOp(body, OpAdd, cur, step)
	body.Ops[0].Args[1] = next
	f.SetBr(body, body)

	ResolveWidths(map[string]*Func{"f": f})

	if got := body.Ops[0].Width; got != 64 {
		t.Errorf("phi Width = %d, want 64", got)
	}
	for _, op := range body.Ops {
		if op.Result.ID == next.ID && op.Width != 64 {
			t.Errorf("phi-derived add Width = %d, want 64", op.Width)
		}
	}
}

// A pointer arrives in a parameter as often as it is allocated locally, and the
// param's declared type is the only thing that says so — widthOfAstType reports
// 32 for every pointer-shaped type, which is its stack-slot size, not its
// register size. `usize` is the same story with an integer type.
func TestResolveWidthsPropagatesFromPointerParams(t *testing.T) {
	for _, ty := range []ast.Type{
		ast.StringType{},
		ast.ArrayType{Elem: ast.NumberType{Width: 32, Signed: true}},
		ast.NumberType{Width: ast.WidthPtr, Spelling: "usize"},
	} {
		f := NewFunc("f")
		b := f.NewBlock()
		p := f.AddParam()
		f.ParamWidths = []int8{widthOfAstType(ty)}
		f.ParamAddrs = []bool{isAddressAstType(ty)}
		off := f.AddOp(b, OpConstInt)
		b.Ops[len(b.Ops)-1].Imm = 4
		elem := f.AddOp(b, OpAdd, p, off)
		f.SetRet(b, elem)

		ResolveWidths(map[string]*Func{"f": f})

		for _, op := range b.Ops {
			if op.Result.ID == elem.ID && op.Width != 64 {
				t.Errorf("%v param: derived address Width = %d, want 64", ty, op.Width)
			}
		}
	}
}

// Some pointers have no declared type behind them at all. A closure's env block
// arrives in a synthesised parameter the IR types as a plain integer, and the
// only place in the program that says it is an address is the load that reaches
// a capture through it — so address-ness has to flow BACKWARDS out of a memory
// op's address operand.
func TestResolveWidthsPropagatesBackFromMemoryOperands(t *testing.T) {
	f := NewFunc("lambda")
	b := f.NewBlock()
	env := f.AddParam() // no ParamAddrs entry: nothing declared it a pointer
	off := f.AddOp(b, OpConstInt)
	b.Ops[len(b.Ops)-1].Imm = 8
	at := f.AddOp(b, OpAdd, env, off)
	got := f.AddOp(b, OpLoad32U, at)
	f.SetRet(b, got)

	ResolveWidths(map[string]*Func{"lambda": f})

	for _, op := range b.Ops {
		if op.Result.ID == at.ID && op.Width != 64 {
			t.Errorf("capture address Width = %d, want 64", op.Width)
		}
	}
}

// The mirror of the argument rule: a callee that dereferences its parameter
// tells every caller that the value it passes there is an address, even when
// the caller computed it out of values none of which looked like one.
func TestResolveWidthsPropagatesOutOfCallee(t *testing.T) {
	callee := NewFunc("deref")
	cb := callee.NewBlock()
	p := callee.AddParam()
	callee.ParamAddrs = []bool{true}
	callee.SetRet(cb, callee.AddOp(cb, OpLoad32U, p))

	caller := NewFunc("caller")
	eb := caller.NewBlock()
	base := caller.AddParam()
	off := caller.AddOp(eb, OpConstInt)
	eb.Ops[len(eb.Ops)-1].Imm = 4
	at := caller.AddOp(eb, OpAdd, base, off)
	caller.SetRet(eb, caller.AddOp(eb, OpCall, at))
	eb.Ops[len(eb.Ops)-1].Str = "deref"

	ResolveWidths(map[string]*Func{"caller": caller, "deref": callee})

	for _, op := range eb.Ops {
		if op.Result.ID == at.ID && op.Width != 64 {
			t.Errorf("argument address Width = %d, want 64", op.Width)
		}
	}
}

// A Map call site names `map_new` while the module holds `map_new_impl`; the
// backends resolve that through ir.CodegenAlias where the callee becomes a
// label, and so must the width pass. Reading the raw name left every Map helper
// looking like an unknown runtime helper, so its pointer result was masked back
// to 32 bits and the first dereference of the map died by signal.
func TestResolveWidthsResolvesCodegenAliases(t *testing.T) {
	callee := NewFunc("map_new_impl")
	cb := callee.NewBlock()
	callee.SetRet(cb, zeroConst(callee, cb))
	callee.ReturnAddr = true

	caller := NewFunc("caller")
	eb := caller.NewBlock()
	r := caller.AddOp(eb, OpCall)
	eb.Ops[len(eb.Ops)-1].Str = "map_new"
	caller.SetRet(eb, r)

	ResolveWidths(map[string]*Func{"caller": caller, "map_new_impl": callee})

	if got := eb.Ops[0].Width; got != 64 {
		t.Errorf("aliased call Width = %d, want 64", got)
	}
}

// An integer literal is the one value in the SSA that is genuinely polymorphic:
// CSE merges by (kind, operands) and the IR carries no type, so the null pointer
// a drop call passes and the zero an i32 expression evaluates to are ONE value.
// Marking it makes every use of that zero an address, and the fixpoint then
// carries the classification into the callees those uses reach — here far enough
// to skip the sign-extension on an unrelated i32 field load, which read back
// zero-extended (4294967077 for -219).
func TestResolveWidthsDoesNotMarkIntegerConstants(t *testing.T) {
	drop := NewFunc("drop")
	db := drop.NewBlock()
	drop.AddParam()
	drop.ParamAddrs = []bool{true}
	drop.SetRet(db, Value{})

	// Takes an i32 and returns it: nothing about it is address-shaped.
	show := NewFunc("show")
	sb := show.NewBlock()
	show.SetRet(sb, show.AddParam())

	caller := NewFunc("caller")
	cb := caller.NewBlock()
	base := caller.AddParam()
	caller.ParamAddrs = []bool{true}
	zero := zeroConst(caller, cb)
	caller.AddOp(cb, OpCall, zero) // drop(null)
	cb.Ops[len(cb.Ops)-1].Str = "drop"
	caller.AddOp(cb, OpCall, zero) // show(0)
	cb.Ops[len(cb.Ops)-1].Str = "show"
	fld := caller.AddOp(cb, OpLoad32U, base)
	caller.SetRet(cb, caller.AddOp(cb, OpCall, fld))
	cb.Ops[len(cb.Ops)-1].Str = "show"

	ResolveWidths(map[string]*Func{"caller": caller, "drop": drop, "show": show})

	widths := map[int32]int8{}
	for _, op := range cb.Ops {
		widths[op.Result.ID] = op.Width
	}
	if widths[zero.ID] == 64 {
		t.Errorf("constant zero Width = 64, want narrow")
	}
	if widths[fld.ID] == 64 {
		t.Errorf("i32 field load Width = 64, want narrow (its sign-extension would be skipped)")
	}
	if len(show.ParamAddrs) > 0 && show.ParamAddrs[0] {
		t.Errorf("show's i32 parameter was classified as an address")
	}
	// The genuine pointer must still be wide: this is the classification the
	// constant guard has to leave intact.
	if !drop.ParamAddrs[0] {
		t.Errorf("drop's pointer parameter lost its address classification")
	}
}

// The exclusion list is what keeps a type nobody thought about from silently
// becoming truncatable, so pin both directions.
func TestIsAddressAstType(t *testing.T) {
	addr := []ast.Type{
		ast.StringType{},
		ast.ArrayType{Elem: ast.NumberType{}},
		ast.SliceType{Elem: ast.NumberType{}},
		ast.StructType{Name: "S"},
		ast.EnumType{Name: "E"},
		ast.TupleType{},
		ast.StreamType{Elem: ast.NumberType{}},
		ast.DynTraitType{},
		&ast.FuncType{},
		ast.NumberType{Width: ast.WidthPtr, Spelling: "usize"},
	}
	for _, ty := range addr {
		if !isAddressAstType(ty) {
			t.Errorf("%T (%v): want address-shaped", ty, ty)
		}
	}
	scalar := []ast.Type{
		nil,
		ast.NumberType{Width: 32, Signed: true},
		ast.NumberType{Width: 64, Signed: true},
		ast.BoolType{},
		ast.VoidType{},
		ast.NeverType{},
		ast.FloatType{Width: 32},
		ast.FloatType{Width: 64},
	}
	for _, ty := range scalar {
		if isAddressAstType(ty) {
			t.Errorf("%T (%v): want scalar", ty, ty)
		}
	}
}

func zeroConst(f *Func, b *Block) Value {
	x := f.AddOp(b, OpConstInt)
	b.Ops[len(b.Ops)-1].Imm = 0
	return x
}
