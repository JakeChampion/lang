package checker

import (
	"strings"
	"testing"
)

// An unsuffixed literal handed to `cell_new` settles to the element type its
// destination names, as it does inside an array or a variant constructor
// (#10220). A value that cannot settle there is still refused.
func TestCellNewSettlesToItsDestination(t *testing.T) {
	good := []struct{ name, src string }{
		{"let", `function main(): i32 { let c: Cell[i64] = cell_new(1); c.set(5); let v: i64 = c.get(); return v as i32; }`},
		{"argument", `function f(c: Cell[i64]): i64 { return c.get(); }
function main(): i32 { return f(cell_new(3)) as i32; }`},
		{"return", `function mk(): Cell[i64] { return cell_new(9); }
function main(): i32 { return mk().get() as i32; }`},
		{"wide literal", `function main(): i32 { let c: Cell[i64] = cell_new(5000000000); return (c.get() / 1000000000) as i32; }`},
		{"narrow", `function main(): i32 { let c: Cell[u8] = cell_new(200); return c.get() as i32; }`},
		{"float", `function main(): i32 { let c: Cell[f64] = cell_new(2); return (c.get() * 2.0) as i32; }`},
		{"no destination", `function main(): i32 { let c = cell_new(7); return c.get(); }`},
	}
	for _, c := range good {
		if err := checkSource(t, c.src); err != nil {
			t.Errorf("%s: %v", c.name, err)
		}
	}
	bad := []struct{ name, src, want string }{
		{"typed local", `function main(): i32 { let x: i32 = 4; let c: Cell[i64] = cell_new(x); return 0; }`,
			"cannot assign Cell[i32] to variable of type Cell[i64]"},
		{"not a number", `function main(): i32 { let c: Cell[string] = cell_new(1); return 0; }`,
			"cannot assign Cell[i32] to variable of type Cell[string]"},
		{"float into int", `function main(): i32 { let c: Cell[i64] = cell_new(1.5); return 0; }`,
			"cannot assign Cell[f64] to variable of type Cell[i64]"},
		{"out of range", `function main(): i32 { let c: Cell[u8] = cell_new(300); return 0; }`,
			"literal 300 does not fit in u8"},
	}
	for _, c := range bad {
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
