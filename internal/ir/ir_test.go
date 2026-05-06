package ir

import (
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/checker"
	"github.com/jakechampion/lang/internal/parser"
)

// lowerSource parses, type-checks, and lowers src to IR. The check is
// expected to pass; failures stop the test.
func lowerSource(t *testing.T, src string) *Program {
	t.Helper()
	prog, err := parser.Parse(src)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	info, err := checker.Check(prog)
	if err != nil {
		t.Fatalf("check: %v", err)
	}
	ir, err := Lower(prog, info)
	if err != nil {
		t.Fatalf("lower: %v", err)
	}
	return ir
}

func mustContainOp(t *testing.T, p *Program, fnName string, want OpKind) {
	t.Helper()
	for _, fn := range p.Funcs {
		if fn.Name != fnName {
			continue
		}
		for _, op := range fn.Ops {
			if op.Kind == want {
				return
			}
		}
	}
	t.Errorf("expected %s in %s; ops:\n%s", want, fnName, p)
}

func TestLowerSimpleArithmetic(t *testing.T) {
	p := lowerSource(t, `function f(): number { return 1 + 2 * 3; }`)
	if len(p.Funcs) != 1 {
		t.Fatalf("got %d funcs", len(p.Funcs))
	}
	got := p.Funcs[0].Ops
	want := []OpKind{
		OpConstI32, // 1
		OpConstI32, // 2
		OpConstI32, // 3
		OpMul,
		OpAdd,
		OpReturn,
	}
	if len(got) != len(want) {
		t.Fatalf("op count mismatch: got %d, want %d:\n%s", len(got), len(want), p)
	}
	for i, w := range want {
		if got[i].Kind != w {
			t.Errorf("op[%d] = %s, want %s", i, got[i].Kind, w)
		}
	}
}

func TestLowerLocals(t *testing.T) {
	p := lowerSource(t, `function f(): number {
		var x: number = 5;
		var y: number = x + 1;
		return y;
	}`)
	mustContainOp(t, p, "f", OpStoreLocal)
	mustContainOp(t, p, "f", OpLoadLocal)
}

func TestLowerIfElse(t *testing.T) {
	p := lowerSource(t, `function f(n: number): number {
		if (n == 0) { return 1; } else { return 2; }
	}`)
	mustContainOp(t, p, "f", OpJumpIfFalse)
	mustContainOp(t, p, "f", OpLabel)
	mustContainOp(t, p, "f", OpEq)
}

func TestLowerWhileBreakContinue(t *testing.T) {
	p := lowerSource(t, `function f(): number {
		var i: number = 0;
		while (i < 10) {
			if (i == 5) { break; }
			i = i + 1;
		}
		return i;
	}`)
	// `break` becomes an OpJump to the while's end label.
	mustContainOp(t, p, "f", OpJump)
	mustContainOp(t, p, "f", OpJumpIfFalse)
}

func TestLowerForLoopWithStep(t *testing.T) {
	p := lowerSource(t, `function f(): number {
		var sum: number = 0;
		for (var i: number = 0; i < 10; i = i + 1) {
			sum = sum + i;
		}
		return sum;
	}`)
	mustContainOp(t, p, "f", OpJump)
	mustContainOp(t, p, "f", OpLabel)
}

func TestLowerDirectCall(t *testing.T) {
	p := lowerSource(t, `function add(a: number, b: number): number { return a + b; }
		function main(): number { return add(2, 3); }`)
	main := findFunc(p, "main")
	if main == nil {
		t.Fatal("main not found")
	}
	hasDirect := false
	for _, op := range main.Ops {
		if op.Kind == OpCallDirect && op.Str == "add" && op.I32 == 2 {
			hasDirect = true
		}
	}
	if !hasDirect {
		t.Errorf("expected call $add (argc=2), ops:\n%s", p)
	}
}

func TestLowerIndirectCall(t *testing.T) {
	p := lowerSource(t, `function add(a: number, b: number): number { return a + b; }
		function apply(f: (number, number) => number, a: number, b: number): number {
			return f(a, b);
		}`)
	apply := findFunc(p, "apply")
	if apply == nil {
		t.Fatal("apply not found")
	}
	hasIndirect := false
	for _, op := range apply.Ops {
		if op.Kind == OpCallIndirect {
			hasIndirect = true
		}
	}
	if !hasIndirect {
		t.Errorf("expected call_indirect, ops:\n%s", p)
	}
}

