package checker

import (
	"strings"
	"testing"
)

// A method call's receiver is not one of its written arguments, so E038
// numbers the arguments from the first one inside the parentheses (#10199).
func TestMethodCallArgumentNumbering(t *testing.T) {
	cases := []struct{ name, src, want string }{
		{"builtin append", `function main(): i32 { let out: string[] = []; out = out.append(5); return out.len(); }`,
			"argument 1: expected string, got i32"},
		{"builtin with", `function main(): i32 { let xs: i32[] = [1]; let ys: i32[] = xs.with(0, "s"); return ys.len(); }`,
			"argument 2: expected i32, got string"},
		{"cell set", `function main(): i32 { let c = cell_new(1); c.set("x"); return 0; }`,
			"argument 1: expected i32, got string"},
		{"user method", `struct P { x: i32 }
impl P { function add(self: Self, y: i32, z: string): i32 { return self.x + y; } }
function main(): i32 { let p = P { x: 1 }; return p.add(2, 3); }`,
			"argument 2: expected string, got i32"},
		{"plain call", `function f(a: i32, b: string): i32 { return a; }
function main(): i32 { return f(1, 2); }`,
			"argument 2: expected string, got i32"},
	}
	for _, c := range cases {
		err := checkSource(t, c.src)
		if err == nil {
			t.Errorf("%s: accepted", c.name)
			continue
		}
		if !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: error %q does not say %q", c.name, err.Error(), c.want)
		}
	}
}
