package checker

import (
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/ast"
	"github.com/jakechampion/lang/internal/diag"
	"github.com/jakechampion/lang/internal/parser"
)

func checkSource(t *testing.T, src string) error {
	t.Helper()
	prog, err := parser.Parse(src)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	_, err = Check(prog)
	return err
}

func TestGoodPrograms(t *testing.T) {
	for _, src := range []string{
		`function f(): i32 { return 1 + 2; }`,
		`function f(n: i32): i32 { return n * 2; }`,
		`function f(n: i32): boolean { return n < 10; }`,
		`function main(): i32 { var x = 1; var y = x + 2; return y; }`,
		`function main(): i32 { var a: i32[] = [1,2,3]; return a[0]; }`,
	} {
		if err := checkSource(t, src); err != nil {
			t.Errorf("%q: unexpected error %v", src, err)
		}
	}
}

func TestTypeErrors(t *testing.T) {
	cases := []struct {
		src  string
		want string
	}{
		{`function f(): i32 { return true; }`, "return type mismatch"},
		{`function f(): i32 { return 1 + true; }`, "requires an integer type"},
		{`function f(): boolean { return 1; }`, "return type mismatch"},
		{`function f() { x; }`, "undefined identifier"},
		{`function f(n: i32): i32 { if (n) { return 0; } return 1; }`, "if condition must be boolean"},
		{`function f() { var x: i32 = true; }`, "cannot assign boolean"},
	}
	for _, c := range cases {
		err := checkSource(t, c.src)
		if err == nil {
			t.Errorf("%q: expected error, got nil", c.src)
			continue
		}
		if !strings.Contains(err.Error(), c.want) {
			t.Errorf("%q: error %q does not contain %q", c.src, err.Error(), c.want)
		}
	}
}

// The state{} block accepts scalar V (i32 / u32 / i64 / u64 / f32 /
// f64 / boolean), `string` (immutable), and pointer-shaped
// containers (`T[]`, `Map[K, V]`) whose mutation routes through
// the two-cursor allocator. Init expressions can be arbitrary —
// scalars settle to wasm `<type>.const` global init exprs when
// possible, anything else routes through the synthesised
// `__state_init` start function with persistent allocator mode
// active so init-time allocations live in the persistent heap.
func TestStateBlockAccepts(t *testing.T) {
	for _, src := range []string{
		`state { var counter: i32 = 0; }
function main(): i32 { counter = counter + 1; return counter; }`,
		`state { var big: i64 = 100i64; }
function main(): i32 { return big as i32; }`,
		`state { var ratio: f64 = 1.5f64; }
function main(): i32 { return ratio as i32; }`,
		`state { var enabled: boolean = true; }
function main(): i32 { if (enabled) { return 1; } return 0; }`,
		`state { var a: i32 = 0; var b: i32 = 0; }
function main(): i32 { a = a + 1; b = b + 2; return a + b; }`,
		// String state vars (immutable, no two-cursor needed).
		`state { var greeting: string = "hello"; }
function main(): i32 { return len(greeting); }`,
		// Non-literal init expressions.
		`state { var precomputed: i32 = 1 + 2 * 3; }
function main(): i32 { return precomputed; }`,
		`state { var concat: string = "hello, " + "world"; }
function main(): i32 { return len(concat); }`,
		// Pointer-shaped containers (two-cursor allocator).
		`state { var tags: i32[] = []; }
function main(): i32 { return len(tags); }`,
		`state { var todos: Map[i32, string] = map_new(4); }
function main(): i32 { return todos.len(); }`,
	} {
		if err := checkSource(t, src); err != nil {
			t.Errorf("%q: unexpected error %v", src, err)
		}
	}
}

func TestStateBlockRejects(t *testing.T) {
	cases := []struct {
		src  string
		want string
	}{
		{
			`state { var c: i32 = 0; var c: i32 = 1; }
function main(): i32 { return c; }`,
			"already declared",
		},
	}
	for _, c := range cases {
		err := checkSource(t, c.src)
		if err == nil {
			t.Errorf("%q: expected error, got nil", c.src)
			continue
		}
		if !strings.Contains(err.Error(), c.want) {
			t.Errorf("%q: error %q does not contain %q", c.src, err.Error(), c.want)
		}
	}
}

// The checker should accumulate multiple errors and report them all in
// a single diag.Errors aggregate.
func TestMultipleErrorsAreReported(t *testing.T) {
	src := `function f(): i32 {
		return true;
		var x = unknownThing;
	}`
	err := checkSource(t, src)
	if err == nil {
		t.Fatal("expected errors")
	}
	msg := err.Error()
	if !strings.Contains(msg, "return type mismatch") {
		t.Errorf("missing return mismatch: %s", msg)
	}
	if !strings.Contains(msg, "undefined identifier") {
		t.Errorf("missing undefined identifier: %s", msg)
	}
}