func TestLowerShortCircuitAnd(t *testing.T) {
	p := lowerSource(t, `function f(a: boolean, b: boolean): boolean { return a && b; }`)
	// `a && b` lowers to a JumpIfFalse + a fall-through evaluating b.
	got := strings.Count(p.String(), "jump_if_false")
	if got < 1 {
		t.Errorf("expected at least one jump_if_false in:\n%s", p)
	}
}

func TestLowerFloatArithmetic(t *testing.T) {
	p := lowerSource(t, `function f(): float { return 1.5 + 2.5; }`)
	mustContainOp(t, p, "f", OpFAdd)
	mustContainOp(t, p, "f", OpConstF32)
}

func TestLowerImplicitReturn(t *testing.T) {
	p := lowerSource(t, `function f(): void { var x: number = 0; }`)
	last := p.Funcs[0].Ops[len(p.Funcs[0].Ops)-1]
	if last.Kind != OpReturnVoid {
		t.Errorf("expected trailing return_void, got %s", last.Kind)
	}
}

func TestLowerImplicitReturnNumber(t *testing.T) {
	p := lowerSource(t, `function f(): number { var x: number = 0; }`)
	ops := p.Funcs[0].Ops
	if ops[len(ops)-1].Kind != OpReturn {
		t.Errorf("expected trailing return, got %s", ops[len(ops)-1].Kind)
	}
	if ops[len(ops)-2].Kind != OpConstI32 {
		t.Errorf("expected pad const before return, got %s", ops[len(ops)-2].Kind)
	}
}

func findFunc(p *Program, name string) *Func {
	for _, fn := range p.Funcs {
		if fn.Name == name {
			return fn
		}
	}
	return nil
}

func TestLowerSwitch(t *testing.T) {
	prog := lowerSource(t, `function f(n: number): number {
		switch (n) {
			case 1, 2: return 10;
			case 3: return 30;
			default: return 0;
		}
		return -1;
	}`)
	mustContainOp(t, prog, "f", OpStoreLocal) // tag stash
	mustContainOp(t, prog, "f", OpEq)
	mustContainOp(t, prog, "f", OpJumpIfFalse)
}

func TestLowerTernary(t *testing.T) {
	prog := lowerSource(t, `function f(b: boolean): number { return b ? 1 : 2; }`)
	mustContainOp(t, prog, "f", OpJumpIfFalse)
	// Ternary lowers to two const-loads followed by a join.
	mustContainOp(t, prog, "f", OpConstI32)
	mustContainOp(t, prog, "f", OpJump)
	mustContainOp(t, prog, "f", OpLabel)
}

func TestLowerArrayLitAndIndex(t *testing.T) {
	prog := lowerSource(t, `function f(): number {
		var a: number[] = [10, 20, 30];
		return a[1];
	}`)
	mustContainOp(t, prog, "f", OpAlloc)
	mustContainOp(t, prog, "f", OpStore) // length prefix + element stores
	// Indexing dispatches via __arr_idx then OpLoad.
	mustContainOp(t, prog, "f", OpCallDirect)
	mustContainOp(t, prog, "f", OpLoad)
}

func TestLowerStringIndex(t *testing.T) {
	prog := lowerSource(t, `function f(): number {
		var s: string = "abc";
		return s[1];
	}`)
	mustContainOp(t, prog, "f", OpCallDirect) // __str_idx
	mustContainOp(t, prog, "f", OpLoadByte)
}

func TestLowerStructLitAndFieldAccess(t *testing.T) {
	prog := lowerSource(t, `struct P { x: number, y: number }
		function main(): number {
			var p: P = P { x: 10, y: 32 };
			return p.x + p.y;
		}`)
	mustContainOp(t, prog, "main", OpAlloc)
	mustContainOp(t, prog, "main", OpStore)
	mustContainOp(t, prog, "main", OpLoad)
}

func TestLowerStringConcat(t *testing.T) {
	prog := lowerSource(t, `function f(): string { return "a" + "b"; }`)
	mustContainOp(t, prog, "f", OpStrConcat)
}

func TestLowerStringEquality(t *testing.T) {
	prog := lowerSource(t, `function f(): boolean { return "a" == "b"; }`)
	mustContainOp(t, prog, "f", OpStrEq)
}

