package checker

import (
	"strings"
	"testing"
)

func TestLanesRejected(t *testing.T) {
	for name, tc := range map[string]struct{ src, code, msg string }{
		"f32 bits index":     {`function f(v: f32x4, k: i32): u32 { return __f32x4_lane_bits(v, k); }`, "E038", "integer literal"},
		"f32 bits range":     {`function f(v: f32x4): u32 { return __f32x4_lane_bits(v, 4); }`, "E038", "integer literal"},
		"f64 bits range":     {`function f(v: f64x2): u64 { return __f64x2_lane_bits(v, 2); }`, "E038", "integer literal"},
		"raw f32 load":       {`function f(p: usize): f32x4 { return __f32x4_load_ptr(p); }`, "E095", "unsafe block"},
		"raw f64 load":       {`function f(p: usize): f64x2 { return __f64x2_load_ptr(p); }`, "E095", "unsafe block"},
		"raw f32 store":      {`function f(p: usize, v: f32x4): void { __f32x4_store(p, v); }`, "E095", "unsafe block"},
		"raw f64 store":      {`function f(p: usize, v: f64x2): void { __f64x2_store(p, v); }`, "E095", "unsafe block"},
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
		"byte index":         {`function f(v: u8x16): u8 { return __u8x16_lane(v, 16); }`, "E038", "the lane index is an integer literal from 0 to 15"},
		"byte index name":    {`function f(v: u8x16, i: i32): u8 { return __u8x16_lane(v, i); }`, "E038", "the lane index is an integer literal from 0 to 15"},
		"byte multiply":      {`function f(v: u8x16): u8x16 { return v * v; }`, "E009", "operator \"*\" is not available for u8x16"},
		"byte divide":        {`function f(v: u8x16): u8x16 { return v / v; }`, "E009", "operator \"/\" is not available for u8x16"},
		"byte negative":      {`function f(v: u8x16): u8x16 { return -v; }`, "E009", "its lanes have no sign"},
		"byte equality":      {`function f(v: u8x16): boolean { return v == v; }`, "E041", "cannot compare values of type u8x16"},
		"byte and float":     {`function f(v: u8x16, w: f32x4): u8x16 { return v + w; }`, "E009", "got u8x16 and f32x4"},
		"byte field":         {`struct H { v: u8x16 } function f(): i32 { return 0; }`, "E085", "a field or payload of type u8x16"},
		"byte array":         {`function f(): i32 { let xs: u8x16[] = []; return xs.len(); }`, "E085", "u8x16[] holds a lane value inside another type"},
		"byte splat type":    {`function f(): u8x16 { return __u8x16_splat(1.5); }`, "E038", "expected u8, got f64"},
		"byte load type":     {`function f(xs: i32[]): u8x16 { return __u8x16_load(xs, 0); }`, "E038", "expected u8[], got i32[]"},
		"word index":         {`function f(v: i16x8): i32 { return __i16x8_lane(v, 8); }`, "E038", "the lane index is an integer literal from 0 to 7"},
		"quad index":         {`function f(v: u32x4): u32 { return __u32x4_lane(v, 4); }`, "E038", "the lane index is an integer literal from 0 to 3"},
		"signed byte times":  {`function f(v: i8x16): i8x16 { return v * v; }`, "E009", "operator \"*\" is not available for i8x16: lanes of 8 bits have no multiply"},
		"word divide":        {`function f(v: u16x8): u16x8 { return v / v; }`, "E009", "operator \"/\" is not available for u16x8: integer lanes have no divide"},
		"quad negative":      {`function f(v: u32x4): u32x4 { return -v; }`, "E009", "its lanes have no sign"},
		"sign mixed":         {`function f(v: u16x8, w: i16x8): u16x8 { return v + w; }`, "E009", "got u16x8 and i16x8"},
		"width mixed":        {`function f(v: i32x4, w: f32x4): i32x4 { return v + w; }`, "E009", "got i32x4 and f32x4"},
		"quad saturate":      {`function f(v: i32x4): i32x4 { return __i32x4_add_sat(v, v); }`, "E001", "cannot find \"__i32x4_add_sat\""},
		"signed average":     {`function f(v: i16x8): i16x8 { return __i16x8_avg(v, v); }`, "E001", "cannot find \"__i16x8_avg\""},
		"unsigned abs":       {`function f(v: u8x16): u8x16 { return __u8x16_abs(v); }`, "E001", "cannot find \"__u8x16_abs\""},
		"word load type":     {`function f(xs: i32[]): i16x8 { return __i16x8_load(xs, 0); }`, "E038", "expected u8[], got i32[]"},
		"shift count type":   {`function f(v: u16x8): u16x8 { return __u16x8_shl(v, v); }`, "E038", "expected i32, got u16x8"},
		"word field":         {`struct H { v: i16x8 } function f(): i32 { return 0; }`, "E085", "a field or payload of type i16x8"},
		"widen result":       {`function f(v: u8x16): u8x16 { return __u8x16_widen_lo(v); }`, "E002", "u16x8"},
		"widen sign":         {`function f(v: i8x16): u16x8 { return __i8x16_widen_hi(v); }`, "E002", "i16x8"},
		"quad widen":         {`function f(v: i32x4): i32x4 { return __i32x4_widen_lo(v); }`, "E001", "cannot find \"__i32x4_widen_lo\""},
		"byte narrow":        {`function f(v: u8x16): u8x16 { return __u8x16_narrow(v, v); }`, "E001", "cannot find \"__u8x16_narrow\""},
		"unsigned narrow_u":  {`function f(v: u16x8): u8x16 { return __u16x8_narrow_u(v, v); }`, "E001", "cannot find \"__u16x8_narrow_u\""},
		"narrow result":      {`function f(v: i16x8): u8x16 { return __i16x8_narrow(v, v); }`, "E002", "i8x16"},
		"signed swizzle":     {`function f(v: i8x16): i8x16 { return __i8x16_swizzle(v, v); }`, "E001", "cannot find \"__i8x16_swizzle\""},
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
		"f32 bits":        `function f(v: f32x4): u32 { return __f32x4_lane_bits(v, 3); }`,
		"f64 bits":        `function f(v: f64x2): u64 { return __f64x2_lane_bits(v, 1); }`,
		"permutation":     `function f(a: f32x4, b: f32x4): u32 { return __f32x4_lane_bits(__f32x4_zip_lo(a, b), 0); }`,
		"arithmetic":      `function f(a: f64x2, b: f64x2): f64x2 { return -(a + b) * a / b - a; }`,
		"f32 lanes":       `function f(a: f32x4): f32x4 { return __f32x4_mul_add(a, a, __f32x4_sqrt(__f32x4_abs(a))); }`,
		"builtins":        `function f(a: f64x2): f64 { let m: f64x2 = __f64x2_lt(a, __f64x2_of(1.0, 2.0)); let b: f64x2 = __f64x2_select(__f64x2_min(a, a), __f64x2_max(a, a), m); return __f64x2_lane(__f64x2_round(__f64x2_floor(b)), 1) + __f64x2_lane(__f64x2_ne(a, b), 0); }`,
		"direct call":     `function g(v: f64x2, k: f64): f64x2 { return v * __f64x2_splat(k); } function f(): f64 { return __f64x2_lane(g(__f64x2_splat(1.0), 2.0), 0); }`,
		"array lanes":     `function f(xs: f64[]): f64[] { let v: f64x2 = __f64x2_load(xs, 0); return __f64x2_with(xs, 2, v); }`,
		"raw load block":  `function f(p: usize): f64x2 { unsafe { return __f64x2_load_ptr(p); } }`,
		"raw store block": `function f(p: usize, v: f32x4): void { unsafe { __f32x4_store(p, v); } }`,
		"packed f32":      `function f(xs: u8[]): u8[] { return __f32x4_with_bytes(xs, 1, __f32x4_load_bytes(xs, 0)); }`,
		"packed f64":      `function f(xs: u8[]): u8[] { return __f64x2_with_bytes(xs, 0, __f64x2_load_bytes(xs, 1)); }`,
		"f32 array":       `function f(xs: f32[]): f32[] { return __f32x4_with(xs, 0, __f32x4_load(xs, 4)); }`,
		"lambda local":    `function f(v: f64x2): f64 { let x: f64 = __f64x2_lane(v, 0); let g = (y: f64): f64 => y + x; return g(1.0); }`,
		"byte arithmetic": `function f(a: u8x16, b: u8x16): u8x16 { return a + b - a; }`,
		"byte builtins":   `function f(a: u8x16): i32 { let m: u8x16 = __u8x16_lt(a, __u8x16_splat(7 as u8)); let b: u8x16 = __u8x16_select(__u8x16_min(a, a), __u8x16_max(a, a), m); return __u8x16_bitmask(__u8x16_and(__u8x16_or(b, m), __u8x16_xor(__u8x16_eq(b, a), __u8x16_ne(b, a)))) + (__u8x16_lane(__u8x16_avg(__u8x16_add_sat(a, b), __u8x16_sub_sat(a, b)), 15) as i32); }`,
		"byte array":      `function f(xs: u8[]): u8[] { let v: u8x16 = __u8x16_load(xs, 0); return __u8x16_with(xs, 16, v); }`,
		"byte call":       `function g(v: u8x16, k: u8): u8x16 { return v + __u8x16_splat(k); } function f(): u8 { return __u8x16_lane(g(__u8x16_splat(1 as u8), 2 as u8), 0); }`,
		"byte shifts":     `function f(a: u8x16): u8x16 { return __u8x16_shr(__u8x16_shl(a, 3), 1); }`,
		"signed bytes":    `function f(a: i8x16): i32 { return __i8x16_lane(__i8x16_abs(-a) + __i8x16_add_sat(a, __i8x16_splat(-3)), 15); }`,
		"words":           `function f(a: u16x8, b: i16x8): i32 { let m: u16x8 = __u16x8_avg(a * a, __u16x8_sub_sat(a, __u16x8_splat(9))); return __u16x8_lane(m, 7) + __i16x8_bitmask(__i16x8_lt(-b * b, __i16x8_abs(b))); }`,
		"quads":           `function f(a: u32x4, b: i32x4): u32 { let m: i32x4 = __i32x4_select(b * b, -b, __i32x4_eq(b, __i32x4_of(1, 2, 3, 4))); return __u32x4_lane(__u32x4_shr(a * a + __u32x4_splat(7 as u32), 2), 3) + (__i32x4_lane(__i32x4_shl(m, 1), 0) as u32); }`,
		"shapes":          `function f(a: u8x16, b: i16x8, c: u32x4): u16x8 { let w: u16x8 = __u8x16_widen_lo(__u8x16_swizzle(a, a)) + __u8x16_widen_hi(a); let n: i8x16 = __i16x8_narrow(b, b); let m: u8x16 = __i16x8_narrow_u(b, __i8x16_widen_lo(n)); let q: i32x4 = __i16x8_widen_hi(b); return __u32x4_narrow(c, c) + __u8x16_widen_lo(__u16x8_narrow(w, w) + m) + __i32x4_narrow_u(q, q); }`,
		"word array":      `function f(xs: u8[]): u8[] { return __i32x4_with(__u16x8_with(xs, 0, __u16x8_load(xs, 1)), 2, __i32x4_load(xs, 3)); }`,
	} {
		if err := checkSource(t, src); err != nil {
			t.Errorf("%s: want accepted, got %v", name, err)
		}
	}
}

func TestLaneWidenLoadTypes(t *testing.T) {
	for name, tc := range map[string]struct {
		source string
		valid  bool
	}{
		"address":         {`function f(p: usize): f64x2 { unsafe { return __f64x2_load_f32_ptr(p); } }`, true},
		"unsafe required": {`function f(p: usize): f64x2 { return __f64x2_load_f32_ptr(p); }`, false},
		"float address":   {`function f(p: f64): f64x2 { unsafe { return __f64x2_load_f32_ptr(p); } }`, false},
		"missing address": {`function f(): f64x2 { unsafe { return __f64x2_load_f32_ptr(); } }`, false},
		"float result":    {`function f(p: usize): f32x4 { unsafe { return __f64x2_load_f32_ptr(p); } }`, false},
	} {
		if err := checkSource(t, tc.source); (err == nil) != tc.valid {
			t.Errorf("%s: valid=%v, got %v", name, tc.valid, err)
		}
	}
}
