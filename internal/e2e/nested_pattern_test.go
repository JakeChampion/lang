// Nested match-pattern coverage (#5353): `match r { Some(Ok(n)) => … }`.
//
// Nested patterns are a purely additive parser feature — a nested
// sub-pattern is desugared, at parse time, into a flat outer arm whose
// body re-matches the payload on an inner `match`, so the checker and
// every backend see only ordinary flat arms (the same shape if-let /
// let-else already desugar to). These tests pin that the compiled x86-64
// binary agrees with the interpreter oracle across the shapes the desugar
// must get right: the headline `Some(Ok(n))`, a flat sibling arm, guards,
// deep nesting, one-nested-plus-one-plain payload, the expression form,
// and the outer-`_` fallthrough (a payload matching no inner pattern must
// run the outer wildcard body, not bail non-exhaustive). A couple also
// run through wasm to confirm the desugar is backend-agnostic.
package e2e

import "testing"

var nestedPatternCases = []struct {
	name string
	src  string
	want int
}{
	{
		// Headline: `Some(Ok(n))` binds n through two levels. Folds all three
		// outcomes so one exit code exercises every arm.
		name: "some_ok_headline",
		src: `function g(o: Option[Result[i32, i32]]): i32 {
  match (o) {
    Some(Ok(n)) => { return n + 100; },
    Some(Err(e)) => { return e; },
    None => { return 0 - 1; },
  }
  return 0 - 2;
}
function main(): i32 {
  return g(Some(Ok(5))) + g(Some(Err(7))) + (0 - g(None));
}`,
		want: 113, // 105 + 7 + 1
	},
	{
		// A flat sibling arm (`Some(other)`) after the nested one acts as the
		// inner catch-all, binding the whole payload.
		name: "flat_sibling",
		src: `function g(o: Option[Result[i32, i32]]): i32 {
  match (o) {
    Some(Ok(n)) => { return n + 100; },
    Some(other) => { return 9; },
    None => { return 0 - 1; },
  }
  return 0 - 2;
}
function main(): i32 { return g(Some(Err(3))) + g(Some(Ok(2))); }`,
		want: 111, // 9 + 102
	},
	{
		// A guard on a nested arm; a value whose payload matches the pattern
		// but fails the guard falls to the next arm.
		name: "guarded_nested",
		src: `function g(o: Option[Result[i32, i32]]): i32 {
  match (o) {
    Some(Ok(n)) when n > 10 => { return n + 100; },
    Some(Ok(n)) => { return n; },
    Some(Err(e)) => { return e; },
    None => { return 0 - 1; },
  }
  return 0 - 2;
}
function main(): i32 { return g(Some(Ok(20))) + g(Some(Ok(3))); }`,
		want: 123, // 120 + 3
	},
	{
		// Two levels of payload-carrying nesting.
		name: "double_nest",
		src: `function g(o: Result[Result[i32, i32], i32]): i32 {
  match (o) {
    Ok(Ok(n)) => { return n + 50; },
    Ok(Err(e)) => { return e + 20; },
    Err(x) => { return x; },
  }
  return 0 - 2;
}
function main(): i32 { return g(Ok(Ok(4))) + g(Ok(Err(3))) + g(Err(1)); }`,
		want: 78, // 54 + 23 + 1
	},
	{
		// One nested payload slot alongside a plain binder slot — the plain
		// slot's name stays in scope in every inner arm body.
		name: "nested_plus_plain",
		src: `enum Inner { A(i32), B(i32) }
enum Outer { P(Inner, i32), Nil }
function g(e: Outer): i32 {
  match (e) {
    P(A(a), b) => { return a + b; },
    P(B(z), b) => { return z - b; },
    Nil => { return 0 - 1; },
  }
  return 0 - 2;
}
function main(): i32 { return g(P(A(4), 5)) + g(P(B(9), 2)); }`,
		want: 16, // 9 + 7
	},
	{
		// A named-field inner pattern (`Boxed(r)` where the payload is a struct)
		// nested under `Some`.
		name: "named_field_inner",
		src: `struct Rect { w: i32, h: i32 }
enum Shape { Boxed(Rect), Empty }
function g(o: Option[Shape]): i32 {
  match (o) {
    Some(Boxed(r)) => { return r.w + r.h; },
    Some(Empty) => { return 0; },
    None => { return 0 - 1; },
  }
  return 0 - 2;
}
function main(): i32 { return g(Some(Boxed(Rect { w: 3, h: 4 }))); }`,
		want: 7,
	},
	{
		// Outer `_` fallthrough: `Some(Err(_))` matches no inner arm, so the
		// outer wildcard body runs (rather than a non-exhaustive bail).
		name: "wildcard_fallthrough",
		src: `function g(o: Option[Result[i32, i32]]): i32 {
  match (o) {
    Some(Ok(n)) => { return n + 100; },
    _ => { return 7; },
  }
  return 0 - 2;
}
function main(): i32 { return g(Some(Ok(5))) + g(Some(Err(9))) + g(None); }`,
		want: 119, // 105 + 7 + 7
	},
	{
		// Expression-form nested match, including plain-slot rebinding through
		// a block-expression.
		name: "expr_form",
		src: `function g(o: Option[Result[i32, i32]]): i32 {
  return match (o) {
    Some(Ok(n)) => n + 100,
    Some(Err(e)) => e,
    None => 0 - 1,
  };
}
function main(): i32 { return g(Some(Ok(5))) + g(Some(Err(3))) + (0 - g(None)); }`,
		want: 109, // 105 + 3 + 1
	},
}

// TestNestedPatternX86_64 runs every case through the interpreter oracle
// and the native x86-64 backend, asserting they agree and match the
// expected value.
func TestNestedPatternX86_64(t *testing.T) {
	for _, tc := range nestedPatternCases {
		t.Run(tc.name, func(t *testing.T) {
			oracle := runInterpByte(t, tc.src)
			if oracle != tc.want {
				t.Fatalf("interp oracle = %d, want %d", oracle, tc.want)
			}
			_, code := compileAndRunX86_64(t, tc.src)
			if code != tc.want {
				t.Errorf("native x86-64 = %d, want %d (interp oracle agrees at %d)", code, tc.want, oracle)
			}
		})
	}
}

// TestNestedPatternWasm confirms the desugar is backend-agnostic by
// running the headline + fallthrough cases through the wasm pipeline.
func TestNestedPatternWasm(t *testing.T) {
	for _, name := range []string{"some_ok_headline", "wildcard_fallthrough", "expr_form"} {
		var tc = nestedPatternCasesByName(t, name)
		t.Run(name, func(t *testing.T) {
			if got := runWasm(t, tc.src); got != tc.want {
				t.Errorf("wasm = %d, want %d", got, tc.want)
			}
		})
	}
}

func nestedPatternCasesByName(t *testing.T, name string) struct {
	name string
	src  string
	want int
} {
	t.Helper()
	for _, tc := range nestedPatternCases {
		if tc.name == name {
			return tc
		}
	}
	t.Fatalf("no nested-pattern case named %q", name)
	return nestedPatternCases[0]
}