func TestBuiltinPutchar(t *testing.T) {
	if err := checkSource(t, `function f() { putchar(65); }`); err != nil {
		t.Errorf("putchar(65) should type-check: %v", err)
	}
	if err := checkSource(t, `function f() { putchar(true); }`); err == nil {
		t.Errorf("putchar(true) should fail")
	}
}

func TestFloatArithmeticIsFlagged(t *testing.T) {
	prog, err := parser.Parse(`function f(x: f32, y: f32): f32 { return x + y; }`)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Check(prog); err != nil {
		t.Fatalf("check: %v", err)
	}
	bin := prog.Funcs[0].Body.Stmts[0].(*ast.Return).Value.(*ast.Binary)
	if !bin.IsFloat {
		t.Errorf("expected IsFloat = true on float + float")
	}
}

func TestFloatArithmeticTypechecks(t *testing.T) {
	for _, src := range []string{
		`function f(x: f32): f32 { return x + 1.5; }`,
		`function f(x: f32): f32 { return x * 2.0 - 0.5; }`,
		`function f(x: f32, y: f32): boolean { return x < y; }`,
		`function f(x: f32): f32 { return -x; }`,
	} {
		if err := checkSource(t, src); err != nil {
			t.Errorf("%q: unexpected error %v", src, err)
		}
	}
}

func TestFloatRejectsMixedArithmetic(t *testing.T) {
	cases := []string{
		// Concrete-int variable + float literal still errors —
		// no implicit widening from i32 to float. The user must
		// cast: `(x as float) + 1.5`.
		`function f(x: i32): f32 { return x + 1.5; }`,
		// `%` is integer-only.
		`function f(x: f32): f32 { return x % 1.0; }`,
	}
	for _, src := range cases {
		if err := checkSource(t, src); err == nil {
			t.Errorf("%q: expected error", src)
		}
	}
}

// Polymorphic integer literals get promoted to the float type
// when one side of an arithmetic / comparison op is a concrete
// float — unlike concrete-int variables, which still need an
// explicit cast. This is the same trick `commonIntegerWidth`
// pulls (poly side wins from concrete side), generalised to
// the cross-class int-literal → float-context promotion.
func TestPolyIntLiteralPromotesToFloat(t *testing.T) {
	cases := []string{
		`function f(x: f32): f32 { return x + 1; }`,
		`function f(x: f32): f32 { return x * 2; }`,
		`function f(x: f64): f64 { return x - 100; }`,
		`function f(x: f32): boolean { return x <= 0; }`,
		`function f(): i32 { var r: f32 = 0; return 0; }`,
		`function takesF32(x: f32): i32 { return 0; }
function f(): i32 { return takesF32(0); }`,
	}
	for _, src := range cases {
		if err := checkSource(t, src); err != nil {
			t.Errorf("%q: unexpected error %v", src, err)
		}
	}
}

