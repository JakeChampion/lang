package e2e

import "testing"

// A closure that captures one of its maker's parameters, rebound in a loop
// body. The capture is a counted store, so the caller may release what it
// passed and drop the closure on every trip (#10112). Six trips per shape.
func TestClosureCapturingAParameterReboundInALoop(t *testing.T) {
	cases := []struct {
		name, decls, body string
		want              int
	}{
		{"fresh_string",
			`function make(s: string): (i32) => i32 { return (x: i32): i32 => { return x + s.len(); }; }`,
			`var f: (i32) => i32 = make("a" + "b");
        t = t + f(i);`, 27},
		{"live_record",
			`struct Box { name: string }
function make(b: Box): (i32) => i32 { return (x: i32): i32 => { return x + b.name.len(); }; }`,
			`var x: Box = Box { name: "b" + "c" };
        var f: (i32) => i32 = make(x);
        t = t + f(i) + x.name.len();`, 39},
		{"fresh_array",
			`function make(xs: i32[]): (i32) => i32 { return (x: i32): i32 => { return x + xs[1]; }; }`,
			`var f: (i32) => i32 = make([i, i + 1]);
        t = t + f(0);`, 21},
		{"dyn",
			`trait Label { function a(self: Self): i32; }
struct Box { name: string }
impl Label for Box { function a(self: Self): i32 { return self.name.len(); } }
function make(l: dyn Label): (i32) => i32 { return (x: i32): i32 => { return x + l.a(); }; }`,
			`var x: Box = Box { name: "b" + "c" };
        var f: (i32) => i32 = make(x);
        t = t + f(i);`, 27},
	}
	for _, c := range cases {
		src := c.decls + `
function main(): i32 {
    var t: i32 = 0;
    var i: i32 = 0;
    while (i < 6) {
        ` + c.body + `
        i = i + 1;
    }
    return t;
}`
		t.Run("x86_64-sanitize/"+c.name, func(t *testing.T) { checkSanitizedBalanced(t, src, c.want, runSanitizeX86_64) })
		t.Run("arm64-sanitize/"+c.name, func(t *testing.T) { checkSanitizedBalanced(t, src, c.want, runSanitizeArm64) })
		t.Run("wasm/"+c.name, func(t *testing.T) {
			if got := runWasm(t, src); got != c.want {
				t.Errorf("got %d, want %d", got, c.want)
			}
		})
	}
}
