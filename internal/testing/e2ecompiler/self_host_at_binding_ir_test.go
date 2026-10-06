package e2ecompiler

import "testing"

// `@` bindings through the self-hosted CLI: `match (b) { n @ Full(v) => … }`,
// `w @ Point { x, y }` and `w @ (a, b)` bind the whole matched value alongside
// the payload, field or element binds, and a guard may read the whole-value
// name. `k @ 1..10` binds a scalar scrutinee in front of a literal or range,
// in a match, an `if let` and a `let … else`. Every answer comes from the
// interpreter.
var selfHostAtBindingCases = []struct {
	name string
	src  string
}{
	{"stmt_whole_and_payload", `enum Box { Full(i32), Empty }
function total(b: Box): i32 { match (b) { Full(v) => { return v; }, Empty => { return 0; } } return 0; }
function f(b: Box): i32 {
  match (b) {
    n @ Full(v) => { return total(n) * 10 + v; },
    Empty => { return 0; },
  }
  return 0;
}
function main(): i32 { return f(Full(3)); }`},
	{"expr_no_guard", `enum Box { Full(i32), Empty }
function total(b: Box): i32 { match (b) { Full(v) => { return v; }, Empty => { return 0; } } return 0; }
function f(b: Box): i32 { return match (b) { n @ Full(v) => total(n) * 10 + v, Empty => 0 }; }
function main(): i32 { return f(Full(3)); }`},
	{"expr_guard_uses_at", `enum Box { Full(i32), Empty }
function is_full(b: Box): boolean { match (b) { Full(v) => { return true; }, Empty => { return false; } } return false; }
function f(b: Box): i32 {
  return match (b) {
    n @ Full(v) when is_full(n) => v + 2,
    Full(v) => v,
    Empty => 0,
  };
}
function main(): i32 { return f(Full(3)); }`},
	// The guard reads `n` inside a struct literal, an immediately invoked
	// capturing lambda, and a slice.
	{"guard_at_in_struct_literal", `enum Box { Full(i32), Empty }
struct H { b: Box }
function unwrap(h: H): i32 { match (h.b) { Full(v) => { return v; }, Empty => { return 0; } } return 0; }
function f(b: Box): i32 {
  match (b) {
    n @ Full(v) when unwrap(H { b: n }) > 0 => { return v + 4; },
    Full(v) => { return v; },
    Empty => { return 0; },
  }
  return 0;
}
function main(): i32 { return f(Full(3)); }`},
	{"guard_at_in_lambda", `enum Box { Full(i32), Empty }
function total(b: Box): i32 { match (b) { Full(v) => { return v; }, Empty => { return 0; } } return 0; }
function f(b: Box): i32 {
  match (b) {
    n @ Full(v) when ((): i32 => { return total(n); })() > 0 => { return v + 5; },
    Full(v) => { return v; },
    Empty => { return 0; },
  }
  return 0;
}
function main(): i32 { return f(Full(3)); }`},
	{"guard_at_in_slice", `enum Box { Full(i32), Empty }
function one(b: Box): i32[] { match (b) { Full(v) => { return [v]; }, Empty => { return []; } } return []; }
function f(b: Box): i32 {
  match (b) {
    n @ Full(v) when one(n)[0:1].len() == 1 => { return v + 6; },
    Full(v) => { return v; },
    Empty => { return 0; },
  }
  return 0;
}
function main(): i32 { return f(Full(3)); }`},
	{"struct_whole_and_fields", `struct Point { x: i32, y: i32 }
function f(p: Point): i32 {
  match (p) {
    w @ Point { x, y } => { return w.x + w.y + x + y; },
  }
  return 0;
}
function main(): i32 { return f(Point { x: 3, y: 4 }); }`},
	{"struct_expr_guard_uses_at", `struct P { a: i32, b: i32 }
function f(p: P): i32 {
  return match (p) {
    w @ P { a, b } when w.a > 0 => a + b,
    P { a, b } => 0,
  };
}
function main(): i32 { return f(P { a: 2, b: 8 }); }`},
	{"struct_at_with_rename", `struct Point { x: i32, y: i32 }
function f(p: Point): i32 { match (p) { w @ Point { x: nx, y: ny } => { return w.x * 100 + nx * 10 + ny; } } return 0; }
function main(): i32 { return f(Point { x: 1, y: 2 }); }`},
	{"tuple_whole_and_elems", `function f(t: (i32, i32)): i32 {
  match (t) {
    w @ (1, x) => { return w.0 * 10 + x; },
    _ => { return 0; },
  }
  return 0 - 1;
}
function main(): i32 { return f((1, 5)); }`},
	{"tuple_expr_guard_uses_at", `function f(t: (i32, i32)): i32 {
  return match (t) {
    w @ (a, b) when w.1 > 0 => a + b,
    _ => 0,
  };
}
function main(): i32 { return f((4, 6)); }`},
	{"scalar_match_stmt", `function classify(n: i32): i32 {
  match (n) {
    k @ 1..10 => { return k * 2; },
    k @ 20 => { return k + 1; },
    k @ 30..=31 when k > 30 => { return k; },
    _ => { return 0; },
  }
}
function main(): i32 {
  return classify(5) + classify(20) + classify(31) + classify(30) + classify(99);
}`},
	{"scalar_match_expr", `function pick(n: i32): i32 {
  return match (n) { k @ 5..7 => k * 3, k @ 40 => k, _ => 7 };
}
function main(): i32 { return pick(6) + pick(40) + pick(99); }`},
	{"scalar_if_let", `function a(n: i32): i32 { if let k @ 1..10 = n { return k * 2; } return 0; }
function main(): i32 { return a(5) + a(99); }`},
	{"scalar_let_else", `function b(n: i32): i32 { let k @ 20..30 = n else { return 0; }; return k + 1; }
function main(): i32 { return b(25) + b(99); }`},
	{"scalar_negative_bounds", `function c(n: i32): i32 {
  match (n) { k @ -10..0 => { return 0 - k; }, k @ -20 => { return k; }, _ => { return 0; } }
}
function main(): i32 { return c(-5) + c(-20) + c(50) + 20; }`},
	{"scalar_string_scrutinee", `function d(s: string): i32 {
  match (s) { k @ "yes" => { return k.len(); }, _ => { return 0; } }
}
function main(): i32 { return d("yes") + d("no"); }`},
}

func TestSelfHostAtBindingX86_64(t *testing.T) {
	cli := newStrictCLI(t)
	interpBin := buildLangBinForInterp(t)
	for _, tc := range selfHostAtBindingCases {
		t.Run(tc.name, func(t *testing.T) {
			want := interpExit(t, interpBin, tc.src+"\n")
			if code, _ := cli.runX86(t, cli.emit(t, "x86-64-linux", tc.src)); code != want {
				t.Errorf("%s exited %d, want %d (interp oracle)", tc.name, code, want)
			}
		})
	}
}

func TestSelfHostAtBindingArm64(t *testing.T) {
	gcc, qemu := arm64Tooling(t)
	cli := newStrictCLI(t)
	interpBin := buildLangBinForInterp(t)
	for _, tc := range selfHostAtBindingCases {
		t.Run(tc.name, func(t *testing.T) {
			want := interpExit(t, interpBin, tc.src+"\n")
			if code, _ := runArm64(t, gcc, qemu, cli.emit(t, "arm64-linux", tc.src)); code != want {
				t.Errorf("%s exited %d, want %d (interp oracle)", tc.name, code, want)
			}
		})
	}
}
