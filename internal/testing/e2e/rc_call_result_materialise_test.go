package e2e

import (
	"fmt"
	"testing"
)

type materialiseCase struct {
	name    string
	g       string
	appends int
}

var materialiseCases = []materialiseCase{
	{"A_inline_append_tail", `function g(a: i32[], v: i32): i32[] { return a.append(v); }`, 1},
	{"B_inline_append_rebind", `function g(a: i32[], v: i32): i32[] { a = a.append(v); return a; }`, 1},
	{"C_call_tail", `function g(b: i32[], v: i32): i32[] { return f(b, v); }`, 1},
	{"F_two_inline_appends", `function g(b: i32[], v: i32): i32[] { b = b.append(v); return b.append(v + 1); }`, 2},

	{"H_call_result_into_local", `function g(b: i32[], v: i32): i32[] { let t: i32[] = f(b, v); return t; }`, 1},
	{"I_call_result_into_param", `function g(b: i32[], v: i32): i32[] { b = f(b, v); return b; }`, 1},
	{"L_two_calls_via_param", `function g(b: i32[], v: i32): i32[] { b = f(b, v); return f(b, v + 1); }`, 2},
	{"J_nested_call_arg", `function g(b: i32[], v: i32): i32[] { return f(f(b, v), v + 1); }`, 2},
	{"K_two_calls_via_local", `function g(b: i32[], v: i32): i32[] { let t: i32[] = f(b, v); return f(t, v + 1); }`, 2},
	{"M_call_then_inline_append", `function g(b: i32[], v: i32): i32[] { let t: i32[] = f(b, v); return t.append(v + 1); }`, 2},
}

func (c materialiseCase) src() string {
	wantLen := 50 * c.appends
	wantLast := 49 + (c.appends - 1)
	return fmt.Sprintf(`function f(b: i32[], v: i32): i32[] { return b.append(v); }
%s
function main(): i32 {
    let acc: i32[] = [];
    let i: i32 = 0;
    while (i < 50) { acc = g(acc, i); i = i + 1; }
    if (acc.len() != %d) { return 254; }
    if (acc[0] != 0 || acc[%d] != %d) { return 253; }
    return __arr_push_shared_count();
}`, c.g, wantLen, wantLen-1, wantLast)
}

func (c materialiseCase) check(t *testing.T, backend string, got int) {
	t.Helper()
	switch got {
	case 254, 253:
		t.Errorf("%s %s: driver returned %d — the accumulator computed the WRONG contents; "+
			"the cliff count below it is meaningless until that is fixed", backend, c.name, got)
	case 0:
	default:
		t.Errorf("%s %s: __arr_push_shared_count() = %d, want 0 — an extra reference is "+
			"making every append copy the whole buffer (#6036)", backend, c.name, got)
	}
}

func TestX86_64CallResultMaterialiseCliff(t *testing.T) {
	for _, c := range materialiseCases {
		t.Run(c.name, func(t *testing.T) {
			_, got := compileAndRunX86_64FreeOn(t, c.src())
			c.check(t, "x86-64-linux", got)
		})
	}
}

func TestInterpCallResultMaterialiseSemantics(t *testing.T) {
	for _, c := range materialiseCases {
		t.Run(c.name, func(t *testing.T) {
			if got := runInterpExit(t, c.src()); got != 0 {
				t.Errorf("interp %s: exit %d, want 0 (the interpreter's cliff counter is a "+
					"constant 0, so anything else is a length / element mismatch)", c.name, got)
			}
		})
	}
}
