package ir

import (
	"testing"

	"github.com/jakechampion/lang/internal/ast"
)

// u64Local builds a function with `n` u64 locals in slot order, so a
// pattern built from OpLoadLocal reads a single-word integer.
func u64Func(ops []Op, n int) *Func {
	locals := make([]*ast.Var, n)
	for i := range locals {
		locals[i] = &ast.Var{Name: "v", Type: ast.NumberType{Width: 64, Signed: false, Spelling: "u64"}}
	}
	return &Func{Name: "f", Locals: locals, Ops: ops}
}

// rotr64 is the op sequence `(local0 >> n) | (local0 << (64-n))` — the
// bound spelling, where the operand is a single local read.
func rotr64(n, m int64) []Op {
	return []Op{
		{Kind: OpLoadLocal, I32: 0},
		{Kind: OpConstI64, I64: n},
		{Kind: OpShrS, Width: 64, Unsigned: true},
		{Kind: OpLoadLocal, I32: 0},
		{Kind: OpConstI64, I64: m},
		{Kind: OpShl, Width: 64},
		{Kind: OpOr, Width: 64},
		{Kind: OpReturn},
	}
}

func kinds(ops []Op) []OpKind {
	out := make([]OpKind, len(ops))
	for i, op := range ops {
		out[i] = op.Kind
	}
	return out
}

func sameKinds(got []Op, want ...OpKind) bool {
	g := kinds(got)
	if len(g) != len(want) {
		return false
	}
	for i := range g {
		if g[i] != want[i] {
			return false
		}
	}
	return true
}

// The bound spelling fuses to a single rotate keeping the logical-right
// shift's count.
func TestFuseRotatesBoundOperand(t *testing.T) {
	fn := u64Func(rotr64(24, 40), 1)
	p := &Program{Funcs: []*Func{fn}}
	if !FuseRotates(p) {
		t.Fatalf("expected a rewrite:\n%s", p)
	}
	if !sameKinds(fn.Ops, OpLoadLocal, OpConstI64, OpRotr, OpReturn) {
		t.Fatalf("unexpected shape:\n%s", p)
	}
	if got := fn.Ops[1].I64; got != 24 {
		t.Errorf("rotate count = %d, want 24", got)
	}
	if got := fn.Ops[2].Width; got != 64 {
		t.Errorf("rotate width = %d, want 64", got)
	}
}

// The generator writes the operand out on both sides of the `|`. Both
// copies must be recognised as one value: the fusion keeps the first
// evaluation and drops the second along with the two shifts.
func TestFuseRotatesRepeatedSubexpression(t *testing.T) {
	xor := []Op{
		{Kind: OpLoadLocal, I32: 0},
		{Kind: OpLoadLocal, I32: 1},
		{Kind: OpXor, Width: 64},
	}
	ops := []Op{}
	ops = append(ops, xor...)
	ops = append(ops, Op{Kind: OpConstI64, I64: 32}, Op{Kind: OpShrS, Width: 64, Unsigned: true})
	ops = append(ops, xor...)
	ops = append(ops, Op{Kind: OpConstI64, I64: 32}, Op{Kind: OpShl, Width: 64})
	ops = append(ops, Op{Kind: OpOr, Width: 64}, Op{Kind: OpReturn})

	fn := u64Func(ops, 2)
	p := &Program{Funcs: []*Func{fn}}
	if !FuseRotates(p) {
		t.Fatalf("expected a rewrite:\n%s", p)
	}
	if !sameKinds(fn.Ops, OpLoadLocal, OpLoadLocal, OpXor, OpConstI64, OpRotr, OpReturn) {
		t.Fatalf("unexpected shape:\n%s", p)
	}
}

// `(x << n) | (x >> (W-n))` is a LEFT rotate by n, which is the same
// value as a right rotate by W-n. The rewrite keeps the right shift's
// count, so one IR op serves both directions.
func TestFuseRotatesLeftSpelling(t *testing.T) {
	fn := u64Func([]Op{
		{Kind: OpLoadLocal, I32: 0},
		{Kind: OpConstI64, I64: 40},
		{Kind: OpShl, Width: 64},
		{Kind: OpLoadLocal, I32: 0},
		{Kind: OpConstI64, I64: 24},
		{Kind: OpShrS, Width: 64, Unsigned: true},
		{Kind: OpOr, Width: 64},
		{Kind: OpReturn},
	}, 1)
	p := &Program{Funcs: []*Func{fn}}
	if !FuseRotates(p) {
		t.Fatalf("expected a rewrite:\n%s", p)
	}
	if !sameKinds(fn.Ops, OpLoadLocal, OpConstI64, OpRotr, OpReturn) {
		t.Fatalf("unexpected shape:\n%s", p)
	}
	if got := fn.Ops[1].I64; got != 24 {
		t.Errorf("rotate count = %d, want 24 (rotl 40 == rotr 24)", got)
	}
}

// u32 at 32-bit width, with the counts as i32 constants.
func TestFuseRotatesWidth32(t *testing.T) {
	fn := &Func{
		Name:   "f",
		Locals: []*ast.Var{{Name: "v", Type: ast.NumberType{Width: 32, Signed: false, Spelling: "u32"}}},
		Ops: []Op{
			{Kind: OpLoadLocal, I32: 0},
			{Kind: OpConstI32, I32: 7},
			{Kind: OpShrS, Width: 32, Unsigned: true},
			{Kind: OpLoadLocal, I32: 0},
			{Kind: OpConstI32, I32: 25},
			{Kind: OpShl, Width: 32},
			{Kind: OpOr, Width: 32},
			{Kind: OpReturn},
		},
	}
	p := &Program{Funcs: []*Func{fn}}
	if !FuseRotates(p) {
		t.Fatalf("expected a rewrite:\n%s", p)
	}
	if !sameKinds(fn.Ops, OpLoadLocal, OpConstI32, OpRotr, OpReturn) {
		t.Fatalf("unexpected shape:\n%s", p)
	}
	if got := fn.Ops[2].Width; got != 32 {
		t.Errorf("rotate width = %d, want 32", got)
	}
}

