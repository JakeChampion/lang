package ir

import (
	"testing"

	"github.com/jakechampion/lang/internal/ast"
)

// optimised lowers src at the native pointer width and runs the whole
// battery, which is where ChainConditions sees the shapes inlining leaves.
func optimised(t *testing.T, src string) *Program {
	t.Helper()
	p := lowerSourceWith(t, src, 8)
	OptimizeProgram(p, 8)
	return p
}

func countIn(p *Program, fnName string, kind OpKind) int {
	for _, fn := range p.Funcs {
		if fn.Name == fnName {
			return countOps(fn, kind)
		}
	}
	return 0
}

// An inlined predicate's `&&` reaches the caller's `if` as a materialised
// boolean; the pass turns it into one branch per operand.
func TestChainConditionsInlinedPredicate(t *testing.T) {
	cases := []struct {
		name, src string
		brIfs     int
	}{
		{"and in if", `function is_digit(c: i32): boolean { return c >= 48 && c <= 57; }
function f(c: i32): i32 { if (is_digit(c)) { return 1; } return 2; }`, 2},
		{"and in if with else", `function is_digit(c: i32): boolean { return c >= 48 && c <= 57; }
function f(c: i32): i32 { var n: i32 = 0; if (is_digit(c)) { n = 1; } else { n = 2; } return n; }`, 2},
		{"or in if", `function is_space(c: i32): boolean { return c == 32 || c == 9; }
function f(c: i32): i32 { if (is_space(c)) { return 1; } return 2; }`, 2},
		{"negated", `function is_space(c: i32): boolean { return c == 32 || c == 9; }
function f(c: i32): i32 { if (!is_space(c)) { return 1; } return 2; }`, 2},
		{"three operands", `function ok(c: i32): boolean { return c > 0 && c < 10 && c != 5; }
function f(c: i32): i32 { if (ok(c)) { return 1; } return 2; }`, 3},
		{"loop condition", `function is_digit(c: i32): boolean { return c >= 48 && c <= 57; }
function f(s: string): i32 { var i: i32 = 0; while (i < s.len() && is_digit(s[i] as i32)) { i = i + 1; } return i; }`, 3},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			p := optimised(t, c.src)
			if n := countIn(p, "f", OpIf); n != 0 {
				t.Errorf("%d if(s) left, want the condition as branches:\n%s", n, p)
			}
			if n := countIn(p, "f", OpBrIf); n != c.brIfs {
				t.Errorf("want %d conditional branches, got %d:\n%s", c.brIfs, n, p)
			}
		})
	}
}

// A boolean that is stored rather than branched on keeps its value form.
func TestChainConditionsLeavesStoredBooleans(t *testing.T) {
	p := optimised(t, `function is_digit(c: i32): boolean { return c >= 48 && c <= 57; }
function f(c: i32): boolean { var d: boolean = is_digit(c); return d; }`)
	if n := countIn(p, "f", OpIf); n != 1 {
		t.Errorf("want the `&&` value kept as one typed if, got %d:\n%s", n, p)
	}
}

