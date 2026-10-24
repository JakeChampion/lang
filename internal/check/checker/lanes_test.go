package checker

import (
	"strings"
	"testing"
)

func TestLanesRejected(t *testing.T) {
	for name, tc := range map[string]struct{ src, code, msg string }{
		"array of lanes":     {`function f(): i32 { let xs: f64x2[] = []; return xs.len(); }`, "E085", "f64x2[] holds a lane value inside another type"},
		"tuple of lanes":     {`function f(): (f32x4, i32) { return (__f32x4_splat(1.0), 1); }`, "E085", "(f32x4, i32) holds a lane value"},
		"array literal":      {`function f(v: f64x2): i32 { let n: i32 = [v].len(); return n; }`, "E085", "holds a lane value inside another type"},
		"field":              {`struct H { v: f64x2 } function f(): i32 { return 0; }`, "E085", "a field or payload of type f64x2"},
		"payload":            {`enum M { One(f32x4), Zero } function f(): i32 { return 0; }`, "E085", "a field or payload of type f32x4"},
		"function type":      {`function f(g: (f64x2) => f64): i32 { return 0; }`, "E085", "(f64x2) => f64 holds a lane value"},
		"function value":     {`function t(v: f64x2): f64x2 { return v; } function f(): i32 { let g = t; return 0; }`, "E085", "function t takes or returns a lane value and cannot be used as a value"},
		"lambda parameter":   {`function f(): i32 { let g = (v: f64x2): f64 => __f64x2_lane(v, 0); return 0; }`, "E085", "a lambda cannot take or return a lane value"},
		"lambda result":      {`function f(): i32 { let g = (x: f64): f64x2 => __f64x2_splat(x); return 0; }`, "E085", "a lambda cannot take or return a lane value"},
		"lambda capture":     {`function f(): i32 { let c: f64x2 = __f64x2_splat(1.0); let g = (x: f64): f64 => x + __f64x2_lane(c, 1); return 0; }`, "E085", "a lambda cannot capture \"c\" of type f64x2"},
		"index not literal":  {`function f(v: f64x2, i: i32): f64 { return __f64x2_lane(v, i); }`, "E038", "the lane index is an integer literal from 0 to 1"},
		"index out of range": {`function f(v: f32x4): f32 { return __f32x4_lane(v, 4); }`, "E038", "the lane index is an integer literal from 0 to 3"},
		"equality":           {`function f(v: f64x2): boolean { return v == v; }`, "E041", "a lane value compares lane by lane"},
		"ordering":           {`function f(v: f32x4): boolean { return v < v; }`, "E041", "cannot compare values of type f32x4"},
		"scalar operand":     {`function f(v: f64x2): f64x2 { return v + 1.0; }`, "E009", "got f64x2 and f64"},
		"mixed lane types":   {`function f(v: f64x2, w: f32x4): f64x2 { return v * w; }`, "E009", "got f64x2 and f32x4"},
	} {
		err := checkSource(t, tc.src)
		if err == nil || !hasCode(err, tc.code) {
			t.Errorf("%s: want %s, got %v", name, tc.code, err)
			continue
		}
		if !strings.Contains(err.Error(), tc.msg) {
			t.Errorf("%s: want %q in the message, got %v", name, tc.msg, err)
		}
	}
}

func TestLanesAccepted(t *testing.T) {
	for name, src := range map[string]string{
		"arithmetic":   `function f(a: f64x2, b: f64x2): f64x2 { return -(a + b) * a / b - a; }`,
		"f32 lanes":    `function f(a: f32x4): f32x4 { return __f32x4_mul_add(a, a, __f32x4_sqrt(__f32x4_abs(a))); }`,
		"builtins":     `function f(a: f64x2): f64 { let m: f64x2 = __f64x2_lt(a, __f64x2_of(1.0, 2.0)); let b: f64x2 = __f64x2_select(__f64x2_min(a, a), __f64x2_max(a, a), m); return __f64x2_lane(__f64x2_round(__f64x2_floor(b)), 1) + __f64x2_lane(__f64x2_ne(a, b), 0); }`,
		"direct call":  `function g(v: f64x2, k: f64): f64x2 { return v * __f64x2_splat(k); } function f(): f64 { return __f64x2_lane(g(__f64x2_splat(1.0), 2.0), 0); }`,
		"array lanes":  `function f(xs: f64[]): f64[] { let v: f64x2 = __f64x2_load(xs, 0); return __f64x2_with(xs, 2, v); }`,
		"f32 array":    `function f(xs: f32[]): f32[] { return __f32x4_with(xs, 0, __f32x4_load(xs, 4)); }`,
		"lambda local": `function f(v: f64x2): f64 { let x: f64 = __f64x2_lane(v, 0); let g = (y: f64): f64 => y + x; return g(1.0); }`,
	} {
		if err := checkSource(t, src); err != nil {
			t.Errorf("%s: want accepted, got %v", name, err)
		}
	}
}
