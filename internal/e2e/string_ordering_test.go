package e2e

import "testing"

// stringOrderingProgram exercises `<` / `<=` / `>` / `>=` on strings (#7110),
// the primitive byte-order comparison that lowers to ir.OpStrCmp and each
// backend's three-way runtime helper. Every step is a distinct exit code so a
// backend that gets one relation wrong names it.
//
// The cases pin the two halves of the order: the FIRST differing byte decides,
// and a common prefix falls back to the length (shorter sorts first). Byte
// order — not case-folded, not codepoint — so "Z" < "a" and a high byte sorts
// after ASCII. Operands cover both the heap and inline (SSO) string forms and
// both operand positions, since the helpers materialise each side separately.
const stringOrderingProgram = `import "std/string";

function main(): i32 {
    var abc: string = "abc";
    // First differing byte decides.
    if (!(abc < "abd")) { return 1; }
    if (!("abd" > abc)) { return 2; }
    if (abc < "abb") { return 3; }
    // Common prefix: the shorter operand sorts first.
    if (!(abc > "ab")) { return 4; }
    if (!("ab" < abc)) { return 5; }
    // Equal operands: strict relations false, non-strict true.
    if (abc < "abc") { return 6; }
    if (abc > "abc") { return 7; }
    if (!(abc <= "abc")) { return 8; }
    if (!(abc >= "abc")) { return 9; }
    // The empty string sorts before everything.
    if (!("" < abc)) { return 10; }
    if ("" > "") { return 11; }
    if (!("" <= "")) { return 12; }
    // Byte order, so uppercase sorts before lowercase and a byte above
    // 0x7f sorts after all of ASCII.
    if (!("Z" < "a")) { return 13; }
    if (!("a" < "\xc3\xa9")) { return 14; }
    // Long operands exercise the heap form on both sides, past whatever
    // small-string threshold the target uses.
    var long1: string = "the quick brown fox jumps over the lazy dog";
    var long2: string = "the quick brown fox jumps over the lazy dogs";
    if (!(long1 < long2)) { return 15; }
    if (!(long2 > long1)) { return 16; }
    if (long1 < "the quick brown fox jumps over the lazy doa") { return 17; }
    // A computed (owned temp) operand on either side: the lowering stashes
    // and releases these, so a leak or a premature free shows up here.
    if (!(("ab" + "c") < "abd")) { return 18; }
    if (!("abb" < ("ab" + "c"))) { return 19; }
    if (!(("ab" + "c") <= ("ab" + "c"))) { return 20; }
    // A str view compares by contents on either side, same as ` + "`==`" + `.
    var v: str = abc;
    if (!(v < "abd")) { return 21; }
    if (!("ab" < v)) { return 22; }
    if (!(v <= abc)) { return 23; }
    // Ordering in a loop, driving the comparison through non-constant
    // operands so no constant fold can stand in for the runtime helper.
    var words: string[] = ["delta", "alpha", "charlie", "bravo"];
    var sorted: i32 = 0;
    var i: i32 = 1;
    while (i < words.len()) {
        if (words[i - 1] < words[i]) { sorted = sorted + 1; }
        i = i + 1;
    }
    if (sorted != 1) { return 24; }
    return 0;
}
`

func TestInterpStringOrdering(t *testing.T) {
	if code := runInterpExit(t, stringOrderingProgram); code != 0 {
		t.Errorf("interp string ordering: exit = %d, want 0", code)
	}
}

func TestX86_64StringOrdering(t *testing.T) {
	if _, code := compileAndRunX86_64(t, stringOrderingProgram); code != 0 {
		t.Errorf("x86-64 string ordering: exit = %d, want 0", code)
	}
}

func TestArm64StringOrdering(t *testing.T) {
	if _, code := compileAndRunArm64(t, stringOrderingProgram); code != 0 {
		t.Errorf("arm64 string ordering: exit = %d, want 0", code)
	}
}

func TestWasmStringOrdering(t *testing.T) {
	if code := compileAndRunWasmbinMain(t, stringOrderingProgram); code != 0 {
		t.Errorf("wasm string ordering: exit = %d, want 0", code)
	}
}
