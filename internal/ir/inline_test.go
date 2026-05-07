package ir

import "testing"

// loweredAndInlined parses, type-checks, lowers, and runs Inline.
func loweredAndInlined(t *testing.T, src string) *Program {
	t.Helper()
	p := lowerSource(t, src)
	Inline(p)
	return p
}

// `dbl(7)` substitutes the body in place: the OpCallDirect goes
// away, replaced by an OpStoreLocal binding the arg plus the
// inlined `x * 2` ops.
func TestInlineSubstitutesBody(t *testing.T) {
	p := loweredAndInlined(t, `function dbl(x: number): number { return x * 2; }
		function main(): number { return dbl(7); }`)
	main := findFunc(p, "main")
	if main == nil {
		t.Fatal("main not found")
	}
	for _, op := range main.Ops {
		if op.Kind == OpCallDirect && op.Str == "dbl" {
			t.Fatalf("OpCallDirect dbl should have been inlined:\n%s", p)
		}
	}
	// Mul + the const 2 should now appear directly in main.
	mustContainOp(t, p, "main", OpMul)
}

// Recursive functions skip inlining — the `if` in the body is a
// control-flow disqualifier *and* the recursive call would loop, so
// either rule rejects it. The OpCallDirect must survive untouched.
func TestInlineSkipsRecursiveFunction(t *testing.T) {
	p := loweredAndInlined(t, `function fact(n: number): number {
		if (n == 0) { return 1; }
		return n * fact(n - 1);
	}`)
	mustContainOp(t, p, "fact", OpCallDirect)
}

// The callee uses its parameter twice — the IR inliner binds the
// arg into a fresh slot once, so the substituted body reads the
// slot for both uses. No duplication of the arg expression.
func TestInlineDoesNotDuplicateArgEvaluation(t *testing.T) {
	p := loweredAndInlined(t, `function dbl(x: number): number { return x + x; }
		function main(): number { return dbl(3); }`)
	main := findFunc(p, "main")
	// Exactly one OpConstI32 3 (the arg literal) — substitution must
	// reuse a local rather than re-evaluate.
	count := 0
	for _, op := range main.Ops {
		if op.Kind == OpConstI32 && op.I32 == 3 {
			count++
		}
	}
	if count != 1 {
		t.Errorf("expected single `const 3` (one arg eval), got %d:\n%s", count, p)
	}
}

// Call sites with non-trivial argument expressions inline cleanly.
// The IR fold collapses the substituted body's arithmetic later in
// the pipeline; here we only check that inlining didn't refuse.
func TestInlineAcceptsArbitraryArgExpressions(t *testing.T) {
	p := loweredAndInlined(t, `function dbl(x: number): number { return x * 2; }
		function main(): number { return dbl(1 + 2); }`)
	main := findFunc(p, "main")
	for _, op := range main.Ops {
		if op.Kind == OpCallDirect && op.Str == "dbl" {
			t.Fatalf("call with `1 + 2` arg should still inline:\n%s", p)
		}
	}
}

// Each call site allocates its own slot range so two inlines of the
// same callee don't share state. We check via NumScratch, which
// grows by the callee's slot count for every site.
func TestInlineAppendsFreshSlotsPerCallSite(t *testing.T) {
	p := loweredAndInlined(t, `function dbl(x: number): number { return x * 2; }
		function main(): number { return dbl(3) + dbl(4); }`)
	main := findFunc(p, "main")
	// dbl has 1 slot (its single param `x`). Two inlines → 2 fresh
	// slots beyond main's own (0 params, 0 locals, 0 prior scratches).
	if main.NumScratch != 2 {
		t.Errorf("expected NumScratch=2 after two inlines, got %d", main.NumScratch)
	}
}

// Functions whose body contains a call themselves are NOT
// candidates — inlining them would duplicate the inner call and
// could blow recursion. Verify they're left alone.
func TestInlineSkipsFunctionsContainingCalls(t *testing.T) {
	p := loweredAndInlined(t, `function add(a: number, b: number): number { return a + b; }
		function compose(x: number): number { return add(x, x); }
		function main(): number { return compose(5); }`)
	// compose contains a call to add, so compose should NOT be
	// inlined into main.
	main := findFunc(p, "main")
	hasComposeCall := false
	for _, op := range main.Ops {
		if op.Kind == OpCallDirect && op.Str == "compose" {
			hasComposeCall = true
		}
	}
	if !hasComposeCall {
		t.Errorf("compose contains a call and should not be inlined:\n%s", p)
	}
}

// Inline + Fold composes: even when Fold can't see through the
// arg-binding `store; ...; load` pair to fully collapse `dbl(7)`
// to a constant (a future store-load propagation pass would do
// that), Fold still simplifies the substituted body's literal
// arithmetic. Here `2 * y` survives because y is a runtime param,
// but a literal call like `7 * 2` inside the inlined body folds.
func TestInlineThenFoldSimplifiesSubstitutedBody(t *testing.T) {
	p := lowerSource(t, `function bumped(x: number): number { return x + (1 + 2); }
		function main(n: number): number { return bumped(n); }`)
	Inline(p)
	Fold(p)
	main := findFunc(p, "main")
	// `1 + 2` inside the inlined body collapses to 3 — the constant
	// arithmetic inside the substituted body is reachable for Fold.
	found := false
	for _, op := range main.Ops {
		if op.Kind == OpConstI32 && op.I32 == 3 {
			found = true
		}
	}
	if !found {
		t.Errorf("expected post-fold const.i32 3 from inlined body:\n%s", p)
	}
}

// Inline must preserve structural-CF balance — the substituted body
// has no internal control flow (eligibility forbids it), so the
// caller's existing structure is undisturbed.
func TestInlineKeepsStructuredCFBalanced(t *testing.T) {
	p := loweredAndInlined(t, `function dbl(x: number): number { return x * 2; }
		function main(n: number): number {
			if (n > 0) { return dbl(n); }
			return 0;
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
			}
		}
		if depth != 0 {
			t.Errorf("%s: ended at depth %d, want 0", fn.Name, depth)
		}
	}
}
