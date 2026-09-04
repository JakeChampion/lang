package ir

import "testing"

// Fold's pruneConstIf collapses a constant-conditioned `if` to the arm
// that runs. When that arm returns, everything after it in the function
// is unreachable — dead code the cleanup itself created, which a DCE
// ordered before the cleanup cannot have seen.
func TestOptimizeCleanupSweepsItsOwnDeadCode(t *testing.T) {
	const src = `
function f(x: i32): i32 {
	if (true) { return x + 1; }
	return x * 1000;
}`
	p := lowerSource(t, src)
	// The order every backend runs: a DCE over the lowering's own dead
	// code, then the cleanup fixpoint.
	EliminateDeadCode(p)
	OptimizeCleanup(p)
	fn := findFunc(p, "f")
	if fn == nil {
		t.Fatal("f not found")
	}
	// The `x * 1000` continuation is unreachable once the constant guard
	// resolves. Nothing may multiply.
	for i, o := range fn.Ops {
		if o.Kind == OpConstI32 && o.I32 == 1000 {
			t.Errorf("op[%d] is the dead `x * 1000` constant; cleanup left its own dead code:\n%s", i, p)
		}
	}
	// And the sweep must not have eaten the live arm.
	sawReturn := false
	for _, o := range fn.Ops {
		if o.Kind == OpReturn {
			sawReturn = true
		}
	}
	if !sawReturn {
		t.Errorf("live arm lost its return:\n%s", p)
	}
}

// The fixpoint must still converge: running it twice may not change a
// program the first call already settled.
func TestOptimizeCleanupIsIdempotent(t *testing.T) {
	const src = `
function f(x: i32): i32 {
	if (true) { return x + 1; }
	return x * 1000;
}
function g(a: i32): i32 {
	var t: i32 = a * 4 + 0;
	return t;
}`
	p := lowerSource(t, src)
	EliminateDeadCode(p)
	OptimizeCleanup(p)
	before := p.String()
	OptimizeCleanup(p)
	if after := p.String(); after != before {
		t.Errorf("OptimizeCleanup is not idempotent:\nbefore:\n%s\nafter:\n%s", before, after)
	}
}
