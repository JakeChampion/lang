package ir

import (
	"fmt"
	"strings"
	"testing"
)

// exportSizePolicySrc builds a program whose callee `big` sits in the band
// where siteAllows consults the reference count: larger than inlineSizeLimit
// (80 ops), no larger than inlineLoopSizeLimit (160). It is called once, from
// no loop, so `refs == 1` is the only clause that can admit it.
//
// The argument has to be genuinely non-constant, and keeping it that way takes
// care: `caller` is small enough to be inlined into `main` first, and if
// `main` passed literals the call would arrive at pass 2 with constant
// arguments and be admitted by the `constArgs` clause instead — which would
// leave this case green no matter what the reference count said. `opaque` is
// self-recursive, so it is never inlined and its result is never folded.
func exportSizePolicySrc() string {
	terms := make([]string, 30)
	for i := range terms {
		terms[i] = "a * b"
	}
	return fmt.Sprintf(
		"function opaque(n: i32): i32 { if (n <= 0) { return 2; } return opaque(n - 1); }\n"+
			"function big(a: i32, b: i32): i32 { return %s; }\n"+
			"function caller(a: i32, b: i32): i32 { return big(a, b); }\n"+
			"function main(): i32 { var x: i32 = opaque(3); return caller(x, x); }",
		strings.Join(terms, " + "))
}

// callsToIn counts OpCallDirect sites for callee inside one function. It is
// deliberately scoped to a single caller: Inline leaves the original callee
// behind (the dead-function cull removes it later), so a program-wide count
// also sees call sites in bodies that are already unreachable.
func callsToIn(p *Program, caller, callee string) int {
	fn := findFunc(p, caller)
	if fn == nil {
		return -1
	}
	n := 0
	for _, op := range fn.Ops {
		if op.Kind == OpCallDirect && op.Str == callee {
			n++
		}
	}
	return n
}

// An exported function must not be admitted over the flat size cap by the
// "sole reference, so the original dies" shortcut: the original does NOT die,
// because the dead-function cull roots it for the caller outside the program.
//
// The unexported half is what keeps this honest — it proves the shortcut
// really does fire on this shape, so the exported half is testing the export
// and not some unrelated disqualifier.
func TestInlineSizePolicyCountsTheExternalCaller(t *testing.T) {
	src := exportSizePolicySrc()

	// Precondition: `big` really is in the reference-count band. If a change
	// to lowering moves it out, this test would silently stop testing
	// anything, so fail loudly instead.
	sized := lowerSource(t, src)
	body := len(findFunc(sized, "big").Ops)
	if body <= inlineSizeLimit || body > inlineLoopSizeLimit {
		t.Fatalf("`big` is %d ops, outside the (%d, %d] band this case needs",
			body, inlineSizeLimit, inlineLoopSizeLimit)
	}

	t.Run("not exported: the shortcut fires", func(t *testing.T) {
		p := lowerSource(t, src)
		Inline(p)
		if n := callsToIn(p, "main", "big"); n != 0 {
			t.Errorf("main still has %d call(s) to `big`; the refs==1 shortcut should have inlined it", n)
		}
	})

	t.Run("exported: the shortcut does not fire", func(t *testing.T) {
		p := lowerSource(t, src)
		MarkExternallyReachable(p, "big")
		Inline(p)
		if n := callsToIn(p, "main", "big"); n != 1 {
			t.Errorf("main has %d call(s) to `big`, want 1: an exported callee must not be admitted over the flat cap by the sole-reference shortcut", n)
		}
		if findFunc(p, "big") == nil {
			t.Error("`big` definition disappeared; an export must keep its standalone body")
		}
	})
}

// Marking a function externally reachable is a size-POLICY signal, not a ban
// on inlining: a small exported callee is still substituted into its internal
// callers, and its standalone definition survives alongside for the outside
// caller. Forbidding that would cost every `-shared` build the inlining of its
// own helpers.
func TestInlineStillSubstitutesSmallExportedCallee(t *testing.T) {
	p := lowerSource(t, `function dbl(x: i32): i32 { return x * 2; }
		function main(): i32 { return dbl(7); }`)
	MarkExternallyReachable(p, "dbl")
	Inline(p)
	if n := callsToIn(p, "main", "dbl"); n != 0 {
		t.Errorf("main still has %d call(s) to `dbl`; a small exported callee should still inline", n)
	}
	if findFunc(p, "dbl") == nil {
		t.Error("`dbl` definition disappeared; an export must keep its standalone body")
	}
}

// MarkExternallyReachable must tolerate a name the tree-shake already removed
// (an export list is source-level; the function may be gone by lowering).
func TestMarkExternallyReachableIgnoresUnknownNames(t *testing.T) {
	p := lowerSource(t, `function main(): i32 { return 0; }`)
	MarkExternallyReachable(p, "nope")
	for _, fn := range p.Funcs {
		if fn.ExternallyReachable {
			t.Errorf("%s was marked by an unrelated name", fn.Name)
		}
	}
}
