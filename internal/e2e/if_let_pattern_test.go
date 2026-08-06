// `if let` consumes the shared pattern grammar (#5356). The parser desugars
// `if let PAT = E <then> [else <else>]` into the equivalent statement match
//
//	match (E) { PAT => { then }, _ => { else } }
//
// tagged ast.Match.Origin "if_let", so every pattern form `match` accepts —
// struct patterns, tuple patterns, nested patterns, or-patterns, `@`
// bindings, literals and ranges — works in an `if let` head with no
// per-form codegen, and refutability/exhaustiveness stays in one place.
// These pin the behaviour of each form across the backends against the
// interpreter oracle.
package e2e

import (
	"os"
	"path/filepath"
	"regexp"
	"testing"

	"github.com/jakechampion/lang/internal/checker"
	"github.com/jakechampion/lang/internal/diag"
	"github.com/jakechampion/lang/internal/modload"
)

var ifLetPatternCases = []struct {
	name string
	src  string
	want int
}{
	{
		// Struct pattern: irrefutable, so the else arm is dead — the
		// synthetic `_` must not draw the unreachable-arm diagnostic.
		name: "struct_fields",
		src: `struct Point { x: i32, y: i32 }
function main(): i32 {
  var p: Point = Point { x: 3, y: 4 };
  if let Point { x, y } = p { return x * 10 + y; }
  return 99;
}`,
		want: 34,
	},
	{
		// Field rename in the struct pattern head.
		name: "struct_rename",
		src: `struct Point { x: i32, y: i32 }
function main(): i32 {
  var p: Point = Point { x: 3, y: 4 };
  if let Point { x: a, y: b } = p { return a * 10 + b; }
  return 99;
}`,
		want: 34,
	},
	{
		name: "tuple_elems",
		src: `function main(): i32 {
  var t: (i32, i32) = (3, 4);
  if let (a, b) = t { return a * 10 + b; }
  return 99;
}`,
		want: 34,
	},
	{
		// A literal tuple element is refutable, so the else branch is live.
		name: "tuple_literal_elem_else",
		src: `function main(): i32 {
  var t: (i32, i32) = (3, 4);
  if let (9, b) = t { return b; } else { return 7; }
  return 99;
}`,
		want: 7,
	},
	{
		name: "at_binding_variant",
		src: `enum Box { Full(i32), Empty }
function total(b: Box): i32 { match (b) { Full(v) => { return v; }, Empty => { return 0; } } return 0; }
function main(): i32 {
  var b: Box = Full(3);
  if let n @ Full(v) = b { return total(n) * 10 + v; }
  return 99;
}`,
		want: 33,
	},
	{
		// Or-pattern: each alternative binds its own name set, so `x` is
		// bound by whichever alternative matched.
		name: "or_pattern",
		src: `enum E { A(i32), B(i32), C }
function pick(e: E): i32 {
  if let A(x) | B(x) = e { return x; }
  return 0;
}
function main(): i32 { return pick(B(5)) * 10 + pick(A(4)) + pick(C); }`,
		want: 54, // 5*10 + 4 + 0
	},
	{
		// Nested pattern: the else branch doubles as the outer fallthrough,
		// so a `Some(Err(_))` payload runs it rather than falling off the
		// merged inner match.
		name: "nested_pattern",
		src: `enum Inner { Ok2(i32), Err2 }
enum Outer { Wrap(Inner), Bare }
function f(o: Outer): i32 {
  if let Wrap(Ok2(n)) = o { return n; } else { return 1; }
  return 99;
}
function main(): i32 { return f(Wrap(Ok2(3))) * 10 + f(Wrap(Err2)) * 2 + f(Bare); }`,
		want: 33, // 3*10 + 1*2 + 1
	},
	{
		name: "literal_and_range",
		src: `function classify(n: i32): i32 {
  if let 0 = n { return 1; }
  if let 10..=20 = n { return 2; }
  return 3;
}
function main(): i32 { return classify(0) * 100 + classify(15) * 10 + classify(50); }`,
		want: 123,
	},
	{
		// Payload bindings stay scoped to the then-branch; the else branch
		// sees the enclosing scope only.
		name: "else_taken",
		src: `enum Box { Full(i32), Empty }
function main(): i32 {
  var b: Box = Empty;
  if let Full(v) = b { return v; } else { return 42; }
  return 99;
}`,
		want: 42,
	},
	{
		// Braceless then-branch: the desugar wraps a bare statement in the
		// block a match arm body needs.
		name: "braceless_then",
		src: `enum Box { Full(i32), Empty }
function main(): i32 {
  var b: Box = Full(8);
  if let Full(v) = b return v;
  return 99;
}`,
		want: 8,
	},
	{
		// Qualified enum variant in the head.
		name: "qualified_variant",
		src: `enum Color { Red(i32), Blue }
function main(): i32 {
  var c: Color = Red(6);
  if let Color.Red(n) = c { return n; }
  return 99;
}`,
		want: 6,
	},
}

