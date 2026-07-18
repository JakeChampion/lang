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
	p := loweredAndInlined(t, `function dbl(x: i32): i32 { return x * 2; }
		function main(): i32 { return dbl(7); }`)
	main := findFunc(p, "main")
	if main == nil {
		t.Fatal("main not found")
	}
	for _, op := range main.Ops {
		if op.Kind == OpCallDirect && op.Str == "dbl" {
			t.Fatalf("OpCallDirect dbl should have been inlined:\n%s", p)
		}
	}
	// `x * 2` strength-reduces to `x << 1` in the IR builder, so
	// OpShl (not OpMul) is what we expect to see in main after
	// inlining.
	mustContainOp(t, p, "main", OpShl)
}

// Recursive functions skip inlining — the `if` in the body is a
// control-flow disqualifier *and* the recursive call would loop, so
// either rule rejects it. The OpCallDirect must survive untouched.
func TestInlineSkipsRecursiveFunction(t *testing.T) {
	p := loweredAndInlined(t, `function fact(n: i32): i32 {
		if (n == 0) { return 1; }
		return n * fact(n - 1);
	}`)
	mustContainOp(t, p, "fact", OpCallDirect)
}

// The callee uses its parameter twice — the IR inliner binds the
// arg into a fresh slot once, so the substituted body reads the
// slot for both uses. No duplication of the arg expression.
func TestInlineDoesNotDuplicateArgEvaluation(t *testing.T) {
	p := loweredAndInlined(t, `function dbl(x: i32): i32 { return x + x; }
		function main(): i32 { return dbl(3); }`)
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
	p := loweredAndInlined(t, `function dbl(x: i32): i32 { return x * 2; }
		function main(): i32 { return dbl(1 + 2); }`)
	main := findFunc(p, "main")
	for _, op := range main.Ops {
		if op.Kind == OpCallDirect && op.Str == "dbl" {
			t.Fatalf("call with `1 + 2` arg should still inline:\n%s", p)
		}
	}
}

// Each call site allocates its own slot range so two inlines of the
// same callee don't share state. We check via len(ScratchTypes),
// which grows by the callee's slot count for every site.
func TestInlineAppendsFreshSlotsPerCallSite(t *testing.T) {
	p := loweredAndInlined(t, `function dbl(x: i32): i32 { return x * 2; }
		function main(): i32 { return dbl(3) + dbl(4); }`)
	main := findFunc(p, "main")
	// dbl has 1 slot (its single param `x`). Two inlines → 2 fresh
	// slots beyond main's own (0 params, 0 locals, 0 prior scratches).
	if got := len(main.ScratchTypes); got != 2 {
		t.Errorf("expected len(ScratchTypes)=2 after two inlines, got %d", got)
	}
}

// Two-level call chain (`main → compose → add`): the inliner
// iterates a fixpoint pass set, so on pass 1 compose substitutes
// into main with the add call still in the spliced body; pass 2
// then sees the now-exposed add call at top level and substitutes
// that too. End state: main has neither call op — both bodies are
// flat in main.
func TestInlineFlattensTwoLevelCallChain(t *testing.T) {
	p := loweredAndInlined(t, `function add(a: i32, b: i32): i32 { return a + b; }
		function compose(x: i32): i32 { return add(x, x); }
		function main(): i32 { return compose(5); }`)
	main := findFunc(p, "main")
	for _, op := range main.Ops {
		if op.Kind == OpCallDirect {
			t.Errorf("expected both compose and add to be inlined; saw call %q:\n%s", op.Str, p)
		}
	}
}

