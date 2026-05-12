package ir

import "testing"

// loweredAndDefuncdAndElided mirrors loweredAndDefuncd but also
// runs ElideClosurePair so callers can assert on the post-elide
// shape of the IR (no OpMakeClosure for fully-rewritten chains).
func loweredAndDefuncdAndElided(t *testing.T, src string) *Program {
	t.Helper()
	p := lowerSource(t, src)
	Inline(p)
	Defunctionalise(p, 8)
	ElideClosurePair(p, 8)
	return p
}

func countOps(fn *Func, kind OpKind) int {
	n := 0
	for _, op := range fn.Ops {
		if op.Kind == kind {
			n++
		}
	}
	return n
}

// Baseline regression for the existing direct-call shape: a
// nested zero-capture closure called from the same scope must
// still elide to OpMakeEnv after the chain-aware rewrite. This
// test would have failed if the new fixed-point analysis broke
// the simple single-slot case the original pass handled.
func TestElideDirectZeroCapture(t *testing.T) {
	p := loweredAndDefuncdAndElided(t, `function main(): i32 {
		function inner(): i32 { return 1; }
		return inner();
	}`)
	main := findFunc(p, "main")
	if main == nil {
		t.Fatal("main not found")
	}
	if got := countOps(main, OpMakeClosure); got != 0 {
		t.Errorf("expected zero OpMakeClosure after elide, got %d:\n%s", got, p)
	}
	if got := countOps(main, OpMakeEnv); got != 1 {
		t.Errorf("expected one OpMakeEnv after elide, got %d:\n%s", got, p)
	}
}

// Chain support: `var f = answer; f()` has an intermediate
// OpStoreLocal whose writer is OpLoadLocal (not OpMakeClosure).
// The fixed-point eligibility analysis must accept this shape
// and rewrite the upstream OpMakeClosure to OpMakeEnv so the
// 16-byte pair allocation disappears.
func TestElideChainedZeroCapture(t *testing.T) {
	p := loweredAndDefuncdAndElided(t, `function main(): i32 {
		function answer(): i32 { return 42; }
		var f = answer;
		return f();
	}`)
	main := findFunc(p, "main")
	if main == nil {
		t.Fatal("main not found")
	}
	if got := countOps(main, OpMakeClosure); got != 0 {
		t.Errorf("expected zero OpMakeClosure after chain elide, got %d:\n%s", got, p)
	}
	if got := countOps(main, OpMakeEnv); got != 1 {
		t.Errorf("expected one OpMakeEnv after chain elide, got %d:\n%s", got, p)
	}
}

// Multi-hop chain: `var a = answer; var b = a; var c = b; c()`
// requires the fixed-point analysis to propagate eligibility
// across three alias edges. A single-pass analysis would stop
// at the first hop and leave the OpMakeClosure unrewritten.
func TestElideMultiHopChain(t *testing.T) {
	p := loweredAndDefuncdAndElided(t, `function main(): i32 {
		function answer(): i32 { return 17; }
		var a = answer;
		var b = a;
		var c = b;
		return c();
	}`)
	main := findFunc(p, "main")
	if main == nil {
		t.Fatal("main not found")
	}
	if got := countOps(main, OpMakeClosure); got != 0 {
		t.Errorf("expected zero OpMakeClosure after multi-hop elide, got %d:\n%s", got, p)
	}
}

// Captures-bearing closure: chain elide rewrites OpMakeClosure
// to OpMakeEnv (which still allocates only the env block, not
// a pair). The captured value still has to flow into the env;
// only the pair wrapper goes away. Sanity-check that the pass
// fires for this case too.
func TestElideChainedWithCapture(t *testing.T) {
	p := loweredAndDefuncdAndElided(t, `function main(): i32 {
		var n: i32 = 10;
		function add(x: i32): i32 { return x + n; }
		var f = add;
		return f(7);
	}`)
	main := findFunc(p, "main")
	if main == nil {
		t.Fatal("main not found")
	}
	if got := countOps(main, OpMakeClosure); got != 0 {
		t.Errorf("expected zero OpMakeClosure after chain-with-capture elide, got %d:\n%s", got, p)
	}
	if got := countOps(main, OpMakeEnv); got != 1 {
		t.Errorf("expected one OpMakeEnv after chain-with-capture elide, got %d:\n%s", got, p)
	}
}

// A closure value that escapes — passed as an arg, stored in a
// data structure, or otherwise consumed outside the canonical
// call pattern — must NOT be elided. The slot still needs the
// real {fn_ptr, env_ptr} pair because some other reader will
// dereference it.
//
// This regression-tests the "every reader must be canonical or
// alias" check. A single non-canonical reader disqualifies the
// whole equivalence class.
func TestElideKeepsEscapingClosure(t *testing.T) {
	// `print_closure` returns its arg unchanged. Calling it
	// with `f` forces `f` to flow through a non-canonical
	// reader (OpLoadLocal followed by an arg push, not by
	// the +8/add/load/call dance).
	p := loweredAndDefuncdAndElided(t, `function take(g: () => i32): i32 {
		return g();
	}
	function main(): i32 {
		function answer(): i32 { return 42; }
		return take(answer);
	}`)
	main := findFunc(p, "main")
	if main == nil {
		t.Fatal("main not found")
	}
	// The closure pair must survive — `take` receives a real
	// {fn_ptr, env_ptr} pair and will dispatch through it.
	if got := countOps(main, OpMakeClosure); got == 0 {
		t.Errorf("expected OpMakeClosure to survive escape, got 0:\n%s", p)
	}
}
