package ir

import (
	"strings"
	"testing"
)

// loweredAndOptimized parses, type-checks, lowers, and runs TCO on src.
func loweredAndOptimized(t *testing.T, src string) *Program {
	t.Helper()
	p := lowerSource(t, src)
	TailCallOptimize(p)
	return p
}

// A function with a `return f(...)` self-tail call gets its body
// wrapped in a single outer OpLoop, the OpCallDirect+OpReturn pair
// disappears, and a new OpBr replaces it. Parameters are re-stored
// in reverse so each argument lands in the correct slot.
func TestTCORewritesSelfTailCall(t *testing.T) {
	src := `function f(n: number, acc: number): number {
		if (n == 0) { return acc; }
		return f(n - 1, acc + n);
	}`
	p := loweredAndOptimized(t, src)
	fn := findFunc(p, "f")
	if fn == nil {
		t.Fatal("f not found")
	}
	// First op of the rewritten body is the wrapping loop.
	if fn.Ops[0].Kind != OpLoop {
		t.Fatalf("expected OpLoop wrapper at op[0], got %s:\n%s", fn.Ops[0].Kind, p)
	}
	// The OpCallDirect for `f` must be gone — no recursive call op
	// should survive when TCO catches the only call site.
	for _, op := range fn.Ops {
		if op.Kind == OpCallDirect && op.Str == "f" {
			t.Errorf("expected OpCallDirect $f to be rewritten away:\n%s", p)
		}
	}
	// At least one OpBr must remain to close the loop with a back-edge.
	mustContainOp(t, p, "f", OpBr)
}

// The TCO pass leaves non-recursive functions completely alone — no
// loop wrapping and no extra ops. The wrapper carries a real cost
// (an extra block frame), so we shouldn't pay it for nothing.
func TestTCOLeavesNonRecursiveFunctionsAlone(t *testing.T) {
	src := `function add(a: number, b: number): number { return a + b; }`
	before := lowerSource(t, src)
	beforeOps := append([]Op(nil), findFunc(before, "add").Ops...)
	after := loweredAndOptimized(t, src)
	afterOps := findFunc(after, "add").Ops
	if len(beforeOps) != len(afterOps) {
		t.Fatalf("non-recursive function got rewritten: before %d ops, after %d", len(beforeOps), len(afterOps))
	}
	for i := range beforeOps {
		if beforeOps[i].Kind != afterOps[i].Kind {
			t.Errorf("op[%d]: %s → %s (no change expected)", i, beforeOps[i].Kind, afterOps[i].Kind)
		}
	}
}

// A non-tail call to self (used as part of a larger expression) is
// NOT rewritten — TCO only fires when the call is immediately
// followed by a return.
func TestTCOIgnoresNonTailRecursion(t *testing.T) {
	src := `function fact(n: number): number {
		if (n == 0) { return 1; }
		return n * fact(n - 1);
	}`
	p := loweredAndOptimized(t, src)
	fn := findFunc(p, "fact")
	if fn == nil {
		t.Fatal("fact not found")
	}
	hasCall := false
	for _, op := range fn.Ops {
		if op.Kind == OpCallDirect && op.Str == "fact" {
			hasCall = true
		}
	}
	if !hasCall {
		t.Errorf("non-tail recursion was incorrectly optimised away:\n%s", p)
	}
	// And the wrapper loop should NOT have been added — no eligible
	// tail call means no wrap.
	if fn.Ops[0].Kind == OpLoop {
		t.Errorf("non-tail-recursive function should not be wrapped:\n%s", p)
	}
}

// The branch depth chosen for the back-edge must walk past every
// scope between the call site and the wrapping loop. Wrapping a tail
// call in two extra `if`s pushes the depth from 1 (loop only) to 3
// (loop + if + if), so the OpBr immediate moves with it.
func TestTCOBranchDepthMatchesScopeNesting(t *testing.T) {
	src := `function f(n: number): number {
		if (n > 100) {
			if (n > 50) {
				return f(n - 1);
			}
		}
		return n;
	}`
	p := loweredAndOptimized(t, src)
	fn := findFunc(p, "f")
	// Walk the rewritten ops and find the OpBr that came from the
	// rewrite (the only one outside structured-CF prelude/postlude).
	// Two enclosing ifs + the wrap loop = depth 3 at the call site;
	// br target distance is depth-1 = 2.
	var foundBr int32 = -1
	depth := int32(0)
	for i, op := range fn.Ops {
		switch op.Kind {
		case OpBlock, OpLoop, OpIf:
			depth++
		case OpEnd:
			depth--
		}
		if op.Kind == OpBr {
			// We expect the rewrite OpBr to follow a run of
			// OpStoreLocal that landed args back in params; the
			// preceding op should be OpStoreLocal slot 0.
			prev := fn.Ops[i-1]
			if prev.Kind == OpStoreLocal && prev.I32 == 0 {
				foundBr = op.I32
				break
			}
		}
	}
	if foundBr < 0 {
		t.Fatalf("no rewrite OpBr found:\n%s", p)
	}
	if foundBr != 2 {
		t.Errorf("OpBr depth = %d, want 2 (loop + outer if + inner if)", foundBr)
	}
}

// Post-TCO ops must still satisfy the structured-control-flow
// invariants every backend assumes: each Block/Loop/If matched by
// an End, depth never negative, and every Br/BrIf target in range.
// The wrapper loop + scope-aware depth rewrite has to preserve this.
func TestTCOOutputStaysBalanced(t *testing.T) {
	p := loweredAndOptimized(t, `function sumRec(n: number, acc: number): number {
		if (n == 0) { return acc; }
		if (n > 100) {
			while (n > 50) { n = n - 1; }
		}
		return sumRec(n - 1, acc + n);
	}`)
	for _, fn := range p.Funcs {
		depth := 0
		for i, op := range fn.Ops {
			switch op.Kind {
			case OpBlock, OpLoop, OpIf:
				depth++
			case OpEnd:
				depth--
				if depth < 0 {
					t.Fatalf("%s: op %d (%s): depth went negative", fn.Name, i, op.Kind)
				}
			case OpBr, OpBrIf:
				if op.I32 < 0 || op.I32 > int32(depth-1) {
					t.Errorf("%s: op %d (%s %d): branch depth out of range (depth=%d)",
						fn.Name, i, op.Kind, op.I32, depth)
				}
			}
		}
		if depth != 0 {
			t.Errorf("%s: ended at depth %d, want 0", fn.Name, depth)
		}
	}
}

// The implicit-return at function end stays untouched. A function
// that doesn't fall through any tail-call branch still has its
// non-tail-call return intact.
func TestTCOPreservesNonTailReturns(t *testing.T) {
	src := `function f(n: number): number {
		if (n == 0) { return 99; }
		return f(n - 1);
	}`
	p := loweredAndOptimized(t, src)
	got := p.String()
	// The literal 99 push must still be present (the early return
	// branch). And the body is wrapped in a single outer loop.
	if !strings.Contains(got, "const.i32 99") {
		t.Errorf("expected const.i32 99 to survive:\n%s", got)
	}
	mustContainOp(t, p, "f", OpReturn)
}
