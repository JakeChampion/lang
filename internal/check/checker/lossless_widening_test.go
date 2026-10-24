package checker

import "testing"

func TestLosslessWideningContexts(t *testing.T) {
	for _, tc := range []struct{ name, source string }{
		{"assignment", `function f(n: i32): i64 { let x: i64 = n; x = n; return x; }`},
		{"argument", `function take(n: i64): i64 { return n; } function f(n: i32): i64 { return take(n); }`},
		{"return", `function f(n: i32): i64 { return n; }`},
		{"some", `function f(n: i32): Option[i64] { return Some(n); }`},
		{"ok", `function f(n: i32): Result[i64, string] { return Ok(n); }`},
		{"err", `function f(n: i32): Result[string, i64] { return Err(n); }`},
		{"nested_some", `function f(n: i32): Option[Option[i64]] { return Some(Some(n)); }`},
		{"field", `struct Box { value: i64 } function f(n: i32): Box { return Box { value: n }; }`},
		{"array", `function f(n: i32): i64[] { let xs: i64[] = [n]; return xs; }`},
		{"tuple", `function f(n: i32): (i64, i64) { return (n, n); }`},
		{"method", `struct S { value: i32 } function (s: S) take(n: i64): i64 { return n; } function f(n: i32): i64 { return S { value: 0 }.take(n); }`},
		{"lambda", `function f(n: i32): i64 { let take = (x: i64): i64 => x; return take(n); }`},
		{"branch", `function f(n: i32, b: boolean): i64 { return if (b) { n } else { 0i64 }; }`},
		{"float_assignment", `function f(n: f32): f64 { let x: f64 = n; return x; }`},
		{"float_argument", `function take(n: f64): f64 { return n; } function f(n: f32): f64 { return take(n); }`},
		{"float_return", `function f(n: f32): f64 { return n; }`},
		{"float_operator", `function f(a: f32, b: f64): f64 { return a + b; }`},
		{"mixed_operator", `function f(a: u32, b: i32): i64 { return a + b; }`},
		{"literal_retains_byte_width", `function f(n: u8): u8 { return n & 15; }`},
		{"mixed_compare", `function f(a: u8, b: i32): boolean { return a < b; }`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := checkSource(t, tc.source); err != nil {
				t.Fatal(err)
			}
		})
	}
	for _, pair := range []struct{ from, to string }{
		{"i32", "i64"}, {"u8", "u32"}, {"u8", "u64"}, {"u8", "i32"}, {"u8", "i64"}, {"u32", "u64"}, {"u32", "i64"}, {"f32", "f64"},
	} {
		t.Run(pair.from+"_to_"+pair.to, func(t *testing.T) {
			source := "function f(n: " + pair.from + "): " + pair.to + " { return n; }"
			if err := checkSource(t, source); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestLosslessWideningRejectsRepresentationChanges(t *testing.T) {
	for _, source := range []string{
		`function f(n: i64): i32 { return n; }`,
		`function f(n: i32): u32 { return n; }`,
		`function f(n: u32): i32 { return n; }`,
		`function f(n: u64): i64 { return n; }`,
		`function f(n: f64): f32 { return n; }`,
		`function f(xs: i32[]): i64[] { return xs; }`,
		`function g(n: i32): i32 { return n; } function f(): (i64) => i64 { return g; }`,
		`function g(n: i64): i32 { return n as i32; } function f(): (i64) => i64 { return g; }`,
		`function f(xs: [i32]): [i64] { return xs; }`,
		`function f(xs: Option[i32]): Option[i64] { return xs; }`,
		`function f(xs: i32[]): i64[][] { return [xs]; }`,
		`function f(xs: (i32, i32)): (i64, i64) { return xs; }`,
		`struct Box[T] { value: T } function f(x: Box[i32]): Box[i64] { return x; }`,
		`function f(b: boolean, a: i32[], c: i64[]): void { let x = if (b) { a } else { c }; }`,
		`function f(b: boolean, a: (i32, i32), c: (i64, i64)): void { let x = if (b) { a } else { c }; }`,
	} {
		if err := checkSource(t, source); err == nil {
			t.Fatalf("accepted lossy or container conversion: %s", source)
		}
	}
}
