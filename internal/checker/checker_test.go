package checker

import (
	"context"
	"errors"
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

// TestGenericStructLitFieldCheckedAgainstDestination covers a generic struct
// literal whose field value conflicts with an explicit destination
// instantiation — `var b: Box[i32] = Box { v: "x" }`. The field used to be
// checked only against the free parameter `T` (which unified to `string`), so
// the mismatch slipped past the checker and crashed monomorph re-check with a
// confusing "compiler bug". The destination now seeds the type-arg
// substitution, so it's a clean E043 at check time — while genuinely valid
// instantiations and unannotated inference still pass.
func TestGenericStructLitFieldCheckedAgainstDestination(t *testing.T) {
	mustErr := func(src, want string) {
		t.Helper()
		err := checkSource(t, src)
		if err == nil {
			t.Errorf("expected an error for %q, got none", src)
			return
		}
		if !strings.Contains(err.Error(), want) {
			t.Errorf("for %q: want error containing %q, got: %v", src, want, err)
		}
	}
	mustOK := func(src string) {
		t.Helper()
		if err := checkSource(t, src); err != nil {
			t.Errorf("expected no error for %q, got: %v", src, err)
		}
	}
	// Mismatch is caught at check time with the substituted type in the
	// message (`i32`, not the bare `T`), not deferred to monomorph.
	mustErr(`struct Box[T] { v: T } function main(): i32 { var b: Box[i32] = Box { v: "x" }; return b.v; }`,
		`field "v": expected i32, got string`)
	mustErr(`struct Pair[A, B] { a: A, b: B } function main(): i32 { var p: Pair[i32, string] = Pair { a: "x", b: 1 }; return 0; }`,
		`field "a": expected i32, got string`)
	// Valid instantiations and unannotated inference are unaffected.
	mustOK(`struct Box[T] { v: T } function main(): i32 { var b: Box[i32] = Box { v: 5 }; return b.v; }`)
	mustOK(`struct Box[T] { v: T } function main(): i32 { var b: Box[string] = Box { v: "x" }; return b.v.len(); }`)
	mustOK(`struct Box[T] { v: T } function main(): i32 { var b = Box { v: "x" }; return 0; }`)
}

// TestGenericStructLitNestedInstantiation locks the fix for a regression
// the destination-seeding above (#3763) introduced: a nested struct literal
// whose field type reuses the SAME generic name re-seeded its type-args from
// the OUTER destination left in c.expectedType, instead of from its own
// field type. `var b: Box[Box[i32]] = Box { v: Box { v: 42 } }` wrongly bound
// the inner Box's T to Box[i32] (not i32) and reported a spurious
// `field "v": expected Box[i32], got i32`. The field value is now checked
// against its substituted field type, so nested generics check cleanly — while
// a genuine nested mismatch is still caught.
func TestGenericStructLitNestedInstantiation(t *testing.T) {
	mustOK := func(src string) {
		t.Helper()
		if err := checkSource(t, src); err != nil {
			t.Errorf("expected no error for %q, got: %v", src, err)
		}
	}
	mustErr := func(src, want string) {
		t.Helper()
		err := checkSource(t, src)
		if err == nil {
			t.Errorf("expected an error for %q, got none", src)
			return
		}
		if !strings.Contains(err.Error(), want) {
			t.Errorf("for %q: want error containing %q, got: %v", src, want, err)
		}
	}
	// The regression repro: nested same-named generic now checks cleanly.
	mustOK(`struct Box[T] { v: T } function main(): i32 { var b: Box[Box[i32]] = Box { v: Box { v: 42 } }; return b.v.v; }`)
	// Three levels deep, still clean.
	mustOK(`struct Box[T] { v: T } function main(): i32 { var b: Box[Box[Box[i32]]] = Box { v: Box { v: Box { v: 7 } } }; return b.v.v.v; }`)
	// A nested generic with a different element type.
	mustOK(`struct Box[T] { v: T } function main(): i32 { var b: Box[Box[string]] = Box { v: Box { v: "x" } }; return b.v.v.len(); }`)
	// A genuine mismatch in the inner literal is still reported.
	mustErr(`struct Box[T] { v: T } function main(): i32 { var b: Box[Box[i32]] = Box { v: Box { v: "x" } }; return 0; }`,
		`field "v": expected i32, got string`)
	// A numeric-literal field value settles to the CONCRETE (substituted)
	// field type, not the bare parameter: an i64-magnitude literal in a
	// Box[i64] field must widen to i64 rather than being left i32 and then
	// rejected by the seeded sub[T]=i64. (#3763 also regressed this.)
	mustOK(`struct Box[T] { v: T } function main(): i32 { var b: Box[i64] = Box { v: 1234567890123 }; if (b.v == 1234567890123) { return 0; } return 1; }`)
}

// TestOperatorClassMismatchNoRedundantShareError covers the cascade where an
// arithmetic/bitwise operator with a wrong-class operand (e.g. a string where
// an integer is required) stacked TWO E009s: one from the per-operand
// requireInteger/requireFloat check, then a redundant "both operands must
// share a … type" follow-on. The follow-on now fires only when both operands
// ARE the right class but differ in width/signedness — the case where its
// `use as` hint is actually actionable.
func TestOperatorClassMismatchNoRedundantShareError(t *testing.T) {
	count := func(src string) int {
		err := checkSource(t, src)
		if err == nil {
			t.Fatalf("expected a type error for %q, got none", src)
		}
		// Each case's only diagnostic is the operator error itself, so the
		// total diagnostic count is the E009 count.
		return strings.Count(err.Error(), "type error at")
	}
	// One wrong-class operand → exactly one E009 (no redundant "share" line).
	if n := count(`function main(): i32 { var x: i32 = 1 - "b"; return 0; }`); n != 1 {
		t.Errorf("int op with string operand: %d E009s, want 1", n)
	}
	if n := count(`function main(): i32 { var s: string = "a"; return s & 1; }`); n != 1 {
		t.Errorf("bitwise op with string operand: %d E009s, want 1", n)
	}
	// Both operands the right class but mismatched width/signedness → the
	// "share a … type" hint is still emitted (it's actionable via `as`).
	if n := count(`function main(): i32 { var a: i32 = 1; var b: u32 = 2; return a - b; }`); n != 1 {
		t.Errorf("i32/u32 mismatch: %d E009s, want 1 (the share hint)", n)
	}
}

// TestUnknownTypeReported covers E064: a nominal type reference that names
// no declared type is now flagged at the annotation, instead of being
// silently accepted (and only surfacing as a confusing E002/E003 cascade,
// or not at all). It fires in every annotation position and recurses into
// composite types — while leaving valid built-ins, declared types, and
// in-scope type parameters untouched.
func TestUnknownTypeReported(t *testing.T) {
	mustE064 := func(src string) {
		t.Helper()
		err := checkSource(t, src)
		if err == nil || !strings.Contains(err.Error(), "unknown type") {
			t.Errorf("expected E064 unknown type for %q, got: %v", src, err)
		}
	}
	mustOK := func(src string) {
		t.Helper()
		if err := checkSource(t, src); err != nil {
			t.Errorf("expected no error for %q, got: %v", src, err)
		}
	}
	// Every annotation position rejects an undefined name.
	mustE064(`function f(a: Wibble): i32 { return 0; }`)
	mustE064(`function f(): Wibble { return 0; }`)
	mustE064(`function main(): i32 { var x: Wibble = 0; return 0; }`)
	mustE064(`struct S { f: Wibble }`)
	mustE064(`enum E { A(Wibble) }`)
	// Recurses into composite types (array / tuple / nested).
	mustE064(`function main(): i32 { var a: Wibble[] = []; return 0; }`)
	mustE064(`function f(): (i32, Wibble) { return (0, 0); }`)
	// `bool` is a common slip — the real type is `boolean`.
	mustE064(`function main(): i32 { var x: bool = 0; return 0; }`)
	// Valid: built-ins, declared types, and in-scope type parameters.
	mustOK(`function id[T](x: T): T { return x; }`)
	mustOK(`struct P { x: i32 } function getx(p: P): i32 { return p.x; }`)
	mustOK(`struct Box[T] { v: T } function main(): i32 { var b: Box[i32] = Box { v: 7 }; return b.v; }`)
	mustOK(`function main(): i32 { var o: Option[i32] = None; return 0; }`)
	mustOK(`function main(): i32 { var n: i32 = 1; var b: boolean = true; if (b) { return n; } return 0; }`)
}

// TestBinaryOperandErrorNoCascade guards two bugs in one: a binary
// expression with an already-errored operand (here `q()`, an undefined
// identifier, whose type is nil) used to emit a *second*, cascading E009
// "operator requires …" diagnostic — and, worse, format the nil operand
// type into that message as the literal Go garbage `%!s(<nil>)`. checkExpr
// on a Binary now bails when either operand failed to type, so only the
// root cause is reported and no malformed type string leaks out.
func TestBinaryOperandErrorNoCascade(t *testing.T) {
	check := func(src string) (errCount, nilFmt int) {
		err := checkSource(t, src)
		if err == nil {
			t.Fatalf("expected a type error for %q, got none", src)
		}
		msg := err.Error()
		return strings.Count(msg, "type error at"), strings.Count(msg, "%!s")
	}
	for _, src := range []string{
		`function main(): i32 { return q() + 1; }`,
		`function main(): i32 { return 1 + q(); }`,
		`function main(): i32 { return q() & 1; }`,
		`function main(): i32 { return q() % 2; }`,
		`function main(): i32 { return (q() + 1) * 2; }`,
	} {
		n, nf := check(src)
		if n != 1 {
			t.Errorf("%q: reported %d errors, want 1 (no cascade)", src, n)
		}
		if nf != 0 {
			t.Errorf("%q: message leaked %d %%!s(<nil>) format error(s)", src, nf)
		}
	}
}

// TestChainedMethodCallErrorReportedOnce guards against a diagnostic
// duplication: a method-call chain `n.foo().bar()` whose inner `n.foo()` is
// invalid (here, a method call on a non-struct i32) reported the inner E043
// twice — once when the outer call checked its receiver, then again when the
// generic callee path re-checked the same target. checkCall now bails as soon
// as the receiver fails to type, so the diagnostic is emitted exactly once.
func TestChainedMethodCallErrorReportedOnce(t *testing.T) {
	count := func(src string) int {
		err := checkSource(t, src)
		if err == nil {
			t.Fatalf("expected a type error for %q, got none", src)
		}
		return strings.Count(err.Error(), "field access on non-struct")
	}
	if n := count(`function main(): i32 { var n: i32 = 5; return n.foo().bar(); }`); n != 1 {
		t.Errorf("chained call: E043 reported %d times, want 1", n)
	}
	if n := count(`function main(): i32 { var n: i32 = 5; return n.a().b().c(); }`); n != 1 {
		t.Errorf("triple chain: E043 reported %d times, want 1", n)
	}
	// A single (non-chained) bad method call was always reported once —
	// pin that it still is, so the fix didn't suppress the lone diagnostic.
	if n := count(`function main(): i32 { var n: i32 = 5; return n.foo(); }`); n != 1 {
		t.Errorf("single call: E043 reported %d times, want 1", n)
	}
}

// TestUnannotatedBigLiteralDefaultsToI64 covers #3676: an unannotated integer
// literal that doesn't fit i32 defaults to i64 (option 2), not the usual i32.
// The inferred type is asserted indirectly through assignability — a binding
// whose value is past i32 range is i64, so storing it into an i32 slot is
// rejected (E003), while a small literal stays i32 and assigns fine, and an
// i64 slot accepts it.
func TestUnannotatedBigLiteralDefaultsToI64(t *testing.T) {
	// Past i32 max → i64, so i64-context assignment is fine...
	if err := checkSource(t, `function main(): i32 { var x = 2147483648; var y: i64 = x; return (y / 1000000000) as i32; }`); err != nil {
		t.Errorf("big unannotated literal should be i64 (assignable to i64), got: %v", err)
	}
	// ...but i32-context assignment is now an error (no implicit narrowing).
	err := checkSource(t, `function main(): i32 { var x = 2147483648; var y: i32 = x; return y; }`)
	if err == nil || !strings.Contains(err.Error(), "i64") {
		t.Errorf("big unannotated literal in i32 slot should error with i64 mention, got: %v", err)
	}
	// A literal that fits i32 still defaults to i32 (unchanged) — assigns fine.
	if err := checkSource(t, `function main(): i32 { var x = 5; var y: i32 = x; return y; }`); err != nil {
		t.Errorf("small unannotated literal should stay i32, got: %v", err)
	}
	// A negative literal past i32 min also widens to i64.
	if err := checkSource(t, `function main(): i32 { var x = -5000000000; var y: i64 = x; return (y / 1000000000) as i32; }`); err != nil {
		t.Errorf("big negative unannotated literal should be i64, got: %v", err)
	}
}

// The wasm reactor builtins (wasm_timer_pollable / wasm_block) are
// registered as FuncSigs and type-check: wasm_timer_pollable takes an
// i64 duration and returns an i32 pollable handle; wasm_block takes a
// pollable and returns i32. (Runtime is wasm-only — Preview-2
// pollables — but the signatures are checked on every backend.)
func TestWasmReactorBuiltinSigs(t *testing.T) {
	ok := `function main(): i32 {
    var p: i32 = wasm_timer_pollable(1000000);
    return wasm_block(p);
}`
	if err := checkSource(t, ok); err != nil {
		t.Errorf("wasm reactor builtins should type-check, got: %v", err)
	}
}

// A generic enum whose type parameter is determined by a payload at a
// non-leading position — in particular a function-typed payload whose
// result is the type parameter — must infer that parameter from the
// payload that actually pins it, not by positionally pairing the
// type-arg slot with the leading constructor argument. Regression for
// the variant post-settle refresh, which previously mis-bound `T` to
// the first (i32-literal) argument and reported `Box[i32]` where
// `Box[string]` was expected. See docs/GENERIC-VARIANT-FN-PAYLOAD-INFERENCE-GAP.md.
func TestVariantTypeParamFromFnPayload(t *testing.T) {
	ok := []string{
		// T comes from the second, function-typed payload; the leading
		// i32 literal must not capture T.
		`enum Box[T] { Mk(i32, (i32) => T), Nil }
function f(): Box[string] { return Box.Mk(7, (x: i32) => "hi"); }
function main(): i32 { match (f()) { Mk(n, g) => { return n; }, Nil => { return 0; }, } }`,
		// Same shape, T = i32 from the function result (the originally
		// "accidentally working" case must keep working).
		`enum Box[T] { Mk(i32, (i32) => T), Nil }
function f(): Box[i32] { return Box.Mk(1, (x: i32) => x); }
function main(): i32 { match (f()) { Mk(n, g) => { return n; }, Nil => { return 0; }, } }`,
		// Leading literal that itself needs widening from the
		// destination, alongside a function payload that pins T — both
		// refreshes must hold simultaneously.
		`enum Box2[T] { Mk(i64, (i32) => T), Nil }
function f(): Box2[string] { return Box2.Mk(1234567890123, (x: i32) => "hi"); }
function main(): i32 { match (f()) { Mk(n, g) => { return 0; }, Nil => { return 0; }, } }`,
	}
	for _, src := range ok {
		if err := checkSource(t, src); err != nil {
			t.Errorf("variant fn-payload inference should type-check, got: %v\nsrc: %s", err, src)
		}
	}
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

// A bare variant struct literal in a struct-field initializer should
// implicitly widen to its union type, the same way it already does in
// `var x: Union = Variant{...}`, returns, and call arguments. Before
// the fix this reported `field "left": expected Tree, got Leaf`.
func TestUnionWidenInStructField(t *testing.T) {
	src := `struct Leaf { v: i32 }
struct Node { left: Tree, right: Tree }
type Tree = Leaf | Node;
function main(): i32 {
    var t: Tree = Node { left: Leaf { v: 40 }, right: Leaf { v: 2 } };
    match (t) {
        Leaf(l) => { return l.v; },
        Node(n) => { return 0; }
    }
}`
	if err := checkSource(t, src); err != nil {
		t.Errorf("unexpected error: %v", err)
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

// Composite `==` / `!=` must be structural equality via the type's
// `Eq` impl, not silent heap-pointer identity. A struct/enum that does
// not implement `Eq` is rejected (with a derive hint); arrays/slices/
// tuples are rejected (no structural eq yet); a type that does
// implement `Eq` type-checks (and desugars to `a.eq(b)`).
// Error-converting `?`: a `Result[_, E]` propagated through a function
// returning `Result[_, dyn Trait]` is accepted when E implements Trait
// (boxing E into `dyn Trait`), and rejected otherwise. See #3234.
func TestTryConvertErrToDyn(t *testing.T) {
	const hdr = `trait Error { function message(self: Self): string; }
struct NotFound { what: string }
impl Error for NotFound { function message(self: Self): string { return self.what; } }
function find(): Result[i32, NotFound] { return Err(NotFound { what: "x" }); }
`
	// Accepted: NotFound implements Error.
	if err := checkSource(t, hdr+`function h(): Result[i32, dyn Error] {
  var v: i32 = find()?;
  return Ok(v);
}`); err != nil {
		t.Errorf("error-converting `?` (NotFound→dyn Error) should type-check, got: %v", err)
	}
	// Rejected: Plain does not implement Error.
	err := checkSource(t, `trait Error { function message(self: Self): string; }
struct Plain { x: i32 }
function find(): Result[i32, Plain] { return Err(Plain { x: 1 }); }
function h(): Result[i32, dyn Error] { var v: i32 = find()?; return Ok(v); }
function main(): i32 { return 0; }`)
	if err == nil || !strings.Contains(err.Error(), "error types must match") {
		t.Errorf("`?` with a non-implementing error type should be rejected, got: %v", err)
	}
	// Accepted (multi-trait dyn error): Both implements EVERY trait in
	// `dyn A + B`, so the error converts via the impl-all gate.
	if err := checkSource(t, `trait A { function a(self: Self): i32; }
trait B { function b(self: Self): i32; }
struct Both { n: i32 }
impl A for Both { function a(self: Self): i32 { return self.n; } }
impl B for Both { function b(self: Self): i32 { return 0; } }
function find(): Result[i32, Both] { return Err(Both { n: 1 }); }
function h(): Result[i32, dyn A + B] { var v: i32 = find()?; return Ok(v); }
function main(): i32 { return 0; }`); err != nil {
		t.Errorf("error-converting `?` into a multi-trait `dyn A + B` should type-check, got: %v", err)
	}
	// Rejected (multi-trait, missing one): OnlyA implements A but not B, so it
	// does not convert into `dyn A + B`.
	err = checkSource(t, `trait A { function a(self: Self): i32; }
trait B { function b(self: Self): i32; }
struct OnlyA { n: i32 }
impl A for OnlyA { function a(self: Self): i32 { return self.n; } }
function find(): Result[i32, OnlyA] { return Err(OnlyA { n: 1 }); }
function h(): Result[i32, dyn A + B] { var v: i32 = find()?; return Ok(v); }
function main(): i32 { return 0; }`)
	if err == nil || !strings.Contains(err.Error(), "error types must match") {
		t.Errorf("`?` into `dyn A + B` with an error implementing only A should be rejected, got: %v", err)
	}
}

// From-based error-converting `?`: a `Result[_, E1]` propagated through a
// function returning `Result[_, E2]` is accepted when E2 has an associated
// `from(E1): E2` (`impl From[E1] for E2`), mapping `Err(e)` to
// `Err(E2.from(e))`. Rejected when no such `from` (and no `dyn`) exists.
// See #2674.
func TestTryConvertErrViaFrom(t *testing.T) {
	const hdr = `trait From[T] { function from(v: T): Self; }
struct IoErr { code: i32 }
struct AppErr { msg: i32 }
impl From[IoErr] for AppErr { function from(e: IoErr): Self { return AppErr { msg: e.code }; } }
function read(): Result[i32, IoErr] { return Err(IoErr { code: 1 }); }
`
	if err := checkSource(t, hdr+`function run(): Result[i32, AppErr] {
  var v: i32 = read()?;
  return Ok(v);
}`); err != nil {
		t.Errorf("From-based error-converting `?` should type-check, got: %v", err)
	}
	// No `from(IoErr)` on the target and no dyn → rejected.
	err := checkSource(t, `struct IoErr { code: i32 }
struct AppErr { msg: i32 }
function read(): Result[i32, IoErr] { return Err(IoErr { code: 1 }); }
function run(): Result[i32, AppErr] { var v: i32 = read()?; return Ok(v); }
function main(): i32 { return 0; }`)
	if err == nil || !strings.Contains(err.Error(), "error types must match") {
		t.Errorf("`?` with no conversion should be rejected, got: %v", err)
	}
}

// Arithmetic operators `+ - * /` on a composite type desugar to its
// conventionally-named method (add/sub/mul/div); a composite without the
// method is rejected with a clear E009. See #2706.
func TestCompositeArithmeticOverload(t *testing.T) {
	const ops = `struct V { x: i32 }
function (self: V) add(o: V): V { return V { x: self.x + o.x }; }
function (self: V) sub(o: V): V { return V { x: self.x - o.x }; }
function (self: V) mul(o: V): V { return V { x: self.x * o.x }; }
function (self: V) div(o: V): V { return V { x: self.x / o.x }; }
`
	if err := checkSource(t, ops+`function main(): i32 {
  var a: V = V { x: 6 };
  var b: V = V { x: 7 };
  var r: V = ((a + b) - a) * b;
  return (r / b).x;
}`); err != nil {
		t.Errorf("composite arithmetic with add/sub/mul/div should type-check, got: %v", err)
	}
	// The remaining binary operators overload too: `%`→rem, `&`→bitand,
	// `|`→bitor, `^`→bitxor, `<<`→shl, `>>`→shr. See #2706.
	const bitOps = `struct F { b: i32 }
function (self: F) rem(o: F): F { return F { b: self.b % o.b }; }
function (self: F) bitand(o: F): F { return F { b: self.b & o.b }; }
function (self: F) bitor(o: F): F { return F { b: self.b | o.b }; }
function (self: F) bitxor(o: F): F { return F { b: self.b ^ o.b }; }
function (self: F) shl(o: F): F { return F { b: self.b << o.b }; }
function (self: F) shr(o: F): F { return F { b: self.b >> o.b }; }
`
	if err := checkSource(t, bitOps+`function main(): i32 {
  var a: F = F { b: 12 };
  var b: F = F { b: 10 };
  var r: F = ((((a & b) | a) ^ b) << F{b:1}) >> F{b:1};
  return (r % F{b:7}).b;
}`); err != nil {
		t.Errorf("composite %% & | ^ << >> should type-check, got: %v", err)
	}
	// A composite without the operator method is rejected.
	err := checkSource(t, `struct W { x: i32 }
function main(): i32 { var a: W = W{x:1}; var b: W = W{x:2}; var c: W = a + b; return c.x; }`)
	if err == nil || !strings.Contains(err.Error(), `operator "+" is not defined for W`) {
		t.Errorf("struct without `add` should be rejected with an operator-overload hint, got: %v", err)
	}
	// Numeric arithmetic is unaffected (the overload path only fires for
	// struct/enum operands).
	if err := checkSource(t, `function main(): i32 { return 2 + 3 * 4 - 1; }`); err != nil {
		t.Errorf("numeric arithmetic should still type-check, got: %v", err)
	}
	// Unary `-` on a composite routes to `neg`; `ops` has no `neg`, so it
	// is rejected; a type WITH `neg` type-checks; numeric unary minus is
	// unaffected. See #2706.
	if err := checkSource(t, ops+`function main(): i32 { var a: V = V{x:5}; var b: V = -a; return b.x; }`); err == nil ||
		!strings.Contains(err.Error(), "unary `-` is not defined for V") {
		t.Errorf("unary `-` on a struct without `neg` should be rejected with a hint, got: %v", err)
	}
	if err := checkSource(t, `struct V { x: i32 }
function (self: V) neg(): V { return V { x: 0 - self.x }; }
function main(): i32 { var a: V = V{x:5}; var b: V = -a; return b.x; }`); err != nil {
		t.Errorf("unary `-` with a `neg` method should type-check, got: %v", err)
	}
	if err := checkSource(t, `function main(): i32 { var x: i32 = 7; return -x; }`); err != nil {
		t.Errorf("numeric unary minus should still type-check, got: %v", err)
	}
}

// Operator overloading over a trait-bounded TYPE PARAMETER: `a + b` / `-a` where
// `a`/`b` have type `T` and `T`'s bound provides the op's trait method desugars
// to `a.add(b)` / `a.neg()` resolved via the bound — the #2706 generic-numeric
// payoff. A type param whose bound lacks the method falls through to the usual
// E009.
func TestOperatorOverloadOverTypeParam(t *testing.T) {
	const traits = `trait Add { function add(self: Self, o: Self): Self; }
trait Mul { function mul(self: Self, o: Self): Self; }
trait Neg { function neg(self: Self): Self; }
trait Arith: Add + Mul + Neg {}
`
	// Binary `+` / `*` (including nested) over a type param bound by the traits.
	if err := checkSource(t, traits+`function poly[T: Arith](a: T, b: T): T { return (a + b) * a; }
function main(): i32 { return 0; }`); err != nil {
		t.Errorf("binary operators over a trait-bounded type param should type-check, got: %v", err)
	}
	// Unary `-` over a `Neg`-bounded type param.
	if err := checkSource(t, traits+`function negate[T: Neg](a: T): T { return -a; }
function main(): i32 { return 0; }`); err != nil {
		t.Errorf("unary `-` over a Neg-bounded type param should type-check, got: %v", err)
	}
	// A direct supertrait bound (`T: Add`) is enough for `+`.
	if err := checkSource(t, traits+`function plus[T: Add](a: T, b: T): T { return a + b; }
function main(): i32 { return 0; }`); err != nil {
		t.Errorf("`+` over an Add-bounded type param should type-check, got: %v", err)
	}
	// A type param whose bound does NOT provide the op's method is rejected
	// (falls through to the numeric path's E009).
	err := checkSource(t, `trait Show { function show(self: Self): i32; }
function bad[T: Show](a: T, b: T): T { return a + b; }
function main(): i32 { return 0; }`)
	if err == nil || !strings.Contains(err.Error(), `operator "+" requires an integer type`) {
		t.Errorf("`+` over a type param without an Add bound should be rejected (E009), got: %v", err)
	}
}

func TestCompositeEqualityRoutesToEq(t *testing.T) {
	const eqI32 = `trait Eq { function eq(self: Self, other: Self): boolean; }
impl Eq for i32 { function eq(self: Self, other: Self): boolean { return self == other; } }
`
	reject := []struct {
		src  string
		want string
	}{
		{`struct P { x: i32 }
function main(): i32 { var a: P = P{x:1}; var b: P = P{x:1}; if (a == b) { return 1; } return 0; }`,
			"does not implement `Eq`"},
		{`function main(): i32 { var a: i32[] = [1,2]; var b: i32[] = [1,2]; if (a == b) { return 1; } return 0; }`,
			"structural equality for arrays"},
	}
	for _, c := range reject {
		err := checkSource(t, c.src)
		if err == nil {
			t.Errorf("%q: expected rejection, got nil", c.src)
			continue
		}
		if !strings.Contains(err.Error(), c.want) {
			t.Errorf("%q: error %q does not contain %q", c.src, err.Error(), c.want)
		}
	}
	// Ordering operators on composites route to `Ord::cmp`; without
	// an Ord impl they're rejected, arrays/tuples are rejected, and a
	// type with Ord type-checks.
	const ordI32 = `trait Ord { function cmp(self: Self, other: Self): i32; }
impl Ord for i32 { function cmp(self: Self, other: Self): i32 { if (self < other) { return 0 - 1; } if (self > other) { return 1; } return 0; } }
`
	ordReject := []struct {
		src  string
		want string
	}{
		{`struct P { x: i32 }
function main(): i32 { var a: P = P{x:1}; var b: P = P{x:2}; if (a < b) { return 1; } return 0; }`,
			"does not implement `Ord`"},
	}
	for _, c := range ordReject {
		err := checkSource(t, c.src)
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%q: want error containing %q, got %v", c.src, c.want, err)
		}
	}
	if err := checkSource(t, ordI32+`@derive(Ord) struct P { x: i32 }
function main(): i32 { var a: P = P{x:1}; var b: P = P{x:2}; if (a < b) { if (b >= a) { return 0; } } return 1; }`); err != nil {
		t.Errorf("composite ordering with derived Ord should type-check, got: %v", err)
	}

	// With Eq derived, composite == type-checks cleanly.
	ok := eqI32 + `@derive(Eq) struct P { x: i32, y: i32 }
@derive(Eq) enum E { A, B(i32) }
function main(): i32 {
    var a: P = P{x:1, y:2}; var b: P = P{x:1, y:2};
    if (a == b) { if (A == A) { if (B(1) != B(2)) { return 0; } } }
    return 1;
}`
	if err := checkSource(t, ok); err != nil {
		t.Errorf("composite == with derived Eq should type-check, got: %v", err)
	}
}

// Methods declared on a generic struct / enum receiver bind the
// receiver's type variables implicitly (`T` in `Box[T]`), so they
// type-check and dispatch per instantiation. A receiver type-arg that
// names a real type stays concrete.
func TestGenericReceiverMethods(t *testing.T) {
	ok := []string{
		`struct Box[T] { v: T }
function (b: Box[T]) get(): T { return b.v; }
function main(): i32 { var b: Box[i32] = Box { v: 7 }; return b.get(); }`,
		`struct Pair[A, B] { fst: A, snd: B }
function (p: Pair[A, B]) first(): A { return p.fst; }
function main(): i32 { var p: Pair[i32, i32] = Pair { fst: 3, snd: 4 }; return p.first(); }`,
		`enum Opt[T] { Nil, Has(T) }
function (o: Opt[T]) unwrap_or(d: T): T { match (o) { Has(x) => { return x; }, Nil => { return d; } } }
function main(): i32 { var o: Opt[i32] = Has(9); return o.unwrap_or(0); }`,
		// A receiver type-arg naming a real struct is a concrete
		// instantiation, not a type variable.
		`struct Foo { v: i32 }
struct Box[T] { v: T }
function (b: Box[Foo]) deep(): i32 { return b.v.v; }
function main(): i32 { var b: Box[Foo] = Box { v: Foo { v: 5 } }; return b.deep(); }`,
		// Element-polymorphic receivers: owned array `T[]` and slice `[T]`.
		`function (xs: T[]) first(): T { return xs[0]; }
function main(): i32 { var a: i32[] = [3, 4]; return a.first(); }`,
		`function (xs: [T]) head(): T { return xs[0]; }
function main(): i32 { var a: i32[] = [7, 8]; var s: [i32] = a[0:2]; return s.head(); }`,
		`function (xs: T[]) count_where(p: (T) => boolean): i32 {
    var n: i32 = 0; var i: i32 = 0;
    while (i < xs.len()) { if (p(xs[i])) { n = n + 1; } i = i + 1; } return n; }
function pos(x: i32): boolean { return x > 0; }
function main(): i32 { var a: i32[] = [1, 0 - 1, 2]; return a.count_where(pos); }`,
		// Method-level type param: `map[U]` introduces U (inferred from
		// the argument) alongside the receiver's T.
		`struct Box[T] { v: T }
function (b: Box[T]) map[U](f: (T) => U): Box[U] { return Box { v: f(b.v) }; }
function (b: Box[T]) get(): T { return b.v; }
function big(x: i32): boolean { return x > 3; }
function main(): i32 { var b: Box[i32] = Box { v: 7 }; var c: Box[boolean] = b.map(big); if (c.get()) { return 0; } return 1; }`,
	}
	for _, src := range ok {
		if err := checkSource(t, src); err != nil {
			t.Errorf("generic-receiver method should type-check, got: %v\nsrc: %s", err, src)
		}
	}
}

// Return-position type inference (#2668): a generic function whose type
// parameter appears only in its result type (not in any argument) can
// have that parameter inferred from the destination — the `var x: T =`
// annotation or the enclosing `return`'s declared type. Without a
// destination there's nothing to infer from, so it must still error.
func TestReturnPositionInference(t *testing.T) {
	ok := []string{
		// T bound from a `var` annotation.
		`function empty[T](): T[] { return []; }
function main(): i32 { var xs: i32[] = empty(); return xs.len(); }`,
		// T bound through a `return` whose function result is concrete.
		`function empty[T](): T[] { return []; }
function strs(): string[] { return empty(); }
function main(): i32 { return strs().len(); }`,
		// Argument-driven binding still wins when both are present; the
		// destination is merely consulted for *unbound* parameters.
		`function wrap[T](x: T): T[] { return [x]; }
function main(): i32 { var xs: i32[] = wrap(5); return xs.len(); }`,
		// Generic struct result inferred from the destination.
		`struct Box[T] { v: T }
function make_box[T](v: T): Box[T] { return Box { v: v }; }
function main(): i32 { var b: Box[i32] = make_box(3); return b.v; }`,
	}
	for _, src := range ok {
		if err := checkSource(t, src); err != nil {
			t.Errorf("return-position inference should type-check, got: %v\nsrc: %s", err, src)
		}
	}

	// No destination to infer from → the unbound parameter must still be
	// reported as un-inferable.
	bad := `function empty[T](): T[] { return []; }
function main(): i32 { empty(); return 0; }`
	err := checkSource(t, bad)
	if err == nil {
		t.Fatal("expected E040 for un-inferable return-only type parameter, got nil")
	}
	if !strings.Contains(err.Error(), "could not infer type parameter") {
		t.Errorf("error %q does not mention the inference failure", err.Error())
	}
}

// Phase 5 of docs/PRELUDE-TO-MODULES.md retired the auto-injected
// prelude: stdlib methods are no longer in scope unless their module
// is imported. A program that calls `.split` without `import
// "std/string";` should get a clean type error rather than silently
// resolving against a magic prelude.
// TestUseWithoutAnnotationDoesNotPanicFormatting guards the regression
// where a `use x <- f()` with no binding annotation, whose callback
// parameter type the checker couldn't infer (inferUseParam bails with
// E032), left the synthesised callback's first param nil. Formatting
// that callback's function type for the follow-on E038 diagnostic then
// dereferenced nil, surfacing as `got %!s(PANIC=...)` instead of a
// readable type. The diagnostic must mention the `use` failure and never
// contain a PANIC marker.
func TestUseWithoutAnnotationDoesNotPanicFormatting(t *testing.T) {
	src := `function withResource(cb: () => i32): i32 { return cb(); }
function g(): i32 { use x <- withResource(); return 0; }
function main(): i32 { return g(); }`
	err := checkSource(t, src)
	if err == nil {
		t.Fatal("expected a `use` inference error, got nil")
	}
	if strings.Contains(err.Error(), "PANIC") {
		t.Errorf("diagnostic formatting panicked: %q", err.Error())
	}
	if !strings.Contains(err.Error(), "use:") {
		t.Errorf("error %q does not look like the expected E032 use diagnostic", err.Error())
	}
}

func TestUnimportedStdlibMethodIsRejected(t *testing.T) {
	err := checkSource(t, `function main(): i32 {
    var xs: string[] = "a,b,c".split(",");
    return len(xs);
}`)
	if err == nil {
		t.Fatal("expected a type error for .split() without import \"std/string\", got nil")
	}
	if !strings.Contains(err.Error(), "non-struct value of type string") {
		t.Errorf("error %q does not look like the expected unresolved-method diagnostic", err.Error())
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

// User code can't redeclare a built-in type. Letting it through
// silently miscompiles — the IR's pair-form lowering and several
// runtime helpers hard-code the canonical variant order
// (Some=0/None=1, Ok=0/Err=1), so a swapped user `enum Option {
// None, Some(T) }` would diverge between pair-form and heap-form
// returns.
func TestReservedBuiltinNamesCannotBeShadowed(t *testing.T) {
	cases := []struct {
		name string
		src  string
	}{
		{"Option", `enum Option[T] { None, Some(T) }
			function main(): i32 { return 0; }`},
		{"Result", `enum Result[T, E] { Err(E), Ok(T) }
			function main(): i32 { return 0; }`},
		{"IoError", `enum IoError { Whatever }
			function main(): i32 { return 0; }`},
		{"JsonValue", `enum JsonValue { JNull }
			function main(): i32 { return 0; }`},
		{"Reader", `struct Reader { fd: i32 }
			function main(): i32 { return 0; }`},
		{"Writer", `struct Writer { fd: i32 }
			function main(): i32 { return 0; }`},
		{"HttpRequest", `struct HttpRequest { method: string, path: string, body: string }
			function main(): i32 { return 0; }`},
		{"HttpResponse", `struct HttpResponse { status: i32, body: string }
			function main(): i32 { return 0; }`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := checkSource(t, c.src)
			if err == nil {
				t.Fatalf("expected reserved-name error for %s", c.name)
			}
			if !strings.Contains(err.Error(), "reserved built-in name") {
				t.Errorf("error %q does not mention reserved built-in", err.Error())
			}
			if !strings.Contains(err.Error(), c.name) {
				t.Errorf("error %q does not name %q", err.Error(), c.name)
			}
			// IsReservedName is the public-facing query the
			// rest of the codebase consults; it must agree
			// with what the checker actually rejects. Pinning
			// them together here keeps the two paths from
			// drifting.
			if !IsReservedName(c.name) {
				t.Errorf("IsReservedName(%q) returned false but the checker rejects it", c.name)
			}
		})
	}
	// Also assert the helper returns false for things that
	// AREN'T reserved — a sanity check that the helper isn't
	// just `return true`.
	for _, name := range []string{"Foo", "Bar", "main", "x"} {
		if IsReservedName(name) {
			t.Errorf("IsReservedName(%q) returned true for a user-name", name)
		}
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

func TestBuiltinUdpSend(t *testing.T) {
	if err := checkSource(t, `function f(): i32 { return udp_send("127.0.0.1", 8125, "metric:1|c"); }`); err != nil {
		t.Errorf("udp_send(string, number, string) should type-check: %v", err)
	}
	// Wrong arg types are rejected: host must be a string, port a number.
	if err := checkSource(t, `function f(): i32 { return udp_send(8125, 8125, "x"); }`); err == nil {
		t.Errorf("udp_send with a non-string host should fail")
	}
	if err := checkSource(t, `function f(): i32 { return udp_send("h", "p", "x"); }`); err == nil {
		t.Errorf("udp_send with a non-number port should fail")
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
	src := `function f(): i32 { return ("hello").len(); }`
	if err := checkSource(t, src); err != nil {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestLenRejectsNumber(t *testing.T) {
	if err := checkSource(t, `function f(): i32 { return (42).len(); }`); err == nil {
		t.Error("expected error len(i32)")
	}
}

// `.len()` resolves through method dispatch on string, array,
// and slice receivers. Verifies the generic-T substitution the
// dispatch path stamps for slice tracks the receiver's element
// type without surfacing a `ParamType` leak in the error path.
func TestLenOnSlice(t *testing.T) {
	src := `function f(): i32 {
		var xs: i32[] = [1, 2, 3, 4];
		var s: [i32] = xs[1:3];
		return s.len();
	}`
	if err := checkSource(t, src); err != nil {
		t.Errorf("unexpected error: %v", err)
	}
}

// `len` is no longer a free builtin — only the method form
// `x.len()` resolves. Calling it as a free function must surface
// the same "undefined identifier" diagnostic any unknown name
// would.
func TestFreeLenIsRejected(t *testing.T) {
	cases := []string{
		`function f(): i32 { var s: string = "hi"; return len(s); }`,
		`function f(): i32 { var xs: i32[] = [1, 2]; return len(xs); }`,
	}
	for _, src := range cases {
		err := checkSource(t, src)
		if err == nil {
			t.Errorf("%s: expected error, got none", src)
			continue
		}
		if !strings.Contains(err.Error(), "undefined identifier \"len\"") {
			t.Errorf("%s: expected `undefined identifier \"len\"` diagnostic, got %v", src, err)
		}
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
	if ce.Fix == nil {
		t.Fatal("expected a machine-applicable fix")
	}
	if ce.Fix.Replacement != "counter" {
		t.Errorf("fix replacement = %q, want %q", ce.Fix.Replacement, "counter")
	}
	if ce.Fix.Title != "replace `countr` with `counter`" {
		t.Errorf("fix title = %q", ce.Fix.Title)
	}
	if ce.Fix.Length != len("countr") || ce.Fix.Pos != ce.Pos {
		t.Errorf("fix span = (%v, %d), want the error's own (%v, %d)", ce.Fix.Pos, ce.Fix.Length, ce.Pos, len("countr"))
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
	if ce.Fix != nil {
		t.Errorf("expected no fix, got %+v", ce.Fix)
	}
}

// E043 unknown-field joins the sound fix family (#4413 Rec §3): both
// the field-ACCESS and struct-LITERAL sites attach a respelling fix
// anchored at the field NAME token, and applying it fixes the program.
func TestUnknownFieldFixApplies(t *testing.T) {
	cases := []struct {
		name string
		src  string
	}{
		{"access", "struct P { count: i32 }\nfunction main(): i32 {\n\tvar p = P { count: 1 };\n\treturn p.countr;\n}"},
		{"literal", "struct P { count: i32 }\nfunction main(): i32 {\n\tvar p = P { countr: 1 };\n\treturn 0;\n}"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			prog, err := parser.Parse(tc.src)
			if err != nil {
				t.Fatal(err)
			}
			_, err = Check(prog)
			es, ok := err.(diag.Errors)
			if !ok || len(es) == 0 {
				t.Fatalf("expected diag.Errors, got %v", err)
			}
			var fix *diag.Suggestion
			for _, e := range es {
				if ce, ok := e.(*Error); ok && ce.Fix != nil {
					fix = ce.Fix
				}
			}
			if fix == nil {
				t.Fatalf("no error carried a fix: %v", es)
			}
			if fix.Replacement != "count" {
				t.Errorf("replacement = %q, want count", fix.Replacement)
			}
			lines := strings.Split(tc.src, "\n")
			ln := lines[fix.Pos.Line-1]
			col := fix.Pos.Col - 1
			if got := ln[col : col+fix.Length]; got != "countr" {
				t.Fatalf("fix span covers %q, want the misspelt field name", got)
			}
			lines[fix.Pos.Line-1] = ln[:col] + fix.Replacement + ln[col+fix.Length:]
			applied := strings.Join(lines, "\n")
			prog2, err := parser.Parse(applied)
			if err != nil {
				t.Fatalf("applied fix does not re-parse: %v\n%s", err, applied)
			}
			if _, err := Check(prog2); err != nil {
				t.Fatalf("applied fix does not check: %v\n%s", err, applied)
			}
		})
	}
}

// A misspelt METHOD call routes through the unknown-field path; the
// receiver's registered method names join the candidates, so
// `p.puzh(2)` suggests the `push` method (the field set alone offers
// nothing that close). Applying it fixes the program.
func TestUnknownMethodFixSuggestsMethod(t *testing.T) {
	src := "struct P { x: i32 }\n" +
		"pub function (p: P) push(v: i32): i32 { return p.x + v; }\n" +
		"function main(): i32 {\n" +
		"\tvar p = P { x: 1 };\n" +
		"\treturn p.puzh(2);\n" +
		"}"
	prog, err := parser.Parse(src)
	if err != nil {
		t.Fatal(err)
	}
	_, err = Check(prog)
	es, ok := err.(diag.Errors)
	if !ok || len(es) == 0 {
		t.Fatalf("expected diag.Errors, got %v", err)
	}
	var fix *diag.Suggestion
	for _, e := range es {
		if ce, ok := e.(*Error); ok && ce.Fix != nil {
			fix = ce.Fix
		}
	}
	if fix == nil {
		t.Fatalf("no error carried a fix: %v", es)
	}
	if fix.Replacement != "push" {
		t.Errorf("replacement = %q, want push", fix.Replacement)
	}
	lines := strings.Split(src, "\n")
	ln := lines[fix.Pos.Line-1]
	col := fix.Pos.Col - 1
	lines[fix.Pos.Line-1] = ln[:col] + fix.Replacement + ln[col+fix.Length:]
	applied := strings.Join(lines, "\n")
	prog2, err := parser.Parse(applied)
	if err != nil {
		t.Fatalf("applied fix does not re-parse: %v\n%s", err, applied)
	}
	if _, err := Check(prog2); err != nil {
		t.Fatalf("applied fix does not check: %v\n%s", err, applied)
	}
}

// The E001 near-miss fix is MACHINE-APPLICABLE (#4413 Rec §3): applying
// the suggested replacement over its span must yield a program that
// parses AND checks cleanly — the soundness bar for attaching one.
func TestUndefinedIdentifierFixApplies(t *testing.T) {
	src := `function f(): i32 {
	var counter: i32 = 0;
	return countr;
}`
	prog, err := parser.Parse(src)
	if err != nil {
		t.Fatal(err)
	}
	_, err = Check(prog)
	es, ok := err.(diag.Errors)
	if !ok || len(es) == 0 {
		t.Fatalf("expected diag.Errors, got %v", err)
	}
	ce := es[0].(*Error)
	if ce.Fix == nil {
		t.Fatal("expected a fix")
	}
	// Apply: replace Length bytes at (Line, Col) with Replacement.
	lines := strings.Split(src, "\n")
	ln := lines[ce.Fix.Pos.Line-1]
	col := ce.Fix.Pos.Col - 1
	fixed := ln[:col] + ce.Fix.Replacement + ln[col+ce.Fix.Length:]
	lines[ce.Fix.Pos.Line-1] = fixed
	applied := strings.Join(lines, "\n")
	prog2, err := parser.Parse(applied)
	if err != nil {
		t.Fatalf("applied fix does not re-parse: %v\n%s", err, applied)
	}
	if _, err := Check(prog2); err != nil {
		t.Fatalf("applied fix does not check: %v\n%s", err, applied)
	}
}

func TestStructTypechecks(t *testing.T) {
	src := `struct Point { x: i32, y: i32 }
		function main(): i32 {
			var p: Point = Point { x: 1, y: 2 };
			p = Point { ...p, x: 10 };
			return p.x + p.y;
		}`
	if err := checkSource(t, src); err != nil {
		t.Errorf("unexpected error: %v", err)
	}
}

// Fields are immutable after construction (E048): an `obj.field = v`
// assignment is rejected. This is the enforcement half of the
// immutable-data-structures migration — the fix is a struct-update
// (`p = Point { ...p, x: 10 }`), which TestStructTypechecks above
// shows still type-checks.
func TestFieldAssignmentRejected(t *testing.T) {
	src := `struct Point { x: i32, y: i32 }
		function main(): i32 {
			var p: Point = Point { x: 1, y: 2 };
			p.x = 10;
			return p.x;
		}`
	err := checkSource(t, src)
	if err == nil {
		t.Fatal("expected a field-immutability error, got no error")
	}
	if !strings.Contains(err.Error(), "fields are immutable after construction") {
		t.Errorf("expected E048 field-immutability error, got: %v", err)
	}
}

// Compound field assignment (`a.v += n`) desugars to `a.v = a.v + n`,
// so it is rejected the same way as a plain field assignment.
func TestCompoundFieldAssignmentRejected(t *testing.T) {
	src := `struct Acc { v: i32 }
		function main(): i32 {
			var a: Acc = Acc { v: 7 };
			a.v += 35;
			return a.v;
		}`
	if err := checkSource(t, src); err == nil || !strings.Contains(err.Error(), "fields are immutable after construction") {
		t.Errorf("expected E048 for compound field assignment, got: %v", err)
	}
}

// A nested field path (`o.inner.x = v`) is also a FieldAccess target
// and is rejected; the fix nests a struct-update
// (`o = Outer { ...o, inner: Inner { ...o.inner, x: v } }`).
func TestNestedFieldAssignmentRejected(t *testing.T) {
	src := `struct Inner { x: i32 }
		struct Outer { inner: Inner }
		function main(): i32 {
			var o: Outer = Outer { inner: Inner { x: 0 } };
			o.inner.x = 42;
			return o.inner.x;
		}`
	if err := checkSource(t, src); err == nil || !strings.Contains(err.Error(), "fields are immutable after construction") {
		t.Errorf("expected E048 for nested field assignment, got: %v", err)
	}
}

// Capture write-back of a REFERENCE-shaped variable is rejected
// (E049): a closure whose env holds a pointer could close a cycle by
// pointing back at a value that points at the closure. This is the
// capture half of immutability enforcement (E048 is the field half).
// Scalar captures stay legal (see TestScalarCaptureWriteBackAllowed).
func TestPointerCaptureWriteBackRejected(t *testing.T) {
	src := `function main(): i32 {
		var name: string = "a";
		var f = function (): i32 {
			name = "b";
			return name.len();
		};
		return f();
	}`
	if err := checkSource(t, src); err == nil || !strings.Contains(err.Error(), "reference-typed closure capture") {
		t.Errorf("expected E049 for pointer capture write-back, got: %v", err)
	}
}

// A nested closure writing a reference-typed variable captured from
// two levels up is still rejected.
func TestNestedPointerCaptureWriteBackRejected(t *testing.T) {
	src := `function main(): i32 {
		var acc: i32[] = [];
		var outer = function (): i32 {
			var inner = function (): i32 {
				acc = acc.append(1);
				return acc.len();
			};
			return inner();
		};
		return outer();
	}`
	if err := checkSource(t, src); err == nil || !strings.Contains(err.Error(), "reference-typed closure capture") {
		t.Errorf("expected E049 for nested pointer capture write-back, got: %v", err)
	}
}

// Scalar capture write-back stays legal — an i32/i64/f32/f64 capture
// can't hold a reference, so it can't form a cycle. This is the
// stateful "counter closure": each call increments the env's count.
func TestScalarCaptureWriteBackAllowed(t *testing.T) {
	src := `function makeCounter(): () => i32 {
		var count: i32 = 0;
		function tick(): i32 {
			count = count + 1;
			return count;
		}
		return tick;
	}
	function main(): i32 {
		var c = makeCounter();
		return c() + c();
	}`
	if err := checkSource(t, src); err != nil {
		t.Errorf("scalar (counter) capture write-back should compile, got: %v", err)
	}
}

// Reading a reference capture stays legal — only write-back is banned.
// The closure's own params / locals are not captures and remain
// assignable.
func TestCaptureReadAndLocalAssignStillAllowed(t *testing.T) {
	src := `function main(): i32 {
		var n: i32 = 5;
		var f = function (x: i32): i32 {
			var local: i32 = x;
			local = local + n;
			return local;
		};
		return f(37);
	}`
	if err := checkSource(t, src); err != nil {
		t.Errorf("capture read + local assignment should compile, got: %v", err)
	}
}

// Enforcement bans only post-construction mutation, not recursive
// types: an immutable recursive tree (the self-host parser's own
// AST shape) must still construct and type-check.
func TestImmutableRecursiveTypeStillCompiles(t *testing.T) {
	src := `struct Leaf { v: i32 }
		struct Node { left: Tree, right: Tree }
		type Tree = Leaf | Node;
		function main(): i32 {
			var t: Tree = Node { left: Leaf { v: 40 }, right: Node { left: Leaf { v: 1 }, right: Leaf { v: 1 } } };
			match (t) {
				Leaf(l) => { return l.v; },
				Node(n) => { return 0; }
			}
		}`
	if err := checkSource(t, src); err != nil {
		t.Errorf("recursive immutable type should compile, got: %v", err)
	}
}

func TestLabeledBreakContinueAccepted(t *testing.T) {
	src := `function f(): i32 {
		outer: for i in 0..3 {
			inner: loop {
				if (i == 1) { break outer; }
				continue inner;
			}
		}
		return 0;
	}`
	if err := checkSource(t, src); err != nil {
		t.Errorf("unexpected error for valid labeled break/continue: %v", err)
	}
}

func TestUnknownLoopLabelRejected(t *testing.T) {
	for _, src := range []string{
		`function f(): i32 { outer: for i in 0..3 { break nope; } return 0; }`,
		`function f(): i32 { outer: while (true) { continue nope; } return 0; }`,
		`function f(): i32 { outer: loop { continue nope; } return 0; }`,
	} {
		if err := checkSource(t, src); err == nil {
			t.Errorf("expected E058 for unknown loop label in: %s", src)
		}
	}
}

// A let-else whose else branch ends in a canonical `loop { … }` counts as
// diverging (E022 accepts it) without needing an explicit trailing
// return/break/continue after the loop — the same conservative
// "unconditional loop never falls through" treatment funcBodyExits
// already gives literal-true While, now keyed off the dedicated Loop
// node instead of pattern-matching a BoolLit condition.
func TestLetElseAcceptsDivergentLoop(t *testing.T) {
	src := `enum Opt { Has(i32), Nil }
		function f(o: Opt): i32 {
			let Has(v) = o else { loop { } };
			return v;
		}`
	if err := checkSource(t, src); err != nil {
		t.Errorf("let-else with a diverging `loop` else-branch should check clean, got: %v", err)
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

// Bare `None` in an if-expression arm gets its type args
// from the other arm, so `if (cond) { Some(x) } else { None }`
// resolves as `Option[i32]` rather than failing the strict
// equality check the IfExpr handler used to apply. Same for
// the symmetric position (None in Then) and for Result.
func TestIfExprUnifiesBareEnumWithSpecifiedArm(t *testing.T) {
	cases := []string{
		// None in the Else arm.
		`function f(b: boolean): Option[i32] {
			return if (b) { Some(7) } else { None };
		}`,
		// None in the Then arm (symmetric position).
		`function f(b: boolean): Option[i32] {
			return if (b) { None } else { Some(7) };
		}`,
		// Result with bare Err in the Then arm — same shape.
		`function f(b: boolean): Result[i32, i32] {
			return if (b) { Err(0) } else { Ok(1) };
		}`,
	}
	for i, src := range cases {
		if err := checkSource(t, src); err != nil {
			t.Errorf("case %d: %v\n%s", i, err, src)
		}
	}
}

// Genuinely mismatched branches still error — the unification
// only kicks in for the "one side has no Args" case at a
// matching enum name.
func TestIfExprStillRejectsActualBranchMismatch(t *testing.T) {
	cases := []string{
		`function f(b: boolean): i32 { return if (b) { 1 } else { true }; }`,
		`function f(b: boolean): Option[i32] { return if (b) { Some(1) } else { Ok(1) }; }`,
	}
	for i, src := range cases {
		if err := checkSource(t, src); err == nil {
			t.Errorf("case %d: expected error\n%s", i, src)
		}
	}
}

// Postfix `?` produces the unwrapped Some payload type.
// Regression: a non-variant call returning Option[T] used to be
// refreshed by postSettleType's Call branch as Option[<arg type>],
// because the gate didn't distinguish variant constructors from
// regular function calls. The minimal repro is a function whose
// first param is an array type — the arg-rebuild would turn
// `Option[i32]` into e.g. `Option[boolean[]]`.
func TestNonVariantCallReturningGenericEnum(t *testing.T) {
	cases := []string{
		`function f(p: boolean[]): Option[i32] { return None; }
function main(): i32 { var v: Option[i32] = f([true]); return 0; }`,
		`function g(p: i32[], q: string): Option[i64] { return None; }
function main(): i32 { var v: Option[i64] = g([1], "x"); return 0; }`,
		`function h(p: boolean[]): Result[i32, i32] { return Ok(0); }
function main(): i32 { var v: Result[i32, i32] = h([true]); return 0; }`,
	}
	for _, src := range cases {
		if err := checkSource(t, src); err != nil {
			t.Errorf("unexpected error for non-variant Option/Result-returning call: %v\nsrc:\n%s", err, src)
		}
	}
}

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

// `arr.append(v)` is a generic method on T[]. The receiver's Elem
// flows into the registered ParamType("T") signature, so the
// argument and return types substitute correctly.
func TestArrayPushTypechecks(t *testing.T) {
	for _, src := range []string{
		`function f(): i32 { var xs: string[] = []; xs = xs.append("a"); return xs.len(); }`,
		`function f(): i32 { var xs: i32[] = [1, 2]; xs = xs.append(3); return xs[2]; }`,
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
		`function f(): i32 { var xs: i64[] = [1i64, 2i64]; xs = xs.append(3i64); return 0; }`,
		`function f(): i32 { var xs: u64[] = [1u64]; xs = xs.append(2u64); return 0; }`,
	} {
		if err := checkSource(t, src); err != nil {
			t.Errorf("%q: unexpected error %v", src, err)
		}
	}
}

func TestArrayPushF64StridePasses(t *testing.T) {
	src := `function f(): i32 { var xs: f64[] = [1.0f64]; xs = xs.append(2.0f64); return 0; }`
	if err := checkSource(t, src); err != nil {
		t.Errorf("unexpected error: %v", err)
	}
}

// Sub-i32 stride: 1-byte (u8) routes to its own lang-prelude
// append helper.
func TestArrayPushSubI32StridePasses(t *testing.T) {
	for _, src := range []string{
		`function f(): i32 { var xs: u8[] = []; xs = xs.append(7u8); return 0; }`,
	} {
		if err := checkSource(t, src); err != nil {
			t.Errorf("%q: unexpected error %v", src, err)
		}
	}
}

// Argument type must match the receiver's Elem.
func TestArrayPushRejectsArgTypeMismatch(t *testing.T) {
	src := `function f(): i32 { var xs: string[] = []; xs = xs.append(1); return xs.len(); }`
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

// A value-returning function that can fall off the end without a return
// is rejected (E052). Regression for F4 in
// docs/ADVERSARIAL-REVIEW-2026-06.md — previously this type-checked and
// crashed at runtime with a void value where a struct was expected.
func TestMissingReturnRejected(t *testing.T) {
	for _, src := range []string{
		// one-armed if: falls through when b is false
		`struct P { x: i32, y: i32 }
		function f(b: boolean): P {
			if (b) { return P { x: 10, y: 20 }; }
		}`,
		// no return at all
		`function g(): i32 { var x = 1; }`,
		// if/else where only one arm returns
		`function h(b: boolean): i32 {
			if (b) { return 1; } else { var z = 2; }
		}`,
		// match whose wildcard arm doesn't return: an unmatched tag falls through
		`function sw(n: i32): i32 {
			match (n) { 0 => { return 0; }, 1 => { return 1; }, _ => { var z = 2; } }
		}`,
	} {
		err := checkSource(t, src)
		if err == nil {
			t.Errorf("expected a missing-return error (E052) for:\n%s", src)
			continue
		}
		if !strings.Contains(err.Error(), "missing return") {
			t.Errorf("expected a missing-return error, got %v for:\n%s", err, src)
		}
	}
}

// Forms that DO return on every path (or never fall through) must NOT
// trip E052 — guarding against false positives that would reject valid
// code. Covers if/else both-return, exhaustive match, infinite loop, and
// void functions.
func TestMissingReturnAcceptsDivergentForms(t *testing.T) {
	for _, src := range []string{
		// if/else both return
		`function f(b: boolean): i32 {
			if (b) { return 1; } else { return 2; }
		}`,
		// trailing return after a one-armed if
		`function g(b: boolean): i32 {
			if (b) { return 1; }
			return 0;
		}`,
		// infinite loop never falls through
		`function loops(): i32 {
			while (true) { var x = 1; }
		}`,
		// canonical `loop` never falls through either
		`function loops2(): i32 {
			loop { var x = 1; }
		}`,
		// match with wildcard, every arm returns
		`function sw(n: i32): i32 {
			match (n) { 0 => { return 0; }, _ => { return 1; } }
		}`,
		// void function may fall through
		`function v(n: i32) { var x = n + 1; }`,
		// plain trailing return
		`function id(n: i32): i32 { return n; }`,
	} {
		if err := checkSource(t, src); err != nil {
			t.Errorf("valid function wrongly rejected: %v\n%s", err, src)
		}
	}
}

func TestStructLitWrongFieldType(t *testing.T) {
	src := `struct P { x: i32 }
		function f(): P { return P { x: true }; }`
	if err := checkSource(t, src); err == nil {
		t.Error("expected error for boolean as i32 field")
	}
}

// A struct-update literal `P { ...base, y: v }` relaxes the
// completeness rule: naming only a subset of fields is fine because
// the base supplies the rest. (A plain `P { y: v }` would be rejected
// for the missing x — see TestStructLitMissingField.)
func TestStructUpdateAllowsSubsetFields(t *testing.T) {
	src := `struct P { x: i32, y: i32 }
		function f(): P {
			var a: P = P { x: 1, y: 2 };
			return P { ...a, y: 9 };
		}`
	if err := checkSource(t, src); err != nil {
		t.Errorf("struct-update with a subset of fields should check clean, got: %v", err)
	}
}

// A pure-copy struct-update `P { ...base }` (no overrides) checks clean.
func TestStructUpdatePureCopyChecks(t *testing.T) {
	src := `struct P { x: i32, y: i32 }
		function f(): P {
			var a: P = P { x: 1, y: 2 };
			return P { ...a };
		}`
	if err := checkSource(t, src); err != nil {
		t.Errorf("pure-copy struct-update should check clean, got: %v", err)
	}
}

// The struct-update base must have the same struct type as the
// literal — a mismatched base is rejected.
func TestStructUpdateRejectsWrongBaseType(t *testing.T) {
	src := `struct P { x: i32, y: i32 }
		struct Q { z: i32 }
		function f(): P {
			var q: Q = Q { z: 1 };
			return P { ...q, y: 9 };
		}`
	if err := checkSource(t, src); err == nil {
		t.Error("expected error: struct-update base type Q does not match P")
	}
}

// An override naming a field the struct doesn't have is still
// rejected, even with a base present.
func TestStructUpdateRejectsUnknownOverrideField(t *testing.T) {
	src := `struct P { x: i32, y: i32 }
		function f(): P {
			var a: P = P { x: 1, y: 2 };
			return P { ...a, nope: 9 };
		}`
	if err := checkSource(t, src); err == nil {
		t.Error("expected error: override names a field P does not have")
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
			function inner(): i32 { return s.len(); }
			return inner();
		}`,
		// T[] (i32 array)
		`function outer(xs: i32[]): i32 {
			function inner(): i32 { return xs.len(); }
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
	// `i32` (a built-in numeric receiver) is permitted — it's how the
	// prelude declares `i32.to_string()` etc. An array receiver is
	// permitted only when it's element-polymorphic (`(xs: T[])`); a
	// CONCRETE element type is rejected, because the "Array" method
	// namespace can't distinguish element types — a `(xs: i32[]) sum()`
	// would wrongly apply to `string[]` too.
	src := `function (xs: i32[]) sum(): i32 { return 0; }`
	if err := checkSource(t, src); err == nil {
		t.Error("expected error for concrete-element array receiver")
	}
	// The element-polymorphic form is accepted (see
	// TestGenericReceiverMethods for the positive cases).
	if err := checkSource(t, `function (xs: T[]) first(): T { return xs[0]; }
function main(): i32 { var a: i32[] = [1]; return a.first(); }`); err != nil {
		t.Errorf("element-polymorphic array receiver should be accepted, got: %v", err)
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
// A named-field variant can be matched with `Rect { w, h }` (any field
// order), binding each field by name; the checker validates the names and
// reorders to declaration order. See docs/NAMED-FIELD-VARIANTS.md.
func TestNamedFieldVariantMatch(t *testing.T) {
	good := `enum Shape { Circle { r: i32 }, Rect { w: i32, h: i32 } }
function area(s: Shape): i32 {
    match (s) {
        Circle { r } => { return r * r; },
        Rect { h, w } => { return w * h; },
    }
    return 0;
}
function main(): i32 { return area(Rect(3, 4)); }`
	if err := checkSource(t, good); err != nil {
		t.Errorf("named-field match should type-check: %v", err)
	}
}

func TestNamedFieldVariantMatchErrors(t *testing.T) {
	cases := []struct {
		src  string
		want string
	}{
		// unknown field in pattern
		{`enum E { V { a: i32 } }
function f(e: E): i32 { match (e) { V { b } => { return b; }, } return 0; }
function main(): i32 { return 0; }`, "has no field"},
		// missing a field
		{`enum E { V { a: i32, b: i32 } }
function f(e: E): i32 { match (e) { V { a } => { return a; }, } return 0; }
function main(): i32 { return 0; }`, "must bind all"},
		// named pattern on a positional variant
		{`enum E { V(i32) }
function f(e: E): i32 { match (e) { V { a } => { return a; }, } return 0; }
function main(): i32 { return 0; }`, "positional payloads"},
	}
	for _, c := range cases {
		err := checkSource(t, c.src)
		if err == nil {
			t.Errorf("%q: expected error", c.src)
			continue
		}
		if !strings.Contains(err.Error(), c.want) {
			t.Errorf("error %q does not contain %q", err.Error(), c.want)
		}
	}
}

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

// String-literal match arms are accepted (they lower to an OpStrEq
// if-else-if chain); the mandatory `_` covers the open string domain (#4407).
func TestStringLiteralMatchAccepted(t *testing.T) {
	src := `function classify(s: string): i32 {
			match (s) {
				"yes" => { return 1; },
				"no" => { return 2; },
				_ => { return 0; }
			}
		}
		function main(): i32 { return classify("yes"); }`
	if err := checkSource(t, src); err != nil {
		t.Errorf("string-literal match should type-check: %v", err)
	}
}

// A string match, like any non-enum match, needs an unguarded `_` arm —
// the string domain is open, so it can never be exhausted by literals (E030).
func TestStringLiteralMatchNonExhaustiveRejected(t *testing.T) {
	src := `function main(): i32 {
			var s: string = "x";
			match (s) {
				"a" => { return 1; },
				"b" => { return 2; }
			}
			return 0;
		}`
	err := checkSource(t, src)
	if err == nil {
		t.Fatal("expected E030: string match without `_` is not exhaustive")
	}
	if !strings.Contains(err.Error(), "is not exhaustive") {
		t.Errorf("want non-exhaustive (E030); got %v", err)
	}
}

// A literal arm whose type doesn't match the scrutinee is rejected (E035):
// an i32 literal on a string match is a type error, not a fallthrough.
func TestStringLiteralMatchTypeMismatchRejected(t *testing.T) {
	src := `function main(): i32 {
			var s: string = "x";
			match (s) {
				"a" => { return 1; },
				0 => { return 2; },
				_ => { return 0; }
			}
			return 0;
		}`
	err := checkSource(t, src)
	if err == nil {
		t.Fatal("expected E035: i32 literal arm on a string match")
	}
	if !strings.Contains(err.Error(), "does not match scrutinee type string") {
		t.Errorf("want literal-type-mismatch (E035); got %v", err)
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
	good := `function main(): i32 {
			var o: Option[i32] = Some(42);
			return 0;
		}`
	if err := checkSource(t, good); err != nil {
		t.Errorf("good: %v", err)
	}
	bad := `function main(): i32 {
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
	src := `function find(): Option[i32] { return None; }
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
	src := `function main(): i32 {
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
//
// The raw-memory helpers (`__alloc`, `__load_ptr`, `__store_ptr`,
// `__memcpy`, `__memset`) declare their pointer params + result as
// `usize`. User code that wants to feed pointer-shaped values (string,
// Map handles, T[], [T], structs) into them must now use an EXPLICIT
// `as usize` / `as i32` cast — the implicit usize wormhole is gated to
// stdlib context so it can't silently launder type confusion in user
// code. See docs/ADVERSARIAL-REVIEW-2026-06.md (F2).
func TestUsizePreludeHelpersRequireExplicitCastInUserCode(t *testing.T) {
	// Explicit casts: type-check cleanly.
	for _, src := range []string{
		`function f(a: string, b: string, n: i32): i32 {
    __memcpy(a as usize, b as usize, n);
    return 0;
}`,
		`function f(): i32 {
    var buf: usize = __alloc(16);
    return buf as i32;
}`,
	} {
		if err := checkSource(t, src); err != nil {
			t.Errorf("explicit-cast form should type-check: %q\ngot: %v", src, err)
		}
	}
	// Implicit usize hop in user code: rejected (the closed wormhole).
	for _, src := range []string{
		// string -> usize with no cast
		`function f(a: string, b: string, n: i32): i32 {
    __memcpy(a, b, n);
    return 0;
}`,
		// usize -> i32 with no cast
		`function f(): i32 {
    var buf: i32 = __alloc(16);
    return buf;
}`,
		// the soundness exploit: i64 -> usize -> i32 laundering
		`function f(): i32 {
    var big: i64 = 5000000000i64;
    var p: usize = big;
    var small: i32 = p;
    return small;
}`,
	} {
		if err := checkSource(t, src); err == nil {
			t.Errorf("implicit usize conversion in user code should be rejected: %q", src)
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

// Auto-discovery: every prelude function named
// `__method_Array_<name>` gets a corresponding
// `Array.<name>` entry in `Info.Methods` without any
// hand-written registration in `checker.go`. Phase 2 of
// the prelude-to-modules migration relies on this so
// later phases can drop new `__method_Array_*` functions
// into a `std/array` module and have dispatch Just Work.
//
// The probe checks for representative methods drawn from
// the existing prelude (one synthetic + one IR-discovered)
// plus the canonical hand-registered `Array.append` which
// must continue to work despite being skipped by the
// auto-discovery loop.
func TestArrayMethodDispatchAutoDiscovers(t *testing.T) {
	// Post-flip there's no auto-prelude, so the discoverable
	// `__method_Array_*` functions are supplied inline here — the
	// same shape std/array ships. Auto-discovery should register
	// each as an `Array.<name>` method without a hand-written
	// entry in checker.go, while the synthetic `Array.append`
	// (registered by hand, IR-intercepted) keeps working.
	prog, err := parser.Parse(`function __method_Array_join(xs: i32[], sep: string): string { return ""; }
function __method_Array_sum(xs: i32[]): i32 { return 0; }
function main(): i32 { return 0; }`)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	info, err := Check(prog)
	if err != nil {
		t.Fatalf("check: %v", err)
	}
	cases := []struct {
		key, mangled string
	}{
		// Synthetic, registered by hand (IR-intercepted). The
		// value-returning `append` is the public spelling; the
		// mutable-looking `push` name was withdrawn (the mangled
		// lowering __method_Array_push is unchanged).
		{"Array.append", "__method_Array_push"},
		// Discovered from naming convention — these are
		// implemented as ordinary prelude functions whose
		// declarations would otherwise be invisible to the
		// method-dispatch map.
		{"Array.join", "__method_Array_join"},
		{"Array.sum", "__method_Array_sum"},
	}
	for _, c := range cases {
		got, ok := info.Methods[c.key]
		if !ok {
			t.Errorf("Methods[%q] missing; auto-discovery didn't pick up the prelude function", c.key)
			continue
		}
		if got != c.mangled {
			t.Errorf("Methods[%q] = %q, want %q", c.key, got, c.mangled)
		}
	}
}

// TestInfoGenericsAggregatesFuncsAndStructs — the kind-agnostic
// `Info.Generics` map (IMPROVEMENTS.md #10) should contain every
// generic function AND every generic struct declared in the
// program. Lets passes consult one map for "is this name a
// generic decl?" instead of running parallel paths over
// `GenericFuncs` / `GenericStructs`.
func TestInfoGenericsAggregatesFuncsAndStructs(t *testing.T) {
	src := `struct Box[T] { v: T }
struct Pair[A, B] { first: A, second: B }
function id[T](x: T): T { return x; }
function pick[A, B](a: A, b: B, take_first: boolean): A { return a; }
function main(): i32 {
	var b = Box { v: 7 };
	var p = Pair { first: 1, second: "hi" };
	return id(b.v) + pick(0, p.second, true);
}`
	prog, err := parser.Parse(src)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	info, err := Check(prog)
	if err != nil {
		t.Fatalf("check: %v", err)
	}
	// Generics aggregates both kinds.
	for _, name := range []string{"Box", "Pair", "id", "pick"} {
		decl, ok := info.Generics[name]
		if !ok {
			t.Errorf("Generics[%q] missing", name)
			continue
		}
		if decl.GenericName() != name {
			t.Errorf("Generics[%q].GenericName() = %q", name, decl.GenericName())
		}
		if len(decl.GenericTypeParams()) == 0 {
			t.Errorf("Generics[%q].GenericTypeParams() unexpectedly empty", name)
		}
	}
	// Non-generic `main` is NOT in the map.
	if _, ok := info.Generics["main"]; ok {
		t.Error(`Generics["main"] should not exist (main has no type params)`)
	}
	// Each entry's kind-specific map agrees on the same decl
	// pointer — no divergence between the typed maps and the
	// kind-agnostic view.
	for name, decl := range info.Generics {
		if fn, ok := decl.(*ast.FuncDecl); ok {
			if info.GenericFuncs[name] != fn {
				t.Errorf("GenericFuncs[%q] != Generics[%q]", name, name)
			}
		} else if sd, ok := decl.(*ast.StructDecl); ok {
			if info.GenericStructs[name] != sd {
				t.Errorf("GenericStructs[%q] != Generics[%q]", name, name)
			}
		} else {
			t.Errorf("Generics[%q] has unexpected type %T", name, decl)
		}
	}
}

// TestQualifiedVariantReferences — `Color.Red`-style references
// let two enums declare the same variant name and disambiguate at
// the use site. Was IMPROVEMENTS.md #15. Replaces the old decl-
// time "variant declared in both" error, which used to force the
// user to rename one of them.
func TestQualifiedVariantReferences(t *testing.T) {
	good := []string{
		// Two enums declaring the same variant — coexist.
		`enum Color { Red, Green, Blue }
		enum Status { Red, Yellow }
		function main(): i32 {
			var c: Color = Color.Red;
			var s: Status = Status.Red;
			return 0;
		}`,
		// Match-arm qualifier on a clashing variant.
		`enum A { Foo(i32), Bar }
		enum B { Foo(i32), Baz }
		function main(): i32 {
			var a: A = A.Foo(11);
			match (a) {
				A.Foo(x) => { return x; },
				A.Bar => { return 0; }
			}
			return 0;
		}`,
		// Qualified call with payload.
		`enum Shape { Circle(i32), Square(i32) }
		function main(): i32 {
			var s: Shape = Shape.Circle(7);
			match (s) {
				Circle(r) => { return r; },
				Square(side) => { return side * side; }
			}
			return 0;
		}`,
		// Single-variant-name case stays working — no qualifier
		// required, no regression.
		`enum Light { Red, Green, Yellow }
		function main(): i32 {
			var l: Light = Red;
			match (l) {
				Red => { return 1; },
				Green => { return 2; },
				Yellow => { return 3; }
			}
			return 0;
		}`,
	}
	for _, src := range good {
		if err := checkSource(t, src); err != nil {
			t.Errorf("expected ok, got %v\nsrc:\n%s", err, src)
		}
	}

	bad := []struct {
		src  string
		want string
	}{
		// Bare reference to a variant declared in two enums — must
		// qualify.
		{
			`enum A { Foo, Bar }
			enum B { Foo, Baz }
			function main(): i32 {
				var x: A = Foo;
				return 0;
			}`,
			"declared in multiple enums",
		},
		// Bare call form too.
		{
			`enum A { Foo(i32) }
			enum B { Foo(i32) }
			function main(): i32 {
				var x: A = Foo(7);
				return 0;
			}`,
			"declared in multiple enums",
		},
		// Qualifier names an enum that doesn't have that variant.
		{
			`enum A { Foo }
			enum B { Bar }
			function main(): i32 {
				var x: A = A.Bar;
				return 0;
			}`,
			"no variant",
		},
		// Match-arm qualifier disagrees with scrutinee.
		{
			`enum A { Foo }
			enum B { Foo }
			function main(): i32 {
				var a: A = A.Foo;
				match (a) {
					B.Foo => { return 1; }
				}
				return 0;
			}`,
			"does not match scrutinee enum",
		},
	}
	for _, c := range bad {
		err := checkSource(t, c.src)
		if err == nil {
			t.Errorf("expected error containing %q, got nil\nsrc:\n%s", c.want, c.src)
			continue
		}
		if !strings.Contains(err.Error(), c.want) {
			t.Errorf("error %q does not contain %q", err.Error(), c.want)
		}
	}
}

// TestCastAsTypeAscription — the `as` operator doubles as a
// zero-cost type-annotation form. Bare `None`, `[]`, partially-
// inferred variant constructors, etc. all get a place to pin a
// concrete type inline where there's no `var x: T = ...` site
// for inference to flow from.
func TestCastAsTypeAscription(t *testing.T) {
	good := []string{
		// Payload-less variant: only the destination type can fix
		// the type arg.
		`function main(): i32 {
			var x: Option[i32] = None as Option[i32];
			return 0;
		}`,
		// Partially-inferred Result — Ok(1) pins T but not E.
		`function main(): i32 {
			var r: Result[i32, string] = Ok(1) as Result[i32, string];
			return 0;
		}`,
		// Empty array literal — `[]` carries no element type
		// without an outside anchor.
		`function main(): i32 {
			var a: i32[] = [] as i32[];
			return a.len() as i32;
		}`,
		// Same-shape ascription (i.e. inner already concretely
		// typed): a no-op annotation, but should still type-check.
		`function main(): i32 {
			var x: Option[i32] = Some(1) as Option[i32];
			return 0;
		}`,
		// Ascription threaded directly into a call argument —
		// the original motivation. Without it, callers have to
		// invent a `none_i32()` helper.
		`function takes(o: Option[i32]): i32 { return 0; }
		function main(): i32 {
			return takes(None as Option[i32]);
		}`,
	}
	for _, src := range good {
		if err := checkSource(t, src); err != nil {
			t.Errorf("expected ok, got %v\nsrc:\n%s", err, src)
		}
	}

	bad := []struct {
		src  string
		want string
	}{
		// Genuinely incompatible types still error — ascription
		// isn't a transmute, it just exposes the existing
		// `assignable` rule inline.
		{
			`function main(): i32 {
				var x: Option[i32] = "hi" as Option[i32];
				return 0;
			}`,
			"cannot cast",
		},
		{
			`function main(): i32 {
				var n: i32 = true as i32;
				return n;
			}`,
			"cannot cast",
		},
	}
	for _, c := range bad {
		err := checkSource(t, c.src)
		if err == nil {
			t.Errorf("expected error containing %q, got nil\nsrc:\n%s", c.want, c.src)
			continue
		}
		if !strings.Contains(err.Error(), c.want) {
			t.Errorf("error %q does not contain %q", err.Error(), c.want)
		}
	}
}

// TestCheckContextCancellationShortCircuits — pre-cancelled
// context returns ctx.Err() without running the body-check
// loop. The LSP rests on this so a new edit can cancel an
// in-flight type-check (docs/IDE-COMPILATION-RESEARCH.md
// Rec §1).
func TestCheckContextCancellationShortCircuits(t *testing.T) {
	prog, err := parser.Parse(`function f(): i32 { return 1; }
function g(): i32 { return 2; }`)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = CheckContext(ctx, prog)
	if err == nil {
		t.Fatal("expected ctx.Err(), got nil")
	}
	if !errors.Is(err, context.Canceled) {
		t.Errorf("expected context.Canceled, got %v", err)
	}
}

// TestCheckContextBackgroundBehavesLikeOldCheck — non-cancellable
// context matches the original `Check(prog)` API behaviour.
// Regression sentinel against breaking the wrapper.
func TestCheckContextBackgroundBehavesLikeOldCheck(t *testing.T) {
	prog, err := parser.Parse(`function main(): i32 { return 42; }`)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	info, err := CheckContext(context.Background(), prog)
	if err != nil {
		t.Fatalf("CheckContext: %v", err)
	}
	if info == nil {
		t.Fatal("CheckContext returned nil info on a valid program")
	}
}

// TestSynthesisedHandleMainRunsInitFirst pins the
// docs/PLATFORM-RESEARCH.md Rec §3 init() ordering: when the
// program defines both `handle` and `init`, the auto-main
// synthesis prepends a call to `init()` BEFORE the
// `tcp_serve` accept loop.
func TestSynthesisedHandleMainRunsInitFirst(t *testing.T) {
	prog, err := parser.Parse(`function tcp_serve(port: i32, handler: (HttpRequest, Platform) => HttpResponse): i32 { return 0; }
function __port_from_env(name: string, def: i32): i32 { return def; }
function init() { print("starting"); }
function handle(req: HttpRequest, plat: Platform): HttpResponse {
    return HttpResponse { status: 200, body: "ok", headers: HeaderMap { names: [], values: [] } };
}`)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if _, err := Check(prog); err != nil {
		t.Fatalf("check: %v", err)
	}
	var main *ast.FuncDecl
	for _, fn := range prog.Funcs {
		if fn.Name == "main" && fn.IsSynthesisedHandlerMain {
			main = fn
			break
		}
	}
	if main == nil {
		t.Fatal("no synthesised main found")
	}
	if len(main.Body.Stmts) != 2 {
		t.Fatalf("synth main has %d stmts, want 2 (init + tcp_serve return)", len(main.Body.Stmts))
	}
	es, ok := main.Body.Stmts[0].(*ast.ExprStmt)
	if !ok {
		t.Fatalf("first stmt = %T, want *ast.ExprStmt (the init call)", main.Body.Stmts[0])
	}
	call, ok := es.Expr.(*ast.Call)
	if !ok {
		t.Fatalf("first stmt expression = %T, want *ast.Call", es.Expr)
	}
	id, ok := call.Callee.(*ast.Ident)
	if !ok || id.Name != "init" {
		t.Errorf("first stmt calls %v, want init", call.Callee)
	}
}

// TestSynthesisedHandleMainNoInitElidesPrepend — backward
// compat: programs without init() still get the original
// single-stmt synth main (just the tcp_serve return).
func TestSynthesisedHandleMainNoInitElidesPrepend(t *testing.T) {
	prog, err := parser.Parse(`function tcp_serve(port: i32, handler: (HttpRequest, Platform) => HttpResponse): i32 { return 0; }
function __port_from_env(name: string, def: i32): i32 { return def; }
function handle(req: HttpRequest, plat: Platform): HttpResponse {
    return HttpResponse { status: 200, body: "ok", headers: HeaderMap { names: [], values: [] } };
}`)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if _, err := Check(prog); err != nil {
		t.Fatalf("check: %v", err)
	}
	var main *ast.FuncDecl
	for _, fn := range prog.Funcs {
		if fn.Name == "main" && fn.IsSynthesisedHandlerMain {
			main = fn
			break
		}
	}
	if main == nil {
		t.Fatal("no synthesised main found")
	}
	if len(main.Body.Stmts) != 1 {
		t.Errorf("synth main has %d stmts, want 1 (just the tcp_serve return; no init prepended)", len(main.Body.Stmts))
	}
}

// TestUserDefinedMainSkipsSynthEvenWithInit — when the user
// writes their own main(), the synth-main path is skipped
// entirely. User's main has the freedom to call init() (or
// not) wherever they choose.
func TestUserDefinedMainSkipsSynthEvenWithInit(t *testing.T) {
	prog, err := parser.Parse(`function init() { print("starting"); }
function handle(req: HttpRequest, plat: Platform): HttpResponse {
    return HttpResponse { status: 200, body: "ok", headers: HeaderMap { names: [], values: [] } };
}
function main(): i32 { return 0; }`)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if _, err := Check(prog); err != nil {
		t.Fatalf("check: %v", err)
	}
	for _, fn := range prog.Funcs {
		if fn.Name == "main" && fn.IsSynthesisedHandlerMain {
			t.Fatalf("synth main was injected even though user defined main()")
		}
	}
}

// A tuple return whose elements are individually assignable (but not
// equal) to the declared element types should type-check. The
// motivating case is the cursor idiom (docs/CURSOR-IDIOM.md): a
// reader declared `(Option[i32], i32)` returns a bare `None` in its
// EOF arm, which types as `(Option, i32)`. Before assignable learned
// to recurse into tuples, the bare-`None` element only widened to
// `Option[i32]` at a top-level return, never inside a tuple, so this
// rejected with an E002 tuple mismatch.
func TestTupleReturnWidensBareEnumElement(t *testing.T) {
	src := `function read(pos: i32, n: i32): (Option[i32], i32) {
    if (pos >= n) { return (None, pos); }
    return (Some(pos), pos + 1);
}
function main(): i32 {
    var (b, p) = read(0, 1);
    match (b) { Some(v) => { return v; }, None => { return -1; } }
}`
	if err := checkSource(t, src); err != nil {
		t.Errorf("unexpected error: %v", err)
	}
}

// The tuple-assignability recursion must still reject a genuine
// element mismatch — it widens bare enums, it doesn't make tuples
// universally assignable.
func TestTupleReturnStillRejectsElementMismatch(t *testing.T) {
	src := `function f(): (i32, string) {
    return (1, 2);
}`
	if err := checkSource(t, src); err == nil {
		t.Errorf("expected a type error for (i32, i32) returned as (i32, string)")
	}
}

// A union-member struct literal in a tuple-literal return widens
// (wraps) to the declared union element — `return (A { … }, cur);`
// from a function declared `(AB, Cur)`. maybeWrapForUnion recurses
// into TupleLit elements; the *Result-struct → tuple-return migration
// of the self-host tree (#4406) leans on this.
func TestTupleReturnWrapsUnionElement(t *testing.T) {
	src := `struct A { x: i32 }
struct B { y: i32 }
type AB = A | B;
struct Cur { pos: i32 }
function mk(k: i32, c: Cur): (AB, Cur) {
    if (k == 0) { return (A { x: 1 }, c); }
    return (B { y: 2 }, c);
}
function main(): i32 {
    var (v, c2) = mk(0, Cur { pos: 0 });
    match (v) { A(a) => { return a.x; }, B(b) => { return b.y + c2.pos; } }
}`
	if err := checkSource(t, src); err != nil {
		t.Errorf("unexpected error: %v", err)
	}
}

// A struct that implements all of a trait's methods with matching
// signatures type-checks, and the impl is recorded in Info.Impls.
// See docs/TRAITS.md.
func TestTraitConformanceAccepted(t *testing.T) {
	src := `trait Display { function to_string(self: Self): string; }
struct Point { x: i32, y: i32 }
impl Display for Point {
    function to_string(self: Self): string { return "p"; }
}
function main(): i32 { var p: Point = Point { x: 1, y: 2 }; var s: string = p.to_string(); return 0; }`
	prog, err := parser.Parse(src)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	info, err := Check(prog)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !info.Impls["Display"]["Point"] {
		t.Errorf("Info.Impls should record Display for Point, got %+v", info.Impls)
	}
}

// A trait method with a default body is inherited by an impl that omits
// it: the impl conforms even though it only provides the abstract
// method, and the default is materialised as a receiver method on the
// impl type. An impl may still override the default. See docs/TRAITS.md.
func TestTraitDefaultMethodConformance(t *testing.T) {
	src := `trait Greet {
    function name(self: Self): string;
    function greeting(self: Self): string { return "hi " + self.name(); }
}
struct Dog { age: i32 }
impl Greet for Dog { function name(self: Self): string { return "rex"; } }
struct Cat { age: i32 }
impl Greet for Cat {
    function name(self: Self): string { return "felix"; }
    function greeting(self: Self): string { return "meow"; }
}
function main(): i32 {
    var d: Dog = Dog { age: 1 };
    var c: Cat = Cat { age: 2 };
    var a: string = d.greeting();
    var b: string = c.greeting();
    return 0;
}`
	prog, err := parser.Parse(src)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	info, err := Check(prog)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !info.Impls["Greet"]["Dog"] || !info.Impls["Greet"]["Cat"] {
		t.Errorf("both impls should conform, got %+v", info.Impls)
	}
	// Dog inherits the default `greeting`; it must be hoisted as a method.
	if info.Methods["Dog.greeting"] == "" {
		t.Errorf("inherited default `greeting` should be registered for Dog, got %+v", info.Methods["Dog.greeting"])
	}
	// Cat's explicit override must still be the registered method.
	if info.Methods["Cat.greeting"] == "" {
		t.Errorf("overriding `greeting` should be registered for Cat")
	}
}

// A default method whose body references a still-abstract trait method
// works through a bounded generic: `announce[T: Greet]` calls
// `x.greeting()`, which monomorphises to the inherited default.
func TestTraitDefaultMethodViaBound(t *testing.T) {
	src := `trait Greet {
    function name(self: Self): string;
    function greeting(self: Self): string { return "hi " + self.name(); }
}
struct Dog { age: i32 }
impl Greet for Dog { function name(self: Self): string { return "rex"; } }
function announce[T: Greet](x: T): string { return x.greeting(); }
function main(): i32 { var d: Dog = Dog { age: 1 }; var s: string = announce(d); return 0; }`
	if err := checkSource(t, src); err != nil {
		t.Errorf("default method via bound should typecheck: %v", err)
	}
}

// Associated types: a trait declares `type Item;`, an impl binds it, and
// A generic-trait bound (`function f[T: From[i32]]`) substitutes the
// bound's args into the trait method signature, so a `T.from(v)` call in
// the body type-checks against `from(v: i32): T`. Wrong arity and a type
// argument that doesn't implement the trait are rejected. See docs/TRAITS.md.
func TestGenericTraitBound(t *testing.T) {
	const hdr = `trait From[T] { function from(v: T): Self; }
struct Celsius { deg: i32 }
impl From[i32] for Celsius { function from(v: i32): Self { return Celsius { deg: v }; } }
`
	if err := checkSource(t, hdr+`function describe[T: From[i32]](proto: T, v: i32): T { return T.from(v); }
function main(): i32 {
  var z: Celsius = Celsius { deg: 0 };
  return describe(z, 20).deg;
}`); err != nil {
		t.Errorf("bounded generic over a generic trait should type-check, got: %v", err)
	}
	// Wrong bound-arg arity.
	err := checkSource(t, hdr+`function describe[T: From[i32, i64]](proto: T): T { return proto; }
function main(): i32 { return 0; }`)
	if err == nil || !strings.Contains(err.Error(), "takes 1 type argument") {
		t.Errorf("wrong bound-arg arity should be rejected, got: %v", err)
	}
	// A type argument that doesn't implement the bound trait.
	err = checkSource(t, hdr+`struct Plain { x: i32 }
function describe[T: From[i32]](proto: T, v: i32): T { return T.from(v); }
function main(): i32 { var p: Plain = Plain { x: 0 }; return describe(p, 5).x; }`)
	if err == nil || !strings.Contains(err.Error(), "does not implement trait From") {
		t.Errorf("non-implementing type argument should be rejected, got: %v", err)
	}
	// Mismatched bound args: the type implements From[i32] but the bound
	// requires From[i64] — precise satisfaction rejects it.
	err = checkSource(t, hdr+`function describe[T: From[i64]](proto: T, v: i64): T { return T.from(v); }
function main(): i32 {
  var z: Celsius = Celsius { deg: 0 };
  return describe(z, 20).deg;
}`)
	if err == nil || !strings.Contains(err.Error(), "the bound requires From[i64]") {
		t.Errorf("mismatched bound args should be rejected, got: %v", err)
	}
}

// A generic trait (`trait Container[T]`) binds its type parameters per
// impl (`impl Container[i32] for B`); the conformance check substitutes
// them, so the impl's concrete method signature lines up. A wrong arity
// is rejected. See docs/TRAITS.md.
func TestGenericTraitConformance(t *testing.T) {
	ok := []string{
		// receiver method returning the trait param
		`trait Container[T] { function get(self: Self): T; }
struct B { v: i32 }
impl Container[i32] for B { function get(self: Self): i32 { return self.v; } }
function main(): i32 { var b: B = B { v: 7 }; return b.get(); }`,
		// associated function taking the trait param, returning Self
		`trait From[T] { function from(v: T): Self; }
struct C { d: i32 }
impl From[i32] for C { function from(v: i32): Self { return C { d: v }; } }
function main(): i32 { return C.from(9).d; }`,
		// two type parameters
		`trait Pair[A, B] { function fst(self: Self): A; function snd(self: Self): B; }
struct P { a: i32, b: i32 }
impl Pair[i32, i32] for P { function fst(self: Self): i32 { return self.a; } function snd(self: Self): i32 { return self.b; } }
function main(): i32 { var p: P = P { a: 1, b: 2 }; return p.fst() + p.snd(); }`,
	}
	for _, src := range ok {
		if err := checkSource(t, src); err != nil {
			t.Errorf("generic-trait program should type-check, got: %v\nsrc:\n%s", err, src)
		}
	}
	// Wrong arity: impl omits the trait's type argument.
	err := checkSource(t, `trait Container[T] { function get(self: Self): T; }
struct B { v: i32 }
impl Container for B { function get(self: Self): i32 { return self.v; } }
function main(): i32 { return 0; }`)
	if err == nil || !strings.Contains(err.Error(), "takes 1 type argument") {
		t.Errorf("generic-trait impl with wrong arity should be rejected, got: %v", err)
	}
	// Mismatched binding: impl's method type doesn't match the substituted
	// trait param (Container[i32] but get returns boolean).
	err = checkSource(t, `trait Container[T] { function get(self: Self): T; }
struct B { v: i32 }
impl Container[i32] for B { function get(self: Self): boolean { return true; } }
function main(): i32 { return 0; }`)
	if err == nil || !strings.Contains(err.Error(), "wrong signature") {
		t.Errorf("generic-trait impl with a mismatched method should be rejected, got: %v", err)
	}
}

// the projection `Self::Item` / `T::Item` resolves to the binding — both
// for a concrete method call and through a bounded generic. See
// docs/ASSOCIATED-TYPES.md.
func TestAssociatedTypesAccepted(t *testing.T) {
	src := `trait Iterator {
    type Item;
    function next(self: Self): Self::Item;
}
struct B { v: i32 }
impl Iterator for B {
    type Item = i32;
    function next(self: Self): Self::Item { return self.v; }
}
function first[I: Iterator](it: I): I::Item { return it.next(); }
function main(): i32 {
    var b: B = B { v: 9 };
    var x: i32 = b.next();
    var y: i32 = first(b);
    return x + y;
}`
	if err := checkSource(t, src); err != nil {
		t.Errorf("associated-types program should type-check: %v", err)
	}
}

func TestAssociatedTypesErrors(t *testing.T) {
	cases := []struct {
		src  string
		want string
	}{
		// impl omits the associated-type binding
		{`trait It { type Item; function next(self: Self): Self::Item; }
struct B { v: i32 }
impl It for B { function next(self: Self): Self::Item { return self.v; } }
function main(): i32 { return 0; }`, "must bind associated type"},
		// impl binds an undeclared associated type
		{`trait It { type Item; function next(self: Self): Self::Item; }
struct B { v: i32 }
impl It for B { type Item = i32; type Extra = i32; function next(self: Self): Self::Item { return self.v; } }
function main(): i32 { return 0; }`, "does not declare"},
		// dyn over a trait with an UNPINNED associated type is not
		// object-safe (pinning it — `dyn It[Item = i32]` — is object-safe;
		// see TestDynAssocTypeChecker).
		{`trait It { type Item; function next(self: Self): Self::Item; }
struct B { v: i32 }
impl It for B { type Item = i32; function next(self: Self): Self::Item { return self.v; } }
function take(x: dyn It): i32 { return 0; }
function main(): i32 { return 0; }`, "is not pinned"},
	}
	for _, c := range cases {
		err := checkSource(t, c.src)
		if err == nil {
			t.Errorf("%q: expected error", c.src)
			continue
		}
		if !strings.Contains(err.Error(), c.want) {
			t.Errorf("error %q does not contain %q", err.Error(), c.want)
		}
	}
}

// A supertrait (`trait Ord: Eq`) lets a `T: Ord` bound call the
// supertrait's methods on T, and requires every `impl Ord for X` to also
// have `impl Eq for X`. See docs/TRAITS.md.
func TestTraitSupertraitAccepted(t *testing.T) {
	src := `trait Eq { function eq(self: Self, other: Self): boolean; }
trait Ord: Eq { function lt(self: Self, other: Self): boolean; }
struct P { x: i32 }
impl Eq for P { function eq(self: Self, other: Self): boolean { return self.x == other.x; } }
impl Ord for P { function lt(self: Self, other: Self): boolean { return self.x < other.x; } }
function cmp[T: Ord](a: T, b: T): boolean { if (a.eq(b)) { return true; } return a.lt(b); }
function main(): i32 { var p: P = P { x: 1 }; if (cmp(p, p)) { return 1; } return 0; }`
	prog, err := parser.Parse(src)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if _, err := Check(prog); err != nil {
		t.Fatalf("supertrait program should typecheck: %v", err)
	}
}

func TestTraitSupertraitErrors(t *testing.T) {
	cases := []struct {
		src  string
		want string
	}{
		// impl Ord for P without impl Eq for P.
		{`trait Eq { function eq(self: Self, other: Self): boolean; }
trait Ord: Eq { function lt(self: Self, other: Self): boolean; }
struct P { x: i32 }
impl Ord for P { function lt(self: Self, other: Self): boolean { return self.x < other.x; } }
function main(): i32 { return 0; }`, "supertrait of Ord"},
		// supertrait names a nonexistent trait.
		{`trait Ord: Nope { function lt(self: Self, other: Self): boolean; }
struct P { x: i32 }
impl Ord for P { function lt(self: Self, other: Self): boolean { return true; } }
function main(): i32 { return 0; }`, "unknown supertrait"},
		// cyclic supertrait graph.
		{`trait A: B { function fa(self: Self): i32; }
trait B: A { function fb(self: Self): i32; }
function main(): i32 { return 0; }`, "cyclic supertrait"},
	}
	for _, c := range cases {
		err := checkSource(t, c.src)
		if err == nil {
			t.Errorf("%q: expected error, got nil", c.src)
			continue
		}
		if !strings.Contains(err.Error(), c.want) {
			t.Errorf("error %q does not contain %q", err.Error(), c.want)
		}
	}
}

// Implementing a trait for a built-in numeric type is allowed when the
// trait is local (orphan rule satisfied by the trait being local).
func TestTraitImplForBuiltinType(t *testing.T) {
	src := `trait Tag { function tag(self: Self): string; }
impl Tag for i32 { function tag(self: Self): string { return "i32"; } }
function main(): i32 { var n: i32 = 5; var s: string = n.tag(); return 0; }`
	if err := checkSource(t, src); err != nil {
		t.Errorf("impl for builtin type should typecheck: %v", err)
	}
}

func TestTraitConformanceErrors(t *testing.T) {
	cases := []struct {
		src  string
		want string
	}{
		{`trait D { function to_string(self: Self): string; }
struct P { x: i32 }
impl D for P { }
function main(): i32 { return 0; }`, "missing method"},
		{`trait D { function to_string(self: Self): string; }
struct P { x: i32 }
impl D for P { function to_string(self: Self): i32 { return 1; } }
function main(): i32 { return 0; }`, "wrong signature"},
		{`trait D { function to_string(self: Self): string; }
struct P { x: i32 }
impl D for P {
    function to_string(self: Self): string { return "p"; }
    function extra(self: Self): string { return "x"; }
}
function main(): i32 { return 0; }`, "not a member of trait"},
		{`struct P { x: i32 }
impl Missing for P { function f(self: Self): void {} }
function main(): i32 { return 0; }`, "unknown trait"},
		{`trait D { function f(self: Self): void; }
struct P { x: i32 }
impl D for P { function f(self: Self): void {} }
impl D for P { function f(self: Self): void {} }
function main(): i32 { return 0; }`, "duplicate impl"},
		{`trait D { function f(self: Self): void; function f(self: Self): void; }
function main(): i32 { return 0; }`, "trait method \"f\" redeclared"},
	}
	for _, c := range cases {
		err := checkSource(t, c.src)
		if err == nil {
			t.Errorf("%q: expected error, got nil", c.src)
			continue
		}
		if !strings.Contains(err.Error(), c.want) {
			t.Errorf("error %q does not contain %q", err.Error(), c.want)
		}
	}
}

// A bounded generic body may call trait methods on its type param;
// a call site whose concrete type argument implements the bound
// type-checks. See docs/TRAITS.md (Phase 2).
func TestBoundedGenericAccepted(t *testing.T) {
	src := `trait Display { function to_string(self: Self): string; }
struct Point { x: i32 }
impl Display for Point { function to_string(self: Self): string { return "p"; } }
function show[T: Display](v: T): string { return v.to_string(); }
function main(): i32 { var p: Point = Point { x: 1 }; var s: string = show(p); return 0; }`
	if err := checkSource(t, src); err != nil {
		t.Errorf("bounded generic should typecheck: %v", err)
	}
}

func TestBoundedGenericErrors(t *testing.T) {
	cases := []struct {
		src  string
		want string
	}{
		// Type argument doesn't implement the bound.
		{`trait Display { function to_string(self: Self): string; }
struct A { x: i32 }
struct B { y: i32 }
impl Display for A { function to_string(self: Self): string { return "a"; } }
function show[T: Display](v: T): string { return v.to_string(); }
function main(): i32 { var b: B = B { y: 1 }; var s: string = show(b); return 0; }`,
			"does not implement trait Display"},
		// Method not provided by any bound on the type param.
		{`trait Display { function to_string(self: Self): string; }
function show[T: Display](v: T): string { return v.bogus(); }
function main(): i32 { return 0; }`,
			"no method \"bogus\" on type parameter T"},
		// Unbounded type param: calling a method on it is rejected.
		{`function show[T](v: T): string { return v.to_string(); }
function main(): i32 { return 0; }`,
			"add a trait bound"},
		// Unknown trait in a bound.
		{`function show[T: Nope](v: T): i32 { return 0; }
function main(): i32 { return 0; }`,
			"unknown trait \"Nope\" in bound"},
	}
	for _, c := range cases {
		err := checkSource(t, c.src)
		if err == nil {
			t.Errorf("%q: expected error, got nil", c.src)
			continue
		}
		if !strings.Contains(err.Error(), c.want) {
			t.Errorf("error %q does not contain %q", err.Error(), c.want)
		}
	}
}

// @derive only accepts the three derivable traits; deriving a struct
// whose field type doesn't implement the trait is a clean error. See
// docs/TRAITS.md.
func TestDeriveErrors(t *testing.T) {
	cases := []struct {
		src  string
		want string
	}{
		// Non-derivable user trait.
		{`trait Foo { function bar(self: Self): i32; }
@derive(Foo)
struct S { x: i32 }
function main(): i32 { return 0; }`, "only Eq, Display, Debug, Ord, Hash, Json, and Default are derivable"},
		// Unknown trait in derive.
		{`@derive(Nope)
struct S { x: i32 }
function main(): i32 { return 0; }`, "unknown trait"},
	}
	for _, c := range cases {
		err := checkSource(t, c.src)
		if err == nil {
			t.Errorf("%q: expected error, got nil", c.src)
			continue
		}
		if !strings.Contains(err.Error(), c.want) {
			t.Errorf("error %q does not contain %q", err.Error(), c.want)
		}
	}
}

// @derive on a generic enum and @derive(Ord) on an enum are rejected
// with clear messages (both are follow-ups). See docs/TRAITS.md.
func TestDeriveEnumErrors(t *testing.T) {
	cases := []struct{ src, want string }{
		// Only Eq/Display/Ord are derivable — a user-defined trait is not.
		{`trait Foo { function foo(self: Self): boolean; }
@derive(Foo)
enum E { A, B }
function main(): i32 { return 0; }`, "only Eq, Display, Debug, Ord, Hash, Json, and Default are derivable"},
	}
	for _, c := range cases {
		err := checkSource(t, c.src)
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("src %q: got %v, want containing %q", c.src, err, c.want)
		}
	}
}

// TestDeriveGenericEnum — `@derive` on a generic enum synthesises a
// parametric impl `impl[T: Trait] Trait for E[T]`, so the derived
// methods type-check field-wise through the bound and monomorphise per
// instantiation. (Generic-enum derive used to be rejected outright.)
func TestDeriveGenericEnum(t *testing.T) {
	src := `trait Eq { function eq(self: Self, other: Self): boolean; }
impl Eq for i32 { function eq(self: i32, other: i32): boolean { return self == other; } }
@derive(Eq)
enum E[T] { A(T), B }
function main(): i32 {
	var x = A(1);
	if (x.eq(A(1))) { return 0; }
	return 1;
}`
	if err := checkSource(t, src); err != nil {
		t.Fatalf("generic-enum @derive should check: %v", err)
	}
}

// `@derive(Hash)` synthesises a field/variant-wise `hash(): i32`,
// composing through each field's own `Hash` impl. A struct, an enum, and
// a generic struct all derive; a field whose type doesn't implement Hash
// is a clean error (the derived method's `[T: Hash]` bound, or the
// missing primitive impl, is unsatisfied). See docs/TRAITS.md.
func TestDeriveHash(t *testing.T) {
	const hashTrait = `trait Hash { function hash(self: Self): i32; }
impl Hash for i32 { function hash(self: Self): i32 { return self; } }
`
	ok := []string{
		// Plain struct.
		hashTrait + `@derive(Hash) struct P { x: i32, y: i32 }
function main(): i32 { var p: P = P { x: 1, y: 2 }; return p.hash(); }`,
		// Enum (tag-seeded, payload-folded).
		hashTrait + `@derive(Hash) enum E { A, B(i32), C(i32, i32) }
function main(): i32 { var e: E = C(1, 2); return e.hash(); }`,
		// Generic struct: parametric `impl[T: Hash] Hash for Box[T]`.
		hashTrait + `@derive(Hash) struct Box[T] { v: T }
function main(): i32 { var b: Box[i32] = Box { v: 7 }; return b.hash(); }`,
	}
	for _, src := range ok {
		if err := checkSource(t, src); err != nil {
			t.Errorf("@derive(Hash) should type-check, got: %v\nsrc: %s", err, src)
		}
	}

	// A field whose type has no Hash impl cannot derive Hash.
	bad := hashTrait + `struct NoHash { z: i32 }
@derive(Hash) struct S { n: NoHash }
function main(): i32 { return 0; }`
	if err := checkSource(t, bad); err == nil {
		t.Error("expected an error deriving Hash for a struct with a non-Hash field, got nil")
	}
}

// `@derive(Default)` synthesises a `default()` associated function that
// builds a type's zero value, called as `Type.default()`. Scalars use
// their zero literal; nominal fields delegate to their own `default()`
// (composition); a generic field uses the bound `T.default()`; an enum
// defaults to its first variant. A field type with no default is a clean
// error. See docs/TRAITS.md.
func TestDeriveDefault(t *testing.T) {
	const defTrait = `trait Default { function default(): Self; }
`
	ok := []string{
		// Plain struct of scalars.
		defTrait + `@derive(Default) struct P { x: i32, y: string, f: boolean }
function main(): i32 { var p: P = P.default(); return p.x + p.y.len(); }`,
		// Composition: a nominal field delegates to its own default().
		defTrait + `@derive(Default) struct Inner { n: i32 }
@derive(Default) struct Outer { i: Inner, k: i32 }
function main(): i32 { var o: Outer = Outer.default(); return o.i.n + o.k; }`,
		// Enum defaults to its first variant.
		defTrait + `@derive(Default) enum E { A, B(i32) }
function main(): i32 { var e: E = E.default(); match (e) { A => { return 0; }, B(n) => { return n; } } }`,
		// Enum whose first variant carries payloads (each defaulted).
		defTrait + `@derive(Default) enum E { First(i32, i32), Second }
function main(): i32 { var e: E = E.default(); match (e) { First(a, b) => { return a + b; }, Second => { return 9; } } }`,
		// Generic struct: parametric `impl[T: Default] Default for Box[T]`.
		defTrait + `@derive(Default) struct Inner { n: i32 }
@derive(Default) struct Box[T] { v: T }
function main(): i32 { var b: Box[Inner] = Box.default(); return b.v.n; }`,
	}
	for _, src := range ok {
		if err := checkSource(t, src); err != nil {
			t.Errorf("@derive(Default) should type-check, got: %v\nsrc: %s", err, src)
		}
	}

	bad := []struct{ src, want string }{
		// A field whose type has no derivable default (array).
		{defTrait + `@derive(Default) struct S { xs: i32[] }
function main(): i32 { return 0; }`, "no default"},
		// An enum whose first variant has a non-defaultable payload.
		{defTrait + `@derive(Default) enum E { A([i32]), B }
function main(): i32 { return 0; }`, "no default"},
	}
	for _, c := range bad {
		err := checkSource(t, c.src)
		if err == nil {
			t.Errorf("expected error containing %q, got nil\nsrc: %s", c.want, c.src)
			continue
		}
		if !strings.Contains(err.Error(), c.want) {
			t.Errorf("error %q does not contain %q", err.Error(), c.want)
		}
	}
}

// `@derive(Json)` synthesises a field/variant-wise `to_json(): string`
// (canonical JSON text), composing through each field's own `Json` impl.
// A struct, an enum (externally tagged), a generic struct, and a nested
// struct all derive; a field whose type doesn't implement Json is a clean
// error. See docs/TRAITS.md.
func TestDeriveJson(t *testing.T) {
	const jsonTrait = `trait Json { function to_json(self: Self): string; }
impl Json for i32 { function to_json(self: Self): string { return "0"; } }
impl Json for string { function to_json(self: Self): string { return self; } }
`
	ok := []string{
		// Plain struct.
		jsonTrait + `@derive(Json) struct P { x: i32, y: i32 }
function main(): i32 { var p: P = P { x: 1, y: 2 }; var s: string = p.to_json(); return 0; }`,
		// Enum: unit, single-payload, multi-payload arms all synthesise.
		jsonTrait + `@derive(Json) enum E { A, B(i32), C(i32, i32) }
function main(): i32 { var e: E = C(1, 2); var s: string = e.to_json(); return 0; }`,
		// Generic struct: parametric impl[T: Json] Json for Box[T].
		jsonTrait + `@derive(Json) struct Box[T] { v: T }
function main(): i32 { var b: Box[string] = Box { v: "hi" }; var s: string = b.to_json(); return 0; }`,
		// Nested derived struct composes through the field's to_json.
		jsonTrait + `@derive(Json) struct Inner { n: i32 }
@derive(Json) struct Outer { a: Inner, tag: string }
function main(): i32 { var o: Outer = Outer { a: Inner { n: 5 }, tag: "x" }; var s: string = o.to_json(); return 0; }`,
	}
	for _, src := range ok {
		if err := checkSource(t, src); err != nil {
			t.Errorf("@derive(Json) should type-check, got: %v\nsrc: %s", err, src)
		}
	}

	// A field whose type has no Json impl cannot derive Json.
	bad := jsonTrait + `struct NoJson { z: i32 }
@derive(Json) struct S { n: NoJson }
function main(): i32 { return 0; }`
	if err := checkSource(t, bad); err == nil {
		t.Error("expected an error deriving Json for a struct with a non-Json field, got nil")
	}
}

// Associated functions: a trait method with no `self` receiver
// (`function f(): Self`) is called as `Type.f(args)` rather than
// `value.f(args)` — the constructor / static-method shape. The impl
// provides it; `Self` resolves to the impl type.
func TestAssociatedFunctions(t *testing.T) {
	ok := []string{
		// Zero-arg constructor on a struct.
		`trait Zero { function zero(): Self; }
struct Point { x: i32, y: i32 }
impl Zero for Point { function zero(): Self { return Point { x: 0, y: 0 }; } }
function main(): i32 { var p: Point = Point.zero(); return p.x + p.y; }`,
		// Constructor with arguments.
		`trait Ctor { function make(a: i32, b: i32): Self; }
struct Point { x: i32, y: i32 }
impl Ctor for Point { function make(a: i32, b: i32): Self { return Point { x: a, y: b }; } }
function main(): i32 { var p: Point = Point.make(3, 4); return p.x + p.y; }`,
		// Associated function on an enum.
		`trait Empty { function empty(): Self; }
enum Opt { Nothing, Just(i32) }
impl Empty for Opt { function empty(): Self { return Nothing; } }
function main(): i32 { var o: Opt = Opt.empty(); match (o) { Nothing => { return 0; }, Just(n) => { return n; } } }`,
		// Result chains directly: `T.f().field`.
		`trait Zero { function zero(): Self; }
struct P { x: i32 }
impl Zero for P { function zero(): Self { return P { x: 9 }; } }
function main(): i32 { return P.zero().x; }`,
		// Generic associated dispatch: `T.f()` on a bounded type param,
		// resolved per monomorphisation.
		`trait Zero { function zero(): Self; }
struct P { x: i32 }
impl Zero for P { function zero(): Self { return P { x: 0 }; } }
function mk[T: Zero](): T { return T.zero(); }
function main(): i32 { var p: P = mk(); return p.x; }`,
		// Generic associated dispatch with an argument.
		`trait From { function of(n: i32): Self; }
struct Box { v: i32 }
impl From for Box { function of(n: i32): Self { return Box { v: n }; } }
function build[T: From](n: i32): T { return T.of(n); }
function main(): i32 { var b: Box = build(7); return b.v; }`,
	}
	for _, src := range ok {
		if err := checkSource(t, src); err != nil {
			t.Errorf("associated function should type-check, got: %v\nsrc: %s", err, src)
		}
	}

	bad := []struct{ src, want string }{
		// Calling a `self`-taking method as `Type.method()`.
		{`struct Point { x: i32 }
function (p: Point) getx(): i32 { return p.x; }
function main(): i32 { return Point.getx(); }`, "is a method; call it on a value"},
		// An impl missing the trait's associated function.
		{`trait Zero { function zero(): Self; }
struct Point { x: i32 }
impl Zero for Point { }
function main(): i32 { return 0; }`, "missing method"},
		// Associated-function signature mismatch.
		{`trait Ctor { function make(a: i32): Self; }
struct Point { x: i32 }
impl Ctor for Point { function make(a: string): Self { return Point { x: 0 }; } }
function main(): i32 { return 0; }`, "wrong signature"},
	}
	for _, c := range bad {
		err := checkSource(t, c.src)
		if err == nil {
			t.Errorf("expected error containing %q, got nil\nsrc: %s", c.want, c.src)
			continue
		}
		if !strings.Contains(err.Error(), c.want) {
			t.Errorf("error %q does not contain %q", err.Error(), c.want)
		}
	}
}

// `dyn Trait` (runtime trait objects): a concrete impl-ing type coerces
// to `dyn Trait`, a trait method dispatches dynamically, and the
// negative cases (non-impl coercion, object-unsafe trait, unknown
// method, unknown trait) are rejected. See docs/DYN-TRAITS.md.
func TestDynTraitChecker(t *testing.T) {
	const prelude = `trait Shape { function area(self: Self): i32; }
struct Circle { r: i32 }
impl Shape for Circle { function area(self: Self): i32 { return self.r; } }
`
	// Accepted: coerce a Circle to dyn Shape, dispatch area().
	if err := checkSource(t, prelude+`
function f(d: dyn Shape): i32 { return d.area(); }
function main(): i32 { var d: dyn Shape = Circle { r: 3 }; return f(d); }`); err != nil {
		t.Fatalf("valid dyn use should check: %v", err)
	}

	cases := []struct{ name, src, want string }{
		{"non-impl coercion",
			prelude + `struct NoShape { z: i32 }
function main(): i32 { var d: dyn Shape = NoShape { z: 1 }; return 0; }`,
			"cannot assign NoShape"},
		{"unknown method",
			prelude + `function f(d: dyn Shape): i32 { return d.perimeter(); }
function main(): i32 { return 0; }`,
			`no method "perimeter" on ` + "`dyn Shape`"},
		{"unknown trait",
			`function f(d: dyn Bogus): i32 { return 0; }
function main(): i32 { return 0; }`,
			"unknown trait"},
		{"object-unsafe",
			`trait Eq { function eq(self: Self, other: Self): boolean; }
function f(d: dyn Eq): i32 { return 0; }
function main(): i32 { return 0; }`,
			"not object-safe"},
		{"heterogeneous array of non-impl",
			prelude + `struct NoShape { z: i32 }
function main(): i32 { var ds: dyn Shape[] = [Circle { r: 1 }, NoShape { z: 2 }]; return 0; }`,
			"does not implement Shape"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := checkSource(t, c.src)
			if err == nil || !strings.Contains(err.Error(), c.want) {
				t.Errorf("got %v, want containing %q", err, c.want)
			}
		})
	}
}

// TestDynGenericTraitChecker exercises generic dyn-trait objects
// (`dyn Container[i32]`, #2691 trait-spine): a concrete type coerces in
// iff it impls the trait at the PINNED arguments; the method signature is
// read with the trait's type params substituted (so `get(): T` returns
// i32); and the negative cases — argument mismatch, an unpinned generic
// trait, and a method-result type check against the pinned arg — are
// rejected.
func TestDynGenericTraitChecker(t *testing.T) {
	const prelude = `trait Container[T] { function get(self: Self): T; }
struct BoxI { v: i32 }
impl Container[i32] for BoxI { function get(self: Self): i32 { return self.v; } }
struct BoxS { v: i32 }
impl Container[string] for BoxS { function get(self: Self): string { return "x"; } }
`
	// Accepted: BoxI coerces to dyn Container[i32]; get() returns i32, so
	// it composes in an i32 context.
	if err := checkSource(t, prelude+`
function take(d: dyn Container[i32]): i32 { return d.get(); }
function main(): i32 { var d: dyn Container[i32] = BoxI { v: 7 }; return take(d); }`); err != nil {
		t.Fatalf("valid generic dyn use should check: %v", err)
	}

	cases := []struct{ name, src, want string }{
		{"argument mismatch",
			prelude + `function main(): i32 { var d: dyn Container[string] = BoxI { v: 1 }; return 0; }`,
			"cannot assign BoxI"},
		{"unpinned generic trait",
			prelude + `function f(d: dyn Container): i32 { return 0; }
function main(): i32 { return 0; }`,
			"must pin its type parameter"},
		{"pinned result type respected",
			prelude + `function f(d: dyn Container[i32]): string { return d.get(); }
function main(): i32 { return 0; }`,
			"string"},
		{"string-pinned accepted",
			prelude + `function f(d: dyn Container[string]): string { return d.get(); }
function main(): i32 { var d: dyn Container[string] = BoxS { v: 1 }; return 0; }`,
			""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := checkSource(t, c.src)
			if c.want == "" {
				if err != nil {
					t.Errorf("expected to type-check, got %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), c.want) {
				t.Errorf("got %v, want containing %q", err, c.want)
			}
		})
	}
}

// TestDynAssocTypeChecker exercises a `dyn` object over a trait with an
// associated type pinned (`dyn Producer[Item = i32]`): pinning makes the
// otherwise object-unsafe trait usable, `Self::Item` in a method signature
// resolves to the pin, the coercion requires the impl's binding to match the
// pin, and an unpinned associated type is still rejected.
func TestDynAssocTypeChecker(t *testing.T) {
	const prelude = `trait Producer { type Item; function get(self: Self): Self::Item; }
struct IntBox { v: i32 }
impl Producer for IntBox { type Item = i32; function get(self: Self): i32 { return self.v; } }
`
	// Accepted: pinned to the impl's binding; get() resolves to i32.
	if err := checkSource(t, prelude+`
function take(d: dyn Producer[Item = i32]): i32 { return d.get() + 1; }
function main(): i32 { return take(IntBox { v: 7 }); }`); err != nil {
		t.Fatalf("pinned dyn assoc type should check: %v", err)
	}
	cases := []struct{ name, src, want string }{
		{"unpinned",
			prelude + `function f(d: dyn Producer): i32 { return 0; }
function main(): i32 { return 0; }`,
			"is not pinned"},
		{"wrong pin coercion",
			prelude + `function main(): i32 { var d: dyn Producer[Item = string] = IntBox { v: 1 }; return 0; }`,
			"cannot assign IntBox"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := checkSource(t, c.src)
			if err == nil || !strings.Contains(err.Error(), c.want) {
				t.Errorf("got %v, want containing %q", err, c.want)
			}
		})
	}
}

// TestDynMultiTraitChecker exercises multi-trait trait objects
// (`dyn A + B`, docs/DYN-TRAITS.md): a concrete coerces in iff it impls
// EVERY trait; a method resolves across the UNION of the traits' method
// sets; a method declared by two traits is ambiguous; a non-object-safe
// or unknown trait anywhere in the set errors. Single-trait `dyn A` is
// unaffected (covered by TestDynTraitChecker).
func TestDynMultiTraitChecker(t *testing.T) {
	const prelude = `trait Show { function show(self: Self): i32; }
trait Sized { function size(self: Self): i32; }
struct Both { v: i32 }
impl Show for Both { function show(self: Self): i32 { return self.v; } }
impl Sized for Both { function size(self: Self): i32 { return 1; } }
struct OnlyShow { v: i32 }
impl Show for OnlyShow { function show(self: Self): i32 { return self.v; } }
`
	// Accepted: Both impls Show AND Sized → coerces to `dyn Show + Sized`,
	// and a method from EACH trait resolves.
	if err := checkSource(t, prelude+`
function f(d: dyn Show + Sized): i32 { return d.show() + d.size(); }
function main(): i32 { var d: dyn Show + Sized = Both { v: 3 }; return f(d); }`); err != nil {
		t.Fatalf("valid multi-trait dyn use should check: %v", err)
	}
	// Order-insensitive: `dyn Sized + Show` is the same type.
	if err := checkSource(t, prelude+`
function main(): i32 { var d: dyn Sized + Show = Both { v: 3 }; return d.show() + d.size(); }`); err != nil {
		t.Fatalf("order-insensitive multi-trait dyn should check: %v", err)
	}

	cases := []struct{ name, src, want string }{
		{"missing one trait",
			prelude + `function main(): i32 { var d: dyn Show + Sized = OnlyShow { v: 1 }; return 0; }`,
			"Sized"},
		{"ambiguous method across traits",
			`trait A { function m(self: Self): i32; }
trait B { function m(self: Self): i32; }
struct C { v: i32 }
impl A for C { function m(self: Self): i32 { return 1; } }
impl B for C { function m(self: Self): i32 { return 2; } }
function f(d: dyn A + B): i32 { return d.m(); }
function main(): i32 { return 0; }`,
			"ambiguous method"},
		{"unknown trait in set",
			`trait Show { function show(self: Self): i32; }
function f(d: dyn Show + Bogus): i32 { return 0; }
function main(): i32 { return 0; }`,
			"unknown trait"},
		{"non-object-safe trait in set",
			`trait Show { function show(self: Self): i32; }
trait Eq { function eq(self: Self, other: Self): boolean; }
function f(d: dyn Show + Eq): i32 { return 0; }
function main(): i32 { return 0; }`,
			"not object-safe"},
		{"method on neither trait",
			prelude + `function f(d: dyn Show + Sized): i32 { return d.nope(); }
function main(): i32 { return 0; }`,
			`no method "nope"`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := checkSource(t, c.src)
			if err == nil || !strings.Contains(err.Error(), c.want) {
				t.Errorf("got %v, want containing %q", err, c.want)
			}
		})
	}
}

// TestDowncastChecker exercises the `e as? T` fallible downcast
// (docs/DYN-TRAITS.md §9): a valid downcast of a `dyn Trait` value to a
// concrete struct/enum that implements the trait checks to `Option[T]`;
// a non-dyn LHS and a target that doesn't implement the trait error.
func TestDowncastChecker(t *testing.T) {
	const prelude = `trait Shape { function area(self: Self): i32; }
struct Circle { r: i32 }
struct Rect { w: i32, h: i32 }
impl Shape for Circle { function area(self: Self): i32 { return self.r; } }
impl Shape for Rect { function area(self: Self): i32 { return self.w * self.h; } }
`
	// Valid downcast: result type Option[Circle], usable through match.
	if err := checkSource(t, prelude+`
function describe(s: dyn Shape): i32 {
    var c: Option[Circle] = s as? Circle;
    return match (c) { Some(x) => x.r, None => 0 };
}
function main(): i32 { return describe(Circle { r: 7 }); }`); err != nil {
		t.Fatalf("valid downcast should check: %v", err)
	}

	// Result type flows: the downcast expression IS Option[Circle].
	prog, err := parser.Parse(prelude + `
function describe(s: dyn Shape): Option[Circle] { return s as? Circle; }
function main(): i32 { return 0; }`)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if _, err := Check(prog); err != nil {
		t.Fatalf("downcast result-type flow should check: %v", err)
	}
	// The checker stamped DowncastExpr.Trait for later codegen.
	ret := prog.Funcs[len(prog.Funcs)-2].Body.Stmts[0].(*ast.Return)
	if dc, ok := ret.Value.(*ast.DowncastExpr); !ok {
		t.Fatalf("expected DowncastExpr, got %T", ret.Value)
	} else if dc.Trait != "Shape" {
		t.Errorf("DowncastExpr.Trait = %q, want Shape", dc.Trait)
	}

	// Enum target: the parser wraps a bare `as? Color` name as a
	// StructType, but the checker rewrites it to EnumType so the result
	// `Option[Color]` matches a `var c: Option[Color]` annotation (whose
	// Color resolveType already canonicalised to EnumType). Without the
	// rewrite this assignment would spuriously E003 ("cannot assign
	// Option[Color] to Option[Color]" — same spelling, different node).
	const enumPrelude = `trait Describe { function tag(self: Self): i32; }
enum Color { Red, Green }
impl Describe for Color { function tag(self: Self): i32 { return 0; } }
`
	enumProg, err := parser.Parse(enumPrelude + `
function check(d: dyn Describe): Option[Color] { return d as? Color; }
function main(): i32 { return 0; }`)
	if err != nil {
		t.Fatalf("parse enum-target: %v", err)
	}
	if _, err := Check(enumProg); err != nil {
		t.Fatalf("enum-target downcast should check: %v", err)
	}
	enumRet := enumProg.Funcs[len(enumProg.Funcs)-2].Body.Stmts[0].(*ast.Return)
	dc, ok := enumRet.Value.(*ast.DowncastExpr)
	if !ok {
		t.Fatalf("expected DowncastExpr, got %T", enumRet.Value)
	}
	if et, isEnum := dc.Target.(ast.EnumType); !isEnum || et.Name != "Color" {
		t.Errorf("enum downcast Target = %#v, want ast.EnumType{Name:\"Color\"}", dc.Target)
	}

	cases := []struct{ name, src, want string }{
		{"non-dyn LHS",
			prelude + `function main(): i32 { var c: i32 = 1; var d: Option[Circle] = c as? Circle; return 0; }`,
			"requires a 'dyn Trait' value on the left"},
		{"target does not implement trait",
			prelude + `struct NoShape { z: i32 }
function describe(s: dyn Shape): i32 { var c: Option[NoShape] = s as? NoShape; return 0; }
function main(): i32 { return 0; }`,
			"does not implement Shape"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := checkSource(t, c.src)
			if err == nil || !strings.Contains(err.Error(), c.want) {
				t.Errorf("got %v, want containing %q", err, c.want)
			}
		})
	}
}

// TestDynCoercionRecorded: the checker records each concrete→`dyn Trait`
// boxing site in Info.DynCoercions with the right (trait, concrete)
// pair, so compiled-backend IR lowering can box the value with the
// correct vtable (docs/DYN-TRAITS.md §4.2.1). Covers a var-init
// coercion and an argument-passing coercion; both must be recorded with
// Trait="Shape", Concrete="Circle".
func TestDynCoercionRecorded(t *testing.T) {
	const src = `trait Shape { function area(self: Self): i32; }
struct Circle { r: i32 }
impl Shape for Circle { function area(self: Self): i32 { return self.r; } }
function f(d: dyn Shape): i32 { return d.area(); }
function main(): i32 {
    var d: dyn Shape = Circle { r: 3 };
    return f(Circle { r: 4 });
}`
	prog, err := parser.Parse(src)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	info, err := Check(prog)
	if err != nil {
		t.Fatalf("check: %v", err)
	}
	if len(info.DynCoercions) != 2 {
		t.Fatalf("want 2 recorded dyn coercions (var-init + arg), got %d: %+v", len(info.DynCoercions), info.DynCoercions)
	}
	for holder, dc := range info.DynCoercions {
		if dc.Trait != "Shape" || dc.Concrete != "Circle" {
			t.Errorf("coercion = %+v, want {Shape Circle}", dc)
		}
		// The holder is the concrete struct literal, not a dyn value.
		if _, ok := holder.(*ast.StructLit); !ok {
			t.Errorf("holder = %T, want *ast.StructLit (the Circle literal)", holder)
		}
	}
}

// A dyn→dyn assignment (no boxing) and a non-dyn destination record
// nothing — only concrete→dyn sites are coercions.
func TestDynCoercionNotRecordedForNonBoxing(t *testing.T) {
	const src = `trait Shape { function area(self: Self): i32; }
struct Circle { r: i32 }
impl Shape for Circle { function area(self: Self): i32 { return self.r; } }
function passthrough(d: dyn Shape): i32 {
    var e: dyn Shape = d;
    return e.area();
}
function main(): i32 { return passthrough(Circle { r: 1 }); }`
	prog, err := parser.Parse(src)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	info, err := Check(prog)
	if err != nil {
		t.Fatalf("check: %v", err)
	}
	// Only the `Circle{r:1}` argument to passthrough is a real coercion;
	// `var e: dyn Shape = d` (dyn→dyn) records nothing.
	if len(info.DynCoercions) != 1 {
		t.Fatalf("want exactly 1 coercion (the Circle arg), got %d: %+v", len(info.DynCoercions), info.DynCoercions)
	}
}

// A body-less `@import` function (extern WIT binding, P4 —
// docs/WIT-BRING-YOUR-OWN.md) type-checks: its signature is registered so
// call sites resolve against it, and its (absent) body is not walked.
func TestExternImportTypeChecks(t *testing.T) {
	src := `@import("wasi:random/random@0.2.0", "get-random-u64")
function get_random(): u64;
function main(): i32 {
	var r: u64 = get_random();
	return 0;
}`
	if err := checkSource(t, src); err != nil {
		t.Fatalf("extern @import call should type-check: %v", err)
	}
}

// WIT resource handles (P5 — docs/WIT-BRING-YOUR-OWN.md): a `resource`
// declaration introduces a nominal handle type; `own R` / `borrow R` (and a
// bare resource name, which means owned) type-check, an owned handle coerces
// to a borrow of the same resource, and a plain i32 is NOT a handle.
func TestResourceHandleChecker(t *testing.T) {
	const prelude = `@import("wasi:io/poll@0.2.0", "pollable")
resource Pollable;

@import("wasi:clocks/monotonic-clock@0.2.0", "subscribe-duration")
function subscribe(ns: u64): own Pollable;

@import("wasi:io/poll@0.2.0", "[method]pollable.ready")
function ready(h: borrow Pollable): boolean;
`
	// Accepted: own handle from subscribe, lent (own → borrow) to ready.
	if err := checkSource(t, prelude+`
function main(): i32 {
	var p: own Pollable = subscribe(0 as u64);
	if (ready(p)) { return 1; }
	return 0;
}`); err != nil {
		t.Fatalf("valid resource-handle use should check: %v", err)
	}
	// Accepted: a bare resource name is an owned handle.
	if err := checkSource(t, prelude+`
function main(): i32 {
	var p: Pollable = subscribe(0 as u64);
	if (ready(p)) { return 1; }
	return 0;
}`); err != nil {
		t.Fatalf("bare resource name (owned handle) should check: %v", err)
	}

	cases := []struct{ name, src, want string }{
		{"unknown resource",
			`function f(h: borrow Bogus): i32 { return 0; }
function main(): i32 { return 0; }`,
			"unknown resource"},
		{"plain i32 is not a handle",
			prelude + `function main(): i32 {
	var n: i32 = 7;
	if (ready(n)) { return 1; }
	return 0;
}`,
			"expected borrow Pollable, got i32"},
		{"handle is not a plain i32",
			prelude + `function main(): i32 {
	var p: own Pollable = subscribe(0 as u64);
	var n: i32 = p;
	return n;
}`,
			"cannot"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := checkSource(t, c.src)
			if err == nil || !strings.Contains(err.Error(), c.want) {
				t.Errorf("got %v, want containing %q", err, c.want)
			}
		})
	}
}

// An `@export` function (P6 — docs/WIT-BRING-YOUR-OWN.md) type-checks like any
// function: its body is checked, and it stays callable from Fern. A generic
// `@export` is rejected (a world export has one concrete ABI).
func TestExportChecker(t *testing.T) {
	if err := checkSource(t, `@export("wasi:cli/run@0.2.0", "run")
function run(): i32 { return 0; }
function main(): i32 { return run(); }`); err != nil {
		t.Fatalf("valid @export should check: %v", err)
	}
	err := checkSource(t, `@export("a:b/c", "d")
function f(): i32 { return "nope"; }`)
	if err == nil || !strings.Contains(err.Error(), "i32") {
		t.Errorf("type error in @export body should be reported, got %v", err)
	}
	err = checkSource(t, `@export("a:b/c", "d")
function f[T](x: T): T { return x; }`)
	if err == nil || !strings.Contains(err.Error(), "generic") {
		t.Errorf("generic @export should be rejected, got %v", err)
	}
}

// Calling an extern @import function with the wrong argument arity is a
// checker error, exactly like an ordinary function.
func TestExternImportArityMismatch(t *testing.T) {
	src := `@import("wasi:foo/bar@0.1.0", "do-thing")
function do_thing(x: i32): i32;
function main(): i32 {
	return do_thing();
}`
	err := checkSource(t, src)
	if err == nil {
		t.Fatal("calling extern @import with wrong arity should error")
	}
}

// E055: a bare statement whose whole expression is a value-returning
// collection mutator silently discards the new collection (the CoW aliasing
// footgun). It must be reassigned, or explicitly discarded with `var _ = …`.
func TestUnusedCollectionResultE055(t *testing.T) {
	// A bare `arr.append(x);` discards the returned array → E055.
	err := checkSource(t, `function main(): i32 {
	var a: i32[] = [1];
	a.append(2);
	return a[0];
}`)
	if err == nil || !strings.Contains(err.Error(), "is unused") {
		t.Errorf("bare append should be E055, got %v", err)
	}
	// Threading the result back is the fix — no error.
	if err := checkSource(t, `function main(): i32 {
	var a: i32[] = [1];
	a = a.append(2);
	return a[0];
}`); err != nil {
		t.Errorf("reassigned append should check, got %v", err)
	}
	// Explicit discard via `var _ = …` is the opt-out — no error.
	if err := checkSource(t, `function main(): i32 {
	var a: i32[] = [1];
	var _ = a.append(2);
	return a.len();
}`); err != nil {
		t.Errorf("var _ discard should check, got %v", err)
	}
	// `arr = arr.with(i, v)` (the value-returning replacement for the removed
	// `arr[i] = v`) is an assignment, not a discarded result — exempt from
	// E055 (and it is not subscript assignment, so no E056 either).
	if err := checkSource(t, `function main(): i32 {
	var a: i32[] = [1, 2];
	a = a.with(0, 9);
	return a[0];
}`); err != nil {
		t.Errorf("a = a.with(...) must check clean, got %v", err)
	}
	// Using the result in a larger expression is fine (not a bare statement).
	if err := checkSource(t, `function main(): i32 {
	var a: i32[] = [1];
	return a.append(2)[0];
}`); err != nil {
		t.Errorf("used append result must not trip E055, got %v", err)
	}
}

// E056: array elements are immutable after construction — the subscript
// counterpart of E048 (field immutability). `arr[i] = v` is rejected; the
// replacement is the value-returning `arr = arr.with(i, v)`. This completes
// the immutable-data surface (docs/PURE-COLLECTION-API-PLAN.md §3a).
func TestArrayElementImmutabilityE056(t *testing.T) {
	// Plain subscript assignment is rejected.
	err := checkSource(t, `function main(): i32 {
	var a: i32[] = [1, 2, 3];
	a[0] = 9;
	return a[0];
}`)
	if err == nil || !strings.Contains(err.Error(), "subscripts are read-only after construction") {
		t.Errorf("expected E056 for subscript assignment, got: %v", err)
	}
	// Compound subscript assignment (`arr[i] += v` desugars to `arr[i] = arr[i] + v`).
	err = checkSource(t, `function main(): i32 {
	var a: i32[] = [1, 2, 3];
	a[1] += 5;
	return a[1];
}`)
	if err == nil || !strings.Contains(err.Error(), "subscripts are read-only after construction") {
		t.Errorf("expected E056 for compound subscript assignment, got: %v", err)
	}
	// The replacement form checks clean.
	if err := checkSource(t, `function main(): i32 {
	var a: i32[] = [1, 2, 3];
	a = a.with(0, 9);
	return a[0];
}`); err != nil {
		t.Errorf("a = a.with(...) must check clean, got: %v", err)
	}
}

// E057: Cell[T] is only allowed for a cycle-free T — a scalar or a
// `string`. A composite / reference element type could reconstruct a
// reference cycle, which the immutable-data model forbids
// (docs/CELL-TYPE-PLAN.md, docs/RC-STRINGS-PLAN.md).
func TestCellElemTypeE057(t *testing.T) {
	// Cell[i32] — scalar, fine. cell_new infers T; get/set type-check.
	if err := checkSource(t, `function main(): i32 {
	var c: Cell[i32] = cell_new(0);
	c.set(c.get() + 1);
	return c.get();
}`); err != nil {
		t.Errorf("Cell[i32] should check, got %v", err)
	}
	// Cell[string] — string is cycle-free (a buffer of bytes, references no
	// other value) and its owning slot is rc-tracked, so it's allowed.
	if err := checkSource(t, `
import "std/string";
function main(): i32 {
	var c: Cell[string] = cell_new("x");
	c.set("yy");
	return c.get().len();
}`); err != nil {
		t.Errorf("Cell[string] should check, got %v", err)
	}
	// Inferred Cell[string] from the cell_new arg — also allowed.
	if err := checkSource(t, `
import "std/string";
function main(): i32 { var c = cell_new("x"); return c.get().len(); }`); err != nil {
		t.Errorf("inferred Cell[string] should check, got %v", err)
	}
	// Cell[Point] (struct) — a composite type can form a cycle: rejected.
	err := checkSource(t, `struct Point { x: i32 }
function main(): i32 { var c: Cell[Point] = cell_new(Point { x: 1 }); return 0; }`)
	if err == nil || !strings.Contains(err.Error(), "must be a scalar") {
		t.Errorf("Cell[Point] should be E057, got %v", err)
	}
	// Cell[i32[]] (array) — also a reference/composite type: rejected.
	err = checkSource(t, `function main(): i32 { var c: Cell[i32[]] = cell_new([1, 2]); return 0; }`)
	if err == nil || !strings.Contains(err.Error(), "must be a scalar") {
		t.Errorf("Cell[i32[]] should be E057, got %v", err)
	}
}

// The annotation form of E057 (`Cell[T]` in a field / param / var / return
// position) must anchor at the annotation's use site, not the synthesized
// builtin `Cell` decl (whose position is 0:0 — which diag.Format renders
// without the `error[E057]` prefix, hiding the code from consumers like the
// self-host checker differential). Pins the position and the code prefix
// for each annotation position.
func TestCellElemTypeE057AnnotationPosition(t *testing.T) {
	cases := []struct {
		name, src string
		line, col int
	}{
		{"field", "struct Point { x: i32 }\nstruct Holder {\n    c: Cell[Point],\n}\nfunction main(): i32 { return 0; }", 3, 5},
		{"param", "struct Point { x: i32 }\nfunction f(c: Cell[Point]): i32 { return 0; }\nfunction main(): i32 { return 0; }", 2, 12},
		{"var", "struct Point { x: i32 }\nfunction main(): i32 {\n    var c: Cell[Point] = cell_new(Point { x: 1 });\n    return 0;\n}", 3, 5},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			prog, err := parser.Parse(tc.src)
			if err != nil {
				t.Fatalf("parse: %v", err)
			}
			_, err = Check(prog)
			if err == nil {
				t.Fatalf("%s-position Cell[Point] should be E057, got no error", tc.name)
			}
			var found *Error
			var errs diag.Errors
			if errors.As(err, &errs) {
				for _, e := range errs {
					var ce *Error
					if errors.As(e, &ce) && ce.ErrCode == "E057" {
						found = ce
						break
					}
				}
			} else {
				var ce *Error
				if errors.As(err, &ce) && ce.ErrCode == "E057" {
					found = ce
				}
			}
			if found == nil {
				t.Fatalf("no E057-coded error in: %v", err)
			}
			if found.Pos.Line != tc.line || found.Pos.Col != tc.col {
				t.Errorf("E057 anchored at %d:%d, want %d:%d (the annotation site)",
					found.Pos.Line, found.Pos.Col, tc.line, tc.col)
			}
			// The rendered form must carry the code prefix — a zero
			// position drops it (that was the original bug).
			rendered := diag.Format("", tc.src, found)
			if !strings.Contains(rendered, "error[E057]") {
				t.Errorf("rendered diagnostic missing error[E057] prefix:\n%s", rendered)
			}
		})
	}
}

// An extern @import call with a type-mismatched argument is rejected.
func TestExternImportArgTypeMismatch(t *testing.T) {
	src := `@import("wasi:foo/bar@0.1.0", "do-thing")
function do_thing(x: i32): i32;
function main(): i32 {
	return do_thing(true);
}`
	err := checkSource(t, src)
	if err == nil {
		t.Fatal("calling extern @import with a mistyped argument should error")
	}
}

// TestNamedArgs covers named-argument resolution: valid reorderings check
// clean, and the misuse cases produce the right diagnostics.
func TestNamedArgs(t *testing.T) {
	good := []string{
		`function f(a: i32, b: i32, c: i32): i32 { return a; } function main(): i32 { return f(1, c = 3, b = 2); }`,
		`function f(a: i32, b: i32 = 2): i32 { return a + b; } function main(): i32 { return f(a = 1); }`,
		`function f(a: i32, b: i32 = 2, c: i32 = 3): i32 { return a; } function main(): i32 { return f(1, c = 9); }`,
	}
	for _, src := range good {
		if err := checkSource(t, src); err != nil {
			t.Errorf("%q: unexpected error %v", src, err)
		}
	}

	bad := []struct{ src, want string }{
		{`function f(a: i32, b: i32): i32 { return a; } function main(): i32 { return f(1, z = 2); }`, "no parameter named"},
		{`function f(a: i32, b: i32): i32 { return a; } function main(): i32 { return f(1, a = 2); }`, "duplicate argument"},
		{`function f(a: i32, b: i32): i32 { return a; } function main(): i32 { return f(a = 1, 2); }`, "positional argument after named"},
		{`function f(a: i32, b: i32): i32 { return a; } function main(): i32 { return f(a = 1); }`, "missing argument for parameter"},
	}
	for _, c := range bad {
		err := checkSource(t, c.src)
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%q: want error containing %q, got %v", c.src, c.want, err)
		}
	}
}

// Display spine (#2696): `print` / `write` / `eprint` accept any value whose
// type carries a `to_string(): string` (the Display spine), not just string.
// A non-string argument is rewritten to `arg.to_string()` so it stringifies
// through the trait before reaching the string-only runtime helper.
func TestCheckPrintDisplayAccepted(t *testing.T) {
	// Concrete struct with an inline Display-shaped impl.
	srcStruct := `trait Display { function to_string(self: Self): string; }
struct Point { x: i32, y: i32 }
impl Display for Point { function to_string(self: Self): string { return "p"; } }
function main(): i32 {
    var p: Point = Point { x: 1, y: 2 };
    print(p);
    write(p);
    eprint(p);
    return 0;
}`
	if err := checkSource(t, srcStruct); err != nil {
		t.Errorf("print(struct: Display) should typecheck: %v", err)
	}
	// A plain string argument still type-checks unchanged.
	srcStr := `function main(): i32 { print("hi"); write("x"); eprint("y"); return 0; }`
	if err := checkSource(t, srcStr); err != nil {
		t.Errorf("print(string) should still typecheck: %v", err)
	}
}

// Bound-driven inference (#2691): a type parameter that appears ONLY inside
// another parameter's parametrised-trait bound (`count[T, I: Iterator[T]]`)
// is pinned not by an argument or the result but by the impl the bound
// resolves to. Once `I` is inferred (here `RangeIter`), the bound `Iterator[T]`
// unifies against `RangeIter`'s `impl Iterator[i32]` to bind `T = i32`.
// Before this, the checker reported E040 (could not infer type parameter T).
func TestCheckBoundDrivenInference(t *testing.T) {
	src := `trait Iterator[T] { function next(self: Self): Option[(T, Self)]; }
struct RangeIter { cur: i32, end: i32 }
impl Iterator[i32] for RangeIter { function next(self: Self): Option[(i32, Self)] { if (self.cur >= self.end) { return None; } return Some((self.cur, RangeIter { cur: self.cur + 1, end: self.end })); } }
function last[T, I: Iterator[T]](it: I, dflt: T): T { var acc = dflt; var cur = it; var go = true; while (go) { match (cur.next()) { Some(t) => { acc = t.0; cur = t.1; }, None => { go = false; }, } } return acc; }
function main(): i32 { return last(RangeIter { cur: 0, end: 5 }, -1); }`
	if err := checkSource(t, src); err != nil {
		t.Errorf("bound-driven inference of T from the impl should typecheck: %v", err)
	}
}

// A bounded generic `T: Display` may forward its parameter straight to
// `print`; the trait bound supplies the `to_string` the rewrite needs.
func TestCheckPrintDisplayGeneric(t *testing.T) {
	src := `trait Display { function to_string(self: Self): string; }
function show[T: Display](v: T): void { print(v); }
struct Point { x: i32 }
impl Display for Point { function to_string(self: Self): string { return "p"; } }
function main(): i32 { show(Point { x: 1 }); return 0; }`
	if err := checkSource(t, src); err != nil {
		t.Errorf("print(v: T) under T: Display should typecheck: %v", err)
	}
}

// A value whose type has no `to_string` is rejected with a Display-specific
// diagnostic, and an unbounded generic parameter is rejected the same way
// (no trait bound supplies the method).
func TestCheckPrintNonDisplayRejected(t *testing.T) {
	cases := []struct{ src, want string }{
		{`struct Point { x: i32 }
function main(): i32 { var p: Point = Point { x: 1 }; print(p); return 0; }`,
			"does not implement `Display`"},
		{`trait Display { function to_string(self: Self): string; }
function show[T](v: T): void { print(v); }
function main(): i32 { return 0; }`,
			"does not implement `Display`"},
	}
	for _, c := range cases {
		err := checkSource(t, c.src)
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%q: want error containing %q, got %v", c.src, c.want, err)
		}
	}
}

// Block-expressions (slice 1): an `if`-expr branch with leading
// statements + a trailing value type-checks to the tail's type; block
// locals don't leak; a value-less block in value position errors E061;
// branch-tail types unify (or mismatch → E031).
func TestBlockExprChecker(t *testing.T) {
	// Tail type flows: `if (c) { var k = e + 1; k } else { 0 }` is i32.
	if err := checkSource(t, `function main(): i32 {
		var e = 5;
		var x: i32 = if (e > 0) { var k = e + 1; k } else { 0 };
		return x;
	}`); err != nil {
		t.Errorf("if-block tail-type: unexpected error: %v", err)
	}

	// String tails unify across branches: one branch `{ ...; "a" }`, the
	// other a bare `"b"` → string.
	if err := checkSource(t, `function main(): i32 {
		var t = 0;
		var label: string = if (t == 0) { var s = "a"; s } else { "b" };
		return label.len();
	}`); err != nil {
		t.Errorf("string-tail unification: unexpected error: %v", err)
	}

	// match-arm block-expr tail type flows.
	if err := checkSource(t, `function main(): i32 {
		var tag = 0;
		var r: i32 = match (tag) { 0 => { var s = tag + 5; s }, _ => 99 };
		return r;
	}`); err != nil {
		t.Errorf("match-arm block tail: unexpected error: %v", err)
	}
}

// A block-shaped `defer { … }` / `errdefer { … }` action (#5153) is
// value-less by design — its result is discarded — so it must NOT trip the
// E061 "block-expression has no trailing value" check that a value-position
// void block does. The exemption is scoped to the immediate defer action:
// a value-position void block anywhere else still errors E061.
func TestCheckDeferBlockVoidAction(t *testing.T) {
	// defer / errdefer with a value-less block action: clean.
	if err := checkSource(t, `function f(): Result[i32, i32] {
		var x: i32 = 0;
		defer { x = x + 1; }
		errdefer { x = x + 2; }
		return Ok(x);
	}`); err != nil {
		t.Errorf("defer/errdefer void block action: unexpected error: %v", err)
	}

	// A value-less block in a genuine value position still errors E061 — the
	// exemption must not leak beyond the immediate defer action.
	err := checkSource(t, `function main(): i32 {
		var y: i32 = { var z = 1; };
		return y;
	}`)
	if err == nil || !strings.Contains(err.Error(), "no trailing value") {
		t.Errorf("value-position void block should still error E061, got: %v", err)
	}
}

// A value-position block whose statements always exit early (`return` /
// `break` / `continue`) has type `never` (bottom), NOT `void`: it is
// assignable to / unifies with any type, so the no-tail forms below
// type-check instead of hitting E061 / E003 / E031 (#4522).
func TestBlockExprCheckerNeverDiverges(t *testing.T) {
	cases := []struct {
		name string
		src  string
	}{
		{
			// General value-position block with no tail: both paths return.
			"general-block-all-return",
			`function f(n: i32): i32 {
				var x: i32 = { if (n < 0) { return 1; } return 2; };
				return x;
			}
			function main(): i32 { return f(5); }`,
		},
		{
			// if-expr with both arms divergent → the whole if is `never`,
			// assignable to i32.
			"if-expr-both-arms-diverge",
			`function f(n: i32): i32 {
				var x: i32 = if (n < 0) { return 1; } else { return 2; };
				return x;
			}
			function main(): i32 { return f(5); }`,
		},
		{
			// match-expr: a divergent arm unifies with a value arm — the
			// result type comes from the value arm.
			"match-arm-diverges",
			`function f(n: i32): i32 {
				var x: i32 = match (n) { 0 => { return 100; }, _ => { n * 2 } };
				return x;
			}
			function main(): i32 { return f(5); }`,
		},
		{
			// No annotation: `never` is inferred, no missing-annotation error.
			"no-annotation",
			`function f(n: i32): i32 {
				var x = { if (n < 0) { return 1; } return 2; };
				return x;
			}
			function main(): i32 { return f(5); }`,
		},
		{
			// break/continue inside a value block in a loop: the block is
			// `never`, assignable to the local's type.
			"break-continue-in-loop",
			`function main(): i32 {
				var s: i32 = 0; var i: i32 = 0;
				while (i < 5) {
					i = i + 1;
					var d: i32 = { if (i == 4) { break; } if (i == 2) { continue; } i };
					s = s + d;
				}
				return s;
			}`,
		},
	}
	for _, c := range cases {
		if err := checkSource(t, c.src); err != nil {
			t.Errorf("%s: unexpected error: %v", c.name, err)
		}
	}
}

func TestBlockExprCheckerErrors(t *testing.T) {
	cases := []struct {
		name string
		src  string
		want string
	}{
		{
			"local-does-not-escape",
			`function main(): i32 {
				var x: i32 = if (true) { var k = 1; k } else { 0 };
				return k;
			}`,
			"undefined identifier",
		},
		{
			"value-less-block-in-value-position",
			`function main(): i32 {
				var x: i32 = if (true) { var k = 1; } else { 0 };
				return x;
			}`,
			"block-expression has no trailing value",
		},
		{
			"mismatched-branch-tails",
			`function main(): i32 {
				var x = if (true) { var k = 1; k } else { var s = "x"; s };
				return 0;
			}`,
			"branches differ",
		},
	}
	for _, c := range cases {
		err := checkSource(t, c.src)
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: want error containing %q, got %v", c.name, c.want, err)
		}
	}
}

// TestDynMethodCallNoSelfParamNoPanic guards against a checker crash on a
// `dyn Trait` method call where the trait method signature has no leading
// `self` param. checkDynMethodCall unconditionally sliced `tm.Params[1:]` to
// "drop the self receiver", which panicked (`slice bounds out of range [1:0]`)
// on the common `function area(): i32;` form and silently dropped the first
// real argument of a method that had params but no explicit self. The call must
// now type-check (resolving the no-arg method) rather than panic.
func TestDynMethodCallNoSelfParamNoPanic(t *testing.T) {
	// No explicit self in the trait method; calling it through `dyn` must not
	// panic. (Whether the bare receiver method conforms is a separate, cleanly
	// reported concern; here we only require the checker to survive.)
	src := `trait Sh { function area(): i32; }
struct Sq { s: i32 }
function (x: Sq) area(): i32 { return x.s * x.s; }
function via(sh: dyn Sh): i32 { return sh.area(); }
function main(): i32 { return 0; }`
	// Must return normally (nil or a diagnostic) — the bug made Check panic.
	_ = checkSource(t, src)

	// A trait method WITH a param but no explicit self: the call must see the
	// real argument count (the old `[1:]` dropped it, reporting "0 arguments").
	srcParam := `trait Sh { function scaled(f: i32): i32; }
struct Sq { s: i32 }
function (x: Sq) scaled(f: i32): i32 { return x.s * f; }
function via(sh: dyn Sh): i32 { return sh.scaled(); }
function main(): i32 { return 0; }`
	err := checkSource(t, srcParam)
	if err == nil || !strings.Contains(err.Error(), "expects 1 argument") {
		t.Errorf("no-self dyn method with a param: want a 1-argument arity error, got %v", err)
	}
}

// TestDynMethodCallProperPattern confirms the canonical `dyn Trait` shape
// (explicit `self: Self` + an `impl` block) still type-checks after the
// self-stripping fix, including a method that takes an extra argument.
func TestDynMethodCallProperPattern(t *testing.T) {
	for _, src := range []string{
		`trait Shape { function area(self: Self): i32; }
struct Circle { r: i32 }
impl Shape for Circle { function area(self: Self): i32 { return self.r * self.r; } }
function describe(s: dyn Shape): i32 { return s.area(); }
function main(): i32 { return describe(Circle { r: 5 }); }`,
		`trait Shape { function scaled(self: Self, f: i32): i32; }
struct Circle { r: i32 }
impl Shape for Circle { function scaled(self: Self, f: i32): i32 { return self.r * f; } }
function describe(s: dyn Shape): i32 { return s.scaled(3); }
function main(): i32 { return describe(Circle { r: 5 }); }`,
	} {
		if err := checkSource(t, src); err != nil {
			t.Errorf("proper dyn pattern: unexpected error %v", err)
		}
	}
}

// TestSliceEscapeRejected (E063): returning a `[T]` slice that views
// function-local storage is a use-after-free — the backing array is
// reclaimed when the frame unwinds. The checker rejects the cases it
// can prove are local; see docs/LANGUAGE-DIRECTION.md's slice lifetime
// contract.
func TestSliceEscapeRejected(t *testing.T) {
	for _, src := range []string{
		// slice of a locally-declared owned array
		`function f(): [i32] { var xs: i32[] = [1, 2, 3]; return xs[0:2]; }`,
		// slice of an array literal
		`function f(): [i32] { return [1, 2, 3][0:2]; }`,
		// slice bound to a local, then returned
		`function f(): [i32] { var xs: i32[] = [1, 2, 3]; var s = xs[0:2]; return s; }`,
		// sub-slice of a local slice that views a local array
		`function f(): [i32] { var xs: i32[] = [1, 2, 3]; var s = xs[0:3]; return s[0:2]; }`,
	} {
		err := checkSource(t, src)
		if err == nil {
			t.Errorf("%q: expected E063, got nil", src)
			continue
		}
		if !strings.Contains(err.Error(), "dangling view") || !strings.Contains(err.Error(), "function-local storage") {
			t.Errorf("%q: want dangling-view error, got %v", src, err)
		}
		if !hasCode(err, "E063") {
			t.Errorf("%q: want diagnostic stamped E063, got %v", src, err)
		}
	}
}

// TestSliceEscapeAllowed: slices the checker can't prove are local must
// not be flagged. Slices of a parameter / receiver stay valid as long as
// the caller's owner does; string slices view param-backed storage (the
// P2 flip) and materialise into owning sinks explicitly; returning the
// owned array itself is a move, not a view.
func TestSliceEscapeAllowed(t *testing.T) {
	for _, src := range []string{
		// slice of a parameter — caller owns the backing array
		`function f(xs: i32[]): [i32] { return xs[0:2]; }`,
		// slice of a parameter, bound through a local first
		`function f(xs: i32[]): [i32] { var s = xs[0:2]; return s; }`,
		// string slice is a view since the P2 flip: returning it as a
		// str of a param-backed source is fine, and an owning string
		// return takes an explicit materialisation
		`function f(s: string): str { return s[0:2]; }`,
		`function f(s: string): string { return s[0:2] + ""; }`,
		// returning the owned array itself is a move
		`function f(): i32[] { var xs: i32[] = [1, 2, 3]; return xs; }`,
		// receiver-backed slice (element-polymorphic method): caller owns
		// the receiver, so the view is valid.
		`function (xs: T[]) head(): [T] { return xs[0:2]; }
function main(): i32 { var a: i32[] = [1,2,3]; return a.head()[0]; }`,
	} {
		if err := checkSource(t, src); err != nil {
			t.Errorf("%q: unexpected error %v", src, err)
		}
	}
}

// hasCode reports whether err (or any aggregated sub-error) carries the
// given stable diagnostic code.
func hasCode(err error, code string) bool {
	type coded interface{ Code() string }
	var walk func(error) bool
	walk = func(e error) bool {
		if e == nil {
			return false
		}
		if c, ok := e.(coded); ok && c.Code() == code {
			return true
		}
		if errs, ok := e.(diag.Errors); ok {
			for _, sub := range errs {
				if walk(sub) {
					return true
				}
			}
		}
		return false
	}
	return walk(err)
}

// TestStrViewBorrowAllowed: the `str` view type (#4813). An owned `string`
// freely borrows INTO a `str` (var init, argument, array element); a `str`
// flows into a `string` PARAMETER (params are borrowed by default); the
// string method surface (builtin .len()) dispatches on a view receiver.
func TestStrViewBorrowAllowed(t *testing.T) {
	for _, src := range []string{
		// string → str var init (borrow)
		`function f() { var s: string = "x"; var v: str = s; }`,
		// string literal → str param
		`function g(v: str): i32 { return v.len(); } function f(): i32 { return g("abc"); }`,
		// str → string param (borrowed position)
		`function t(o: string): i32 { return o.len(); } function f(v: str): i32 { return t(v); }`,
		// str[] literal from string elements; element method call
		`function f(): i32 { var vs: str[] = ["ab"]; return vs[0].len(); }`,
		// str → str passthrough return
		`function f(v: str): str { return v; }`,
	} {
		if err := checkSource(t, src); err != nil {
			t.Errorf("%q: expected OK, got %v", src, err)
		}
	}
}

// TestStrViewPromoteRejected: a `str` never silently promotes to an owned
// `string` — the owning sinks (var init, return) must materialise a fresh
// copy via .to_owned(). This is the type-level guard for the #4294 bug
// class: an owned sink may be freed by the RC passes, and freeing a view
// corrupts the source.
func TestStrViewPromoteRejected(t *testing.T) {
	for _, src := range []string{
		// str → string var init
		`function f(v: str) { var o: string = v; }`,
		// str → string return
		`function f(v: str): string { return v; }`,
	} {
		err := checkSource(t, src)
		if err == nil {
			t.Errorf("%q: expected rejection, got nil", src)
			continue
		}
		if !strings.Contains(err.Error(), "str") {
			t.Errorf("%q: want the str/string mismatch surfaced, got %v", src, err)
		}
	}
}

// TestStrEscapeRejected: E065 (#4814) — a `str` view of function-local
// string storage must not escape via return; the local's box is reclaimed
// at exit and the escaped view dangles (the #4294 corruption class).
func TestStrEscapeRejected(t *testing.T) {
	for _, src := range []string{
		// local string returned as a str (direct borrow escape)
		`function mk(): string { return "a" + "b"; } function f(): str { var s: string = mk(); return s; }`,
		// view of a local, bound then returned
		`function mk(): string { return "a" + "b"; } function f(): str { var s: string = mk(); var v: str = s; return v; }`,
		// chained view-of-view of a local
		`function mk(): string { return "a" + "b"; } function f(): str { var s: string = mk(); var v: str = s; var w: str = v; return w; }`,
	} {
		err := checkSource(t, src)
		if err == nil {
			t.Errorf("%q: expected E065, got nil", src)
			continue
		}
		if !hasCode(err, "E065") {
			t.Errorf("%q: want diagnostic stamped E065, got %v", src, err)
		}
		if !strings.Contains(err.Error(), "dangling view") {
			t.Errorf("%q: want dangling-view error, got %v", src, err)
		}
	}
}

// TestStrEscapeAllowed: param-sourced and 'static (literal) views outlive
// the function, so returning them is fine; an owned .to_owned() result is
// a move, not a view.
func TestStrEscapeAllowed(t *testing.T) {
	for _, src := range []string{
		// param passthrough
		`function f(v: str): str { return v; }`,
		// param through a local view binding
		`function f(p: str): str { var v: str = p; return v; }`,
		// literal directly and via a binding ('static)
		`function f(): str { return "s"; }`,
		`function f(): str { var v: str = "s"; return v; }`,
		// owned fresh call result (a move; call results are not chased)
		`function mk(): string { return "a" + "b"; } function f(): str { return mk(); }`,
	} {
		if err := checkSource(t, src); err != nil {
			t.Errorf("%q: expected OK, got %v", src, err)
		}
	}
}

// TestStrSliceProducesView: the #4813 P2 producer flip — s[a:b] on an owned
// `string` yields a `str` sub-view of its bytes, not a fresh owned string.
// A slice binds to `str`, returns as a view of a caller-owned param, feeds
// the read surface (.len(), comparison), and materialises into owning sinks
// only through an explicit copy.
func TestStrSliceProducesView(t *testing.T) {
	for _, src := range []string{
		`function f() { var s: string = "abcd"; var v: str = s[1:3]; }`,
		`function f(s: string): str { return s[1:3]; }`,
		`function f() { var s: string = "abcd"; var o: string = s[1:3] + ""; }`,
		`function f(s: string): i32 { return s[1:3].len(); }`,
		`function f(s: string): boolean { return s[0:2] == "ab"; }`,
		// a view prints as its bytes (print/write/eprint accept str)
		`function f(s: string) { print(s[1:3]); }`,
		`function f(v: str) { print(v); }`,
	} {
		if err := checkSource(t, src); err != nil {
			t.Errorf("%q: expected OK, got %v", src, err)
		}
	}
	for _, src := range []string{
		// slice → string var init (owning sink, no materialisation)
		`function f() { var s: string = "abcd"; var o: string = s[1:3]; }`,
		// slice → string return
		`function f(s: string): string { return s[1:3]; }`,
	} {
		err := checkSource(t, src)
		if err == nil {
			t.Errorf("%q: expected rejection, got nil", src)
			continue
		}
		if !strings.Contains(err.Error(), "str") {
			t.Errorf("%q: want the str/string mismatch surfaced, got %v", src, err)
		}
	}
}

// TestStrSliceEscapeRejected: E065 through the slice producer — s[a:b]
// views s's bytes, so returning a slice of a function-LOCAL string (bare or
// through a `str` binding) escapes storage that is reclaimed at exit.
// Slicing a parameter stays fine (caller-owned backing).
func TestStrSliceEscapeRejected(t *testing.T) {
	for _, src := range []string{
		`function mk(): string { return "a" + "b"; } function f(): str { var s: string = mk(); return s[0:1]; }`,
		`function mk(): string { return "a" + "b"; } function f(): str { var s: string = mk(); var v: str = s[0:1]; return v; }`,
	} {
		err := checkSource(t, src)
		if err == nil {
			t.Errorf("%q: expected E065, got nil", src)
			continue
		}
		if !hasCode(err, "E065") {
			t.Errorf("%q: want diagnostic stamped E065, got %v", src, err)
		}
	}
}

// TestStrViewOwnParamRejected: an `own` (consuming) parameter takes
// ownership of its argument — the callee frees it. A `str` view must never
// be freed by its holder, so the borrowed-position carve-out does not
// apply to `own` params (the #4813 tightening): lending a view to a
// consumer is the #4294 corruption shape. The plain borrowed param in the
// same program stays accepting.
func TestStrViewOwnParamRejected(t *testing.T) {
	src := `function consume(own x: string): i32 { return x.len(); }
function borrow(x: string): i32 { return x.len(); }
function f(v: str): i32 { return consume(v) + borrow(v); }`
	err := checkSource(t, src)
	if err == nil {
		t.Fatalf("expected the own-param str argument to be rejected, got nil")
	}
	if !hasCode(err, "E038") {
		t.Errorf("want E038 on the own-param position, got %v", err)
	}
	// The borrowed position must NOT be flagged: exactly one str mismatch.
	if n := strings.Count(err.Error(), "expected string, got str"); n != 1 {
		t.Errorf("want exactly 1 str-arg mismatch (own position only), got %d in %v", n, err)
	}
}

// TestPointerReinterpretFromI32Rejected (E069, #5053) locks the rule that a
// 32-bit-only value (`i32` / `u32`) reinterpreted as a pointer-shaped type
// (`string`, an array, or a struct) via `as` is a truncation footgun — the
// high 32 bits of the address were lost when the value became i32, so the
// recovered pointer is corrupt once the heap exceeds 4 GiB. A `usize` source
// carries the full width and stays allowed (the stdlib's raw-block-to-handle
// promotion), and a genuine numeric conversion (`i32 as usize`) is unaffected.
func TestPointerReinterpretFromI32Rejected(t *testing.T) {
	mustCode := func(src, code string) {
		t.Helper()
		err := checkSource(t, src)
		if err == nil {
			t.Errorf("expected %s for %q, got none", code, src)
			return
		}
		if !hasCode(err, code) {
			t.Errorf("%q: want %s, got %v", src, code, err)
		}
	}
	mustOK := func(src string) {
		t.Helper()
		if err := checkSource(t, src); err != nil {
			t.Errorf("expected no error for %q, got: %v", src, err)
		}
	}
	// i32 / u32 reinterpreted as a pointer-shaped type — rejected.
	mustCode(`function f(k: i32): string { return k as string; } function main(): i32 { return 0; }`, "E069")
	mustCode(`function f(k: i32): i32[] { return k as i32[]; } function main(): i32 { return 0; }`, "E069")
	mustCode(`struct P { x: i32 } function f(k: i32): P { return k as P; } function main(): i32 { return 0; }`, "E069")
	mustCode(`function f(k: u32): string { return k as string; } function main(): i32 { return 0; }`, "E069")
	// usize source is pointer-width — the honest promotion, still allowed.
	mustOK(`function f(k: usize): string { return k as string; } function main(): i32 { return 0; }`)
	mustOK(`function f(k: usize): i32[] { return k as i32[]; } function main(): i32 { return 0; }`)
	// A plain numeric conversion (not a pointer reinterpret) is unaffected.
	mustOK(`function main(): i32 { var a: i32 = 5; var b: usize = a as usize; return b as i32; }`)
	// The forward pointer->i32 narrowing (the __memcpy escape hatch) is not
	// this rule's target and stays allowed.
	mustOK(`function main(): i32 { var s: string = "x"; return s as i32; }`)
}
