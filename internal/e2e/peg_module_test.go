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
