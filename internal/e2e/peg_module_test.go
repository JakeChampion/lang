package e2e

import "testing"

// TestPegModule exercises std/peg end-to-end on the compiled backends
// (the TAP suite in examples/tests/peg_test.fern covers the full API
// via the interp gate TestRunnerPegExamplePasses; this test proves the
// module's core machinery — recursive named rules, ordered choice,
// captures, lookahead, the functional match-state threading — lowers
// and runs on every backend). The program exercises a recursive
// balanced-parens grammar with a trailing captured number and returns
// distinct codes per failure mode.
func TestPegModule(t *testing.T) {
	src := `import "std/peg";
function main(): i32 {
    var rules: Map[string, peg.Pattern] = map_new(4);
    rules = rules.insert("b", PChoice([
        PSeq([PLit("("), PRef("b"), PLit(")"), PRef("b")]),
        PLit("")
    ]));
    rules = rules.insert("all", PSeq([PRef("b"), PCap("tail", PPlus(peg.peg_digit())), PEof]));
    let Ok(g) = peg.peg_grammar(rules, "all") else { return 90; };
    var r: peg.PegResult = peg.peg_match(g, "(()(()))42");
    if (!r.ok) { return 91; }
    if (r.caps.get_or("tail", "") != "42") { return 92; }
    if (peg.peg_match(g, "((x").ok) { return 93; }
    var kw: peg.Pattern = PSeq([PLit("if"), PNot(peg.peg_alnum())]);
    if (peg.peg_match_pattern(kw, "iffy").ok) { return 94; }
    if (!peg.peg_match_pattern(kw, "if (x)").ok) { return 95; }
    return 55;
}`
	const want = 55
	t.Run("interp", func(t *testing.T) {
		if code := runInterpByte(t, src); code != want {
			t.Errorf("interp exit = %d, want %d", code, want)
		}
	})
	t.Run("arm64", func(t *testing.T) {
		if _, code := compileAndRunArm64(t, src); code != want {
			t.Errorf("arm64 exit = %d, want %d", code, want)
		}
	})
	t.Run("x86_64", func(t *testing.T) {
		if _, code := compileAndRunX86_64(t, src); code != want {
			t.Errorf("x86_64 exit = %d, want %d", code, want)
		}
	})
	t.Run("wasm", func(t *testing.T) {
		if code := compileAndRunWasmbinMain(t, src); code != want {
			t.Errorf("wasm exit = %d, want %d", code, want)
		}
	})
}

// TestPegLeftRecursion pins #5402: a LEFT-recursive rule — one that
// re-enters itself (directly or through another rule) at the SAME input
// position, so no progress is possible — must fail the match fast on
// every backend. The old guard was only a PRef depth budget of 8192,
// whose ~2 native frames × ~0.75 KB per expansion needs ~12 MB of call
// stack: the interpreter's growable stack absorbed it, but the compiled
// backends SIGSEGV'd on the 8 MB guard page (exit 139) — the one crash
// left in the examples corpus. __peg_run now threads the (rule, pos)
// pairs on the call chain and fails a same-position re-entry
// immediately; the depth budget (2048) remains as the backstop for
// deep non-cyclic recursion and is sized to fit the native stack. The
// positive controls prove the chain does NOT over-fire: right
// recursion re-enters at advancing positions, and balanced parens
// re-enter the same rule only after consuming "(".
func TestPegLeftRecursion(t *testing.T) {
	src := `import "std/peg";
function main(): i32 {
    var r1: Map[string, peg.Pattern] = map_new(2);
    r1 = r1.insert("l", PSeq([PRef("l"), PLit("a")]));
    let Ok(g1) = peg.peg_grammar(r1, "l") else { return 90; };
    if (peg.peg_match(g1, "aaa").ok) { return 91; }
    var r2: Map[string, peg.Pattern] = map_new(2);
    r2 = r2.insert("a", PSeq([PRef("b"), PLit("x")]));
    r2 = r2.insert("b", PRef("a"));
    let Ok(g2) = peg.peg_grammar(r2, "a") else { return 92; };
    if (peg.peg_match(g2, "xxx").ok) { return 93; }
    var r3: Map[string, peg.Pattern] = map_new(2);
    r3 = r3.insert("r", PChoice([PSeq([PLit("a"), PRef("r")]), PLit("")]));
    var deep: string = "";
    var i: i32 = 0;
    while (i < 200) { deep = deep + "a"; i = i + 1; }
    let Ok(g3) = peg.peg_grammar(r3, "r") else { return 94; };
    if (!peg.peg_match(g3, deep).ok) { return 95; }
    var r4: Map[string, peg.Pattern] = map_new(2);
    r4 = r4.insert("b", PChoice([PSeq([PLit("("), PRef("b"), PLit(")")]), PLit("")]));
    let Ok(g4) = peg.peg_grammar(r4, "b") else { return 96; };
    if (!peg.peg_match(g4, "((()))").ok) { return 97; }
    return 55;
}`
	const want = 55
	t.Run("interp", func(t *testing.T) {
		if code := runInterpByte(t, src); code != want {
			t.Errorf("interp exit = %d, want %d", code, want)
		}
	})
	t.Run("arm64", func(t *testing.T) {
		if _, code := compileAndRunArm64(t, src); code != want {
			t.Errorf("arm64 exit = %d, want %d", code, want)
		}
	})
	t.Run("x86_64", func(t *testing.T) {
		if _, code := compileAndRunX86_64(t, src); code != want {
			t.Errorf("x86_64 exit = %d, want %d", code, want)
		}
	})
	t.Run("wasm", func(t *testing.T) {
		if code := compileAndRunWasmbinMain(t, src); code != want {
			t.Errorf("wasm exit = %d, want %d", code, want)
		}
	})
}