func TestIfLetPatternInterp(t *testing.T) {
	for _, tc := range ifLetPatternCases {
		t.Run(tc.name, func(t *testing.T) {
			if got := runInterpByte(t, tc.src); got != tc.want {
				t.Errorf("interp = %d, want %d", got, tc.want)
			}
		})
	}
}

func TestIfLetPatternX86_64(t *testing.T) {
	for _, tc := range ifLetPatternCases {
		t.Run(tc.name, func(t *testing.T) {
			if _, code := compileAndRunX86_64(t, tc.src); code != tc.want {
				t.Errorf("native x86-64 = %d, want %d", code, tc.want)
			}
		})
	}
}

func TestIfLetPatternArm64(t *testing.T) {
	for _, tc := range ifLetPatternCases {
		t.Run(tc.name, func(t *testing.T) {
			if _, code := compileAndRunArm64(t, tc.src); code != tc.want {
				t.Errorf("native arm64 = %d, want %d", code, tc.want)
			}
		})
	}
}

func TestIfLetPatternWasm(t *testing.T) {
	for _, name := range []string{"struct_fields", "tuple_elems", "at_binding_variant", "or_pattern", "nested_pattern", "else_taken"} {
		tc := ifLetPatternCaseByName(t, name)
		t.Run(name, func(t *testing.T) {
			if got := runWasm(t, tc.src); got != tc.want {
				t.Errorf("wasm = %d, want %d", got, tc.want)
			}
		})
	}
}

// The pattern-binding diagnostics survive the desugar: an `if let` whose
// source can't be destructured by the pattern is E022 (not the generic
// shape errors a hand-written match of the same form would draw), a bad
// variant is E014, and a payload-arity mismatch is E015.
func TestIfLetPatternDiagnostics(t *testing.T) {
	cases := []struct {
		name string
		src  string
		want string
	}{
		{
			name: "source_not_enum",
			src:  `function main(): i32 { var n: i32 = 5; if let Has(v) = n { return 0; } return 0; }`,
			want: "E022",
		},
		{
			name: "source_struct_variant_pattern",
			src: `struct P { x: i32 }
function main(): i32 { var p: P = P { x: 1 }; if let Has(v) = p { return 0; } return 0; }`,
			want: "E022",
		},
		{
			name: "unknown_variant",
			src: `enum O { Has(i32), Nil }
function main(): i32 { var o: O = Nil; if let Bogus(v) = o { return 0; } return 0; }`,
			want: "E014",
		},
		{
			name: "payload_arity",
			src: `enum O { Has(i32), Nil }
function main(): i32 { var o: O = Nil; if let Has(a, b) = o { return 0; } return 0; }`,
			want: "E015",
		},
	}
	dir := t.TempDir()
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// The stable E0XX code lives in the diag formatting layer, not
			// the checker error's bare message.
			path := filepath.Join(dir, tc.name+".fern")
			if err := os.WriteFile(path, []byte(tc.src), 0o644); err != nil {
				t.Fatalf("write: %v", err)
			}
			prog, _, err := modload.Load(path)
			if err != nil {
				t.Fatalf("load: %v", err)
			}
			_, cerr := checker.Check(prog)
			if cerr == nil {
				t.Fatalf("expected %s, got no error", tc.want)
			}
			got := ifLetCodeRE.FindAllString(diag.Format(path, tc.src, cerr), -1)
			if !containsStr(got, tc.want) {
				t.Errorf("codes = %v, want %s\n%s", got, tc.want, diag.Format(path, tc.src, cerr))
			}
		})
	}
}

var ifLetCodeRE = regexp.MustCompile(`E[0-9]{3}`)

func containsStr(xs []string, want string) bool {
	for _, x := range xs {
		if x == want {
			return true
		}
	}
	return false
}

// An irrefutable `if let` head (struct pattern, all-binder tuple pattern)
// leaves the synthetic else arm unreachable. That arm is the desugar's, not
// the programmer's, so it must not draw E026 / E030.
func TestIfLetIrrefutableHeadAccepted(t *testing.T) {
	for _, src := range []string{
		`struct P { x: i32, y: i32 }
function main(): i32 { var p: P = P { x: 1, y: 2 }; if let P { x, y } = p { return x + y; } else { return 0; } return 0; }`,
		`function main(): i32 { var t: (i32, i32) = (1, 2); if let (a, b) = t { return a + b; } else { return 0; } return 0; }`,
	} {
		prog, _, err := modload.LoadSource(src)
		if err != nil {
			t.Fatalf("load: %v", err)
		}
		if _, cerr := checker.Check(prog); cerr != nil {
			t.Errorf("irrefutable if-let head rejected: %v\n%s", cerr, src)
		}
	}
}

func ifLetPatternCaseByName(t *testing.T, name string) struct {
	name string
	src  string
	want int
} {
	t.Helper()
	for _, tc := range ifLetPatternCases {
		if tc.name == name {
			return tc
		}
	}
	t.Fatalf("no if-let pattern case named %q", name)
	return ifLetPatternCases[0]
}
