package ir

import "testing"

// loweredAndDefuncd parses, type-checks, lowers, runs Inline +
// Defunctionalise, and returns the resulting Program. Mirrors
// loweredAndInlined in inline_test.go; kept separate so each pass
// can be exercised independently when investigating regressions.
func loweredAndDefuncd(t *testing.T, src string) *Program {
	t.Helper()
	p := lowerSource(t, src)
	Inline(p)
	Defunctionalise(p)
	return p
}

// A local function declaration with no captures lowers, post
// closureconv, to `var <name> = MakeClosure(target, [])`. Calling
// `<name>` from the same scope should defunctionalise to a direct
// call to the hoisted target.
func TestDefuncRewritesLocalFunctionCall(t *testing.T) {
	p := loweredAndDefuncd(t, `function main(): i32 {
		function inner(): i32 { return 42; }
		return inner();
	}`)
	main := findFunc(p, "main")
	if main == nil {
		t.Fatal("main not found")
	}
	indirectCount := 0
	directClosureCount := 0
	for _, op := range main.Ops {
		switch op.Kind {
		case OpCallIndirect:
			indirectCount++
		case OpCallClosureDirect:
			directClosureCount++
		}
	}
	if indirectCount != 0 {
		t.Errorf("expected zero OpCallIndirect after defunctionalisation, got %d:\n%s", indirectCount, p)
	}
	if directClosureCount != 1 {
		t.Errorf("expected exactly one OpCallClosureDirect, got %d:\n%s", directClosureCount, p)
	}
}

// A captured-variable closure: `function add(x) { return x + n; }`
// closureconv turns into `var add = MakeClosure(target, [n])`.
// Single MakeClosure flow → defunctionalises to a direct call;
// the env-load synthesises the captured `n` access via the
// closure_pair+4 read path.
func TestDefuncRewritesCapturingClosure(t *testing.T) {
	p := loweredAndDefuncd(t, `function main(): i32 {
		var n: i32 = 7;
		function add(x: i32): i32 { return x + n; }
		return add(35);
	}`)
	main := findFunc(p, "main")
	if main == nil {
		t.Fatal("main not found")
	}
	for _, op := range main.Ops {
		if op.Kind == OpCallIndirect {
			t.Errorf("expected OpCallIndirect to be defunctionalised:\n%s", p)
		}
	}
}

// Cross-function closure return (the closure-factory pattern):
// `var f = makeAdder(7);` puts a closure pair into f, but the
// flow source is a CALL, not a MakeClosure. The conservative
// analysis keeps these as OpCallIndirect.
func TestDefuncSkipsCrossFunctionFlow(t *testing.T) {
	p := loweredAndDefuncd(t, `function makeAdder(n: i32): (i32) => i32 {
		function add(x: i32): i32 { return x + n; }
		return add;
	}
	function main(): i32 {
		var f = makeAdder(7);
		return f(35);
	}`)
	main := findFunc(p, "main")
	if main == nil {
		t.Fatal("main not found")
	}
	hasIndirect := false
	for _, op := range main.Ops {
		if op.Kind == OpCallIndirect {
			hasIndirect = true
		}
	}
	if !hasIndirect {
		t.Errorf("expected OpCallIndirect to survive on a cross-function closure flow:\n%s", p)
	}
}