// Inline + Fold composes: even when Fold can't see through the
// arg-binding `store; ...; load` pair to fully collapse `dbl(7)`
// to a constant (a future store-load propagation pass would do
// that), Fold still simplifies the substituted body's literal
// arithmetic. Here `2 * y` survives because y is a runtime param,
// but a literal call like `7 * 2` inside the inlined body folds.
func TestInlineThenFoldSimplifiesSubstitutedBody(t *testing.T) {
	p := lowerSource(t, `function bumped(x: i32): i32 { return x + (1 + 2); }
		function main(n: i32): i32 { return bumped(n); }`)
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

// Inlining a float-param callee adds an f32 slot to the caller's
// ScratchTypes — codegen relies on that to declare the WAT local
// with the right type, otherwise the validator rejects an `f32`
// value being stored into an `i32` slot.
func TestInlineRecordsFloatScratchTypes(t *testing.T) {
	p := loweredAndInlined(t, `function neg(x: f32): f32 { return -x; }
		function main(): f32 { return neg(3.5); }`)
	main := findFunc(p, "main")
	if len(main.ScratchTypes) == 0 {
		t.Fatalf("expected at least one scratch slot, got 0:\n%s", p)
	}
	// First scratch is the inlined `x` param, which is a float.
	if _, ok := main.ScratchTypes[0].(interface{ String() string }); !ok {
		t.Fatal("ScratchTypes[0] missing")
	}
	if got := main.ScratchTypes[0].String(); got != "f32" {
		t.Errorf("ScratchTypes[0] = %s, want f32", got)
	}
}

// Inline must preserve structural-CF balance — the substituted body
// has no internal control flow (eligibility forbids it), so the
// caller's existing structure is undisturbed.
func TestInlineKeepsStructuredCFBalanced(t *testing.T) {
	p := loweredAndInlined(t, `function dbl(x: i32): i32 { return x * 2; }
		function main(n: i32): i32 {
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

// A callee with a single trailing return + internal control flow
// (an if-else expression) inlines without a wrapper block. Pure
// straight-line splice: the trailing OpReturn is dropped and the
// caller's continuation picks up the value off the operand stack.
func TestInlineControlFlowWithoutEarlyReturn(t *testing.T) {
	p := loweredAndInlined(t, `function abs(n: i32): i32 {
		var v: i32 = n;
		if (v < 0) { v = 0 - v; }
		return v;
	}
function main(): i32 { return abs(0 - 7); }`)
	main := findFunc(p, "main")
	if main == nil {
		t.Fatal("main not found")
	}
	for _, op := range main.Ops {
		if op.Kind == OpCallDirect && op.Str == "abs" {
			t.Fatalf("OpCallDirect abs should have been inlined:\n%s", p)
		}
	}
}

// A callee with an early return wraps in a block; the inner Return
// becomes an OpBr targeting the wrapper. Verify the wrapper is
// emitted and the inner call is gone.
func TestInlineEarlyReturnWrapsInBlock(t *testing.T) {
	p := loweredAndInlined(t, `function clamp_zero(n: i32): i32 {
		if (n < 0) { return 0; }
		return n;
	}
function main(): i32 { return clamp_zero(0 - 5); }`)
	main := findFunc(p, "main")
	if main == nil {
		t.Fatal("main not found")
	}
	for _, op := range main.Ops {
		if op.Kind == OpCallDirect && op.Str == "clamp_zero" {
			t.Fatalf("OpCallDirect clamp_zero should have been inlined:\n%s", p)
		}
	}
	// At least one OpBlock must appear in main as the return-target
	// wrapper — there's no other source of blocks in this program.
	hasBlock := false
	for _, op := range main.Ops {
		if op.Kind == OpBlock {
			hasBlock = true
			break
		}
	}
	if !hasBlock {
		t.Errorf("expected a wrapper block for the early-return inline:\n%s", p)
	}
}

// `@noinline` excludes an otherwise-trivially-inlineable callee: the
// OpCallDirect survives (#4412 Rec §14).
func TestInlineHintNeverBlocksInlining(t *testing.T) {
	p := loweredAndInlined(t, `@noinline
function dbl(x: i32): i32 { return x * 2; }
function main(): i32 { return dbl(7); }`)
	main := findFunc(p, "main")
	if main == nil {
		t.Fatal("main not found")
	}
	var sawCall bool
	for _, op := range main.Ops {
		if op.Kind == OpCallDirect && op.Str == "dbl" {
			sawCall = true
		}
	}
	if !sawCall {
		t.Fatalf("@noinline dbl should NOT have been inlined:\n%s", p)
	}
}

// `@inline` lifts the size cap: a callee over inlineSizeLimit ops still
// substitutes. The body is a long straight-line accumulator chain that
// comfortably exceeds the cap; without the hint it stays a call.
func TestInlineHintAlwaysLiftsSizeCap(t *testing.T) {
	body := "var a: i32 = x;\n"
	for i := 0; i < 60; i++ {
		body += "a = a + x;\na = a - 1;\n"
	}
	src := "function big(x: i32): i32 {\n" + body + "return a;\n}\n" +
		"function main(): i32 { return big(3); }"

	// Baseline: over the cap, not inlined.
	p := loweredAndInlined(t, src)
	if fn := findFunc(p, "big"); fn != nil && len(fn.Ops) <= inlineSizeLimit {
		t.Fatalf("test premise broken: big has %d ops, need > %d", len(fn.Ops), inlineSizeLimit)
	}
	mustContainOp(t, p, "main", OpCallDirect)

	// Hinted: same body inlines.
	p2 := loweredAndInlined(t, "@inline\n"+src)
	main := findFunc(p2, "main")
	if main == nil {
		t.Fatal("main not found")
	}
	for _, op := range main.Ops {
		if op.Kind == OpCallDirect && op.Str == "big" {
			t.Fatalf("@inline big should have been inlined despite exceeding the size cap:\n%s", p2)
		}
	}
}
