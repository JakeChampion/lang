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

// Phase 5 of docs/PRELUDE-TO-MODULES.md retired the auto-injected
// prelude: stdlib methods are no longer in scope unless their module
// is imported. A program that calls `.split` without `import
// "std/string";` should get a clean type error rather than silently
// resolving against a magic prelude.
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
				acc = acc.push(1);
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

// `arr.push(v)` is a generic method on T[]. The receiver's Elem
// flows into the registered ParamType("T") signature, so the
// argument and return types substitute correctly.
func TestArrayPushTypechecks(t *testing.T) {
	for _, src := range []string{
		`function f(): i32 { var xs: string[] = []; xs = xs.push("a"); return xs.len(); }`,
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
	src := `function f(): i32 { var xs: string[] = []; xs = xs.push(1); return xs.len(); }`
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
		// switch with no default: an unmatched tag falls through
		`function sw(n: i32): i32 {
			switch (n) { case 0: return 0; case 1: return 1; }
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
		// switch with default, every arm returns
		`function sw(n: i32): i32 {
			switch (n) { case 0: return 0; default: return 1; }
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
// plus the canonical hand-registered `Array.push` which
// must continue to work despite being skipped by the
// auto-discovery loop.
func TestArrayMethodDispatchAutoDiscovers(t *testing.T) {
	// Post-flip there's no auto-prelude, so the discoverable
	// `__method_Array_*` functions are supplied inline here — the
	// same shape std/array ships. Auto-discovery should register
	// each as an `Array.<name>` method without a hand-written
	// entry in checker.go, while the synthetic `Array.push`
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
		// Synthetic, registered by hand (IR-intercepted).
		{"Array.push", "__method_Array_push"},
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
function main(): i32 { return 0; }`, "only Eq, Display, and Ord are derivable"},
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
function main(): i32 { return 0; }`, "only Eq, Display, and Ord are derivable"},
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
