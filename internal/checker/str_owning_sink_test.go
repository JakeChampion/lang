package checker

import (
	"strings"
	"testing"
)

// A builtin that keeps its argument in the value it returns is an owning sink,
// so the borrow that lets a `str` reach a `string` parameter does not apply
// there: the view would outlive the bytes it names.
func TestStrIntoAStoringBuiltinIsRefused(t *testing.T) {
	const hint = "add `.to_owned()`"
	bad := []struct{ name, body, want string }{
		{"append", `var out: string[] = []; out = out.append(slice_unchecked(s, 0, 1)); return out.len();`,
			"argument 2: expected string, got str"},
		{"with", `var out: string[] = ["x"]; out = out.with(0, slice_unchecked(s, 0, 1)); return out.len();`,
			"argument 3: expected string, got str"},
		{"map key", `var m: Map[string, i32] = map_new(2); m = m.insert(slice_unchecked(s, 0, 1), 1); return m.len();`,
			"argument 2: expected string, got str"},
		{"map value", `var m: Map[string, string] = map_new(2); m = m.insert("k", slice_unchecked(s, 0, 1)); return m.len();`,
			"argument 3: expected string, got str"},
	}
	for _, c := range bad {
		src := "function f(s: string): i32 { " + c.body + " }\nfunction main(): i32 { return f(\"ab\"); }"
		err := checkSource(t, src)
		if err == nil {
			t.Errorf("%s: a str into an owning sink was accepted", c.name)
			continue
		}
		if !strings.Contains(err.Error(), c.want) || !strings.Contains(err.Error(), hint) {
			t.Errorf("%s: error %q does not say %q with the %q hint", c.name, err.Error(), c.want, hint)
		}
	}

	good := []struct{ name, src string }{
		{"owned copy", `function f(s: string): i32 { var out: string[] = []; out = out.append(slice_unchecked(s, 0, 1) + ""); return out.len(); }
function main(): i32 { return f("ab"); }`},
		{"str array", `function f(s: string): i32 { var out: str[] = []; out = out.append(slice_unchecked(s, 0, 1)); return out.len(); }
function main(): i32 { return f("ab"); }`},
		{"borrowed string parameter", `function n(x: string): i32 { return x.len(); }
function f(s: string): i32 { return n(slice_unchecked(s, 0, 1)); }
function main(): i32 { return f("ab"); }`},
	}
	for _, c := range good {
		if err := checkSource(t, c.src); err != nil {
			t.Errorf("%s: %v", c.name, err)
		}
	}
}