func TestStringEqualityTypechecks(t *testing.T) {
	src := `function f(): boolean {
		var a: string = "hi";
		var b: string = "hi";
		return a == b;
	}`
	if err := checkSource(t, src); err != nil {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestStringIndexReturnsNumber(t *testing.T) {
	src := `function f(): i32 {
		var s: string = "abc";
		return s[1];
	}`
	if err := checkSource(t, src); err != nil {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestLenOnString(t *testing.T) {
	src := `function f(): i32 { return len("hello"); }`
	if err := checkSource(t, src); err != nil {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestLenRejectsNumber(t *testing.T) {
	if err := checkSource(t, `function f(): i32 { return len(42); }`); err == nil {
		t.Error("expected error len(i32)")
	}
}

func TestStringCmpFlagSet(t *testing.T) {
	prog, err := parser.Parse(`function f(): boolean {
		var a: string = "x";
		return a == "x";
	}`)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Check(prog); err != nil {
		t.Fatal(err)
	}
	bin := prog.Funcs[0].Body.Stmts[1].(*ast.Return).Value.(*ast.Binary)
	if !bin.IsStringCmp {
		t.Errorf("expected IsStringCmp = true on string == string")
	}
}

func TestUndefinedIdentifierSuggestsClosest(t *testing.T) {
	prog, err := parser.Parse(`function f(): i32 {
		var counter: i32 = 0;
		return countr;
	}`)
	if err != nil {
		t.Fatal(err)
	}
	_, err = Check(prog)
	if err == nil {
		t.Fatal("expected error")
	}
	es, ok := err.(diag.Errors)
	if !ok || len(es) == 0 {
		t.Fatalf("expected diag.Errors, got %T", err)
	}
	ce, ok := es[0].(*Error)
	if !ok {
		t.Fatalf("expected *Error, got %T", es[0])
	}
	if ce.Note != `did you mean "counter"?` {
		t.Errorf("hint = %q, want suggestion of \"counter\"", ce.Note)
	}
	if ce.Span != len("countr") {
		t.Errorf("span = %d, want %d", ce.Span, len("countr"))
	}
}

func TestUndefinedIdentifierNoSuggestionWhenFar(t *testing.T) {
	prog, err := parser.Parse(`function f(): i32 {
		var counter: i32 = 0;
		return totallyUnrelated;
	}`)
	if err != nil {
		t.Fatal(err)
	}
	_, err = Check(prog)
	if err == nil {
		t.Fatal("expected error")
	}
	es := err.(diag.Errors)
	ce := es[0].(*Error)
	if ce.Note != "" {
		t.Errorf("expected no hint, got %q", ce.Note)
	}
}

func TestSwitchTypechecks(t *testing.T) {
	src := `function f(n: i32): i32 {
		switch (n) {
			case 1, 2: return 10;
			case 3: return 30;
			default: return 0;
		}
		return -1;
	}`
	if err := checkSource(t, src); err != nil {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestStructTypechecks(t *testing.T) {
	src := `struct Point { x: i32, y: i32 }
		function main(): i32 {
			var p: Point = Point { x: 1, y: 2 };
			p.x = 10;
			return p.x + p.y;
		}`
	if err := checkSource(t, src); err != nil {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestSwitchRejectsTypeMismatchedCase(t *testing.T) {
	src := `function f(n: i32): i32 {
		switch (n) { case true: return 1; default: return 0; }
	}`
	if err := checkSource(t, src); err == nil {
		t.Error("expected type-mismatch error on case value")
	}
}

func TestSwitchRejectsFloatTag(t *testing.T) {
	src := `function f(x: f32): i32 {
		switch (x) { case 1.0: return 1; default: return 0; }
	}`
	if err := checkSource(t, src); err == nil {
		t.Error("expected error switching on float")
	}
}

func TestBreakInSwitchAllowed(t *testing.T) {
	src := `function f(n: i32): i32 {
		switch (n) { case 1: break; default: break; }
		return 0;
	}`
	if err := checkSource(t, src); err != nil {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestContinueInSwitchOutsideLoopRejected(t *testing.T) {
	src := `function f(n: i32): i32 {
		switch (n) { case 1: continue; default: return 0; }
	}`
	if err := checkSource(t, src); err == nil {
		t.Error("expected `continue outside of a loop`")
	}
}

func TestIfExprTypechecks(t *testing.T) {
	for _, src := range []string{
		`function f(b: boolean): i32 { return if (b) { 1 } else { 2 }; }`,
		`function f(b: boolean): f32 { return if (b) { 1.5 } else { 2.5 }; }`,
	} {
		if err := checkSource(t, src); err != nil {
			t.Errorf("%q: unexpected error %v", src, err)
		}
	}
}

func TestIfExprRejectsNonBoolCond(t *testing.T) {
	if err := checkSource(t, `function f(): i32 { return if (1) { 2 } else { 3 }; }`); err == nil {
		t.Error("expected error for non-bool cond")
	}
}

func TestIfExprRejectsBranchTypeMismatch(t *testing.T) {
	if err := checkSource(t, `function f(b: boolean): i32 { return if (b) { 1 } else { true }; }`); err == nil {
		t.Error("expected error for mismatched branches")
	}
}

// Postfix `?` produces the unwrapped Some payload type.
func TestOptionTryTypechecks(t *testing.T) {
	src := `function f(m: Map[i32, i32]): Option[i32] {
		var v: i32 = m.get(1)?;
		return Some(v);
	}`
	if err := checkSource(t, src); err != nil {
		t.Errorf("unexpected error: %v", err)
	}
}

// `?` rejected when the receiver type is not Option[_].
func TestOptionTryRejectsNonOption(t *testing.T) {
	src := `function f(): Option[i32] {
		var n: i32 = 5;
		var v: i32 = n?;
		return Some(v);
	}`
	if err := checkSource(t, src); err == nil {
		t.Error("expected error: `?` on non-Option")
	}
}

// `?` rejected when the surrounding function doesn't return
// Option — the early-return target wouldn't unify.
func TestOptionTryRejectsNonOptionReturn(t *testing.T) {
	src := `function f(m: Map[i32, i32]): i32 {
		var v: i32 = m.get(1)?;
		return v;
	}`
	if err := checkSource(t, src); err == nil {
		t.Error("expected error: enclosing fn must return Option[_]")
	}
}

// `?` on a Result[T, E] yields T and requires the enclosing
// function to return Result[_, E] for some success type.
func TestResultTryTypechecks(t *testing.T) {
	src := `function inner(): Result[i32, i32] { return Ok(42); }
function outer(): Result[i32, i32] {
	var v: i32 = inner()?;
	return Ok(v + 1);
}`
	if err := checkSource(t, src); err != nil {
		t.Errorf("unexpected error: %v", err)
	}
}

// Surrounding function must also return Result.
func TestResultTryRejectsNonResultReturn(t *testing.T) {
	src := `function inner(): Result[i32, i32] { return Ok(42); }
function outer(): i32 {
	var v: i32 = inner()?;
	return v;
}`
	if err := checkSource(t, src); err == nil {
		t.Error("expected error: enclosing fn must return Result[_, E]")
	}
}

// Source's E must match the enclosing function's E. No
// auto-conversion (no Rust-style From shim).
func TestResultTryRejectsErrTypeMismatch(t *testing.T) {
	src := `function inner(): Result[i32, i32] { return Ok(42); }
function outer(): Result[i32, string] {
	var v: i32 = inner()?;
	return Ok(v);
}`
	if err := checkSource(t, src); err == nil {
		t.Error("expected error: source's Err type doesn't match enclosing return")
	}
}

// `match` in expression position: arms must agree on a single
// type, exhaustiveness still required, payload bindings are in
// scope inside arm bodies.
func TestMatchExprTypechecks(t *testing.T) {
	src := `function f(o: Option[i32]): i32 {
		return match (o) {
			Some(x) => x + 1,
			None    => 0
		};
	}`
	if err := checkSource(t, src); err != nil {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestMatchExprRejectsBranchTypeMismatch(t *testing.T) {
	src := `function f(o: Option[i32]): i32 {
		return match (o) {
			Some(x) => x,
			None    => true
		};
	}`
	if err := checkSource(t, src); err == nil {
		t.Error("expected error: arm types differ")
	}
}

func TestMatchExprRejectsNonExhaustive(t *testing.T) {
	src := `enum Light { Red, Green, Yellow }
function pick(): i32 {
	var l: Light = Green;
	return match (l) {
		Red   => 1,
		Green => 2
	};
}`
	if err := checkSource(t, src); err == nil {
		t.Error("expected error: missing Yellow arm")
	}
}

// Typed numeric literal suffixes resolve to concrete types at
// parse time. The checker stamps NumberLit/FloatLit Width based
// on the suffix so binary-op + assignment paths treat them as
// non-polymorphic, removing the need for `as` casts in
// expressions like `Circle(r: f32) when r <= 0f32 =>`.
func TestNumericLiteralSuffixesTypecheck(t *testing.T) {
	for _, src := range []string{
		// var-init context confirms suffix-stamped literals carry
		// the right concrete type without any `as` cast.
		`function f(): i32 { var x: i64 = 42i64; return 0; }`,
		`function f(): i32 { var x: u8 = 7u8; return 0; }`,
		`function f(): i32 { var x: f64 = 1.5f64; return 0; }`,
		// f32 suffix on integer-shaped text → float literal.
		`function f(): i32 { var x: f32 = 42f32; return 0; }`,
		// Compares against suffixed literal — no `as` cast needed.
		`enum Shape { Circle(f32), Square(f32) }
function classify(s: Shape): i32 {
	match (s) {
		Circle(r) when r <= 0f32 => { return 1; },
		Circle(_)                => { return 2; },
		Square(_)                => { return 3;}
	}
	return 0;
}`,
	} {
		if err := checkSource(t, src); err != nil {
			t.Errorf("unexpected error in %q: %v", src, err)
		}
	}
}

// Suffix mismatch surfaces as an assignment error, not a silent
// truncation.
func TestNumericLiteralSuffixesRejectMismatch(t *testing.T) {
	if err := checkSource(t, `function f(): i32 { var x: i32 = 42i64; return 0; }`); err == nil {
		t.Error("expected error: assigning i64 literal to i32 var")
	}
}

// `arr.push(v)` is a generic method on T[]. The receiver's Elem
// flows into the registered ParamType("T") signature, so the
// argument and return types substitute correctly.
func TestArrayPushTypechecks(t *testing.T) {
	for _, src := range []string{
		`function f(): i32 { var xs: string[] = []; xs = xs.push("a"); return len(xs); }`,
		`function f(): i32 { var xs: i32[] = [1, 2]; xs = xs.push(3); return xs[2]; }`,
	} {
		if err := checkSource(t, src); err != nil {
			t.Errorf("%q: unexpected error %v", src, err)
		}
	}
}

// Wide-stride arrays (i64[], f64[]) have no append helper wired
// up yet — checker rejects with a clear pointer at the storage
// class rather than producing broken wat.
// 8-byte int strides (i64 / u64) now route to a separate
// `__array_append_i64` wat helper. f64 is still gated — its
// 8-byte slot needs its own f64.store helper which isn't wired
// up yet.
func TestArrayPushI64StridePasses(t *testing.T) {
	for _, src := range []string{
		`function f(): i32 { var xs: i64[] = [1i64, 2i64]; xs = xs.push(3i64); return 0; }`,
		`function f(): i32 { var xs: u64[] = [1u64]; xs = xs.push(2u64); return 0; }`,
	} {
		if err := checkSource(t, src); err != nil {
			t.Errorf("%q: unexpected error %v", src, err)
		}
	}
}

func TestArrayPushF64StridePasses(t *testing.T) {
	src := `function f(): i32 { var xs: f64[] = [1.0f64]; xs = xs.push(2.0f64); return 0; }`
	if err := checkSource(t, src); err != nil {
		t.Errorf("unexpected error: %v", err)
	}
}

// Sub-i32 strides: 1-byte (u8 / i8) and 2-byte (u16 / i16)
// each route to their own lang-prelude append helper.
func TestArrayPushSubI32StridePasses(t *testing.T) {
	for _, src := range []string{
		`function f(): i32 { var xs: u8[] = []; xs = xs.push(7u8); return 0; }`,
		`function f(): i32 { var xs: i8[] = []; xs = xs.push(7i8); return 0; }`,
		`function f(): i32 { var xs: u16[] = []; xs = xs.push(300u16); return 0; }`,
		`function f(): i32 { var xs: i16[] = []; xs = xs.push((0i16 - 1i16)); return 0; }`,
	} {
		if err := checkSource(t, src); err != nil {
			t.Errorf("%q: unexpected error %v", src, err)
		}
	}
}

// Argument type must match the receiver's Elem.
func TestArrayPushRejectsArgTypeMismatch(t *testing.T) {
	src := `function f(): i32 { var xs: string[] = []; xs = xs.push(1); return len(xs); }`
	if err := checkSource(t, src); err == nil {
		t.Error("expected error: pushing i32 onto string[]")
	}
}

// Wildcard `_` arm covers exhaustiveness for the same shape that
// would otherwise need every variant listed.
func TestMatchExprWildcardCoversExhaustiveness(t *testing.T) {
	src := `enum Light { Red, Green, Yellow }
function pick(): i32 {
	var l: Light = Green;
	return match (l) {
		Red => 1,
		_   => 0
	};
}`
	if err := checkSource(t, src); err != nil {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestCompoundAssignTypechecks(t *testing.T) {
	src := `function f(): i32 {
		var x: i32 = 0;
		x += 1; x -= 1; x *= 2; x /= 2; x %= 3;
		x &= 7; x |= 8; x ^= 1; x <<= 1; x >>= 1;
		return x;
	}`
	if err := checkSource(t, src); err != nil {
		t.Errorf("unexpected error %v", err)
	}
}

func TestStructLitMissingField(t *testing.T) {
	src := `struct P { x: i32, y: i32 }
		function f(): P { return P { x: 1 }; }`
	if err := checkSource(t, src); err == nil {
		t.Error("expected error for missing field y")
	}
}

func TestStructLitWrongFieldType(t *testing.T) {
	src := `struct P { x: i32 }
		function f(): P { return P { x: true }; }`
	if err := checkSource(t, src); err == nil {
		t.Error("expected error for boolean as i32 field")
	}
}

func TestUnknownStructType(t *testing.T) {
	src := `function f(): i32 {
		var p: NoSuchStruct = NoSuchStruct { x: 1 };
		return p.x;
	}`
	if err := checkSource(t, src); err == nil {
		t.Error("expected error for unknown struct type")
	}
}

func TestNestedFunctionTypechecks(t *testing.T) {
	src := `function makeAdder(n: i32): (i32) => i32 {
		function add(x: i32): i32 { return x + n; }
		return add;
	}`
	if err := checkSource(t, src); err != nil {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestNestedFunctionRecordsCaptures(t *testing.T) {
	prog, err := parser.Parse(`function outer(seed: i32): i32 {
		var bonus: i32 = 100;
		function inner(x: i32): i32 { return x + seed + bonus; }
		return inner(1);
	}`)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Check(prog); err != nil {
		t.Fatal(err)
	}
	// The inner function statement is the third statement in outer.
	body := prog.Funcs[0].Body.Stmts
	var inner *ast.FuncDecl
	for _, s := range body {
		if fn, ok := s.(*ast.FuncDecl); ok {
			inner = fn
			break
		}
	}
	if inner == nil {
		t.Fatal("expected nested FuncDecl in outer's body")
	}
	if len(inner.Captures) != 2 {
		t.Errorf("captures = %v, want [seed, bonus]", inner.Captures)
	}
}

// Pointer-shaped captures (string, T[], [T], structs, enums,
// tuples, function values) all type-check now — their 4-byte
// heap reference fits in the same env-slot scalars use.
func TestPointerCapturesTypecheck(t *testing.T) {
	cases := []string{
		// string
		`function outer(s: string): i32 {
			function inner(): i32 { return len(s); }
			return inner();
		}`,
		// T[] (i32 array)
		`function outer(xs: i32[]): i32 {
			function inner(): i32 { return len(xs); }
			return inner();
		}`,
		// struct
		`struct Pt { x: i32, y: i32 }
		function outer(p: Pt): i32 {
			function inner(): i32 { return p.x + p.y; }
			return inner();
		}`,
		// enum
		`function outer(o: Option[i32]): i32 {
			function inner(): i32 {
				return match (o) {
					Some(x) => x,
					None    => 0
				};
			}
			return inner();
		}`,
	}
	for _, src := range cases {
		if err := checkSource(t, src); err != nil {
			t.Errorf("%q: unexpected error %v", src, err)
		}
	}
}

func TestMethodTypechecksAndRewritesCall(t *testing.T) {
	prog, err := parser.Parse(`struct Point { x: i32, y: i32 }
		function (p: Point) sum(): i32 { return p.x + p.y; }
		function main(): i32 {
			var p: Point = Point { x: 10, y: 32 };
			return p.sum();
		}`)
	if err != nil {
		t.Fatal(err)
	}
	info, err := Check(prog)
	if err != nil {
		t.Fatalf("check: %v", err)
	}
	// The method should be hoisted to a mangled name and registered
	// in info.Methods.
	mangled, ok := info.Methods["Point.sum"]
	if !ok {
		t.Fatal("Methods map missing Point.sum")
	}
	if mangled != "__method_Point_sum" {
		t.Errorf("mangled = %q, want \"__method_Point_sum\"", mangled)
	}
	// The call site `p.sum()` should be rewritten to a regular call.
	main := prog.Funcs[1] // sum was renamed but stays index 0; main is index 1
	for _, fn := range prog.Funcs {
		if fn.Name == "main" {
			main = fn
		}
	}
	ret := main.Body.Stmts[1].(*ast.Return)
	call, ok := ret.Value.(*ast.Call)
	if !ok {
		t.Fatalf("return value should be a Call, got %T", ret.Value)
	}
	id, ok := call.Callee.(*ast.Ident)
	if !ok || id.Name != "__method_Point_sum" {
		t.Errorf("callee = %v, want Ident{__method_Point_sum}", call.Callee)
	}
	if len(call.Args) != 1 {
		t.Errorf("args = %d, want 1 (the receiver)", len(call.Args))
	}
}

func TestMethodRejectsNonStructReceiver(t *testing.T) {
	// `i32` (a built-in numeric receiver) is now permitted —
	// it's how the prelude declares `i32.to_string()` etc.
	// What's rejected is receivers that aren't a struct,
	// enum, or built-in scalar — eg an array type.
	src := `function (xs: i32[]) sum(): i32 { return 0; }`
	if err := checkSource(t, src); err == nil {
		t.Error("expected error for array receiver")
	}
}

func TestMethodCallOnUnknownMethodErrors(t *testing.T) {
	src := `struct P { x: i32 }
		function main(): i32 {
			var p: P = P { x: 1 };
			return p.unknown();
		}`
	if err := checkSource(t, src); err == nil {
		t.Error("expected error for missing method")
	}
}

// Variant constructors type-check argument count + payload types.
func TestEnumVariantConstructorTypeChecks(t *testing.T) {
	good := `enum E { Pair(i32, i32) }
		function main(): i32 {
			var e: E = Pair(1, 2);
			return 0;
		}`
	if err := checkSource(t, good); err != nil {
		t.Errorf("good source should type-check: %v", err)
	}

	wrongCount := `enum E { Pair(i32, i32) }
		function main(): i32 {
			var e: E = Pair(1);
			return 0;
		}`
	if err := checkSource(t, wrongCount); err == nil {
		t.Error("expected error for wrong arg count")
	}

	wrongType := `enum E { Pair(i32, i32) }
		function main(): i32 {
			var e: E = Pair(1, "two");
			return 0;
		}`
	if err := checkSource(t, wrongType); err == nil {
		t.Error("expected error for wrong arg type")
	}
}

// Non-exhaustive matches (missing a variant, no wildcard) are
// rejected with a diagnostic naming the missing variant.
func TestMatchExhaustivenessChecked(t *testing.T) {
	src := `enum Light { Red, Green, Yellow }
		function main(): i32 {
			var l: Light = Green;
			match (l) {
				Red => { return 1; },
				Green => { return 2; }
			}
			return 0;
		}`
	err := checkSource(t, src)
	if err == nil {
		t.Fatal("expected exhaustiveness error")
	}
	if !strings.Contains(err.Error(), "Yellow") {
		t.Errorf("error should name the missing variant; got %v", err)
	}
}

// A wildcard arm satisfies exhaustiveness even when not every
// variant is listed explicitly.
func TestMatchWildcardCoversMissingVariants(t *testing.T) {
	src := `enum Light { Red, Green, Yellow }
		function main(): i32 {
			var l: Light = Green;
			match (l) {
				Red => { return 1; },
				_ => { return 0; }
			}
			return 0;
		}`
	if err := checkSource(t, src); err != nil {
		t.Errorf("wildcard should satisfy exhaustiveness: %v", err)
	}
}

// Match arm bindings count must match the variant's payload
// arity, mirroring how variant construction validates argument
// counts at the constructor site.
func TestMatchPayloadArityChecked(t *testing.T) {
	src := `enum E { A, B(i32, i32) }
		function main(): i32 {
			var e: E = A;
			match (e) {
				A => { return 0; },
				B(x) => { return x; }
			}
			return -1;
		}`
	if err := checkSource(t, src); err == nil {
		t.Error("expected error: B has 2 payloads but only 1 binding")
	}
}

// Generic enums infer their type arguments from the constructor's
// payload types: `Some(42)` resolves T to `i32`, so the
// resulting type is `Option[i32]`. The right-hand side of an
// assignment with a wrong concrete type fails at the slot.
func TestGenericVariantInfersTypeArgs(t *testing.T) {
	good := `enum Option[T] { Some(T), None }
		function main(): i32 {
			var o: Option[i32] = Some(42);
			return 0;
		}`
	if err := checkSource(t, good); err != nil {
		t.Errorf("good: %v", err)
	}
	bad := `enum Option[T] { Some(T), None }
		function main(): i32 {
			var o: Option[string] = Some(42);
			return 0;
		}`
	if err := checkSource(t, bad); err == nil {
		t.Error("expected mismatch: Option[string] vs Option[i32]")
	}
}

// Payload-less variants on generic enums (`None`) can flow into
// any concrete instantiation of the same enum because the
// constructor itself can't determine the type arguments — the
// surrounding context (var annotation, return type, function
// arg slot) supplies them.
func TestPayloadlessGenericVariantFlowsIntoContext(t *testing.T) {
	src := `enum Option[T] { Some(T), None }
		function find(): Option[i32] { return None; }
		function main(): i32 {
			var o: Option[i32] = None;
			return 0;
		}`
	if err := checkSource(t, src); err != nil {
		t.Errorf("None should flow into Option[i32]: %v", err)
	}
}

// Match arms substitute the scrutinee's concrete type arguments
// into payload bindings: matching `Option[i32]` types
// `Some(v)` so that `v` is `i32`, not the abstract `T`.
func TestMatchSubstitutesTypeArgs(t *testing.T) {
	src := `enum Option[T] { Some(T), None }
		function main(): i32 {
			var o: Option[i32] = Some(7);
			match (o) {
				Some(v) => { return v + 1; },
				None => { return 0; }
			}
			return -1;
		}`
	if err := checkSource(t, src); err != nil {
		t.Errorf("type-substituted match arms should type-check: %v", err)
	}
}

// Wrong-arity instantiations (missing or extra type arguments)
// are rejected at the type position before any value-level
// checking happens.
// `usize + i32` (and the reverse) auto-widens the concrete-int
// side to usize so prelude pointer math stays readable —
// `base + idx * stride` doesn't need `(idx as usize) * (stride
// as usize)` boilerplate. Mixed signedness is allowed only
// when one side is usize; signed/unsigned at other widths
// still errors out so accidental conversions stay loud.
// The raw-memory prelude helpers (`__alloc`, `__load_ptr`,
// `__store_ptr`, `__memcpy`, `__memset`) now declare their
// pointer params + result as `usize`. User code (and prelude
// code) that passes pointer-shaped values (string, Map handles,
// T[], [T], structs) into these helpers must continue to
// type-check without explicit `as usize` casts — the runtime
// representation is the same pointer, and the relaxation in
// `assignable` permits the silent type-level hop.
//
// Held together by 3 relaxations:
//   1. pointer-shape → usize / usize → pointer-shape
//   2. usize ↔ any concrete int (for `var X: i32 = __alloc(...)`)
//   3. enum-arg-pairwise assignable (for `Option[V]` ↔ `Option[usize]`)
func TestUsizePreludeHelperSignaturesAcceptPointerArgs(t *testing.T) {
	for _, src := range []string{
		// String passed straight to a usize-typed param.
		// Without the pointer-shape → usize relaxation, this
		// would error "argument 1: expected usize, got string".
		`function f(a: string, b: string, n: i32): i32 {
    __memcpy(a, b, n);
    return 0;
}`,
		// usize result of __alloc assignable to i32 (legacy
		// shape). Without relaxation #2 this would error
		// "cannot assign usize to variable of type i32".
		`function f(): i32 {
    var buf: i32 = __alloc(16);
    return buf;
}`,
	} {
		if err := checkSource(t, src); err != nil {
			t.Errorf("%q: expected to type-check, got: %v", src, err)
		}
	}
}

func TestUsizeAutowidensWithSignedInt(t *testing.T) {
	for _, src := range []string{
		`function f(base: usize, idx: i32): usize {
    return base + idx;
}`,
		`function f(p: usize): usize {
    return p + 4;
}`,
		`function f(idx: i32, stride: i32): usize {
    var base: usize = 100;
    return base + idx * stride;
}`,
	} {
		if err := checkSource(t, src); err != nil {
			t.Errorf("%q: expected to type-check, got: %v", src, err)
		}
	}
}

// The auto-widen relaxation is scoped to usize: other
// signed/unsigned width mismatches must still error out.
func TestSignednessMismatchStillErrorsAtOtherWidths(t *testing.T) {
	src := `function f(a: u32, b: i32): u32 {
    return a + b;
}`
	if err := checkSource(t, src); err == nil {
		t.Error("expected signedness error: u32 + i32 should not auto-widen")
	}
}

func TestGenericEnumArityChecked(t *testing.T) {
	src := `enum Pair[A, B] { Both(A, B) }
		function main(): i32 {
			var p: Pair[i32] = Both(1, 2);
			return 0;
		}`
	if err := checkSource(t, src); err == nil {
		t.Error("expected arity error: Pair has 2 type params, 1 supplied")
	}
}

// `use n <- generic_fn(args...);` must infer `n`'s type by
// solving the callee's type parameters from the args, then
// applying the substitution to the callback's first param.
// Previously the checker stamped the raw parameter type
// straight onto `n` — which left it as `T` for generic
// callees, breaking subsequent uses like `n + 1`.
func TestUseInfersBindingTypeFromGenericCallee(t *testing.T) {
	src := `function each[T](items: T[], cb: (T) => i32): i32 {
    return cb(items[0]);
}
function main(): i32 {
    var nums: i32[] = [10, 20, 30];
    use n <- each(nums);
    return n + 1;
}`
	if err := checkSource(t, src); err != nil {
		t.Errorf("expected generic-callee inference to succeed, got: %v", err)
	}
}

// Generic inference works through enum-typed args too — e.g.
// when the callback takes the unwrapped payload of an
// `Option[T]` arg, T is resolved by unifying the actual
// Option[i32] arg against the param `Option[T]`.
func TestUseInfersFromEnumPayloadGeneric(t *testing.T) {
	src := `function try_opt[T](opt: Option[T], cb: (T) => i32): i32 {
    if let Some(v) = opt { return cb(v); }
    return 0;
}
function main(): i32 {
    var x: Option[i32] = Some(7);
    use n <- try_opt(x);
    return n + 1;
}`
	if err := checkSource(t, src); err != nil {
		t.Errorf("expected Option[T] inference to succeed, got: %v", err)
	}
}

// When inference can't pin every relevant type parameter
// (the callback's first param references a T that doesn't
// appear in any non-callback parameter position), the
// checker must reject the `use` rather than silently
// stamping the unresolved ParamType — which would cascade
// into confusing type errors at every use of the binding.
func TestUseRejectsUninferableGenericCallee(t *testing.T) {
	src := `function mystery[T](cb: (T) => i32): i32 {
    return 0;
}
function main(): i32 {
    use n <- mystery();
    return n;
}`
	err := checkSource(t, src)
	if err == nil {
		t.Fatal("expected error for uninferable T")
	}
	if !strings.Contains(err.Error(), "could not infer") {
		t.Errorf("expected 'could not infer' error, got: %v", err)
	}
}
