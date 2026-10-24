package e2ecompiler

import "testing"

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
