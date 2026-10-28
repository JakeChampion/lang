package checker

import (
	"strings"
	"testing"
)

const vecDecls = `trait Wide { function first(self: Self): f64; function add(self: Self, o: Self): Self; }
impl Wide for f64x2 { function first(self: f64x2): f64 { return __f64x2_lane(self, 0); } function add(self: f64x2, o: f64x2): f64x2 { return self + o; } }
impl Wide for f32x4 { function first(self: f32x4): f64 { return __f32x4_lane(self, 0) as f64; } function add(self: f32x4, o: f32x4): f32x4 { return self + o; } }
trait Narrow { function only(self: Self): i32; }
impl Narrow for f64x2 { function only(self: f64x2): i32 { return 2; } }
function head[const N: i32, T](v: Vec[N, T]): f64 { return v.first(); }
function twice[const N: i32, T](v: Vec[N, T]): Vec[N, T] { return v + v; }
`

func TestVecLaneSpelling(t *testing.T) {
	for name, src := range map[string]string{
		"concrete f64":     `function f(v: Vec[2, f64]): f64x2 { return v; }`,
		"concrete f32":     `function f(): f32 { let v: Vec[4, f32] = __f32x4_splat(1.0); return __f32x4_lane(v, 3); }`,
		"infer from lanes": `function f(): f64 { return head(__f64x2_splat(1.0)) + head(__f32x4_splat(2.0)); }`,
		"result is lanes":  `function f(): f32x4 { return twice(__f32x4_splat(1.0)); }`,
		"explicit":         `function f(): f64 { return head[2, f64](__f64x2_splat(1.0)); }`,
		"one lane type":    `function g(v: Vec[2, f64]): i32 { return v.only(); } function h[const N: i32](v: Vec[N, f64]): i32 { return v.only(); }`,
		"element type":     `function el[const N: i32, T](v: Vec[N, T], x: T): T { return x; } function f(): f32 { let y: f32 = 1.0; return el(__f32x4_splat(1.0), y); }`,
	} {
		if err := checkSource(t, vecDecls+src); err != nil {
			t.Errorf("%s: want accepted, got %v", name, err)
		}
	}
	for name, tc := range map[string]struct{ src, code, msg string }{
		"no pair":         {`function f(v: Vec[3, f64]): i32 { return 0; }`, "E096", "Vec[3, f64] names no lane type"},
		"no generic pair": {`function f[T](v: Vec[8, T]): i32 { return 0; }`, "E096", "Vec[8, T] names no lane type"},
		"not common":      {`function f[const N: i32, T](v: Vec[N, T]): i32 { return v.only(); }`, "E021", `no method "only" on Vec[N, T]`},
		"wrong lanes":     {`function f(): f64 { return head2(__f32x4_splat(1.0)); } function head2[const N: i32](v: Vec[N, f64]): f64 { return v.first(); }`, "E038", "got f32x4"},
	} {
		err := checkSource(t, vecDecls+tc.src)
		if err == nil || !hasCode(err, tc.code) {
			t.Errorf("%s: want %s, got %v", name, tc.code, err)
			continue
		}
		if !strings.Contains(err.Error(), tc.msg) {
			t.Errorf("%s: want %q in the message, got %v", name, tc.msg, err)
		}
	}
}
