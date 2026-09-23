package e2e

import "testing"

// An alias of a borrowed string parameter takes no count of its own when
// every escape of it takes one, as a closure capture does (#10113). Four
// calls.
func TestBorrowedParamAliasWithACountedEscape(t *testing.T) {
	const main = `
function mkstr(a: string): string { return a + "!"; }
function main(): i32 {
    var acc: i32 = 0;
    var i: i32 = 0;
    while (i < 4) {
        var line: string = mkstr("a string long enough to defeat the small-string optimisation");
        acc = acc + tag(line, i);
        i = i + 1;
    }
    return acc;
}`
	cases := []struct {
		name, tag string
		want      int
	}{
		{"captured", `function tag(src: string, i: i32): i32 {
    var x: string = src;
    var f: (i32) => i32 = (k: i32) => x.len() + k;
    return f(i);
}`, 250},
	}
	for _, c := range cases {
		src := c.tag + main
		t.Run("x86_64-sanitize/"+c.name, func(t *testing.T) { checkSanitizedBalanced(t, src, c.want, runSanitizeX86_64) })
		t.Run("arm64-sanitize/"+c.name, func(t *testing.T) { checkSanitizedBalanced(t, src, c.want, runSanitizeArm64) })
		t.Run("wasm/"+c.name, func(t *testing.T) {
			if got := runWasm(t, src); got != c.want%256 {
				t.Errorf("got %d, want %d", got, c.want%256)
			}
		})
	}
}
