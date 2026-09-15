package checker

import (
	"strings"
	"testing"
)

// `?` dispatches on the `@try` marker, not on two builtin enum names. The
// shape rule is enforced at the declaration (E078), so the operator's
// lowering can assume success-at-tag-0 / failure-at-tag-1 for every type it
// accepts. See docs/TRY.md.
func TestTryMarkerAccepted(t *testing.T) {
	cases := []struct{ name, src string }{
		{
			// Payloadless failure variant — the Option shape. `?` builds a
			// fresh failure value of the enclosing function's return enum.
			name: "payloadless failure variant",
			src: `@try
enum MyOpt[T] { Here(T), Gone }
function pick(m: MyOpt[i32]): MyOpt[i32] { var v: i32 = m?; return Here(v + 1); }
function main(): i32 { match (pick(Here(7))) { Here(v) => { return v; }, Gone => { return 0; } } }`,
		},
		{
			// Payload-carrying failure variant — the Result shape. `?`
			// forwards the source value unchanged.
			name: "failure variant carries a payload",
			src: `@try
enum Outcome[T, E] { Good(T), Bad(E) }
function step(o: Outcome[i32, string]): Outcome[i32, string] { var v: i32 = o?; return Good(v * 2); }
function main(): i32 { match (step(Good(21))) { Good(v) => { return v; }, Bad(e) => { return e.len(); } } }`,
		},
		{
			// A non-generic marked enum: the shape rule is about variant
			// arity, not about type parameters.
			name: "no type parameters",
			src: `@try
enum Flag { On(i32), Off }
function pick(f: Flag): Flag { var v: i32 = f?; return On(v); }
function main(): i32 { match (pick(On(3))) { On(v) => { return v; }, Off => { return 0; } } }`,
		},
		{
			// The builtins keep working, and keep needing no marker.
			name: "Option and Result still work unmarked",
			src: `function o(x: Option[i32]): Option[i32] { var v: i32 = x?; return Some(v); }
function r(x: Result[i32, string]): Result[i32, string] { var v: i32 = x?; return Ok(v); }
function main(): i32 { return 0; }`,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if err := checkSource(t, c.src); err != nil {
				t.Errorf("should type-check: %v", err)
			}
		})
	}
}

// E078: the marker's shape obligation, reported at the declaration.
func TestTryMarkerShapeErrors(t *testing.T) {
	cases := []struct{ name, src, want string }{
		{"three variants", `@try
enum Three[T] { A(T), B, C }
function main(): i32 { return 0; }`, "must have exactly two variants"},
		{"one variant", `@try
enum One[T] { A(T) }
function main(): i32 { return 0; }`, "must have exactly two variants"},
		{"success variant carries nothing", `@try
enum NoPay { A, B }
function main(): i32 { return 0; }`, `the success variant "A" must carry exactly one payload`},
		{"success variant carries two", `@try
enum TwoPay[T] { A(T, T), B }
function main(): i32 { return 0; }`, `the success variant "A" must carry exactly one payload`},
		{"failure variant carries two", `@try
enum BadFail[T, E] { A(T), B(E, E) }
function main(): i32 { return 0; }`, `the failure variant "B" must carry at most one payload`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := checkSource(t, c.src)
			if err == nil {
				t.Fatalf("expected E078")
			}
			// The stable E078 code is stamped by the diag formatting layer,
			// not carried in the checker error's bare message, so the text
			// is what this asserts. The code itself is pinned by the
			// self-host checker-codes differential.
			if !strings.Contains(err.Error(), c.want) {
				t.Errorf("error %q does not contain %q", err.Error(), c.want)
			}
		})
	}
}

// `?` on a type the marker was never applied to, and the message that names
// the marker as the reader's next action.
func TestTryOnUnmarkedEnum(t *testing.T) {
	src := `enum Plain[T] { Here(T), Gone }
function pick(p: Plain[i32]): Plain[i32] { var v: i32 = p?; return Here(v); }
function main(): i32 { return 0; }`
	err := checkSource(t, src)
	if err == nil {
		t.Fatalf("expected an error: Plain is not marked @try")
	}
	if !strings.Contains(err.Error(), "mark it `@try`") {
		t.Errorf("error %q should point at the marker", err.Error())
	}
}

// The enclosing function has to return the SAME enum: the failure value
// propagates out of it, and `@try` has no cross-enum conversion.
func TestTryRequiresMatchingReturnEnum(t *testing.T) {
	src := `@try
enum MyOpt[T] { Here(T), Gone }
function pick(m: MyOpt[i32]): Option[i32] { var v: i32 = m?; return Some(v); }
function main(): i32 { return 0; }`
	err := checkSource(t, src)
	if err == nil {
		t.Fatalf("expected an error: MyOpt? inside an Option-returning function")
	}
	if !strings.Contains(err.Error(), "requires the surrounding function to return MyOpt") {
		t.Errorf("error %q should name the required return enum", err.Error())
	}
}
