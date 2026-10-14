package e2ecompiler

import "testing"

// A stdlib root spelled with `..` names the same directory as its cleaned
// form, so a program importing from it compiles (#12398): the root was used
// as given while module paths were cleaned, and the two spellings of one
// file read as an import cycle.
func TestSelfHostStdlibRootWithDotDot(t *testing.T) {
	c := buildSelfHostCLI(t)
	c.stdlib = c.stdlib + "/../stdlib/./"
	_, code := c.exitOf(t, `import "std/string";
import "std/i32";
function main(): i32 {
    let s: string = 42.to_string();
    return s.len() + 5;
}
`, "x86-64-linux")
	if code != 7 {
		t.Fatalf("exit %d, want 7", code)
	}
}