// Everything the pattern must NOT fire on.
func TestFuseRotatesRejects(t *testing.T) {
	signed := rotr64(24, 40)
	signed[2].Unsigned = false // `>>` on a signed type shifts the sign in

	mixedWidth := rotr64(24, 40)
	mixedWidth[5].Width = 32

	badLocal := rotr64(24, 40)
	badLocal[3].I32 = 1 // the two halves shift different values

	nonConst := rotr64(24, 40)
	nonConst[4] = Op{Kind: OpLoadLocal, I32: 1}

	cases := []struct {
		name   string
		ops    []Op
		locals int
	}{
		{"signed right shift", signed, 1},
		{"counts do not sum to the width", rotr64(24, 24), 1},
		{"count zero", rotr64(0, 64), 1},
		{"count equals the width", rotr64(64, 0), 1},
		{"mismatched shift widths", mixedWidth, 1},
		{"different operands", badLocal, 2},
		{"non-constant count", nonConst, 2},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			fn := u64Func(c.ops, c.locals)
			before := append([]Op(nil), fn.Ops...)
			p := &Program{Funcs: []*Func{fn}}
			if FuseRotates(p) {
				t.Fatalf("rewrote what it should have left alone:\n%s", p)
			}
			if !opsEqual(fn.Ops, before) {
				t.Fatalf("ops changed without the pass reporting it:\n%s", p)
			}
		})
	}
}

// A call in the operand is not something the second evaluation can be
// deleted around, whatever it returns.
func TestFuseRotatesRejectsImpureOperand(t *testing.T) {
	call := []Op{{Kind: OpCallDirect, Str: "g"}}
	ops := []Op{}
	ops = append(ops, call...)
	ops = append(ops, Op{Kind: OpConstI64, I64: 32}, Op{Kind: OpShrS, Width: 64, Unsigned: true})
	ops = append(ops, call...)
	ops = append(ops, Op{Kind: OpConstI64, I64: 32}, Op{Kind: OpShl, Width: 64})
	ops = append(ops, Op{Kind: OpOr, Width: 64}, Op{Kind: OpReturn})

	fn := u64Func(ops, 0)
	p := &Program{Funcs: []*Func{fn}}
	if FuseRotates(p) {
		t.Fatalf("fused across a call:\n%s", p)
	}
}

// A string local rides two stack slots, so a backward walk that counted
// it as one would mis-locate the operand boundary. It is refused.
func TestFuseRotatesRejectsTwoWordLocal(t *testing.T) {
	fn := &Func{
		Name:       "f",
		PtrW:       8,
		TwoWordStr: true,
		Locals: []*ast.Var{
			{Name: "s", Type: ast.StringType{}},
			{Name: "v", Type: ast.NumberType{Width: 64, Signed: false, Spelling: "u64"}},
		},
		Ops: []Op{
			{Kind: OpLoadLocal, I32: 0},
			{Kind: OpConstI64, I64: 24},
			{Kind: OpShrS, Width: 64, Unsigned: true},
			{Kind: OpLoadLocal, I32: 0},
			{Kind: OpConstI64, I64: 40},
			{Kind: OpShl, Width: 64},
			{Kind: OpOr, Width: 64},
			{Kind: OpReturn},
		},
	}
	p := &Program{Funcs: []*Func{fn}}
	if FuseRotates(p) {
		t.Fatalf("fused a two-word local read:\n%s", p)
	}
}

// The fused op folds when its operand is constant, at both widths.
func TestFoldRotr(t *testing.T) {
	cases := []struct {
		name string
		ops  []Op
		want int64
	}{
		{"i32", []Op{
			{Kind: OpConstI32, I32: 0x12345678},
			{Kind: OpConstI32, I32: 8},
			{Kind: OpRotr, Width: 32},
		}, 0x78123456},
		{"i64", []Op{
			{Kind: OpConstI64, I64: 0x0123456789abcdef},
			{Kind: OpConstI64, I64: 8},
			{Kind: OpRotr, Width: 64},
		}, -1224658842671273011},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			fn := &Func{Name: "f", Ops: append(c.ops, Op{Kind: OpReturn})}
			p := &Program{Funcs: []*Func{fn}}
			foldProgram(p)
			if len(fn.Ops) != 2 {
				t.Fatalf("expected a single constant after folding:\n%s", p)
			}
			got := fn.Ops[0].I64
			if fn.Ops[0].Kind == OpConstI32 {
				got = int64(fn.Ops[0].I32)
			}
			if got != c.want {
				t.Errorf("rotr folded to %#x, want %#x", got, c.want)
			}
		})
	}
}

// The pass is reachable from the shipped battery, not just from its own
// entry point: the cleanup fixpoint is what every backend runs.
func TestFuseRotatesRunsInCleanup(t *testing.T) {
	fn := u64Func(rotr64(16, 48), 1)
	p := &Program{Funcs: []*Func{fn}}
	OptimizeCleanup(p)
	for _, op := range fn.Ops {
		if op.Kind == OpRotr {
			return
		}
	}
	t.Fatalf("OptimizeCleanup did not fuse the rotate:\n%s", p)
}