// The rewrite on op streams directly: the else arm's body moves one scope
// deeper, so a branch out of it is renumbered; an operand holding a
// branch out of the value is refused.
func TestChainConditionsRewritesOps(t *testing.T) {
	and := func(rest ...Op) []Op {
		ops := []Op{
			{Kind: OpLoadLocal, I32: 0},
			{Kind: OpIf, I32: BlockTypeI32},
			{Kind: OpLoadLocal, I32: 1},
			{Kind: OpElse},
			{Kind: OpConstI32, I32: 0},
			{Kind: OpEnd},
		}
		return append(ops, rest...)
	}
	// block; <a && b>; if { br 1 } else { br 1 } end; end
	ops := append([]Op{{Kind: OpBlock, I32: BlockTypeVoid}}, and(
		Op{Kind: OpIf, I32: BlockTypeVoid},
		Op{Kind: OpBr, I32: 1},
		Op{Kind: OpElse},
		Op{Kind: OpBr, I32: 1},
		Op{Kind: OpEnd},
		Op{Kind: OpEnd},
		Op{Kind: OpReturnVoid},
	)...)
	got, ok := chainOneCondition(ops, nil, NewCallShapes(&Program{}))
	if !ok {
		t.Fatal("the and-fed if was not rewritten")
	}
	want := []Op{
		{Kind: OpBlock, I32: BlockTypeVoid},
		{Kind: OpBlock, I32: BlockTypeVoid}, // else target
		{Kind: OpBlock, I32: BlockTypeVoid}, // past-body target
		{Kind: OpLoadLocal, I32: 0},
		{Kind: OpNot},
		{Kind: OpBrIf, I32: 0},
		{Kind: OpLoadLocal, I32: 1},
		{Kind: OpNot},
		{Kind: OpBrIf, I32: 0},
		{Kind: OpBr, I32: 2}, // the body's `br 1`, one scope deeper
		{Kind: OpBr, I32: 1}, // over the else arm
		{Kind: OpEnd},
		{Kind: OpBr, I32: 1}, // the else arm's `br 1`, unchanged
		{Kind: OpEnd},
		{Kind: OpEnd},
		{Kind: OpReturnVoid},
	}
	if !sameKinds(got, kinds(want)...) {
		t.Fatalf("got\n%v\nwant\n%v", got, want)
	}
	for i := range want {
		if got[i].I32 != want[i].I32 {
			t.Errorf("op %d: %v, want %v", i, got[i], want[i])
		}
	}

	// `a && b` branched on when true: the left operand needs a block to
	// skip the right one's branch.
	ops = append([]Op{{Kind: OpBlock, I32: BlockTypeVoid}}, and(
		Op{Kind: OpBrIf, I32: 0},
		Op{Kind: OpEnd},
		Op{Kind: OpReturnVoid},
	)...)
	got, ok = chainOneCondition(ops, nil, NewCallShapes(&Program{}))
	if !ok {
		t.Fatal("the and-fed br_if was not rewritten")
	}
	want = []Op{
		{Kind: OpBlock, I32: BlockTypeVoid},
		{Kind: OpBlock, I32: BlockTypeVoid},
		{Kind: OpLoadLocal, I32: 0},
		{Kind: OpNot},
		{Kind: OpBrIf, I32: 0},
		{Kind: OpLoadLocal, I32: 1},
		{Kind: OpBrIf, I32: 1},
		{Kind: OpEnd},
		{Kind: OpEnd},
		{Kind: OpReturnVoid},
	}
	if !sameKinds(got, kinds(want)...) {
		t.Fatalf("got\n%v\nwant\n%v", got, want)
	}
	for i := range want {
		if got[i].I32 != want[i].I32 {
			t.Errorf("op %d: %v, want %v", i, got[i], want[i])
		}
	}

	// A right operand that branches out of the value is left alone.
	ops = []Op{
		{Kind: OpBlock, I32: BlockTypeVoid},
		{Kind: OpLoadLocal, I32: 0},
		{Kind: OpIf, I32: BlockTypeI32},
		{Kind: OpBr, I32: 1},
		{Kind: OpElse},
		{Kind: OpConstI32, I32: 0},
		{Kind: OpEnd},
		{Kind: OpIf, I32: BlockTypeVoid},
		{Kind: OpEnd},
		{Kind: OpEnd},
		{Kind: OpReturnVoid},
	}
	if _, ok := chainOneCondition(ops, nil, NewCallShapes(&Program{})); ok {
		t.Error("an operand with an escaping branch was rewritten")
	}
	// A value with something else beneath it on the stack is left alone:
	// a block opened before it could not reach that value.
	ops = append([]Op{{Kind: OpConstI32, I32: 7}}, and(
		Op{Kind: OpIf, I32: BlockTypeVoid},
		Op{Kind: OpDrop},
		Op{Kind: OpEnd},
		Op{Kind: OpReturnVoid},
	)...)
	if _, ok := chainOneCondition(ops, nil, NewCallShapes(&Program{})); ok {
		t.Error("a value above another stack entry was rewritten")
	}
}

// The stack simulation counts a call's operands the way the backends do:
// a dyn call's I32 is its vtable slot, so the arguments come from the
// receiver-first signature, with the receiver word below them and the
// vtable word above; a direct closure call's count already includes the
// env pointer; an indirect call has the closure pair on top of its
// arguments. The verifier is the reference for each.
func TestChainStackEffectCountsEveryCallShape(t *testing.T) {
	i32 := ast.NumberType{Width: 32}
	shapes := NewCallShapes(&Program{})
	dyn := Op{Kind: OpCallDyn, I32: 3, Ext: &OpExt{Sig: &ast.FuncType{Params: []ast.Type{i32, i32, i32}, Result: i32}}}
	if pops, pushes, ok := chainStackEffect(dyn, nil, shapes); !ok || pops != 4 || pushes != 1 {
		t.Errorf("dyn call with two arguments: got (%d, %d, %v), want (4, 1, true): receiver, two args, vtable", pops, pushes, ok)
	}
	slotless := Op{Kind: OpCallDyn, I32: 0}
	if _, _, ok := chainStackEffect(slotless, nil, shapes); ok {
		t.Error("a dyn call without a signature was counted as taking nothing")
	}

	for _, ptrW := range []int{8, 4} {
		p := lowerSourceWith(t, `function makeAdder(n: i32): (i32) => i32 {
	function add(x: i32): i32 { return x + n; }
	return add;
}
function main(): i32 {
	var f = makeAdder(7);
	return f(35);
}`, ptrW)
		Inline(p)
		Defunctionalise(p, int32(ptrW))
		sigs := buildFuncSigs(p)
		shapes := NewCallShapes(p)
		found := false
		for _, op := range findFunc(p, "main").Ops {
			if op.Kind != OpCallClosureDirect {
				continue
			}
			found = true
			pops, pushes, ok := chainStackEffect(op, sigs, shapes)
			if !ok || pops != int(op.I32) || pushes != 1 {
				t.Errorf("ptrW=%d: direct closure call with argc %d: got (%d, %d, %v), want (%d, 1, true)", ptrW, op.I32, pops, pushes, ok, op.I32)
			}
		}
		if !found {
			t.Fatalf("ptrW=%d: main was not defunctionalised:\n%s", ptrW, p)
		}
	}
}
