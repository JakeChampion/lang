package ir_test

import (
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/checker"
	"github.com/jakechampion/lang/internal/constfold"
	"github.com/jakechampion/lang/internal/ir"
	"github.com/jakechampion/lang/internal/parser"
)

// An aggregate literal buys its box AFTER evaluating its operands (#9614).
//
// Every aggregate in Fern is an implicit heap allocation, so "when does the
// box appear" is not an internal detail: `__heap_alloc_count()` reads the
// allocator from inside the literal, and native and the self-host used to
// print different numbers for the same source — native bought the box first,
// the self-host last. Fields first is the post-order rule, and the one every
// other allocating expression here already follows.
//
// The check is on the op stream rather than on a running program because a
// tuple or a variant small enough to stay unboxed allocates nothing at all in
// some contexts, which would let a program-level assertion pass vacuously.
// conformance/cases/eval_order_aggregate_literal covers the observable half
// for the two that always allocate.
func lowerFuncOps(t *testing.T, src, fn string) []ir.Op {
	t.Helper()
	prog, err := parser.Parse(src)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if err := constfold.Fold(prog, nil); err != nil {
		t.Fatalf("constfold: %v", err)
	}
	info, err := checker.Check(prog)
	if err != nil {
		t.Fatalf("check: %v", err)
	}
	p, err := ir.LowerWith(prog, info, 8)
	if err != nil {
		t.Fatalf("lower: %v", err)
	}
	for _, f := range p.Funcs {
		if f.Name == fn || strings.HasSuffix(f.Name, "_"+fn) {
			return f.Ops
		}
	}
	t.Fatalf("function %q not in the lowered program", fn)
	return nil
}

// firstIndex returns the index of the first op satisfying pred, or -1.
func firstIndex(ops []ir.Op, pred func(ir.Op) bool) int {
	for i, op := range ops {
		if pred(op) {
			return i
		}
	}
	return -1
}

func wantOperandBeforeAlloc(t *testing.T, name, src string) {
	t.Helper()
	ops := lowerFuncOps(t, src, "build")
	probe := firstIndex(ops, func(op ir.Op) bool {
		return op.Kind == ir.OpCallDirect && strings.Contains(op.Str, "probe")
	})
	alloc := firstIndex(ops, func(op ir.Op) bool { return op.Kind == ir.OpAlloc })
	if probe < 0 {
		t.Fatalf("%s: the operand's call is not in build's op stream", name)
	}
	if alloc < 0 {
		t.Fatalf("%s: build allocates nothing, so the case proves nothing", name)
	}
	if probe > alloc {
		t.Errorf("%s: box bought at op %d, operand evaluated at op %d — the parts must exist before the value",
			name, alloc, probe)
	}
}

const evalOrderPreamble = `struct P { a: i32, b: i32 }
enum E { Wrapped(i32, i32), Empty }
function probe(): i32 { return 1; }
`

func TestStructLitBuysItsBoxAfterTheFields(t *testing.T) {
	wantOperandBeforeAlloc(t, "struct literal", evalOrderPreamble+`
function build(): P { return P { a: probe(), b: 0 }; }
function main(): i32 { return build().a; }`)
}

func TestArrayLitBuysItsBoxAfterTheElements(t *testing.T) {
	wantOperandBeforeAlloc(t, "array literal", evalOrderPreamble+`
function build(): i32[] { return [probe(), 0]; }
function main(): i32 { return build()[0]; }`)
}

func TestTupleLitBuysItsBoxAfterTheElements(t *testing.T) {
	wantOperandBeforeAlloc(t, "tuple literal", evalOrderPreamble+`
function build(): (i32, i32) { return (probe(), 0); }
function main(): i32 { var t: (i32, i32) = build(); return t.0; }`)
}

func TestVariantCallBuysItsBoxAfterThePayload(t *testing.T) {
	wantOperandBeforeAlloc(t, "enum variant construction", evalOrderPreamble+`
function build(): E { return Wrapped(probe(), 0); }
function main(): i32 {
    match (build()) {
        Wrapped(x, _) => { return x; },
        Empty => { return 0; },
    }
}`)
}

// A literal whose operands can neither call nor allocate keeps the cheaper
// shape — the destination address is pushed first and the value stored where
// it lands — because nothing can observe which came first. Without this the
// fix would cost a store/load pair per field on every literal in the language.
//
// Nothing is evaluated ahead of the box here, so the only ops that may precede
// the OpAlloc are the size constant and the position bookkeeping.
func TestPureLiteralKeepsTheDirectStoreShape(t *testing.T) {
	ops := lowerFuncOps(t, evalOrderPreamble+`
function build(): P { return P { a: 7, b: 2 }; }
function main(): i32 { return build().a; }`, "build")
	alloc := firstIndex(ops, func(op ir.Op) bool { return op.Kind == ir.OpAlloc })
	if alloc < 0 {
		t.Fatalf("build allocates nothing, so the case proves nothing")
	}
	for i, op := range ops[:alloc] {
		switch op.Kind {
		case ir.OpConstI32, ir.OpLine, ir.OpCoverPoint:
		default:
			t.Errorf("op %d (%v) runs before the box is bought: a literal of constants must not spill", i, op.Kind)
		}
	}
}
