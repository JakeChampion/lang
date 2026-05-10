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
	prog, err := parser.Parse(`function f(x: float, y: float): float { return x + y; }`)
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
		`function f(x: float): float { return x + 1.5; }`,
		`function f(x: float): float { return x * 2.0 - 0.5; }`,
		`function f(x: float, y: float): boolean { return x < y; }`,
		`function f(x: float): float { return -x; }`,
	} {
		if err := checkSource(t, src); err != nil {
			t.Errorf("%q: unexpected error %v", src, err)
		}
	}
}

func TestFloatRejectsMixedArithmetic(t *testing.T) {
	cases := []string{
		`function f(x: float): float { return x + 1; }`,
		`function f(x: i32): float { return x + 1.5; }`,
		`function f(x: float): float { return x % 1.0; }`, // % is integer-only
	}
	for _, src := range cases {
		if err := checkSource(t, src); err == nil {
			t.Errorf("%q: expected error", src)
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
	src := `function f(x: float): i32 {
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
		`function f(b: boolean): float { return if (b) { 1.5 } else { 2.5 }; }`,
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

func TestCaptureOfNonScalarRejected(t *testing.T) {
	// String capture isn't supported in this PR.
	src := `function outer(s: string): i32 {
		function inner(): i32 { return len(s); }
		return inner();
	}`
	if err := checkSource(t, src); err == nil {
		t.Error("expected error for capturing a string")
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
