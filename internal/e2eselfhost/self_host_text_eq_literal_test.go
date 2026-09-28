package e2eselfhost

import "testing"

// A string compared with a literal checks the other operand's length first and
// calls the runtime's equality only when the lengths match. Each probe prints
// a bit per comparison, so a wrong answer on either side of the length test
// shows as a wrong digit.
const textEqLiteralProgram = `@noinline function kw(s: string): i32 {
    if (s == "fn") { return 1; }
    if (s == "var") { return 2; }
    if ("while" == s) { return 3; }
    if (s == "") { return 4; }
    if (s == "a\nb") { return 5; }
    return 0;
}
@noinline function differs(s: string): i32 {
    if (s != "abc") { return 1; }
    return 0;
}
@noinline function joined(a: string, b: string): i32 {
    if (a + b == "varx") { return 1; }
    return 0;
}
@noinline function window(s: string): i32 {
    if (slice_unchecked(s, 1, 3) == "hi") { return 1; }
    return 0;
}
function print_int(n: i32): i32 {
    if (n > 9) { print_int(n / 10); }
    putchar(48 + n % 10);
    return 0;
}
function main(): i32 {
    var t: i32 = 0;
    for w in ["fn", "fx", "var", "vaz", "while", "whilf", "", "a\nb", "a b", "longer"] { t = t * 10 + kw(w); }
    print_int(t); print("");
    print_int(differs("abc") * 100 + differs("abd") * 10 + differs("ab")); print("");
    print_int(joined("va", "rx") * 10 + joined("var", "y")); print("");
    print_int(window("xhiy") * 10 + window("xhoy")); print("");
    return 0;
}
`

func TestSelfHostTextEqLiteral(t *testing.T) {
	want := "1020304500\n11\n10\n10\n"
	runSemanticProgram(t, "texteq", textEqLiteralProgram, []string{"kw", "differs", "joined", "window"},
		map[string]string{"arm64-linux": want, "x86-64-linux": want, "x86-64-sanitize": want, "wasm32-wasi": want})
}
