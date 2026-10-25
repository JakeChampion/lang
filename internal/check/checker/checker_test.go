package checker

import (
	"context"
	"errors"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/check/constfold"
	"github.com/jakechampion/lang/internal/pkg/modload"
	"github.com/jakechampion/lang/internal/syntax/ast"
	"github.com/jakechampion/lang/internal/syntax/diag"
	"github.com/jakechampion/lang/internal/syntax/parser"
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

func TestUnderInferredGenericStructLitIsE040(t *testing.T) {
	const decls = "struct Stack[T] { items: T[] }\nstruct Tagged[T] { n: i32 }\n"
	bad := []string{
		`function main(): i32 { let s = Stack { items: [] }; return 0; }`,
		`function main(): i32 { let t = Tagged { n: 1 }; return t.n; }`,
	}
	for _, body := range bad {
		err := checkSource(t, decls+body)
		if err == nil {
			t.Errorf("expected E040 for %s, got none", body)
			continue
		}
		if !strings.Contains(err.Error(), "could not infer type parameter T for struct") {
			t.Errorf("error %q is not the E040 inference message\nsrc: %s", err.Error(), body)
		}
		if strings.Contains(err.Error(), "Stack__") || strings.Contains(err.Error(), "Tagged__") {
			t.Errorf("error %q leaks a mangled name\nsrc: %s", err.Error(), body)
		}
	}

	ok := []string{
		`function wrap[T](x: T): i32 { let s = Stack { items: [x] }; return s.items.len(); }
function main(): i32 { return wrap(7); }`,
		`function wrap[U](x: U): i32 { let s: Stack[U] = Stack { items: [x] }; return s.items.len(); }
function main(): i32 { return wrap(7); }`,
		`function main(): i32 { let s = Stack[i32] { items: [] }; return s.items.len(); }`,
		`function main(): i32 { let s: Stack[i32] = Stack { items: [] }; return s.items.len(); }`,
	}
	for _, src := range ok {
		if err := checkSource(t, decls+src); err != nil {
			t.Errorf("should type-check, got: %v\nsrc: %s", err, src)
		}
	}
}

func TestGenericFuncAsValueIsE040(t *testing.T) {
	const decls = "function skip[T](s: i32, a: T): T { return a; }\n"
	bad := []string{
		`function main(): i32 { let f = skip; return 0; }`,
		`function main(): i32 { let fs = [skip]; return 0; }`,
	}
	for _, body := range bad {
		err := checkSource(t, decls+body)
		if err == nil {
			t.Errorf("expected E040 for:\n%s", body)
			continue
		}
		if !strings.Contains(err.Error(), "cannot be used as a value") {
			t.Errorf("error %q is not the E040 generic-as-value message\nsrc: %s", err.Error(), body)
		}
		if strings.Contains(err.Error(), "compiler bug") {
			t.Errorf("error %q is still an internal error\nsrc: %s", err.Error(), body)
		}
	}

	ok := []string{
		`function main(): i32 { return skip(1, 7); }`,
		`function inner[T](acc: T, f: (i32, T) => T): T { return f(1, acc); }
function outer[T](acc: T): T { return inner(acc, (n: i32, a: T) => skip(n, a)); }
function main(): i32 { return outer(7); }`,
		`function apply(v: i32, skip: (i32) => i32): i32 { return skip(v); }
function main(): i32 { return apply(7, (x: i32) => x + 1); }`,
	}
	for _, src := range ok {
		if err := checkSource(t, decls+src); err != nil {
			t.Errorf("should type-check, got: %v\nsrc: %s", err, src)
		}
	}
}

func TestCallTypeArgNamingUserType(t *testing.T) {
	const decls = `struct Box { n: i32 }
enum Shape { Sq(i32) }
function id[T](a: T): T { return a; }
function k(x: i32): i32 { return x; }
`
	ok := []string{
		`function main(): i32 { let b = Box { n: 5 }; return id[Box](b).n; }`,
		`function main(): i32 { match (id[Shape](Sq(2))) { Sq(n) => { return n; } } }`,
		`function f[T](a: T): T { return id[T](a); }
function main(): i32 { return f(3); }`,
		`function apply(id: ((i32) => i32)[], i: i32): i32 { return id[i](3); }
function main(): i32 { return apply([k], 0); }`,
	}
	for _, src := range ok {
		if err := checkSource(t, decls+src); err != nil {
			t.Errorf("should type-check, got: %v\nsrc: %s", err, src)
		}
	}
	bad := map[string]string{
		`function main(): i32 { return id[Box](5).n; }`:                            "expected Box, got i32",
		`function main(): i32 { return id[Nope](5); }`:                             "cannot be used as a value",
		`function main(): i32 { let x = 0; return id[x](5); }`:                     "cannot be used as a value",
		`function main(): i32 { let b = Box { n: 1 }; return id[Box, Box](b).n; }`: "takes 1 type argument",
	}
	for src, want := range bad {
		err := checkSource(t, decls+src)
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("want an error containing %q, got %v\nsrc: %s", want, err, src)
		}
	}
}

func TestMethodTypeArgsNameTheMethodsOwnParams(t *testing.T) {
	const decls = `enum Box[T, E] { Full(T), Blank(E) }
struct Pair[T, E] { a: T, b: E }
function (b: Box[T, E]) pair[U](other: Box[U, E]): Box[U, E] { return other; }
function (p: Pair[T, E]) pick[U](x: U): U { return x; }
function take(b: Box[i32, string]): i32 {
    match (b) {
        Full(n) => { return n; },
        Blank(s) => { return s.len(); }
    }
}
`
	ok := []string{
		`function main(): i32 { let b: Box[i32, string] = Full(5); return take(b.pair(Full(9))); }`,
		`function main(): i32 { let b: Box[i32, string] = Full(5); return take(b.pair[i32](Full(9))); }`,
		`function main(): i32 { let b: Box[i32, string] = Full(5); return take(b.pair[i32, string, i32](Full(9))); }`,
		`function main(): i32 { let p: Pair[i32, string] = Pair[i32, string] { a: 1, b: "x" }; return p.pick[i32](5); }`,
		`function main(): i32 { let p: Pair[i32, string] = Pair[i32, string] { a: 1, b: "x" }; return p.pick(5); }`,
	}
	for _, src := range ok {
		if err := checkSource(t, decls+src); err != nil {
			t.Errorf("should type-check, got: %v\nsrc: %s", err, src)
		}
	}
	widening := `function g(n: i32): Result[i64, i32] { return Ok(n); }
function main(): i32 { match (g(40)) { Ok(v) => { return (v / 8) as i32; }, Err(e) => { return e; } } }`
	if err := checkSource(t, widening); err != nil {
		t.Errorf("an i32 payload widening to its destination's i64 should type-check, got: %v", err)
	}

	badSrc := `function main(): i32 { let b: Box[i32, string] = Full(5); return take(b.pair[string](Full(9))); }`
	err := checkSource(t, decls+badSrc)
	if err == nil {
		t.Fatalf("a written type argument the argument contradicts should not type-check")
	}
	if !strings.Contains(err.Error(), "expected Box[string, string], got Box[i32, string]") {
		t.Errorf("error %q does not name the substituted parameter type", err.Error())
	}
	annotated := `function main(): i32 {
    let b: Box[i32, string] = Full(5);
    let x: Box[string, string] = b.pair[string](Full(9));
    match (x) { Full(s) => { return s.len(); }, Blank(e) => { return e.len(); } }
}`
	if err := checkSource(t, decls+annotated); err == nil {
		t.Errorf("an i32 payload reaching a Box[string, string] should not type-check")
	}
	surplus := []string{
		`function main(): i32 { let p: Pair[i32, string] = Pair[i32, string] { a: 1, b: "x" }; return p.pick[i32, string, i32, f64](5); }`,
		`function main(): i32 { let b: Box[i32, string] = Full(5); return take(b.pair[i32, string, i32, f64](Full(9))); }`,
	}
	for _, src := range surplus {
		err := checkSource(t, decls+src)
		if err == nil {
			t.Errorf("a surplus type argument should not type-check\nsrc: %s", src)
			continue
		}
		if !strings.Contains(err.Error(), "takes 3 type arguments but was given 4") {
			t.Errorf("error %q does not report the written list's arity\nsrc: %s", err.Error(), src)
		}
	}
}

func TestE040CallSiteAdvisesAWritableSpelling(t *testing.T) {
	const decls = `struct Holder { n: i32 }
enum Wrap { A, B }
function (h: Holder) make[T](): T[] { return []; }
function (h: Holder) read_all[T](): T[] { return []; }
function (w: Wrap) pick[T](): T[] { return []; }
function empty[T](): T[] { return []; }
`
	bad := []struct{ body, want string }{
		{`function main(): i32 { let xs = empty(); return 0; }`,
			"could not infer type parameter T for empty — supply it explicitly at the call site (e.g. empty[i32](...))"},
		{`function main(): i32 { let h = Holder { n: 1 }; let xs = h.make(); return 0; }`,
			"could not infer type parameter T for Holder.make — supply it explicitly at the call site (e.g. .make[i32](...))"},
		{`function main(): i32 { let h = Holder { n: 1 }; let xs = h.read_all(); return 0; }`,
			"for Holder.read_all — supply it explicitly at the call site (e.g. .read_all[i32](...))"},
		{`function main(): i32 { let w = A; let xs = w.pick(); return 0; }`,
			"for Wrap.pick — supply it explicitly at the call site (e.g. .pick[i32](...))"},
		{`function main(): i32 { let h = Holder { n: 1 }; let xs = h.make[i32, i32](); return 0; }`,
			"Holder.make takes 1 type argument but was given 2"},
		{`function main(): i32 { return empty[i32, i32]().len(); }`,
			"empty takes 1 type argument but was given 2"},
	}
	for _, tc := range bad {
		err := checkSource(t, decls+tc.body)
		if err == nil {
			t.Errorf("expected E040 for %s, got none", tc.body)
			continue
		}
		if !strings.Contains(err.Error(), tc.want) {
			t.Errorf("error %q does not contain %q\nsrc: %s", err.Error(), tc.want, tc.body)
		}
		if strings.Contains(err.Error(), "__method_") {
			t.Errorf("error %q leaks a mangled name\nsrc: %s", err.Error(), tc.body)
		}
	}

	ok := []string{
		`function main(): i32 { let h = Holder { n: 1 }; let xs = h.make[i32](); return xs.len(); }`,
		`function main(): i32 { let h = Holder { n: 1 }; let xs: i32[] = h.make(); return xs.len(); }`,
		`function main(): i32 { let xs = empty[i32](); return xs.len(); }`,
		`function main(): i32 { let xs: i32[] = empty(); return xs.len(); }`,
	}
	for _, src := range ok {
		if err := checkSource(t, decls+src); err != nil {
			t.Errorf("should type-check, got: %v\nsrc: %s", err, src)
		}
	}
}

func TestAssignmentTargetPinsTypeArgs(t *testing.T) {
	const decls = `struct Holder { n: i32 }
struct Box[T] { items: T[] }
trait Make { function make(): Self; }
impl[T] Make for Box[T] { function make(): Self { return Box { items: [] }; } }
function (h: Holder) fresh[T](): T[] { return []; }
function empty[T](): T[] { return []; }
`
	ok := []string{
		`function main(): i32 { let xs: i32[] = [1]; xs = empty(); return xs.len(); }`,
		`function main(): i32 { let h = Holder { n: 1 }; let xs: i32[] = [1]; xs = h.fresh(); return xs.len(); }`,
		`function main(): i32 { let b: Box[i32] = Box { items: [1] }; b = Box.make(); return b.items.len(); }`,
	}
	for _, body := range ok {
		if err := checkSource(t, decls+body); err != nil {
			t.Errorf("should type-check, got: %v\nsrc: %s", err, body)
		}
	}
	bad := `function main(): i32 { let n: i32 = 1; n = empty(); return n; }`
	if err := checkSource(t, decls+bad); err == nil {
		t.Errorf("expected an error assigning an array to an i32, got none")
	}
}

func TestAssignmentTargetStampsMapNewTypeArgs(t *testing.T) {
	prog, err := parser.Parse(`function main(): i32 { let m: Map[string, i32] = map_new(4); m = map_new(8); return 0; }`)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if _, err := Check(prog); err != nil {
		t.Fatalf("want no diagnostic, got %v", err)
	}
	var calls []*ast.Call
	ast.Walk(prog.Funcs[0].Body, func(n ast.Node) bool {
		if c, ok := n.(*ast.Call); ok {
			if id, ok := c.Callee.(*ast.Ident); ok && id.Name == "map_new" {
				calls = append(calls, c)
			}
		}
		return true
	})
	if len(calls) != 2 {
		t.Fatalf("found %d map_new calls, want 2", len(calls))
	}
	assigned := calls[1]
	if len(assigned.TypeArgs) != 2 {
		t.Fatalf("the assigned map_new's type arguments were not stamped: %v", assigned.TypeArgs)
	}
	if _, isStr := assigned.TypeArgs[0].(ast.StringType); !isStr {
		t.Errorf("key type argument = %v, want string", assigned.TypeArgs[0])
	}
}

func TestStructLitConstructionSiteTypeArgs(t *testing.T) {
	const decls = "struct Box[T] { val: T }\nstruct Stack[T] { items: T[] }\nstruct Point { x: i32 }\n"
	ok := []string{
		`function main(): i32 { let b = Box[i32] { val: 1 }; return b.val; }`,
		`function main(): i32 { let s = Stack[i32] { items: [] }; return s.items.len(); }`,
		`function main(): i32 { let b: Box[i64] = Box[i64] { val: 1 }; return 0; }`,
	}
	for _, body := range ok {
		if err := checkSource(t, decls+body); err != nil {
			t.Errorf("should type-check, got: %v\nsrc: %s", err, body)
		}
	}

	bad := []struct{ body, want string }{
		{`function main(): i32 { let b = Box[i32, string] { val: 1 }; return 0; }`,
			"Box takes 1 type argument but was given 2"},
		{`function main(): i32 { let p = Point[i32] { x: 1 }; return 0; }`,
			"Point takes 0 type arguments but was given 1"},
		{`function main(): i32 { let b: Box[i64] = Box[i32] { val: 1 }; return 0; }`,
			"expected Box[i64], got Box[i32]"},
		{`function main(): i32 { let b = Box[i32] { val: "x" }; return 0; }`,
			`field "val": expected i32, got string`},
	}
	for _, tc := range bad {
		err := checkSource(t, decls+tc.body)
		if err == nil {
			t.Errorf("expected an error for %s, got none", tc.body)
			continue
		}
		if !strings.Contains(err.Error(), tc.want) {
			t.Errorf("error %q does not contain %q\nsrc: %s", err.Error(), tc.want, tc.body)
		}
	}
}

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
	mustErr(`struct Box[T] { v: T } function main(): i32 { let b: Box[i32] = Box { v: "x" }; return b.v; }`,
		`field "v": expected i32, got string`)
	mustErr(`struct Pair[A, B] { a: A, b: B } function main(): i32 { let p: Pair[i32, string] = Pair { a: "x", b: 1 }; return 0; }`,
		`field "a": expected i32, got string`)
	mustOK(`struct Box[T] { v: T } function main(): i32 { let b: Box[i32] = Box { v: 5 }; return b.v; }`)
	mustOK(`struct Box[T] { v: T } function main(): i32 { let b: Box[string] = Box { v: "x" }; return b.v.len(); }`)
	mustOK(`struct Box[T] { v: T } function main(): i32 { let b = Box { v: "x" }; return 0; }`)
	mustErr(`struct Box[T] { v: T } function take(b: Box[string]): i32 { return 0; } function main(): i32 { return take(Box { v: 3 }); }`,
		`field "v": expected string, got i32`)
	mustOK(`struct Box[T] { v: T } function take(b: Box[i64]): i64 { return b.v; } function main(): i32 { let n: i64 = take(Box { v: 5 }); return 0; }`)
	mustOK(`struct Box[T] { v: T } function take[T](b: Box[T]): T { return b.v; } function main(): i32 { return take(Box { v: 3 }); }`)
	mustOK(`struct Box[T] { v: T } function wrap[T](x: T): Box[T] { let b: Box[T] = Box { v: x }; return b; } function main(): i32 { return wrap(3).v; }`)
	mustErr(`struct Box[T] { v: T } function wrap[T](x: T): i32 { let b: Box[T] = Box { v: 3 }; return 0; } function main(): i32 { return wrap(3); }`,
		`field "v": expected T, got i32`)
}

func TestArrayLitElementReadsElementDestination(t *testing.T) {
	pre := `trait C[T] { function c(self: Self): T; } struct A { n: i32 } impl C[i32] for A { function c(self: Self): i32 { return self.n; } } function f[T, I: C[T]](i: I): T { return i.c(); } `
	if err := checkSource(t, pre+`function main(): i32 { let ys: i32[] = [f(A { n: 2 }), 3]; return ys[0]; }`); err != nil {
		t.Errorf("valid element call refused: %v", err)
	}
	err := checkSource(t, pre+`function main(): i32 { let xs: string[] = [f(A { n: 1 })]; return 0; }`)
	if err == nil || !strings.Contains(err.Error(), "the bound requires C[string] (in f)") {
		t.Errorf("want the element's E021 against C[string], got: %v", err)
	}
	if err != nil && strings.Contains(err.Error(), "E003") {
		t.Errorf("the element's bound is the error; no E003 for the var: %v", err)
	}
}

func TestGenericStructLitInfersThroughArrayField(t *testing.T) {
	decls := `struct W[T] { items: T[] } struct P[T] { a: T[], b: T } `
	for _, body := range []string{
		`let y: i64 = 3; let w = W { items: [y, 1] }; let t: i64 = w.items[0];`,
		`let w = W { items: [1, 2] }; let t: i32 = w.items[0];`,
		`let w = W { items: [1.5] }; let t: f64 = w.items[0];`,
		`let p = P { a: [], b: 5 }; let t: i32 = p.b;`,
		`let p = P { a: [], b: "x" }; let n: i32 = p.a.len();`,
		`let w: W[i64] = W { items: [] };`,
	} {
		if err := checkSource(t, decls+`function main(): i32 { `+body+` return 0; }`); err != nil {
			t.Errorf("%s: %v", body, err)
		}
	}
	err := checkSource(t, decls+`function main(): i32 { let w = W { items: [] }; return 0; }`)
	if err == nil || !strings.Contains(err.Error(), "could not infer type parameter T for struct W") {
		t.Errorf("an empty array alone binds nothing; want E040, got: %v", err)
	}
	if err != nil && strings.Contains(err.Error(), "E043") {
		t.Errorf("the empty field is not itself a mismatch: %v", err)
	}
}

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
	mustOK(`struct Box[T] { v: T } function main(): i32 { let b: Box[Box[i32]] = Box { v: Box { v: 42 } }; return b.v.v; }`)
	mustOK(`struct Box[T] { v: T } function main(): i32 { let b: Box[Box[Box[i32]]] = Box { v: Box { v: Box { v: 7 } } }; return b.v.v.v; }`)
	mustOK(`struct Box[T] { v: T } function main(): i32 { let b: Box[Box[string]] = Box { v: Box { v: "x" } }; return b.v.v.len(); }`)
	mustErr(`struct Box[T] { v: T } function main(): i32 { let b: Box[Box[i32]] = Box { v: Box { v: "x" } }; return 0; }`,
		`field "v": expected i32, got string`)
	mustOK(`struct Box[T] { v: T } function main(): i32 { let b: Box[i64] = Box { v: 1234567890123 }; if (b.v == 1234567890123) { return 0; } return 1; }`)
}

func TestOperatorClassMismatchNoRedundantShareError(t *testing.T) {
	count := func(src string) int {
		err := checkSource(t, src)
		if err == nil {
			t.Fatalf("expected a type error for %q, got none", src)
		}
		return strings.Count(err.Error(), "type error at")
	}
	if n := count(`function main(): i32 { let x: i32 = 1 - "b"; return 0; }`); n != 1 {
		t.Errorf("int op with string operand: %d E009s, want 1", n)
	}
	if n := count(`function main(): i32 { let s: string = "a"; return s & 1; }`); n != 1 {
		t.Errorf("bitwise op with string operand: %d E009s, want 1", n)
	}
	if n := count(`function main(): i32 { let a: i32 = 1; let b: u32 = 2; return a - b; }`); n != 1 {
		t.Errorf("i32/u32 mismatch: %d E009s, want 1 (the share hint)", n)
	}
}

func TestUnknownTypeReported(t *testing.T) {
	mustE064 := func(src string) {
		t.Helper()
		err := checkSource(t, src)
		if err == nil || !strings.Contains(err.Error(), "cannot find type") {
			t.Errorf("expected E064 unknown type for %q, got: %v", src, err)
		}
	}
	mustOK := func(src string) {
		t.Helper()
		if err := checkSource(t, src); err != nil {
			t.Errorf("expected no error for %q, got: %v", src, err)
		}
	}
	mustE064(`function f(a: Wibble): i32 { return 0; }`)
	mustE064(`function f(): Wibble { return 0; }`)
	mustE064(`function main(): i32 { let x: Wibble = 0; return 0; }`)
	mustE064(`struct S { f: Wibble }`)
	mustE064(`enum E { A(Wibble) }`)
	mustE064(`function main(): i32 { let a: Wibble[] = []; return 0; }`)
	mustE064(`function f(): (i32, Wibble) { return (0, 0); }`)
	mustE064(`function main(): i32 { let x: bool = 0; return 0; }`)
	mustOK(`function id[T](x: T): T { return x; }`)
	mustOK(`struct P { x: i32 } function getx(p: P): i32 { return p.x; }`)
	mustOK(`struct Box[T] { v: T } function main(): i32 { let b: Box[i32] = Box { v: 7 }; return b.v; }`)
	mustOK(`function main(): i32 { let o: Option[i32] = None; return 0; }`)
	mustOK(`function main(): i32 { let n: i32 = 1; let b: boolean = true; if (b) { return n; } return 0; }`)
}

func TestUnknownTypeReachesLambdaAnnotations(t *testing.T) {
	for name, src := range map[string]string{
		"lambda param":         `function main(): i32 { let f = ((x: Wibble) => 1); return 0; }`,
		"lambda return":        `function main(): i32 { let f = ((x: i32): Wibble => x); return 0; }`,
		"lambda param array":   `function main(): i32 { let f = ((x: Wibble[]) => 1); return 0; }`,
		"function-form lambda": `function main(): i32 { let f = (x: Wibble): i32 => { return 1; }; return 0; }`,
		"nested in a lambda":   `function main(): i32 { let f = (() => { let g = ((y: Wibble) => 2); return 1; }); return 0; }`,
	} {
		t.Run(name, func(t *testing.T) {
			err := checkSource(t, src)
			if err == nil || !strings.Contains(err.Error(), "cannot find type") {
				t.Errorf("expected E064 unknown type, got: %v", err)
			}
		})
	}
	for name, src := range map[string]string{
		"unannotated arrow return": `function main(): i32 { let f = ((x: i32) => x + 1); return f(1); }`,
		"declared type":            `struct P { x: i32 } function main(): i32 { let f = ((p: P) => p.x); return f(P { x: 1 }); }`,
		"type parameter in scope":  `function apply[T](v: T): i32 { let f = ((x: T) => 1); return f(v); }` + "\n" + `function main(): i32 { return apply(1); }`,
	} {
		t.Run(name, func(t *testing.T) {
			if err := checkSource(t, src); err != nil {
				t.Errorf("expected no error, got: %v", err)
			}
		})
	}
}

func TestDynTraitReachesLambdaAnnotations(t *testing.T) {
	src := `function main(): i32 { let f = ((x: dyn Bogus) => 1); return 0; }`
	err := checkSource(t, src)
	if err == nil {
		t.Fatalf("want E021, got none")
	}
	if code := firstErrCode(err); code != "E021" {
		t.Errorf("code = %q, want E021: %v", code, err)
	}
}

func TestDynTraitObjectSatisfiesItsBound(t *testing.T) {
	src := `trait Shape { function area(self: Self): i32; }
struct Sq { s: i32 }
impl Shape for Sq { function area(self: Sq): i32 { return self.s * self.s; } }
function doubled[T: Shape](v: T): i32 { return v.area() * 2; }
function err_area[T: Shape](r: Result[i32, T]): i32 { match (r) { Ok(v) => { return v; }, Err(e) => { return e.area(); } } return 0; }
function main(): i32 { let d: dyn Shape = Sq { s: 3 }; let r: Result[i32, dyn Shape] = Err(Sq { s: 2 }); return doubled(d) + err_area(r); }`
	if err := checkSource(t, src); err != nil {
		t.Errorf("expected no error, got: %v", err)
	}
	other := `trait Shape { function area(self: Self): i32; }
trait Named { function name(self: Self): string; }
struct Sq { s: i32 }
impl Named for Sq { function name(self: Sq): string { return "sq"; } }
function doubled[T: Shape](v: T): i32 { return v.area() * 2; }
function main(): i32 { let d: dyn Named = Sq { s: 3 }; return doubled(d); }`
	err := checkSource(t, other)
	if err == nil {
		t.Fatalf("want E021 for dyn Named against T: Shape, got none")
	}
	if code := firstErrCode(err); code != "E021" {
		t.Errorf("code = %q, want E021: %v", code, err)
	}
}

func TestUseCallbackReturnsWhatTheSlotReturns(t *testing.T) {
	src := `trait Shape { function area(self: Self): i32; }
struct Sq { s: i32 }
impl Shape for Sq { function area(self: Sq): i32 { return self.s * self.s; } }
function mk(n: i32, k: (i32) => dyn Shape): i32 { return k(n).area(); }
function a(): i32 {
    use v <- mk(3);
    return Sq { s: v };
}
function main(): i32 { return a(); }`
	if err := checkSource(t, src); err != nil {
		t.Errorf("expected no error, got: %v", err)
	}
	wrong := `function mk(n: i32, k: (i32) => string): i32 { return k(n).len(); }
function a(): i32 {
    use v <- mk(3);
    return v + 1;
}
function main(): i32 { return a(); }`
	err := checkSource(t, wrong)
	if err == nil {
		t.Fatalf("want E002 for an i32 returned where the slot returns string, got none")
	}
	if code := firstErrCode(err); code != "E002" {
		t.Errorf("code = %q, want E002: %v", code, err)
	}
}

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

func TestChainedMethodCallErrorReportedOnce(t *testing.T) {
	count := func(src string) int {
		err := checkSource(t, src)
		if err == nil {
			t.Fatalf("expected a type error for %q, got none", src)
		}
		return strings.Count(err.Error(), "no method \"")
	}
	if n := count(`function main(): i32 { let n: i32 = 5; return n.foo().bar(); }`); n != 1 {
		t.Errorf("chained call: E043 reported %d times, want 1", n)
	}
	if n := count(`function main(): i32 { let n: i32 = 5; return n.a().b().c(); }`); n != 1 {
		t.Errorf("triple chain: E043 reported %d times, want 1", n)
	}
	if n := count(`function main(): i32 { let n: i32 = 5; return n.foo(); }`); n != 1 {
		t.Errorf("single call: E043 reported %d times, want 1", n)
	}
}

func TestUnannotatedBigLiteralDefaultsToI64(t *testing.T) {
	if err := checkSource(t, `function main(): i32 { let x = 2147483648; let y: i64 = x; return (y / 1000000000) as i32; }`); err != nil {
		t.Errorf("big unannotated literal should be i64 (assignable to i64), got: %v", err)
	}
	err := checkSource(t, `function main(): i32 { let x = 2147483648; let y: i32 = x; return y; }`)
	if err == nil || !strings.Contains(err.Error(), "i64") {
		t.Errorf("big unannotated literal in i32 slot should error with i64 mention, got: %v", err)
	}
	if err := checkSource(t, `function main(): i32 { let x = 5; let y: i32 = x; return y; }`); err != nil {
		t.Errorf("small unannotated literal should stay i32, got: %v", err)
	}
	if err := checkSource(t, `function main(): i32 { let x = -5000000000; let y: i64 = x; return (y / 1000000000) as i32; }`); err != nil {
		t.Errorf("big negative unannotated literal should be i64, got: %v", err)
	}
}

func TestUnannotatedCompoundWithBigLiteralDefaultsToI64(t *testing.T) {
	const big = "4611686018427387904"
	widened := []string{
		`let t = 3 - ` + big + `;`,
		`let t = ` + big + ` - 3;`,
		`let t = -` + big + ` + 1;`,
		`let t = (1 + 2) * ` + big + `;`,
		`let t = if (true) { 3 - ` + big + ` } else { 0 };`,
	}
	for _, decl := range widened {
		if err := checkSource(t, "function main(): i32 { "+decl+" let u: i64 = t; return 0; }"); err != nil {
			t.Errorf("%s: should be i64 (assignable to i64), got: %v", decl, err)
		}
		err := checkSource(t, "function main(): i32 { "+decl+" let u: i32 = t; return 0; }")
		if err == nil || !strings.Contains(err.Error(), "got i64") {
			t.Errorf("%s: into an i32 slot should be E003 naming i64, got: %v", decl, err)
		}
	}
	if err := checkSource(t, "function main(): i32 { let t = 3 - "+big+"; let f = (3 - "+big+") as f64; return 0; }"); err != nil {
		t.Errorf("compound with a wide literal should check clean, got: %v", err)
	}
	if err := checkSource(t, "function main(): i32 { let t = 3 - 4; let u: i32 = t; return u; }"); err != nil {
		t.Errorf("small compound should stay i32, got: %v", err)
	}
	err := checkSource(t, "function main(): i32 { let t = 3 - 18446744073709551616; return 0; }")
	if err == nil || !strings.Contains(err.Error(), "does not fit in i64") {
		t.Errorf("past-u64 literal in a compound should be E047 naming i64, got: %v", err)
	}
	err = checkSource(t, "function main(): i32 { let a: i32 = 3; let t = a - "+big+"; return 0; }")
	if err == nil || !strings.Contains(err.Error(), "does not fit in i32") {
		t.Errorf("wide literal beside an i32 operand should stay E047 naming i32, got: %v", err)
	}
	const id = "function id[T](v: T): T { return v; } "
	if err := checkSource(t, id+"function main(): i32 { let t = id("+big+"); let u: i64 = t; return 0; }"); err != nil {
		t.Errorf("generic call on a wide literal should be i64, got: %v", err)
	}
	err = checkSource(t, id+"function main(): i32 { let t = id("+big+"); let u: i32 = t; return 0; }")
	if err == nil || !strings.Contains(err.Error(), "got i64") {
		t.Errorf("generic call on a wide literal into an i32 slot should be E003 naming i64, got: %v", err)
	}
	if err := checkSource(t, id+"function main(): i32 { let t = id(5); let u: i32 = t; return u; }"); err != nil {
		t.Errorf("generic call on a small literal should stay i32, got: %v", err)
	}
}

func TestUnannotatedCompositeInitWidensElementToI64(t *testing.T) {
	const big = "4611686018427387904"
	widened := []struct{ decl, read string }{
		{`let t = (1, ` + big + `);`, "t.1"},
		{`let t = (` + big + `, 1);`, "t.0"},
		{`let t = (1, -` + big + `);`, "t.1"},
		{`let t = (1, ` + big + ` / 2);`, "t.1"},
		{`let t = [` + big + `];`, "t[0]"},
		{`let t = [1, ` + big + `];`, "t[0]"},
		{`let t = (1, (2, ` + big + `));`, "t.1.1"},
		{`let t = (1, [` + big + `]);`, "t.1[0]"},
		{`let t = if (true) { (1, ` + big + `) } else { (2, 3) };`, "t.1"},
	}
	for _, c := range widened {
		if err := checkSource(t, "function main(): i32 { "+c.decl+" let u: i64 = "+c.read+"; return 0; }"); err != nil {
			t.Errorf("%s: %s should be i64, got: %v", c.decl, c.read, err)
		}
		err := checkSource(t, "function main(): i32 { "+c.decl+" let u: i32 = "+c.read+"; return 0; }")
		if err == nil || !strings.Contains(err.Error(), "got i64") {
			t.Errorf("%s: %s into an i32 slot should be E003 naming i64, got: %v", c.decl, c.read, err)
		}
	}
	kept := []struct{ decl, read string }{
		{`let t = (1, ` + big + `);`, "t.0"},
		{`let t = (1, 2);`, "t.1"},
		{`let t = [1, 2];`, "t[0]"},
	}
	for _, c := range kept {
		if err := checkSource(t, "function main(): i32 { "+c.decl+" let u: i32 = "+c.read+"; return u; }"); err != nil {
			t.Errorf("%s: %s should stay i32, got: %v", c.decl, c.read, err)
		}
	}
	if err := checkSource(t, "function main(): i32 { let a: i32 = 3; let t = (a, "+big+"); let u: i32 = t.0; let v: i64 = t.1; return u; }"); err != nil {
		t.Errorf("committed element beside a widened one should stay i32, got: %v", err)
	}
	for _, src := range []string{
		"let t = (18446744073709551616, 1);",
		"let xs = [18446744073709551616];",
	} {
		err := checkSource(t, "function main(): i32 { "+src+" return 0; }")
		if err == nil || !strings.Contains(err.Error(), "does not fit in i64") {
			t.Errorf("%s: past-u64 element should be E047 naming i64, got: %v", src, err)
		}
	}
}

func TestGenericCallScrutineeWidensLiteralBoundT(t *testing.T) {
	const big = "4611686018427387904"
	const decls = "function pick[T](a: T, b: T): Option[T] { return Some(b); } "
	for _, body := range []string{
		`match (pick(1, ` + big + `)) { Some(v) => { let u: i64 = v; if (v == ` + big + `) { return 1; } }, None => { } }`,
		`let m = match (pick(1, ` + big + `)) { Some(v) => v, None => 0 }; let u: i64 = m;`,
	} {
		if err := checkSource(t, decls+"function main(): i32 { "+body+" return 0; }"); err != nil {
			t.Errorf("%s: should settle T at i64, got: %v", body, err)
		}
	}
	for _, body := range []string{
		`match (pick(1, 2)) { Some(v) => { let u: i32 = v; }, None => { } }`,
		`let x: i32 = 1; match (pick(x, 2)) { Some(v) => { let u: i32 = v; }, None => { } }`,
	} {
		if err := checkSource(t, decls+"function main(): i32 { "+body+" return 0; }"); err != nil {
			t.Errorf("%s: should stay i32, got: %v", body, err)
		}
	}
}

func TestGenericCallResultCarryingWideLiteralTWidens(t *testing.T) {
	const big = "4611686018427387904"
	const decls = "function pair[A, B](a: A, b: B): (A, B) { return (a, b); } function both[T](a: T, b: T): (T, T) { return (a, b); } "
	accepted := []string{
		`let p = pair(` + big + `, "hello"); let q: i64 = p.0;`,
		`let p = pair(` + big + `, "hello"); if (p.0 == ` + big + ` && p.1 == "hello") { return 1; }`,
		`let p = pair("hello", ` + big + `); let q: i64 = p.1;`,
		`let q = both(1, ` + big + `); let a: i64 = q.0; let b: i64 = q.1;`,
		`let p = pair(5, "x"); let q: i32 = p.0;`,
	}
	for _, src := range accepted {
		if err := checkSource(t, decls+"function main(): i32 { "+src+" return 0; }"); err != nil {
			t.Errorf("%s: rejected, want accepted: %v", src, err)
		}
	}
	rejected := []struct{ src, want string }{
		{`let p = pair(` + big + `, "hello"); let q: i32 = p.0;`, "got i64"},
		{`let q = both(1, ` + big + `); let a: i32 = q.0;`, "got i64"},
		{`let a: i32 = 1; let q = both(a, ` + big + `);`, "does not fit in i32"},
	}
	for _, c := range rejected {
		err := checkSource(t, decls+"function main(): i32 { "+c.src+" return 0; }")
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: want an error containing %q, got: %v", c.src, c.want, err)
		}
	}
}

func TestArrayArgumentIsItsElementsDestination(t *testing.T) {
	const decls = "struct Same[T] { a: T, b: T } function take(xs: Same[i64][]): i32 { return xs.len(); } "
	for _, body := range []string{
		`return take([Same { a: 1, b: 2 }]);`,
		`let y: i64 = 5; return take([Same { a: 1, b: y }]);`,
	} {
		if err := checkSource(t, decls+"function main(): i32 { "+body+" }"); err != nil {
			t.Errorf("%s: rejected, want accepted: %v", body, err)
		}
	}
	for _, c := range []struct{ body, want string }{
		{`return take([Same { a: 1, b: "x" }]);`, `field "b": expected i64, got string`},
	} {
		err := checkSource(t, decls+"function main(): i32 { "+c.body+" }")
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: want an error containing %q, got: %v", c.body, c.want, err)
		}
	}
}

func TestClashingGenericStructLiteralDoesNotCascade(t *testing.T) {
	const decls = "struct Same[T] { a: T, b: T } function take1(x: Same[i64]): i32 { return 1; } "
	for _, body := range []string{
		`let q = Same { a: 1, b: "x" }; return take1(q);`,
		`let q = Same { a: 1, b: "x" }; let z: string = q; return 0;`,
		`let xs = [Same { a: 1, b: "x" }]; return take1(xs[0]);`,
	} {
		err := checkSource(t, decls+"function main(): i32 { "+body+" }")
		if err == nil {
			t.Errorf("%s: accepted, want E043", body)
			continue
		}
		msg := err.Error()
		if !strings.Contains(msg, `field "b": expected i32, got string`) {
			t.Errorf("%s: want the field clash, got: %v", body, err)
		}
		if n := strings.Count(msg, "\n") + 1; n != 1 {
			t.Errorf("%s: want only the field clash, got %d diagnostics: %v", body, n, err)
		}
	}
}

func TestTypedValueBindsAheadOfEarlierLiteral(t *testing.T) {
	const decls = "struct Same[T] { a: T, b: T } function take1(x: Same[i64]): i32 { return 1; } function pair[T](a: T, b: T): T { return a; } "
	for _, body := range []string{
		`let y: i64 = 5; let q = Same { a: 1, b: y }; return take1(q);`,
		`let y: i64 = 5; let q = Same { a: y, b: 1 }; return take1(q);`,
		`let y: i64 = 5; let r: i64 = pair(1, y); return 0;`,
		`let y: i64 = 5; let r: i64 = pair(y, 1); return 0;`,
	} {
		if err := checkSource(t, decls+"function main(): i32 { "+body+" }"); err != nil {
			t.Errorf("%s: rejected, want accepted: %v", body, err)
		}
	}
	prog, err := parser.Parse(decls + "function main(): i32 { let y: i64 = 5; let q = Same { a: 1, b: y }; return take1(q); }")
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if _, err := Check(prog); err != nil {
		t.Fatalf("check: %v", err)
	}
	sl := prog.Funcs[len(prog.Funcs)-1].Body.Stmts[1].(*ast.Var).Init.(*ast.StructLit)
	if n, ok := sl.Fields[0].Value.(*ast.NumberLit); !ok || n.Width != 64 {
		t.Errorf("a: 1 settled as %#v, want width 64", sl.Fields[0].Value)
	}
}

func TestGenericStructLiteralLocalTakesOneWidth(t *testing.T) {
	const decls = "struct Same[T] { a: T, b: T } function take1(x: Same[i64]): i32 { return 1; } function take(xs: Same[i64][]): i32 { return 1; } "
	for _, body := range []string{
		`let q = Same { a: 1, b: 2 }; let r: i64 = q.a; return 0;`,
		`let q = Same { a: 1, b: 2 }; return take1(q);`,
		`let q = Same { a: 1, b: 2 }; return take([q]);`,
		`let q = Same { a: 1, b: 2 }; let r: i64 = q.a; let z: i64 = q.b; return take1(q);`,
		`let q = Same { a: 1, b: 2 }; let z: i32 = q.a; return 0;`,
	} {
		if err := checkSource(t, decls+"function main(): i32 { "+body+" }"); err != nil {
			t.Errorf("%s: rejected, want accepted: %v", body, err)
		}
	}
	for _, body := range []string{
		`let q = Same { a: 1, b: 2 }; let r: i64 = q.a; let z: i32 = q.b; return 0;`,
		`let q = Same { a: 1, b: 2 }; let t = take1(q); let z: i32 = q.a; return 0;`,
		`let q = Same { a: 1, b: 4611686018427387904 }; let z: i32 = q.a; return 0;`,
	} {
		err := checkSource(t, decls+"function main(): i32 { "+body+" }")
		if err == nil || !strings.Contains(err.Error(), "expected i32, got i64") {
			t.Errorf("%s: want the i32 read refused, got: %v", body, err)
		}
	}
}

func TestGenericCallLiteralBoundTSettlesByPosition(t *testing.T) {
	const big = "4611686018427387904"
	const decls = "struct Box[T] { v: T } function box[T](v: T): Box[T] { return Box { v: v }; } " +
		"function both[T](a: T, b: T): (T, T) { return (a, b); } " +
		"function pick[T](a: T, b: T): Option[T] { return Some(b); } " +
		"function id[T](a: T): T { return a; } "
	accepted := []string{
		`let q = both(1, ` + big + `); if (q.1 == ` + big + `) { return 7; }`,
		`match (pick(1, ` + big + `)) { Some(v) => { if (v == ` + big + `) { return 7; } }, None => { } }`,
		`if (both(1, ` + big + `).1 == ` + big + `) { return 7; }`,
		`let x: (u64, u64) = both(1, ` + big + `);`,
		`let z: i64 = both(1, ` + big + `).1;`,
		`let z: u64 = both(1, 2).1;`,
		`let b: Box[u64] = box(1);`,
		`let v: u64 = box(1).v;`,
		`if (id(1) == ` + big + `) { return 7; }`,
		`if (id(1) == id(` + big + `)) { return 7; }`,
		`let x: i64 = id(1) + 1;`,
		`let y: u64 = 3 as u64; if (id(1) < y) { return 7; }`,
	}
	for _, src := range accepted {
		if err := checkSource(t, decls+"function main(): i32 { "+src+" return 0; }"); err != nil {
			t.Errorf("%s: rejected, want accepted: %v", src, err)
		}
	}
	rejected := []struct{ src, want string }{
		{`let x: i32 = id(4294967298);`, "does not fit in i32"},
		{`let x: u8 = id(300);`, "does not fit in u8"},
		{`let z: i32 = both(1, ` + big + `).1;`, "does not fit in i32"},
	}
	for _, c := range rejected {
		err := checkSource(t, decls+"function main(): i32 { "+c.src+" return 0; }")
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: want an error containing %q, got: %v", c.src, c.want, err)
		}
	}

	stamped := []struct {
		body string
		call func(ast.Stmt) *ast.Call
		want ast.NumberType
	}{
		{`let x: (u64, u64) = both(1, ` + big + `);`, func(st ast.Stmt) *ast.Call { return st.(*ast.Var).Init.(*ast.Call) }, ast.NumberType{Width: 64}},
		{`let z: i64 = both(1, ` + big + `).1;`, func(st ast.Stmt) *ast.Call {
			return st.(*ast.Var).Init.(*ast.FieldAccess).Target.(*ast.Call)
		}, ast.NumberType{Width: 64, Signed: true}},
		{`if (both(1, ` + big + `).1 == ` + big + `) { return 7; }`, func(st ast.Stmt) *ast.Call {
			return st.(*ast.If).Cond.(*ast.Binary).Left.(*ast.FieldAccess).Target.(*ast.Call)
		}, ast.NumberType{Width: 64, Signed: true}},
	}
	for _, c := range stamped {
		prog, err := parser.Parse(decls + "function main(): i32 { " + c.body + " return 0; }")
		if err != nil {
			t.Fatalf("parse: %v", err)
		}
		if _, err := Check(prog); err != nil {
			t.Fatalf("%s: %v", c.body, err)
		}
		main := prog.Funcs[len(prog.Funcs)-1]
		call := c.call(main.Body.Stmts[0])
		got, ok := call.TypeArgs[0].(ast.NumberType)
		if !ok || got.NormalWidth() != c.want.NormalWidth() || got.IsSigned() != c.want.IsSigned() || got.Polymorphic {
			t.Errorf("%s: T = %#v, want %s", c.body, call.TypeArgs[0], c.want)
		}
		for _, a := range call.Args {
			if lit, ok := a.(*ast.NumberLit); ok && lit.Width != c.want.NormalWidth() {
				t.Errorf("%s: literal %s settled at width %d, want %d", c.body, lit.Raw, lit.Width, c.want.NormalWidth())
			}
		}
	}
}

func TestGenericCallInCalleePositionWidensByLiteral(t *testing.T) {
	src := `function make[T](seed: T): (i32) => T { return (k: i32): T => seed; }
function main(): i32 { let b = make(5000000000)(2); return 0; }`
	prog, err := parser.Parse(src)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if _, err := Check(prog); err != nil {
		t.Fatalf("check: %v", err)
	}
	main := prog.Funcs[len(prog.Funcs)-1]
	inner := main.Body.Stmts[0].(*ast.Var).Init.(*ast.Call).Callee.(*ast.Call)
	got, ok := inner.TypeArgs[0].(ast.NumberType)
	if !ok || got.NormalWidth() != 64 || !got.IsSigned() || got.Polymorphic {
		t.Errorf("T = %#v, want i64", inner.TypeArgs[0])
	}
	if lit := inner.Args[0].(*ast.NumberLit); lit.Width != 64 {
		t.Errorf("literal settled at width %d, want 64", lit.Width)
	}
}

func TestTypeParamBoundsReachClosures(t *testing.T) {
	const decls = `trait Mk { function make(): Self; function get(self: Self): i32; }
trait Other { function other(self: Self): i32; }
struct W { v: i32 }
impl Mk for W { function make(): W { return W { v: 41 }; } function get(self: W): i32 { return self.v; } }
function call0(g: () => i32): i32 { return g(); }
`
	for _, src := range []string{
		`function f[T: Mk](y: T): i32 { return call0((): i32 => { return T.make().get() + y.get(); }); }`,
		`function f[T: Mk](y: T): i32 { function inner(): i32 { return T.make().get() + y.get(); } return inner(); }`,
		`function f[T: Mk](y: T): i32 { return call0((): i32 => { return call0((): i32 => { return T.make().get(); }); }); }`,
	} {
		prog, err := parser.Parse(decls + src + "\nfunction main(): i32 { return f[W](W { v: 1 }); }")
		if err != nil {
			t.Fatalf("parse: %v", err)
		}
		if _, err := Check(prog); err != nil {
			t.Errorf("%s\ncheck: %v", src, err)
		}
	}
	prog, err := parser.Parse(decls + `function f[T: Mk](y: T): i32 { return call0((): i32 => { return y.other(); }); }
function main(): i32 { return f[W](W { v: 1 }); }`)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if _, err := Check(prog); err == nil || !strings.Contains(err.Error(), `no method "other" on type parameter T`) {
		t.Errorf("a method the bounds lack: got %v, want no method \"other\"", err)
	}
}

func TestGenericStructLiteralWideningInArrayAndWritten(t *testing.T) {
	const decls = "struct Same[T] { a: T, b: T } "
	if err := checkSource(t, decls+"function main(): i32 { let xs = [Same { a: 1, b: 4611686018427387904 }]; let r: i64 = xs[0].b; return 0; }"); err != nil {
		t.Errorf("array of a wide literal: rejected, want accepted: %v", err)
	}
	err := checkSource(t, decls+"function main(): i32 { let q = Same[i32] { a: 1, b: 4611686018427387904 }; return 0; }")
	if err == nil || !strings.Contains(err.Error(), "does not fit in i32") {
		t.Errorf("written Same[i32] with a wide field: want the literal refused, got %v", err)
	}
	for _, decl := range []string{
		"struct Stack[T] { items: T[] } function main(): i32 { let q = Stack { items: [4611686018427387904] }; let r: i64 = q.items[0]; return 0; }",
		"struct Tagged[T] { p: (T, string) } function main(): i32 { let q = Tagged { p: (4611686018427387904, \"x\") }; let r: i64 = q.p.0; return 0; }",
	} {
		prog, err := parser.Parse(decl)
		if err != nil {
			t.Fatalf("parse: %v", err)
		}
		if _, err := Check(prog); err != nil {
			t.Errorf("%s: check: %v", decl, err)
			continue
		}
		if got := prog.Funcs[0].Body.Stmts[0].(*ast.Var).Type.String(); !strings.HasSuffix(got, "[i64]") {
			t.Errorf("%s: q is %s, want the i64 instantiation", decl, got)
		}
	}
}

func TestTypedFieldRebindStaysInsideTheLiteral(t *testing.T) {
	const decls = "struct Same[T] { a: T, b: T } "
	err := checkSource(t, decls+"function main(): i32 { let q = Same { a: 1, b: 2 }; let y: i64 = 8589934592; let w = Same { ...q, b: y }; return 0; }")
	if err == nil || !strings.Contains(err.Error(), `field "b": expected i32, got i64`) {
		t.Errorf("struct update over an i32 base: want the field refused, got %v", err)
	}
	for _, body := range []string{
		"let y: i64 = 8589934592; let xs = [Same { a: 1, b: 2 }, Same { a: 3, b: y }]; let r: i64 = xs[0].a; return 0;",
		"let y: i64 = 8589934592; let xs = [Same { a: 3, b: y }, Same { a: 1, b: 2 }]; let r: i64 = xs[1].a; return 0;",
		"let q = Same { a: 1, b: 2 }; let r = q; let z: i64 = r.a; let w: i64 = q.b; return 0;",
		"let q = Same { a: 1, b: 2 }; let f = (): i64 => q.a; return 0;",
	} {
		if err := checkSource(t, decls+"function main(): i32 { "+body+" }"); err != nil {
			t.Errorf("%s: rejected, want accepted: %v", body, err)
		}
	}
	if err := checkSource(t, decls+"function main(): i32 { let q = Same { a: 1, b: 2 }; let r = q; let z: i64 = r.a; let w: i32 = q.b; return 0; }"); err == nil || !strings.Contains(err.Error(), "already inferred") {
		t.Errorf("a copy and its source read at two widths: want E003, got %v", err)
	}
	if err := checkSource(t, decls+"function main(): i32 { let xs = [Same { a: 1, b: 2 }, Same { a: \"x\", b: \"y\" }]; return 0; }"); err == nil || !strings.Contains(err.Error(), "array element:") {
		t.Errorf("an array of two instantiations no literal reconciles: want E034, got %v", err)
	}
}

func TestNestedGenericStructLiteralInfersItsOwnArgs(t *testing.T) {
	const decls = "struct Box[T] { v: T } struct Outer[T] { b: Box[T] } struct Two[T] { b: Box[T], c: T } " +
		"struct List[T] { tail: Option[List[T]], head: T } struct C[T] { v: T } struct B[T] { c: C[T] } struct A[T] { b: B[T] } "
	for _, body := range []string{
		"let o = Outer { b: Box { v: 4 } }; let r: i32 = o.b.v; return r;",
		"let o = Outer { b: Box { v: 4611686018427387904 } }; let r: i64 = o.b.v; return 0;",
		"let o: Outer[i64] = Outer { b: Box { v: 4 } }; let r: i64 = o.b.v; return 0;",
		"let o = Two { b: Box { v: \"x\" }, c: \"y\" }; return o.c.len();",
		"let o = Two { b: Box { v: 4611686018427387904 }, c: 1 }; let r: i64 = o.c; return 0;",
		"let l = List { tail: None, head: 1 }; return l.head;",
		"let a = A { b: B { c: C { v: 1 } } }; return a.b.c.v;",
	} {
		if err := checkSource(t, decls+"function main(): i32 { "+body+" }"); err != nil {
			t.Errorf("%s: rejected, want accepted: %v", body, err)
		}
	}
	if err := checkSource(t, "struct Holder[T] { xs: T[], z: T } function emptyArr[A](): A[] { return []; } "+
		"function f[T](t: T): Holder[T] { let h = Holder { xs: emptyArr(), z: t }; return h; } "+
		"function main(): i32 { let h = f(7); return h.z; }"); err != nil {
		t.Errorf("a generic call in an unbound field: rejected, want accepted: %v", err)
	}
	err := checkSource(t, decls+"function main(): i32 { let o = Two { b: Box { v: 1 }, c: \"x\" }; return 0; }")
	if err == nil || !strings.Contains(err.Error(), `field "c": expected i32, got string`) {
		t.Errorf("clash across the nesting: want the field refused, got %v", err)
	}
}

func TestGenericStructLiteralLocalWidensByLiteral(t *testing.T) {
	src := "struct Same[T] { a: T, b: T } function main(): i32 { " +
		"let q = Same { a: 1, b: 4611686018427387904 }; let r: i64 = q.b; return 0; }"
	prog, err := parser.Parse(src)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if _, err := Check(prog); err != nil {
		t.Fatalf("check: %v", err)
	}
	v := prog.Funcs[len(prog.Funcs)-1].Body.Stmts[0].(*ast.Var)
	if got := v.Type.String(); got != "Same[i64]" {
		t.Errorf("q: type %s, want Same[i64]", got)
	}
	sl := v.Init.(*ast.StructLit)
	if len(sl.TypeArgs) != 1 || sl.TypeArgs[0].String() != "i64" {
		t.Errorf("literal type args %v, want [i64]", sl.TypeArgs)
	}
	for _, f := range sl.Fields {
		if lit, ok := f.Value.(*ast.NumberLit); ok && lit.Width != 64 {
			t.Errorf("field %s: literal settled at width %d, want 64", f.Name, lit.Width)
		}
	}
	narrow, err := parser.Parse("struct Same[T] { a: T, b: T } function main(): i32 { let q = Same { a: 1, b: 2 }; return 0; }")
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if _, err := Check(narrow); err != nil {
		t.Fatalf("check: %v", err)
	}
	if got := narrow.Funcs[len(narrow.Funcs)-1].Body.Stmts[0].(*ast.Var).Type.String(); got != "Same[i32]" {
		t.Errorf("narrow q: type %s, want Same[i32]", got)
	}
}

func TestPolymorphicComparisonWithBigLiteralSettlesAtI64(t *testing.T) {
	const big = "4611686018427387904"
	cases := []struct {
		name      string
		init      string
		wantWidth int
	}{
		{"ordering", big + " > 1", 64},
		{"ordering, wide on the right", "1 < " + big, 64},
		{"equality", big + " != 0", 64},
		{"ordering of a compound", "3 - " + big + " < 0", 64},
		{"small literals keep the default", "3 > 1", 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			prog, err := parser.Parse("function main(): i32 { let b = " + tc.init + "; if (b) { return 1; } return 0; }")
			if err != nil {
				t.Fatalf("parse: %v", err)
			}
			if _, err := Check(prog); err != nil {
				t.Fatalf("check: %v", err)
			}
			bin, ok := varInit(t, prog, "b").(*ast.Binary)
			if !ok {
				t.Fatalf("init of b is %T, want *ast.Binary", varInit(t, prog, "b"))
			}
			if bin.IntWidth != tc.wantWidth {
				t.Errorf("comparison settled at width %d, want %d", bin.IntWidth, tc.wantWidth)
			}
		})
	}
	err := checkSource(t, "function main(): i32 { let a: i32 = 1; if (a < "+big+") { return 1; } return 0; }")
	if err == nil || !strings.Contains(err.Error(), "does not fit in i32") {
		t.Errorf("wide literal beside an i32 operand should stay E047 naming i32, got: %v", err)
	}
}

func varInit(t *testing.T, prog *ast.Program, name string) ast.Expr {
	t.Helper()
	for _, fn := range prog.Funcs {
		if fn.Name != "main" {
			continue
		}
		for _, st := range fn.Body.Stmts {
			if v, ok := st.(*ast.Var); ok && v.Name == name {
				return v.Init
			}
		}
	}
	t.Fatalf("no `let %s` in main", name)
	return nil
}

func TestWasmReactorBuiltinSigs(t *testing.T) {
	ok := `function main(): i32 {
    let p: i32 = wasm_timer_pollable(1000000);
    return wasm_block(p);
}`
	if err := checkSource(t, ok); err != nil {
		t.Errorf("wasm reactor builtins should type-check, got: %v", err)
	}
}

func TestReadDirInoSig(t *testing.T) {
	ok := `function main(): i32 {
    match (read_dir_ino(".")) {
        Ok(es) => {
            let e: DirEntry = es[0];
            let n: string = e.name;
            let ino: i64 = e.ino;
            return n.len();
        },
        Err(_) => { return 1; },
    }
}`
	if err := checkSource(t, ok); err != nil {
		t.Errorf("read_dir_ino should type-check, got: %v", err)
	}
	for _, bad := range []string{
		`function main(): i32 { match (read_dir_ino(".")) { Ok(es) => { let n: i32 = es[0].ino; return n; }, Err(_) => { return 1; } } }`,
		`function main(): i32 { match (read_dir_ino(".")) { Ok(es) => { let s: string[] = es; return 0; }, Err(_) => { return 1; } } }`,
		`function main(): i32 { match (read_dir_ino(1)) { Ok(_) => { return 0; }, Err(_) => { return 1; } } }`,
		`struct DirEntry { name: string }
function main(): i32 { return 0; }`,
	} {
		if err := checkSource(t, bad); err == nil {
			t.Errorf("want a type error, got none:\n%s", bad)
		}
	}
}

func TestVariantTypeParamFromFnPayload(t *testing.T) {
	ok := []string{
		`enum Box[T] { Mk(i32, (i32) => T), Nil }
function f(): Box[string] { return Box.Mk(7, (x: i32) => "hi"); }
function main(): i32 { match (f()) { Mk(n, g) => { return n; }, Nil => { return 0; }, } }`,
		`enum Box[T] { Mk(i32, (i32) => T), Nil }
function f(): Box[i32] { return Box.Mk(1, (x: i32) => x); }
function main(): i32 { match (f()) { Mk(n, g) => { return n; }, Nil => { return 0; }, } }`,
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
		`function main(): i32 { let x = 1; let y = x + 2; return y; }`,
		`function main(): i32 { let a: i32[] = [1,2,3]; return a[0]; }`,
	} {
		if err := checkSource(t, src); err != nil {
			t.Errorf("%q: unexpected error %v", src, err)
		}
	}
}

func TestUnionWidenInStructField(t *testing.T) {
	src := `struct Leaf { v: i32 }
struct Node { left: Tree, right: Tree }
type Tree = Leaf | Node;
function main(): i32 {
    let t: Tree = Node { left: Leaf { v: 40 }, right: Leaf { v: 2 } };
    match (t) {
        Leaf(l) => { return l.v; },
        Node(n) => { return 0; }
    }
}`
	if err := checkSource(t, src); err != nil {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestUnionWidenInVariantPayload(t *testing.T) {
	src := `struct A { n: i32 }
struct B { s: string }
type U = A | B;
function pick(flag: boolean): Option[U] {
    if (flag) {
        return Some(A { n: 4 });
    }
    return None;
}
function ok(): Result[U, string] {
    return Ok(B { s: "b" });
}
function main(): i32 {
    let held: Option[U] = Some(B { s: "x" });
    match (pick(true)) {
        Some(u) => {
            match (u) {
                A(a) => { return a.n; },
                B(b) => { return 0; }
            }
        },
        None => { return 1; }
    }
}`
	if err := checkSource(t, src); err != nil {
		t.Errorf("unexpected error: %v", err)
	}
	outside := `struct A { n: i32 }
struct B { s: string }
struct C { f: f64 }
type U = A | B;
function pick(): Option[U] {
    return Some(C { f: 1.0 });
}
function main(): i32 { return 0; }`
	if err := checkSource(t, outside); err == nil || !strings.Contains(err.Error(), "return value: expected Option[U]") {
		t.Errorf("a struct outside the union: got %v, want the return-value E002", err)
	}
}

func TestTypeErrors(t *testing.T) {
	cases := []struct {
		src  string
		want string
	}{
		{`function f(): i32 { return true; }`, "return value: expected"},
		{`function f(): i32 { return 1 + true; }`, "requires an integer type"},
		{`function f(): boolean { return 1; }`, "return value: expected"},
		{`function f(): void { x; }`, "cannot find"},
		{`function f(n: i32): i32 { if (n) { return 0; } return 1; }`, "if condition must be boolean"},
		{`function f(): void { let x: i32 = true; }`, "got boolean"},
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

func TestTryConvertErrToDyn(t *testing.T) {
	const hdr = `trait Error { function message(self: Self): string; }
struct NotFound { what: string }
impl Error for NotFound { function message(self: Self): string { return self.what; } }
function find(): Result[i32, NotFound] { return Err(NotFound { what: "x" }); }
`
	if err := checkSource(t, hdr+`function h(): Result[i32, dyn Error] {
  let v: i32 = find()?;
  return Ok(v);
}`); err != nil {
		t.Errorf("error-converting `?` (NotFound→dyn Error) should type-check, got: %v", err)
	}
	err := checkSource(t, `trait Error { function message(self: Self): string; }
struct Plain { x: i32 }
function find(): Result[i32, Plain] { return Err(Plain { x: 1 }); }
function h(): Result[i32, dyn Error] { let v: i32 = find()?; return Ok(v); }
function main(): i32 { return 0; }`)
	if err == nil || !strings.Contains(err.Error(), "`?` cannot box Plain into dyn Error: Plain does not implement Error") {
		t.Errorf("`?` with a non-implementing error type should be rejected, naming the missing impl, got: %v", err)
	}
	if err := checkSource(t, `trait A { function a(self: Self): i32; }
trait B { function b(self: Self): i32; }
struct Both { n: i32 }
impl A for Both { function a(self: Self): i32 { return self.n; } }
impl B for Both { function b(self: Self): i32 { return 0; } }
function find(): Result[i32, Both] { return Err(Both { n: 1 }); }
function h(): Result[i32, dyn A + B] { let v: i32 = find()?; return Ok(v); }
function main(): i32 { return 0; }`); err != nil {
		t.Errorf("error-converting `?` into a multi-trait `dyn A + B` should type-check, got: %v", err)
	}
	err = checkSource(t, `trait A { function a(self: Self): i32; }
trait B { function b(self: Self): i32; }
struct OnlyA { n: i32 }
impl A for OnlyA { function a(self: Self): i32 { return self.n; } }
function find(): Result[i32, OnlyA] { return Err(OnlyA { n: 1 }); }
function h(): Result[i32, dyn A + B] { let v: i32 = find()?; return Ok(v); }
function main(): i32 { return 0; }`)
	if err == nil || !strings.Contains(err.Error(), "`?` cannot box OnlyA into dyn A + B: OnlyA does not implement B") {
		t.Errorf("`?` into `dyn A + B` with an error implementing only A should be rejected, naming B, got: %v", err)
	}
}

func TestTryConvertErrViaFrom(t *testing.T) {
	const hdr = `trait From[T] { function from(v: T): Self; }
struct IoErr { code: i32 }
struct AppErr { msg: i32 }
impl From[IoErr] for AppErr { function from(e: IoErr): Self { return AppErr { msg: e.code }; } }
function read(): Result[i32, IoErr] { return Err(IoErr { code: 1 }); }
`
	if err := checkSource(t, hdr+`function run(): Result[i32, AppErr] {
  let v: i32 = read()?;
  return Ok(v);
}`); err != nil {
		t.Errorf("From-based error-converting `?` should type-check, got: %v", err)
	}
	err := checkSource(t, `struct IoErr { code: i32 }
struct AppErr { msg: i32 }
function read(): Result[i32, IoErr] { return Err(IoErr { code: 1 }); }
function run(): Result[i32, AppErr] { let v: i32 = read()?; return Ok(v); }
function main(): i32 { return 0; }`)
	if err == nil || !strings.Contains(err.Error(), "AppErr has no `from(IoErr)` constructor to convert it") {
		t.Errorf("`?` with no conversion should be rejected, naming the missing constructor, got: %v", err)
	}
}

func TestCompositeArithmeticOverload(t *testing.T) {
	const ops = `struct V { x: i32 }
function (self: V) add(o: V): V { return V { x: self.x + o.x }; }
function (self: V) sub(o: V): V { return V { x: self.x - o.x }; }
function (self: V) mul(o: V): V { return V { x: self.x * o.x }; }
function (self: V) div(o: V): V { return V { x: self.x / o.x }; }
`
	if err := checkSource(t, ops+`function main(): i32 {
  let a: V = V { x: 6 };
  let b: V = V { x: 7 };
  let r: V = ((a + b) - a) * b;
  return (r / b).x;
}`); err != nil {
		t.Errorf("composite arithmetic with add/sub/mul/div should type-check, got: %v", err)
	}
	const bitOps = `struct F { b: i32 }
function (self: F) rem(o: F): F { return F { b: self.b % o.b }; }
function (self: F) bitand(o: F): F { return F { b: self.b & o.b }; }
function (self: F) bitor(o: F): F { return F { b: self.b | o.b }; }
function (self: F) bitxor(o: F): F { return F { b: self.b ^ o.b }; }
function (self: F) shl(o: F): F { return F { b: self.b << o.b }; }
function (self: F) shr(o: F): F { return F { b: self.b >> o.b }; }
`
	if err := checkSource(t, bitOps+`function main(): i32 {
  let a: F = F { b: 12 };
  let b: F = F { b: 10 };
  let r: F = ((((a & b) | a) ^ b) << F{b:1}) >> F{b:1};
  return (r % F{b:7}).b;
}`); err != nil {
		t.Errorf("composite %% & | ^ << >> should type-check, got: %v", err)
	}
	err := checkSource(t, `struct W { x: i32 }
function main(): i32 { let a: W = W{x:1}; let b: W = W{x:2}; let c: W = a + b; return c.x; }`)
	if err == nil || !strings.Contains(err.Error(), `operator "+" is not defined for W`) {
		t.Errorf("struct without `add` should be rejected with an operator-overload hint, got: %v", err)
	}
	if err := checkSource(t, `function main(): i32 { return 2 + 3 * 4 - 1; }`); err != nil {
		t.Errorf("numeric arithmetic should still type-check, got: %v", err)
	}
	if err := checkSource(t, ops+`function main(): i32 { let a: V = V{x:5}; let b: V = -a; return b.x; }`); err == nil ||
		!strings.Contains(err.Error(), "unary `-` is not defined for V") {
		t.Errorf("unary `-` on a struct without `neg` should be rejected with a hint, got: %v", err)
	}
	if err := checkSource(t, `struct V { x: i32 }
function (self: V) neg(): V { return V { x: 0 - self.x }; }
function main(): i32 { let a: V = V{x:5}; let b: V = -a; return b.x; }`); err != nil {
		t.Errorf("unary `-` with a `neg` method should type-check, got: %v", err)
	}
	if err := checkSource(t, `function main(): i32 { let x: i32 = 7; return -x; }`); err != nil {
		t.Errorf("numeric unary minus should still type-check, got: %v", err)
	}
}

func TestOperatorOverloadOverTypeParam(t *testing.T) {
	const traits = `trait Add { function add(self: Self, o: Self): Self; }
trait Mul { function mul(self: Self, o: Self): Self; }
trait Neg { function neg(self: Self): Self; }
trait Arith: Add + Mul + Neg {}
`
	if err := checkSource(t, traits+`function poly[T: Arith](a: T, b: T): T { return (a + b) * a; }
function main(): i32 { return 0; }`); err != nil {
		t.Errorf("binary operators over a trait-bounded type param should type-check, got: %v", err)
	}
	if err := checkSource(t, traits+`function negate[T: Neg](a: T): T { return -a; }
function main(): i32 { return 0; }`); err != nil {
		t.Errorf("unary `-` over a Neg-bounded type param should type-check, got: %v", err)
	}
	if err := checkSource(t, traits+`function plus[T: Add](a: T, b: T): T { return a + b; }
function main(): i32 { return 0; }`); err != nil {
		t.Errorf("`+` over an Add-bounded type param should type-check, got: %v", err)
	}
	err := checkSource(t, `trait Show { function show(self: Self): i32; }
function bad[T: Show](a: T, b: T): T { return a + b; }
function main(): i32 { return 0; }`)
	if err == nil || !strings.Contains(err.Error(), `operator "+" requires an integer type`) {
		t.Errorf("`+` over a type param without an Add bound should be rejected (E009), got: %v", err)
	}
}

func TestComparisonOverTypeParam(t *testing.T) {
	const traits = `trait Ord { function cmp(self: Self, o: Self): i32; }
trait Eq { function eq(self: Self, o: Self): boolean; }
trait Total: Ord {}
`
	for _, src := range []string{
		`function gt[T: Ord](a: T, b: T): boolean { return a > b && b <= a; }`,
		`function gt[T: Total](a: T, b: T): boolean { return a >= b; }`,
		`function same[T: Eq](a: T, b: T): boolean { return a == b || a != b; }`,
		`function same[T](a: T, b: T): boolean { return a == b; }`,
	} {
		if err := checkSource(t, traits+src+"\nfunction main(): i32 { return 0; }"); err != nil {
			t.Errorf("%s: should type-check, got: %v", src, err)
		}
	}
	for _, src := range []string{
		`function gt[T](a: T, b: T): boolean { return a > b; }`,
		`function gt[T: Eq](a: T, b: T): boolean { return a < b; }`,
	} {
		err := checkSource(t, traits+src+"\nfunction main(): i32 { return 0; }")
		if err == nil || !strings.Contains(err.Error(), "requires matching numeric or string operands") {
			t.Errorf("%s: should be rejected (E009), got: %v", src, err)
		}
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
function main(): i32 { let a: P = P{x:1}; let b: P = P{x:1}; if (a == b) { return 1; } return 0; }`,
			"does not implement `Eq`"},
		{`function main(): i32 { let a: i32[] = [1,2]; let b: i32[] = [1,2]; if (a == b) { return 1; } return 0; }`,
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
	const ordI32 = `trait Ord { function cmp(self: Self, other: Self): i32; }
impl Ord for i32 { function cmp(self: Self, other: Self): i32 { if (self < other) { return 0 - 1; } if (self > other) { return 1; } return 0; } }
`
	ordReject := []struct {
		src  string
		want string
	}{
		{`struct P { x: i32 }
function main(): i32 { let a: P = P{x:1}; let b: P = P{x:2}; if (a < b) { return 1; } return 0; }`,
			"does not implement `Ord`"},
	}
	for _, c := range ordReject {
		err := checkSource(t, c.src)
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%q: want error containing %q, got %v", c.src, c.want, err)
		}
	}
	if err := checkSource(t, ordI32+`@derive(Ord) struct P { x: i32 }
function main(): i32 { let a: P = P{x:1}; let b: P = P{x:2}; if (a < b) { if (b >= a) { return 0; } } return 1; }`); err != nil {
		t.Errorf("composite ordering with derived Ord should type-check, got: %v", err)
	}

	ok := eqI32 + `@derive(Eq) struct P { x: i32, y: i32 }
@derive(Eq) enum E { A, B(i32) }
function main(): i32 {
    let a: P = P{x:1, y:2}; let b: P = P{x:1, y:2};
    if (a == b) { if (A == A) { if (B(1) != B(2)) { return 0; } } }
    return 1;
}`
	if err := checkSource(t, ok); err != nil {
		t.Errorf("composite == with derived Eq should type-check, got: %v", err)
	}
}

func TestGenericReceiverMethods(t *testing.T) {
	ok := []string{
		`struct Box[T] { v: T }
function (b: Box[T]) get(): T { return b.v; }
function main(): i32 { let b: Box[i32] = Box { v: 7 }; return b.get(); }`,
		`struct Pair[A, B] { fst: A, snd: B }
function (p: Pair[A, B]) first(): A { return p.fst; }
function main(): i32 { let p: Pair[i32, i32] = Pair { fst: 3, snd: 4 }; return p.first(); }`,
		`enum Opt[T] { Nil, Has(T) }
function (o: Opt[T]) unwrap_or(d: T): T { match (o) { Has(x) => { return x; }, Nil => { return d; } } }
function main(): i32 { let o: Opt[i32] = Has(9); return o.unwrap_or(0); }`,
		`struct Foo { v: i32 }
struct Box[T] { v: T }
function (b: Box[Foo]) deep(): i32 { return b.v.v; }
function main(): i32 { let b: Box[Foo] = Box { v: Foo { v: 5 } }; return b.deep(); }`,
		`function (xs: T[]) first(): T { return xs[0]; }
function main(): i32 { let a: i32[] = [3, 4]; return a.first(); }`,
		`function (xs: [T]) head(): T { return xs[0]; }
function main(): i32 { let a: i32[] = [7, 8]; let s: [i32] = a[0:2]; return s.head(); }`,
		`function (xs: T[]) count_where(p: (T) => boolean): i32 {
    let n: i32 = 0; let i: i32 = 0;
    while (i < xs.len()) { if (p(xs[i])) { n = n + 1; } i = i + 1; } return n; }
function pos(x: i32): boolean { return x > 0; }
function main(): i32 { let a: i32[] = [1, 0 - 1, 2]; return a.count_where(pos); }`,
		`struct Box[T] { v: T }
function (b: Box[T]) map[U](f: (T) => U): Box[U] { return Box { v: f(b.v) }; }
function (b: Box[T]) get(): T { return b.v; }
function big(x: i32): boolean { return x > 3; }
function main(): i32 { let b: Box[i32] = Box { v: 7 }; let c: Box[boolean] = b.map(big); if (c.get()) { return 0; } return 1; }`,
	}
	for _, src := range ok {
		if err := checkSource(t, src); err != nil {
			t.Errorf("generic-receiver method should type-check, got: %v\nsrc: %s", err, src)
		}
	}
}

func TestReturnPositionInference(t *testing.T) {
	ok := []string{
		`function empty[T](): T[] { return []; }
function main(): i32 { let xs: i32[] = empty(); return xs.len(); }`,
		`function empty[T](): T[] { return []; }
function strs(): string[] { return empty(); }
function main(): i32 { return strs().len(); }`,
		`function wrap[T](x: T): T[] { return [x]; }
function main(): i32 { let xs: i32[] = wrap(5); return xs.len(); }`,
		`struct Box[T] { v: T }
function make_box[T](v: T): Box[T] { return Box { v: v }; }
function main(): i32 { let b: Box[i32] = make_box(3); return b.v; }`,
	}
	for _, src := range ok {
		if err := checkSource(t, src); err != nil {
			t.Errorf("return-position inference should type-check, got: %v\nsrc: %s", err, src)
		}
	}

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
    let xs: string[] = "a,b,c".split(",");
    return len(xs);
}`)
	if err == nil {
		t.Fatal("expected a type error for .split() without import \"std/string\", got nil")
	}
	for _, want := range []string{`no method "split" on string`, "add `import \"std/string\"`"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not contain %q", err.Error(), want)
		}
	}
}

func TestMultipleErrorsAreReported(t *testing.T) {
	src := `function f(): i32 {
		return true;
		let x = unknownThing;
	}`
	err := checkSource(t, src)
	if err == nil {
		t.Fatal("expected errors")
	}
	msg := err.Error()
	if !strings.Contains(msg, "return value: expected") {
		t.Errorf("missing return mismatch: %s", msg)
	}
	if !strings.Contains(msg, "cannot find") {
		t.Errorf("missing undefined identifier: %s", msg)
	}
}

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
		{"Reader", `struct Reader { fd: i32 }
			function main(): i32 { return 0; }`},
		{"Writer", `struct Writer { fd: i32 }
			function main(): i32 { return 0; }`},
		{"HttpRequest", `struct HttpRequest { method: string, path: string, body: string }
			function main(): i32 { return 0; }`},
		{"HttpResponse", `struct HttpResponse { status: i32, body: string }
			function main(): i32 { return 0; }`},
		{"Span", `enum Span { Empty, Wide(f64, string) }
			function main(): i32 { return 0; }`},
		{"Option", `struct Option { n: i32 }
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
			if !IsReservedName(c.name) {
				t.Errorf("IsReservedName(%q) returned false but the checker rejects it", c.name)
			}
		})
	}
	for _, name := range []string{"Foo", "Bar", "main", "x"} {
		if IsReservedName(name) {
			t.Errorf("IsReservedName(%q) returned true for a user-name", name)
		}
	}
}

func TestJsonValueIsNotReserved(t *testing.T) {
	if err := checkSource(t, `enum JsonValue { JNull }
function main(): i32 { let v: JsonValue = JNull; return 0; }`); err != nil {
		t.Fatalf("check: %v", err)
	}
}

func TestReservedShadowIsReportedOnce(t *testing.T) {
	err := checkSource(t, "enum Option { A, B }\nfunction main(): i32 { return 0; }\n")
	if err == nil || !strings.Contains(err.Error(), "reserved built-in name") {
		t.Fatalf("want the reserved-name error, got %v", err)
	}
	if strings.Contains(err.Error(), "type parameter(s)") {
		t.Fatalf("the redeclaration was reported again as an arity mismatch: %v", err)
	}
}

func TestBuiltinPutchar(t *testing.T) {
	if err := checkSource(t, `function f(): void { putchar(65); }`); err != nil {
		t.Errorf("putchar(65) should type-check: %v", err)
	}
	if err := checkSource(t, `function f(): void { putchar(true); }`); err == nil {
		t.Errorf("putchar(true) should fail")
	}
}

func TestRetiredIoBuiltinsAreUnknown(t *testing.T) {
	for _, src := range []string{
		`function f(): u8[] { return tcp_recv(3, 16); }`,
		`function f(): i32 { return tcp_send(3, "x"); }`,
		`function f(): i32 { return tcp_connect(16777343, 80); }`,
		`function f(): i32 { return udp_send("127.0.0.1", 8125, "x"); }`,
		`function f(b: u8[]): i32 { return udp_send_bytes("127.0.0.1", 8125, b); }`,
		`function f(): Option[string] { return read_line(); }`,
		`function f(): Option[string] { return stdin().read_line(); }`,
	} {
		if err := checkSource(t, src); err == nil {
			t.Errorf("%s: want an error, the builtin is retired", src)
		}
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
		`function f(x: i32): f32 { return x + 1.5; }`,
		`function f(x: f32): f32 { return x % 1.0; }`,
	}
	for _, src := range cases {
		if err := checkSource(t, src); err == nil {
			t.Errorf("%q: expected error", src)
		}
	}
}

func TestPolyIntLiteralPromotesToFloat(t *testing.T) {
	cases := []string{
		`function f(x: f32): f32 { return x + 1; }`,
		`function f(x: f32): f32 { return x * 2; }`,
		`function f(x: f64): f64 { return x - 100; }`,
		`function f(x: f32): boolean { return x <= 0; }`,
		`function f(): i32 { let r: f32 = 0; return 0; }`,
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
		let a: string = "hi";
		let b: string = "hi";
		return a == b;
	}`
	if err := checkSource(t, src); err != nil {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestStringIndexReturnsByte(t *testing.T) {
	src := `function f(): u8 {
		let s: string = "abc";
		return s[1];
	}`
	if err := checkSource(t, src); err != nil {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestStringIndexWidensToI32(t *testing.T) {
	src := `function f(): i32 {
		let s: string = "abc";
		return s[1];
	}`
	if err := checkSource(t, src); err != nil {
		t.Fatal(err)
	}
}

func TestStringIndexWidensWithCast(t *testing.T) {
	src := `function f(): i32 {
		let s: string = "abc";
		return s[1] as i32;
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

func TestLenOnSlice(t *testing.T) {
	src := `function f(): i32 {
		let xs: i32[] = [1, 2, 3, 4];
		let s: [i32] = xs[1:3];
		return s.len();
	}`
	if err := checkSource(t, src); err != nil {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestFreeLenIsRejected(t *testing.T) {
	cases := []string{
		`function f(): i32 { let s: string = "hi"; return len(s); }`,
		`function f(): i32 { let xs: i32[] = [1, 2]; return len(xs); }`,
	}
	for _, src := range cases {
		err := checkSource(t, src)
		if err == nil {
			t.Errorf("%s: expected error, got none", src)
			continue
		}
		if !strings.Contains(err.Error(), "cannot find \"len\" in this scope") {
			t.Errorf("%s: expected `cannot find \"len\" in this scope` diagnostic, got %v", src, err)
		}
	}
}

func TestStringCmpFlagSet(t *testing.T) {
	prog, err := parser.Parse(`function f(): boolean {
		let a: string = "x";
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

func TestUndefinedIdentifierSuggestionBreaksTiesTheSameWay(t *testing.T) {
	for _, c := range []struct{ src, want string }{
		{"function f(): i32 {\n\tlet x: i32 = 1;\n\tlet y: i32 = 2;\n\treturn n;\n}", "y"},
		{"function ab(): i32 { return 1; }\nfunction aa(): i32 { return 2; }\nfunction f(): i32 { return ac(); }", "aa"},
	} {
		for range 20 {
			prog, err := parser.Parse(c.src)
			if err != nil {
				t.Fatal(err)
			}
			_, err = Check(prog)
			es, ok := err.(diag.Errors)
			if !ok || len(es) == 0 {
				t.Fatalf("expected diag.Errors, got %v", err)
			}
			if ce, ok := es[0].(*Error); !ok || ce.Fix == nil || ce.Fix.Replacement != c.want {
				t.Fatalf("%q: fix = %+v, want the replacement %q", c.src, es[0], c.want)
			}
		}
	}
}

func TestUndefinedIdentifierSuggestsClosest(t *testing.T) {
	prog, err := parser.Parse(`function f(): i32 {
		let counter: i32 = 0;
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
		let counter: i32 = 0;
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

func TestUnknownFieldFixApplies(t *testing.T) {
	cases := []struct {
		name string
		src  string
	}{
		{"access", "struct P { count: i32 }\nfunction main(): i32 {\n\tlet p = P { count: 1 };\n\treturn p.countr;\n}"},
		{"literal", "struct P { count: i32 }\nfunction main(): i32 {\n\tlet p = P { countr: 1 };\n\treturn 0;\n}"},
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

func TestUnknownMethodFixSuggestsMethod(t *testing.T) {
	src := "struct P { x: i32 }\n" +
		"pub function (p: P) push(v: i32): i32 { return p.x + v; }\n" +
		"function main(): i32 {\n" +
		"\tlet p = P { x: 1 };\n" +
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

func TestUndefinedIdentifierFixApplies(t *testing.T) {
	src := `function f(): i32 {
	let counter: i32 = 0;
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
			let p: Point = Point { x: 1, y: 2 };
			p = Point { ...p, x: 10 };
			return p.x + p.y;
		}`
	if err := checkSource(t, src); err != nil {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestFieldAssignmentRejected(t *testing.T) {
	src := `struct Point { x: i32, y: i32 }
		function main(): i32 {
			let p: Point = Point { x: 1, y: 2 };
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

func TestCompoundFieldAssignmentRejected(t *testing.T) {
	src := `struct Acc { v: i32 }
		function main(): i32 {
			let a: Acc = Acc { v: 7 };
			a.v += 35;
			return a.v;
		}`
	if err := checkSource(t, src); err == nil || !strings.Contains(err.Error(), "fields are immutable after construction") {
		t.Errorf("expected E048 for compound field assignment, got: %v", err)
	}
}

func TestNestedFieldAssignmentRejected(t *testing.T) {
	src := `struct Inner { x: i32 }
		struct Outer { inner: Inner }
		function main(): i32 {
			let o: Outer = Outer { inner: Inner { x: 0 } };
			o.inner.x = 42;
			return o.inner.x;
		}`
	if err := checkSource(t, src); err == nil || !strings.Contains(err.Error(), "fields are immutable after construction") {
		t.Errorf("expected E048 for nested field assignment, got: %v", err)
	}
}

func TestPointerCaptureWriteBackRejected(t *testing.T) {
	src := `function main(): i32 {
		let name: string = "a";
		let f = (): i32 => {
			name = "b";
			return name.len();
		};
		return f();
	}`
	if err := checkSource(t, src); err == nil || !strings.Contains(err.Error(), "reference-typed closure capture") {
		t.Errorf("expected E049 for pointer capture write-back, got: %v", err)
	}
}

func TestNestedPointerCaptureWriteBackRejected(t *testing.T) {
	src := `function main(): i32 {
		let acc: i32[] = [];
		let outer = (): i32 => {
			let inner = (): i32 => {
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

func TestClosureCapturesGenericValue(t *testing.T) {
	if err := checkSource(t, `function apply[S](init: S, f: (S) => i32): i32 {
    let run: () => i32 = (): i32 => f(init);
    return run();
}
function main(): i32 { return apply(7, (n: i32): i32 => n + 1); }`); err != nil {
		t.Fatalf("a generic-typed capture should be accepted, got: %v", err)
	}
	err := checkSource(t, `function log(): void { print("x"); }
function main(): i32 {
    let done = log();
    let f = () => done;
    return 0;
}`)
	if err == nil || !hasCode(err, "E044") {
		t.Fatalf("want E044 on a captured void, got: %v", err)
	}
}

func TestOuterRebindReachingCapturedClosureRejected(t *testing.T) {
	cases := []struct{ name, src string }{
		{"direct", `function main(): i32 {
			let g: () => i32 = (): i32 => { return 1; };
			let f: () => i32 = (): i32 => { return g(); };
			g = f;
			return 0;
		}`},
		{"through_call", `function ident(x: () => i32): () => i32 { return x; }
		function main(): i32 {
			let g: () => i32 = (): i32 => { return 1; };
			let f: () => i32 = (): i32 => { return g(); };
			g = ident(f);
			return 0;
		}`},
		{"local_func", `function main(): i32 {
			let g: () => i32 = (): i32 => { return 1; };
			function f(): i32 { return g(); }
			g = f;
			return 0;
		}`},
		{"mutual", `function main(): i32 {
			let a: () => i32 = (): i32 => { return 1; };
			let b: () => i32 = (): i32 => { return 2; };
			let x: () => i32 = (): i32 => { return b(); };
			let y: () => i32 = (): i32 => { return a(); };
			a = x;
			b = y;
			return 0;
		}`},
		{"struct_field", `struct Holder { cb: () => i32 }
		function main(): i32 {
			let h: Holder = Holder { cb: (): i32 => { return 0; } };
			let f: () => i32 = (): i32 => { return (h.cb)(); };
			h = Holder { cb: f };
			return 0;
		}`},
		{"nested_struct", `struct Inner { cb: () => i32 }
		struct Outer { inner: Inner }
		function main(): i32 {
			let o: Outer = Outer { inner: Inner { cb: (): i32 => { return 0; } } };
			let f: () => i32 = (): i32 => { return (o.inner.cb)(); };
			o = Outer { inner: Inner { cb: f } };
			return 0;
		}`},
		{"array_element", `function main(): i32 {
			let arr: (() => i32)[] = [];
			let f: () => i32 = (): i32 => { return arr.len(); };
			arr = arr.append(f);
			return 0;
		}`},
		{"tuple_element", `function main(): i32 {
			let t: (i32, () => i32) = (1, (): i32 => { return 0; });
			let f: () => i32 = (): i32 => { return t.0; };
			t = (2, f);
			return 0;
		}`},
		{"enum_payload", `enum Box { None, Some(() => i32) }
		function main(): i32 {
			let e: Box = Box.None;
			let f: () => i32 = (): i32 => { return match (e) { Box.None => 1, Box.Some(_) => 2 }; };
			e = Box.Some(f);
			return 0;
		}`},
		{"dyn_object", `trait Runner { function run(self: Self): i32; }
		struct Wrap { cb: () => i32 }
		impl Runner for Wrap { function run(self: Self): i32 { return (self.cb)(); } }
		function main(): i32 {
			let d: dyn Runner = Wrap { cb: (): i32 => { return 0; } };
			let f: () => i32 = (): i32 => { return d.run(); };
			d = Wrap { cb: f };
			return 0;
		}`},
		{"generic_enum_of_closure", `function main(): i32 {
			let o: Option[() => i32] = None;
			let f: () => i32 = (): i32 => { return match (o) { Some(_) => 1, None => 0 }; };
			o = Some(f);
			return 0;
		}`},
	}
	for _, tc := range cases {
		err := checkSource(t, tc.src)
		if err == nil || !strings.Contains(err.Error(), "would close a reference cycle") {
			t.Errorf("%s: expected E049 for an outer rebind reaching a captured closure, got: %v", tc.name, err)
		}
	}
}

func TestNestedClosureLocalRebindRejected(t *testing.T) {
	src := `function main(): i32 {
		let outer: () => i32 = (): i32 => {
			let g: () => i32 = (): i32 => { return 1; };
			let inner: () => i32 = (): i32 => { return g(); };
			g = inner;
			return 0;
		};
		return outer();
	}`
	if err := checkSource(t, src); err == nil || !strings.Contains(err.Error(), "would close a reference cycle") {
		t.Errorf("expected E049 for a rebind inside the declaring closure, got: %v", err)
	}
}

func TestOuterRebindRejectedWhenClosureIsDeclaredLater(t *testing.T) {
	src := `function main(): i32 {
		let g: () => i32 = (): i32 => { return 1; };
		let f: () => i32 = (): i32 => { return 2; };
		let i: i32 = 0;
		while (i < 2) {
			g = f;
			f = (): i32 => { return g(); };
			i = i + 1;
		}
		return 0;
	}`
	if err := checkSource(t, src); err == nil || !strings.Contains(err.Error(), "would close a reference cycle") {
		t.Errorf("expected E049 for a store preceding the capturing closure, got: %v", err)
	}
}

func TestOuterRebindOfFunctionFreeCaptureAllowed(t *testing.T) {
	cases := []struct{ name, src string }{
		{"string", `function main(): i32 {
			let s: string = "a";
			let f: () => i32 = (): i32 => { return s.len(); };
			s = "bb";
			return f();
		}`},
		{"array", `function main(): i32 {
			let arr: i32[] = [1, 2];
			let f: () => i32 = (): i32 => { return arr.len(); };
			arr = arr.append(3);
			return f();
		}`},
		{"struct", `struct P { a: i32 }
		function main(): i32 {
			let p: P = P { a: 1 };
			let f: () => i32 = (): i32 => { return p.a; };
			p = P { a: 2 };
			return f();
		}`},
		{"scalar", `function main(): i32 {
			let n: i32 = 1;
			let f: () => i32 = (): i32 => { return n; };
			n = 99;
			return f();
		}`},
		{"generic-enum-of-scalar", `struct S { o: Option[i32] }
		function main(): i32 {
			let s: S = S { o: Some(1) };
			let f: () => i32 = (): i32 => { return match (s.o) { Some(v) => v, None => 0 }; };
			s = S { o: None };
			return f();
		}`},
		{"generic-struct-of-scalar", `struct Box[T] { v: T }
		function main(): i32 {
			let b: Box[i32] = Box { v: 1 };
			let f: () => i32 = (): i32 => { return b.v; };
			b = Box { v: 2 };
			return f();
		}`},
		{"struct-into-dyn", `trait Shape { function area(self: Self): i32; }
		struct Sq { s: i32 }
		impl Shape for Sq { function area(self: Self): i32 { return self.s; } }
		function main(): i32 {
			let d: dyn Shape = Sq { s: 3 };
			let f: () => i32 = (): i32 => { return d.area(); };
			d = Sq { s: 5 };
			return f();
		}`},
	}
	for _, tc := range cases {
		if err := checkSource(t, tc.src); err != nil {
			t.Errorf("%s: an outer rebind that cannot reach a closure should compile, got: %v", tc.name, err)
		}
	}
}

func TestFreshClosureLiteralRebindOfCapturedVariableAllowed(t *testing.T) {
	src := `function main(): i32 {
		let flip: boolean = false;
		let g = (): (string, i32) => { return ("abcd", 4); };
		if (flip) { g = (): (string, i32) => { return ("z", 1); }; }
		let h = () => g().1 + 38;
		return h();
	}`
	if err := checkSource(t, src); err != nil {
		t.Errorf("swapping a captured callback for a fresh closure literal should compile, got: %v", err)
	}
}

func TestClosureLiteralThatCapturesIsStillRejected(t *testing.T) {
	cases := []struct{ name, src string }{
		{"captures_target", `function main(): i32 {
			let g: () => i32 = (): i32 => { return 1; };
			let f: () => i32 = (): i32 => { return g(); };
			g = (): i32 => { return g() + 1; };
			return 0;
		}`},
		{"captures_a_closure", `function main(): i32 {
			let g: () => i32 = (): i32 => { return 1; };
			let f: () => i32 = (): i32 => { return g(); };
			g = (): i32 => { return f(); };
			return 0;
		}`},
	}
	for _, tc := range cases {
		err := checkSource(t, tc.src)
		if err == nil || !strings.Contains(err.Error(), "reference cycle") {
			t.Errorf("%s: expected E049, got: %v", tc.name, err)
		}
	}
}

func TestRebindOfUncapturedClosureVariableAllowed(t *testing.T) {
	src := `function main(): i32 {
		let g: () => i32 = (): i32 => { return 1; };
		let h: () => i32 = (): i32 => { return 2; };
		g = h;
		return g();
	}`
	if err := checkSource(t, src); err != nil {
		t.Errorf("rebinding an uncaptured closure variable should compile, got: %v", err)
	}
}

func TestShadowingLocalIsNotTheCapturedVariable(t *testing.T) {
	src := `function main(): i32 {
		let g: () => i32 = (): i32 => { return 1; };
		let outer: () => i32 = (): i32 => { return g(); };
		let pick: () => i32 = (): i32 => {
			let g: () => i32 = (): i32 => { return 2; };
			let h: () => i32 = (): i32 => { return 3; };
			g = h;
			return g();
		};
		return outer() + pick();
	}`
	if err := checkSource(t, src); err != nil {
		t.Errorf("assigning a closure to a shadowing local should compile, got: %v", err)
	}
}

func TestScalarCaptureWriteBackAllowed(t *testing.T) {
	src := `function makeCounter(): () => i32 {
		let count: i32 = 0;
		function tick(): i32 {
			count = count + 1;
			return count;
		}
		return tick;
	}
	function main(): i32 {
		let c = makeCounter();
		return c() + c();
	}`
	if err := checkSource(t, src); err != nil {
		t.Errorf("scalar (counter) capture write-back should compile, got: %v", err)
	}
}

func TestCaptureReadAndLocalAssignStillAllowed(t *testing.T) {
	src := `function main(): i32 {
		let n: i32 = 5;
		let f = (x: i32): i32 => {
			let local: i32 = x;
			local = local + n;
			return local;
		};
		return f(37);
	}`
	if err := checkSource(t, src); err != nil {
		t.Errorf("capture read + local assignment should compile, got: %v", err)
	}
}

func TestImmutableRecursiveTypeStillCompiles(t *testing.T) {
	src := `struct Leaf { v: i32 }
		struct Node { left: Tree, right: Tree }
		type Tree = Leaf | Node;
		function main(): i32 {
			let t: Tree = Node { left: Leaf { v: 40 }, right: Node { left: Leaf { v: 1 }, right: Leaf { v: 1 } } };
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

func TestExitCallDiverges(t *testing.T) {
	for _, c := range []struct{ name, src string }{
		{"body-ends-in-exit", `function f(): i32 { exit(4); }`},
		{"both-branches-exit", `function f(b: boolean): i32 {
			if (b) { return 1; } else { eprint("no"); exit(2); }
		}`},
		{"match-arm-exits", `function f(n: i32): i32 {
			match (n) { 0 => { return 0; }, _ => { exit(1); } }
		}`},
		{"let-else-exits", `enum Opt { Has(i32), Nil }
		function f(o: Opt): i32 {
			let Has(v) = o else { eprint("absent"); exit(1); };
			return v;
		}`},
		{"todo", `function f(): i32 { todo("later"); }`},
		{"exit-moves-nothing-past-it", `function take(own s: string): void { print(s); }
		function f(b: boolean, own s: string): void {
			if (b) { take(s); exit(1); }
			print(s);
		}`},
	} {
		t.Run(c.name, func(t *testing.T) {
			if err := checkSource(t, c.src); err != nil {
				t.Errorf("a trailing exit should diverge, got: %v", err)
			}
		})
	}
}

func TestShadowedOrNonExitCallDoesNotDiverge(t *testing.T) {
	for _, c := range []struct{ name, src, code string }{
		{"param-named-exit", `function f(exit: (i32) => void): i32 { exit(4); }`, "E052"},
		{"local-named-exit", `function f(): i32 {
			let exit: (i32) => void = (n: i32): void => { print("local"); };
			exit(4);
		}`, "E052"},
		{"let-else-local-exit", `enum Opt { Has(i32), Nil }
		function f(o: Opt, exit: (i32) => void): i32 {
			let Has(v) = o else { exit(1); };
			return v;
		}`, "E022"},
		{"assert-false", `function f(): i32 { assert(false, "never"); }`, "E052"},
		{"exit-not-last", `function f(): i32 { exit(1); print("after"); }`, "E052"},
	} {
		t.Run(c.name, func(t *testing.T) {
			err := checkSource(t, c.src)
			if errorWithCode(err, c.code) == nil {
				t.Fatalf("want %s, got %v", c.code, err)
			}
		})
	}
}

func TestAssertAndTodoIgnoreShadowingNames(t *testing.T) {
	for _, c := range []struct{ name, src string }{
		{"todo-param-closure", `function f(exit: (i32) => void): i32 { todo("later"); }`},
		{"todo-let-values", `function f(): string {
			let exit: i32 = 1;
			let eprint: string = "x";
			todo;
		}`},
		{"todo-arm-binding", `enum Opt { Has(i32), Nil }
		function f(o: Opt): i32 {
			match (o) { Has(exit) => { todo; }, Nil => { return 0; } }
		}`},
		{"todo-in-closure", `function f(exit: i32): i32 {
			let g: () => i32 = (): i32 => { todo; };
			return g() + exit;
		}`},
		{"todo-after-owned-move", `function take(own s: string): void { print(s); }
		function f(own exit: string): i32 {
			take(exit);
			todo;
		}`},
		{"assert-param-values", `function f(exit: i32, eprint: string): i32 {
			assert(exit > 0, eprint);
			return exit;
		}`},
		{"assert-in-closure", `function f(exit: i32): i32 {
			let g: (i32) => i32 = (k: i32): i32 => { assert(k > exit); return k; };
			return g(2);
		}`},
	} {
		t.Run(c.name, func(t *testing.T) {
			if err := checkSource(t, c.src); err != nil {
				t.Errorf("assert and todo call the builtins whatever is in scope, got: %v", err)
			}
		})
	}
}

func TestFoldedAssertFalseDoesNotDiverge(t *testing.T) {
	prog, err := parser.Parse(`function f(): i32 { assert(false, "never"); }`)
	if err != nil {
		t.Fatal(err)
	}
	if err := constfold.Fold(prog, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := Check(prog); errorWithCode(err, "E052") == nil {
		t.Fatalf("an assert folded to if (true) must not end the body: want E052, got %v", err)
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

func TestIfExprUnifiesBareEnumWithSpecifiedArm(t *testing.T) {
	cases := []string{
		`function f(b: boolean): Option[i32] {
			return if (b) { Some(7) } else { None };
		}`,
		`function f(b: boolean): Option[i32] {
			return if (b) { None } else { Some(7) };
		}`,
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

func TestNonVariantCallReturningGenericEnum(t *testing.T) {
	cases := []string{
		`function f(p: boolean[]): Option[i32] { return None; }
function main(): i32 { let v: Option[i32] = f([true]); return 0; }`,
		`function g(p: i32[], q: string): Option[i64] { return None; }
function main(): i32 { let v: Option[i64] = g([1], "x"); return 0; }`,
		`function h(p: boolean[]): Result[i32, i32] { return Ok(0); }
function main(): i32 { let v: Result[i32, i32] = h([true]); return 0; }`,
	}
	for _, src := range cases {
		if err := checkSource(t, src); err != nil {
			t.Errorf("unexpected error for non-variant Option/Result-returning call: %v\nsrc:\n%s", err, src)
		}
	}
}

func TestOptionTryTypechecks(t *testing.T) {
	src := `function f(m: Map[i32, i32]): Option[i32] {
		let v: i32 = m.get(1)?;
		return Some(v);
	}`
	if err := checkSource(t, src); err != nil {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestOptionTryRejectsNonOption(t *testing.T) {
	src := `function f(): Option[i32] {
		let n: i32 = 5;
		let v: i32 = n?;
		return Some(v);
	}`
	if err := checkSource(t, src); err == nil {
		t.Error("expected error: `?` on non-Option")
	}
}

func TestOptionTryRejectsNonOptionReturn(t *testing.T) {
	src := `function f(m: Map[i32, i32]): i32 {
		let v: i32 = m.get(1)?;
		return v;
	}`
	if err := checkSource(t, src); err == nil {
		t.Error("expected error: enclosing fn must return Option[_]")
	}
}

func TestResultTryTypechecks(t *testing.T) {
	src := `function inner(): Result[i32, i32] { return Ok(42); }
function outer(): Result[i32, i32] {
	let v: i32 = inner()?;
	return Ok(v + 1);
}`
	if err := checkSource(t, src); err != nil {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestResultTryRejectsNonResultReturn(t *testing.T) {
	src := `function inner(): Result[i32, i32] { return Ok(42); }
function outer(): i32 {
	let v: i32 = inner()?;
	return v;
}`
	if err := checkSource(t, src); err == nil {
		t.Error("expected error: enclosing fn must return Result[_, E]")
	}
}

func TestResultTryRejectsErrTypeMismatch(t *testing.T) {
	src := `function inner(): Result[i32, i32] { return Ok(42); }
function outer(): Result[i32, string] {
	let v: i32 = inner()?;
	return Ok(v);
}`
	if err := checkSource(t, src); err == nil {
		t.Error("expected error: source's Err type doesn't match enclosing return")
	}
}

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
	let l: Light = Green;
	return match (l) {
		Red   => 1,
		Green => 2
	};
}`
	if err := checkSource(t, src); err == nil {
		t.Error("expected error: missing Yellow arm")
	}
}

func TestNumericLiteralSuffixesTypecheck(t *testing.T) {
	for _, src := range []string{
		`function f(): i32 { let x: i64 = 42i64; return 0; }`,
		`function f(): i32 { let x: u8 = 7u8; return 0; }`,
		`function f(): i32 { let x: f64 = 1.5f64; return 0; }`,
		`function f(): i32 { let x: f32 = 42f32; return 0; }`,
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

func TestNumericLiteralSuffixesRejectMismatch(t *testing.T) {
	if err := checkSource(t, `function f(): i32 { let x: i32 = 42i64; return 0; }`); err == nil {
		t.Error("expected error: assigning i64 literal to i32 var")
	}
}

func TestArrayPushTypechecks(t *testing.T) {
	for _, src := range []string{
		`function f(): i32 { let xs: string[] = []; xs = xs.append("a"); return xs.len(); }`,
		`function f(): i32 { let xs: i32[] = [1, 2]; xs = xs.append(3); return xs[2]; }`,
	} {
		if err := checkSource(t, src); err != nil {
			t.Errorf("%q: unexpected error %v", src, err)
		}
	}
}

func TestArrayPushI64StridePasses(t *testing.T) {
	for _, src := range []string{
		`function f(): i32 { let xs: i64[] = [1i64, 2i64]; xs = xs.append(3i64); return 0; }`,
		`function f(): i32 { let xs: u64[] = [1u64]; xs = xs.append(2u64); return 0; }`,
	} {
		if err := checkSource(t, src); err != nil {
			t.Errorf("%q: unexpected error %v", src, err)
		}
	}
}

func TestArrayPushF64StridePasses(t *testing.T) {
	src := `function f(): i32 { let xs: f64[] = [1.0f64]; xs = xs.append(2.0f64); return 0; }`
	if err := checkSource(t, src); err != nil {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestArrayPushSubI32StridePasses(t *testing.T) {
	for _, src := range []string{
		`function f(): i32 { let xs: u8[] = []; xs = xs.append(7u8); return 0; }`,
	} {
		if err := checkSource(t, src); err != nil {
			t.Errorf("%q: unexpected error %v", src, err)
		}
	}
}

func TestArrayPushRejectsArgTypeMismatch(t *testing.T) {
	src := `function f(): i32 { let xs: string[] = []; xs = xs.append(1); return xs.len(); }`
	if err := checkSource(t, src); err == nil {
		t.Error("expected error: pushing i32 onto string[]")
	}
}

func TestMatchExprWildcardCoversExhaustiveness(t *testing.T) {
	src := `enum Light { Red, Green, Yellow }
function pick(): i32 {
	let l: Light = Green;
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
		let x: i32 = 0;
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

func TestMissingReturnRejected(t *testing.T) {
	for _, src := range []string{
		`struct P { x: i32, y: i32 }
		function f(b: boolean): P {
			if (b) { return P { x: 10, y: 20 }; }
		}`,
		`function g(): i32 { let x = 1; }`,
		`function h(b: boolean): i32 {
			if (b) { return 1; } else { let z = 2; }
		}`,
		`function sw(n: i32): i32 {
			match (n) { 0 => { return 0; }, 1 => { return 1; }, _ => { let z = 2; } }
		}`,
		`function never(): i32 {
			if (false) { return 1; }
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

func TestMissingReturnAcceptsDivergentForms(t *testing.T) {
	for _, src := range []string{
		`function f(b: boolean): i32 {
			if (b) { return 1; } else { return 2; }
		}`,
		`function hosted(): i32 {
			if (true) { return 1; }
		}`,
		`function dialled(): i32 {
			if (false) { let z = 1; } else { return 2; }
		}`,
		`function g(b: boolean): i32 {
			if (b) { return 1; }
			return 0;
		}`,
		`function loops(): i32 {
			while (true) { let x = 1; }
		}`,
		`function loops2(): i32 {
			loop { let x = 1; }
		}`,
		`function sw(n: i32): i32 {
			match (n) { 0 => { return 0; }, _ => { return 1; } }
		}`,
		`function v(n: i32): void { let x = n + 1; }`,
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

func TestStructUpdateAllowsSubsetFields(t *testing.T) {
	src := `struct P { x: i32, y: i32 }
		function f(): P {
			let a: P = P { x: 1, y: 2 };
			return P { ...a, y: 9 };
		}`
	if err := checkSource(t, src); err != nil {
		t.Errorf("struct-update with a subset of fields should check clean, got: %v", err)
	}
}

func TestStructUpdatePureCopyChecks(t *testing.T) {
	src := `struct P { x: i32, y: i32 }
		function f(): P {
			let a: P = P { x: 1, y: 2 };
			return P { ...a };
		}`
	if err := checkSource(t, src); err != nil {
		t.Errorf("pure-copy struct-update should check clean, got: %v", err)
	}
}

func TestStructUpdateRejectsWrongBaseType(t *testing.T) {
	src := `struct P { x: i32, y: i32 }
		struct Q { z: i32 }
		function f(): P {
			let q: Q = Q { z: 1 };
			return P { ...q, y: 9 };
		}`
	if err := checkSource(t, src); err == nil {
		t.Error("expected error: struct-update base type Q does not match P")
	}
}

func TestStructUpdateRejectsUnknownOverrideField(t *testing.T) {
	src := `struct P { x: i32, y: i32 }
		function f(): P {
			let a: P = P { x: 1, y: 2 };
			return P { ...a, nope: 9 };
		}`
	if err := checkSource(t, src); err == nil {
		t.Error("expected error: override names a field P does not have")
	}
}

func TestUnknownStructType(t *testing.T) {
	src := `function f(): i32 {
		let p: NoSuchStruct = NoSuchStruct { x: 1 };
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
		let bonus: i32 = 100;
		function inner(x: i32): i32 { return x + seed + bonus; }
		return inner(1);
	}`)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Check(prog); err != nil {
		t.Fatal(err)
	}
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

func TestArrayDestinationBindsGenericElement(t *testing.T) {
	accepted := map[string]string{
		"literal argument": `function wrap[T](x: T): T[] { return [x]; }
		function main(): i32 { let xs: i64[] = wrap(5); return xs[0] as i32; }`,
		"wide literal argument": `function wrap[T](x: T): T[] { return [x]; }
		function main(): i32 { let xs: i64[] = wrap(1234567890123); return (xs[0] / 1000000000000) as i32; }`,
		"two literal arguments": `function two[T](a: T, b: T): T[] { return [a, b]; }
		function main(): i32 { let xs: i64[] = two(1, 4611686018427387904); return xs[0] as i32; }`,
	}
	for name, src := range accepted {
		if err := checkSource(t, src); err != nil {
			t.Errorf("%s: unexpected error: %v", name, err)
		}
	}
	rejected := `function wrap[T](x: T): T[] { return [x]; }
	function main(): i32 { let xs: i64[] = wrap(5i32); return xs[0] as i32; }`
	err := checkSource(t, rejected)
	es, ok := err.(diag.Errors)
	if !ok || len(es) == 0 {
		t.Fatalf("concrete i32 element into i64[]: want E003, got %v", err)
	}
	if ce, ok := es[0].(*Error); !ok || ce.ErrCode != "E003" {
		t.Errorf("concrete i32 element into i64[]: want E003, got %v", es[0])
	}
}

func TestNestedFunctionNameDoesNotShadowTopLevel(t *testing.T) {
	cases := map[string]string{
		"top-level declared after": `function outer(n: i32): i32 {
			function at(x: i32): i32 { return x + 1; }
			return at(n);
		}
		function at(p: i32, q: i32): i32 { return p * q; }
		function main(): i32 { return at(3, 4) + outer(1); }`,
		"top-level declared before": `function at(p: i32, q: i32): i32 { return p * q; }
		function outer(n: i32): i32 {
			function at(x: i32): i32 { return x + 1; }
			return at(n);
		}
		function main(): i32 { return at(3, 4) + outer(1); }`,
		"nested passed as a value": `function apply(f: (i32) => i32, v: i32): i32 { return f(v); }
		function outer(n: i32): i32 {
			function at(x: i32): i32 { return x + n; }
			return apply(at, 1);
		}
		function at(p: i32, q: i32): i32 { return p * q; }
		function main(): i32 { return at(3, 4) + outer(1); }`,
	}
	for name, src := range cases {
		if err := checkSource(t, src); err != nil {
			t.Errorf("%s: unexpected error: %v", name, err)
		}
	}
}

func TestPointerCapturesTypecheck(t *testing.T) {
	cases := []string{
		`function outer(s: string): i32 {
			function inner(): i32 { return s.len(); }
			return inner();
		}`,
		`function outer(xs: i32[]): i32 {
			function inner(): i32 { return xs.len(); }
			return inner();
		}`,
		`struct Pt { x: i32, y: i32 }
		function outer(p: Pt): i32 {
			function inner(): i32 { return p.x + p.y; }
			return inner();
		}`,
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
			let p: Point = Point { x: 10, y: 32 };
			return p.sum();
		}`)
	if err != nil {
		t.Fatal(err)
	}
	info, err := Check(prog)
	if err != nil {
		t.Fatalf("check: %v", err)
	}
	mangled, ok := info.Methods["Point.sum"]
	if !ok {
		t.Fatal("Methods map missing Point.sum")
	}
	if mangled != "__method_Point_sum" {
		t.Errorf("mangled = %q, want \"__method_Point_sum\"", mangled)
	}
	main := prog.Funcs[1]
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

func TestMethodAcceptsBuiltinReceivers(t *testing.T) {
	if err := checkSource(t, `function (xs: i32[]) total(): i32 { return 0; }
function main(): i32 { let a: i32[] = [1]; return a.total(); }`); err != nil {
		t.Errorf("concrete-element array receiver should be accepted, got: %v", err)
	}
	if err := checkSource(t, `function (xs: [u8]) first_byte(): i32 { return 0; }
function main(): i32 { return 0; }`); err != nil {
		t.Errorf("concrete-element slice receiver should be accepted, got: %v", err)
	}
	if err := checkSource(t, `function (xs: T[]) first(): T { return xs[0]; }
function main(): i32 { let a: i32[] = [1]; return a.first(); }`); err != nil {
		t.Errorf("element-polymorphic array receiver should be accepted, got: %v", err)
	}
}

func TestNestedArrayReceiverRejected(t *testing.T) {
	err := checkSource(t, `function (xs: T[][]) flat(): i32 { return xs.len(); }`)
	if err == nil {
		t.Fatal("expected error for nested-array receiver")
	}
	if !strings.Contains(err.Error(), "nested array/slice element") {
		t.Errorf("want the nested-element reason, got: %v", err)
	}
	if strings.Contains(err.Error(), "cannot find") {
		t.Errorf("rejected receiver must still be hoisted for the body walk, got: %v", err)
	}
}

func TestConcreteArrayReceiverRejectsOtherElementType(t *testing.T) {
	err := checkSource(t, `function (xs: i32[]) total(): i32 { return 0; }
function main(): i32 { let s: string[] = ["a"]; return s.total(); }`)
	if err == nil {
		t.Fatal("expected error calling an i32[] method on string[]")
	}
	msg := err.Error()
	if !strings.Contains(msg, `no method "total" on string[]`) {
		t.Errorf("want an unresolved-method message, got: %v", msg)
	}
	if strings.Contains(msg, "argument 1") {
		t.Errorf("receiver mismatch must not be reported as an argument slot, got: %v", msg)
	}
	if !strings.Contains(msg, "declared for i32[]") {
		t.Errorf("want the declared receiver type named, got: %v", msg)
	}
}

func TestMethodCallOnUnknownMethodErrors(t *testing.T) {
	src := `struct P { x: i32 }
		function main(): i32 {
			let p: P = P { x: 1 };
			return p.unknown();
		}`
	if err := checkSource(t, src); err == nil {
		t.Error("expected error for missing method")
	}
}

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

func TestNamedFieldVariantRenameAndRest(t *testing.T) {
	src := `enum Shape { Circle { r: i32 }, Rect { w: i32, h: i32 } }
function area(s: Shape): i32 {
    match (s) {
        Circle { r: radius } => { return radius * radius; },
        Rect { h: height, w } => { return w * height; },
    }
    return 0;
}
function width(s: Shape): i32 {
    return match (s) {
        Rect { w: ww, .. } => ww,
        Circle { .. } => 0,
    };
}
function nested(o: Option[Shape]): i32 {
    match (o) {
        Some(Rect { h: hh, .. }) => { return hh; },
        Some(Circle { .. }) => { return 1; },
        None => { return 0; },
    }
    return 0;
}
function zero_width(s: Shape): i32 {
    return match (s) {
        Rect { w: 0, h } => h,
        Rect { .. } => 1,
        Circle { .. } => 2,
    };
}
function if_let(s: Shape): i32 {
    if let Rect { w: ww, .. } = s {
        return ww;
    }
    return 0;
}
function main(): i32 { return area(Rect(3, 4)) + width(Circle(1)) + nested(None) + zero_width(Rect(0, 5)) + if_let(Rect(2, 3)); }`
	prog, err := parser.Parse(src)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if _, err := Check(prog); err != nil {
		t.Fatalf("named-field variant rename / `..` should type-check: %v", err)
	}
	m, ok := prog.Funcs[0].Body.Stmts[0].(*ast.Match)
	if !ok {
		t.Fatalf("area body[0] = %T, want *ast.Match", prog.Funcs[0].Body.Stmts[0])
	}
	if got := m.Arms[1].Bindings; !slices.Equal(got, []string{"w", "height"}) {
		t.Errorf("Rect { h: height, w } settled to %v, want [w height]", got)
	}
	ret, ok := prog.Funcs[1].Body.Stmts[0].(*ast.Return)
	if !ok {
		t.Fatalf("width body[0] = %T, want *ast.Return", prog.Funcs[1].Body.Stmts[0])
	}
	me, ok := ret.Value.(*ast.MatchExpr)
	if !ok {
		t.Fatalf("width returns %T, want *ast.MatchExpr", ret.Value)
	}
	if got := me.Arms[0].Bindings; !slices.Equal(got, []string{"ww", "_"}) {
		t.Errorf("Rect { w: ww, .. } settled to %v, want [ww _]", got)
	}
}

func TestStructPatternRestOnlyInPayloadSlot(t *testing.T) {
	src := `struct Pt { x: i32, y: i32 }
enum H { S(Pt), N(i32) }
function f(h: H): i32 {
    match (h) {
        S(Pt { .. }) => { return 1; },
        N(n) => { return n; },
    }
}
function main(): i32 { return f(N(4)); }`
	if err := checkSource(t, src); err != nil {
		t.Errorf("S(Pt { .. }) should type-check: %v", err)
	}
}

func TestNamedFieldVariantMatchErrors(t *testing.T) {
	cases := []struct {
		src  string
		want string
	}{
		{`enum E { V { a: i32 } }
function f(e: E): i32 { match (e) { V { b } => { return b; }, } return 0; }
function main(): i32 { return 0; }`, "has no field"},
		{`enum E { V { a: i32, b: i32 } }
function f(e: E): i32 { match (e) { V { a } => { return a; }, } return 0; }
function main(): i32 { return 0; }`, "must bind all"},
		{`enum E { V(i32) }
function f(e: E): i32 { match (e) { V { a } => { return a; }, } return 0; }
function main(): i32 { return 0; }`, "positional payloads"},
		{`enum E { V { a: i32, b: i32 } }
function f(e: E): i32 { match (e) { V { a, a: x, .. } => { return x; }, } return 0; }
function main(): i32 { return 0; }`, "bound more than once"},
		{`enum E { V { a: i32, b: i32 } }
function f(e: E): i32 { match (e) { V { c: x, .. } => { return 0; }, } return 0; }
function main(): i32 { return 0; }`, "has no field"},
		{`enum E { V { a: i32, b: i32 }, W }
function f(e: E): i32 { return match (e) { V { a: 0, .. } => 1, W => 2 }; }
function main(): i32 { return 0; }`, "does not cover variant V"},
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
			let e: E = Pair(1, 2);
			return 0;
		}`
	if err := checkSource(t, good); err != nil {
		t.Errorf("good source should type-check: %v", err)
	}

	wrongCount := `enum E { Pair(i32, i32) }
		function main(): i32 {
			let e: E = Pair(1);
			return 0;
		}`
	if err := checkSource(t, wrongCount); err == nil {
		t.Error("expected error for wrong arg count")
	}

	wrongType := `enum E { Pair(i32, i32) }
		function main(): i32 {
			let e: E = Pair(1, "two");
			return 0;
		}`
	if err := checkSource(t, wrongType); err == nil {
		t.Error("expected error for wrong arg type")
	}
}

func TestMatchExhaustivenessChecked(t *testing.T) {
	src := `enum Light { Red, Green, Yellow }
		function main(): i32 {
			let l: Light = Green;
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

func TestMatchWildcardCoversMissingVariants(t *testing.T) {
	src := `enum Light { Red, Green, Yellow }
		function main(): i32 {
			let l: Light = Green;
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

func TestStringLiteralMatchNonExhaustiveRejected(t *testing.T) {
	src := `function main(): i32 {
			let s: string = "x";
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

func TestBoolLiteralMatchTrueFalseCloses(t *testing.T) {
	ok := map[string]string{
		"statement":  `function f(b: boolean): i32 { match (b) { true => { return 1; }, false => { return 2; } } }`,
		"expression": `function f(b: boolean): i32 { return match (b) { false => 2, true => 1 }; }`,
		"guarded arm before the pair": `function f(b: boolean, c: boolean): i32 {
  return match (b) { true when c => 3, true => 1, false => 2 };
}`,
	}
	for name, src := range ok {
		t.Run(name, func(t *testing.T) {
			if err := checkSource(t, src); err != nil {
				t.Errorf("true/false arms should close a boolean match: %v", err)
			}
		})
	}
	bad := map[string]string{
		"one literal": `function f(b: boolean): i32 { match (b) { true => { return 1; } } return 0; }`,
		"guarded literal": `function f(b: boolean, c: boolean): i32 {
  return match (b) { true => 1, false when c => 2 };
}`,
	}
	for name, src := range bad {
		t.Run(name, func(t *testing.T) {
			err := checkSource(t, src)
			if err == nil {
				t.Fatal("expected E030: the boolean match is missing an arm")
			}
			if !strings.Contains(err.Error(), "match on boolean is not exhaustive") {
				t.Errorf("want the boolean E030 text; got %v", err)
			}
		})
	}
}

func TestStringLiteralMatchTypeMismatchRejected(t *testing.T) {
	src := `function main(): i32 {
			let s: string = "x";
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

func TestMatchPayloadArityChecked(t *testing.T) {
	src := `enum E { A, B(i32, i32) }
		function main(): i32 {
			let e: E = A;
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

func TestGenericVariantInfersTypeArgs(t *testing.T) {
	good := `function main(): i32 {
			let o: Option[i32] = Some(42);
			return 0;
		}`
	if err := checkSource(t, good); err != nil {
		t.Errorf("good: %v", err)
	}
	bad := `function main(): i32 {
			let o: Option[string] = Some(42);
			return 0;
		}`
	if err := checkSource(t, bad); err == nil {
		t.Error("expected mismatch: Option[string] vs Option[i32]")
	}
}

func TestPayloadlessGenericVariantFlowsIntoContext(t *testing.T) {
	src := `function find(): Option[i32] { return None; }
		function main(): i32 {
			let o: Option[i32] = None;
			return 0;
		}`
	if err := checkSource(t, src); err != nil {
		t.Errorf("None should flow into Option[i32]: %v", err)
	}
}

func TestMatchSubstitutesTypeArgs(t *testing.T) {
	src := `function main(): i32 {
			let o: Option[i32] = Some(7);
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

func TestUsizePreludeHelpersRequireExplicitCastInUserCode(t *testing.T) {
	for _, src := range []string{
		`function f(a: string, b: string, n: i32): i32 {
    __memcpy(a as usize, b as usize, n);
    return 0;
}`,
		`function f(): i32 {
    let buf: usize = __alloc(16);
    return buf as i32;
}`,
	} {
		if err := checkSource(t, src); err != nil {
			t.Errorf("explicit-cast form should type-check: %q\ngot: %v", src, err)
		}
	}
	for _, src := range []string{
		`function f(a: string, b: string, n: i32): i32 {
    __memcpy(a, b, n);
    return 0;
}`,
		`function f(): i32 {
    let buf: i32 = __alloc(16);
    return buf;
}`,
		`function f(): i32 {
    let big: i64 = 5000000000i64;
    let p: usize = big;
    let small: i32 = p;
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
    let base: usize = 100;
    return base + idx * stride;
}`,
	} {
		if err := checkSource(t, src); err != nil {
			t.Errorf("%q: expected to type-check, got: %v", src, err)
		}
	}
}

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
			let p: Pair[i32] = Both(1, 2);
			return 0;
		}`
	if err := checkSource(t, src); err == nil {
		t.Error("expected arity error: Pair has 2 type params, 1 supplied")
	}
}

func TestUseInfersBindingTypeFromGenericCallee(t *testing.T) {
	src := `function each[T](items: T[], cb: (T) => i32): i32 {
    return cb(items[0]);
}
function main(): i32 {
    let nums: i32[] = [10, 20, 30];
    use n <- each(nums);
    return n + 1;
}`
	if err := checkSource(t, src); err != nil {
		t.Errorf("expected generic-callee inference to succeed, got: %v", err)
	}
}

func TestUseInfersFromEnumPayloadGeneric(t *testing.T) {
	src := `function try_opt[T](opt: Option[T], cb: (T) => i32): i32 {
    if let Some(v) = opt { return cb(v); }
    return 0;
}
function main(): i32 {
    let x: Option[i32] = Some(7);
    use n <- try_opt(x);
    return n + 1;
}`
	if err := checkSource(t, src); err != nil {
		t.Errorf("expected Option[T] inference to succeed, got: %v", err)
	}
}

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

func TestArrayMethodDispatchRegistersReceiverMethods(t *testing.T) {
	prog, err := parser.Parse(`function (xs: string[]) join2(sep: string): string { return ""; }
function (xs: T[]) sum2(): i32 { return 0; }
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
		{"Array.append", "__method_Array_push"},
		{"Array.join2", "__method_Array_join2"},
		{"Array.sum2", "__method_Array_sum2"},
	}
	for _, c := range cases {
		got, ok := info.Methods[c.key]
		if !ok {
			t.Errorf("Methods[%q] missing; receiver method did not register", c.key)
			continue
		}
		if got != c.mangled {
			t.Errorf("Methods[%q] = %q, want %q", c.key, got, c.mangled)
		}
	}
}

func TestArrayMethodNamePatternDoesNotDispatch(t *testing.T) {
	prog, err := parser.Parse(`function __method_Array_ghost(xs: i32[]): i32 { return 0; }
function main(): i32 { return 0; }`)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	info, err := Check(prog)
	if err != nil {
		t.Fatalf("check: %v", err)
	}
	if got, ok := info.Methods["Array.ghost"]; ok {
		t.Errorf("Methods[\"Array.ghost\"] = %q, want no entry", got)
	}
}

func TestInfoGenericsAggregatesFuncsAndStructs(t *testing.T) {
	src := `struct Box[T] { v: T }
struct Pair[A, B] { first: A, second: B }
function id[T](x: T): T { return x; }
function pick[A, B](a: A, b: B, take_first: boolean): A { return a; }
function main(): i32 {
	let b = Box { v: 7 };
	let p = Pair { first: 1, second: "hi" };
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
	if _, ok := info.Generics["main"]; ok {
		t.Error(`Generics["main"] should not exist (main has no type params)`)
	}
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

func TestQualifiedVariantReferences(t *testing.T) {
	good := []string{
		`enum Color { Red, Green, Blue }
		enum Status { Red, Yellow }
		function main(): i32 {
			let c: Color = Color.Red;
			let s: Status = Status.Red;
			return 0;
		}`,
		`enum A { Foo(i32), Bar }
		enum B { Foo(i32), Baz }
		function main(): i32 {
			let a: A = A.Foo(11);
			match (a) {
				A.Foo(x) => { return x; },
				A.Bar => { return 0; }
			}
			return 0;
		}`,
		`enum Shape { Circle(i32), Square(i32) }
		function main(): i32 {
			let s: Shape = Shape.Circle(7);
			match (s) {
				Circle(r) => { return r; },
				Square(side) => { return side * side; }
			}
			return 0;
		}`,
		`enum Light { Red, Green, Yellow }
		function main(): i32 {
			let l: Light = Red;
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
		{
			`enum A { Foo, Bar }
			enum B { Foo, Baz }
			function main(): i32 {
				let x: A = Foo;
				return 0;
			}`,
			"declared in multiple enums",
		},
		{
			`enum A { Foo(i32) }
			enum B { Foo(i32) }
			function main(): i32 {
				let x: A = Foo(7);
				return 0;
			}`,
			"declared in multiple enums",
		},
		{
			`enum A { Foo }
			enum B { Bar }
			function main(): i32 {
				let x: A = A.Bar;
				return 0;
			}`,
			"no variant",
		},
		{
			`enum A { Foo }
			enum B { Foo }
			function main(): i32 {
				let a: A = A.Foo;
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

func TestAmbiguousVariantHintIsSpellable(t *testing.T) {
	const libEnum = "pub enum Kind { Text, Number }\n"
	const otherEnum = "pub enum Kind { Text, Blah }\n"

	t.Run("prefers the reader's own module", func(t *testing.T) {
		err, _ := checkFiles(t, map[string]string{
			"main.fern": "import \"./lib\";\n" +
				"function main(): i32 { return lib.pick(); }\n",
			"lib.fern": "import \"./other\";\n" + libEnum + "pub function pick(): i32 {\n" +
				"    let k: Kind = Text;\n    return 0;\n}\n",
			"other.fern": otherEnum + "pub function n(): i32 { return 0; }\n",
		}, "main.fern")
		if err == nil {
			t.Fatal("expected E036 for the ambiguous bare variant")
		}
		msg := err.Error()
		if strings.Contains(msg, "__") {
			t.Errorf("diagnostic names a mangled enum:\n%s", msg)
		}
		if !strings.Contains(msg, "e.g. `Kind.Text`") {
			t.Errorf("hint should qualify with the referring module's own enum:\n%s", msg)
		}
		for _, want := range []string{"Kind", "other.Kind"} {
			if !strings.Contains(msg, want) {
				t.Errorf("candidate list missing %q:\n%s", want, msg)
			}
		}
	})

	t.Run("names imported enums as mod.Enum", func(t *testing.T) {
		err, _ := checkFiles(t, map[string]string{
			"main.fern": "import \"./lib\";\nimport \"./other\";\n" +
				"function main(): i32 {\n    let k: lib.Kind = Text;\n    return 0;\n}\n",
			"lib.fern":   libEnum,
			"other.fern": otherEnum,
		}, "main.fern")
		if err == nil {
			t.Fatal("expected E036 for the ambiguous bare variant")
		}
		msg := err.Error()
		if strings.Contains(msg, "__") {
			t.Errorf("diagnostic names a mangled enum:\n%s", msg)
		}
		for _, want := range []string{"lib.Kind", "other.Kind"} {
			if !strings.Contains(msg, want) {
				t.Errorf("candidate list missing %q:\n%s", want, msg)
			}
		}
	})
}

func TestVariantDiagnosticsNeverNameMangledEnums(t *testing.T) {
	const main = "import \"./lib\";\nfunction main(): i32 { return lib.go(); }\n"

	for _, tc := range []struct {
		name string
		lib  string
		want []string
	}{
		{
			name: "qualifier does not match scrutinee",
			lib: `pub enum Kind { Text, Number }
pub enum Other { Text, Blah }

pub function go(): i32 {
    let k: Kind = Kind.Text;
    match (k) {
        Other.Text => { return 1; },
        Kind.Number => { return 2; }
    }
    return 0;
}
`,
			want: []string{`qualifier "Other" does not match scrutinee enum Kind`},
		},
		{
			name: "no such variant on the qualified enum",
			lib: `pub enum Kind { Text, Number }

pub function go(): i32 {
    let k: Kind = Kind.Missing;
    return 0;
}
`,
			want: []string{`enum Kind has no variant "Missing"`},
		},
		{
			name: "qualified variant needs its payload",
			lib: `pub enum Kind { Text, Number(i32) }

pub function go(): i32 {
    let k: Kind = Kind.Number;
    return 0;
}
`,
			want: []string{"variant Kind.Number expects 1 payload argument(s)", "call it as Kind.Number(...)"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err, _ := checkFiles(t, map[string]string{
				"main.fern": main,
				"lib.fern":  tc.lib,
			}, "main.fern")
			if err == nil {
				t.Fatal("expected a diagnostic")
			}
			msg := err.Error()
			if strings.Contains(msg, "__") {
				t.Errorf("diagnostic names a mangled enum:\n%s", msg)
			}
			for _, want := range tc.want {
				if !strings.Contains(msg, want) {
					t.Errorf("diagnostic missing %q:\n%s", want, msg)
				}
			}
		})
	}
}

func TestCastAsTypeAscription(t *testing.T) {
	good := []string{
		`function main(): i32 {
			let x: Option[i32] = None as Option[i32];
			return 0;
		}`,
		`function main(): i32 {
			let r: Result[i32, string] = Ok(1) as Result[i32, string];
			return 0;
		}`,
		`function main(): i32 {
			let a: i32[] = [] as i32[];
			return a.len() as i32;
		}`,
		`function main(): i32 {
			let x: Option[i32] = Some(1) as Option[i32];
			return 0;
		}`,
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
		{
			`function main(): i32 {
				let x: Option[i32] = "hi" as Option[i32];
				return 0;
			}`,
			"cannot cast",
		},
		{
			`function main(): i32 {
				let n: i32 = true as i32;
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

const hostStubDecls = `struct platform__Host { reactor: i32 }
function platform__host(): platform__Host { return platform__Host { reactor: 0 }; }
`

const serveStubDecls = hostStubDecls + `struct serve__Config { backlog: i32 }
function serve__config(): serve__Config { return serve__Config { backlog: 128 }; }
function serve____init_platform(): platform__Host { return platform__host(); }
function serve__supervise(port: i32, opts: serve__Config, handler: (HttpRequest, platform__Host) => HttpResponse): i32 { return 0; }
function serve__supervise_shutdown(port: i32, opts: serve__Config, handler: (HttpRequest, platform__Host) => HttpResponse, shutdown: (string) => void): i32 { return 0; }
function serve__supervise_with[S](port: i32, opts: serve__Config, init: S, handler: (S, HttpRequest, platform__Host) => (S, HttpResponse)): i32 { return 0; }
function serve__supervise_with_shutdown[S](port: i32, opts: serve__Config, init: S, handler: (S, HttpRequest, platform__Host) => (S, HttpResponse), shutdown: (string, S) => void): i32 { return 0; }
function serve__run(port: i32, opts: serve__Config, handler: (HttpRequest, platform__Host) => HttpResponse): i32 { return 0; }
function serve__run_shutdown(port: i32, opts: serve__Config, handler: (HttpRequest, platform__Host) => HttpResponse, shutdown: (string) => void): i32 { return 0; }
function serve__run_with[S](port: i32, opts: serve__Config, init: S, handler: (S, HttpRequest, platform__Host) => (S, HttpResponse)): i32 { return 0; }
function serve__run_with_shutdown[S](port: i32, opts: serve__Config, init: S, handler: (S, HttpRequest, platform__Host) => (S, HttpResponse), shutdown: (string, S) => void): i32 { return 0; }
function serve____port_from_env(name: string, def: i32): i32 { return def; }
`

func TestSynthesisedHandleMainFollowsTargetProcesses(t *testing.T) {
	const response = `HttpResponse { status: 200, body: BodyText("ok"), headers: HeaderMap { names: [], values: [] }, trailers: HeaderMap { names: [], values: [] }, background: None }`
	cases := []struct{ name, src, target, entry string }{
		{"stateless on wasi", serveStubDecls + `function handle(req: HttpRequest, plat: platform__Host): HttpResponse { return ` + response + `; }`, "wasm32-wasi", "serve__run"},
		{"stateful hook on wasi", serveStubDecls + `function init(): i32 { return 7; }
function handle(n: i32, req: HttpRequest, plat: platform__Host): (i32, HttpResponse) { return (n, ` + response + `); }
function shutdown(reason: string, n: i32): void { print(reason); }`, "wasm32-wasi", "serve__run_with_shutdown"},
		{"stateful on linux", serveStubDecls + `function init(): i32 { return 7; }
function handle(n: i32, req: HttpRequest, plat: platform__Host): (i32, HttpResponse) { return (n, ` + response + `); }`, "x86-64-linux", "serve__supervise_with"},
		{"stateless hook on darwin", serveStubDecls + `function handle(req: HttpRequest, plat: platform__Host): HttpResponse { return ` + response + `; }
function shutdown(reason: string): void { print(reason); }`, "arm64-darwin", "serve__supervise_shutdown"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			prog, err := parser.Parse(tc.src)
			if err != nil {
				t.Fatalf("parse: %v", err)
			}
			if _, err := CheckTarget(prog, tc.target); err != nil {
				t.Fatalf("check: %v", err)
			}
			main := findDecl(prog, "main")
			if main == nil || !main.IsSynthesisedHandlerMain {
				t.Fatal("no synthesised main")
			}
			call := main.Body.Stmts[len(main.Body.Stmts)-1].(*ast.Return).Value.(*ast.Call)
			if id, ok := call.Callee.(*ast.Ident); !ok || id.Name != tc.entry {
				t.Fatalf("synth main for %s calls %v, want %s", tc.target, call.Callee, tc.entry)
			}
		})
	}
}

func TestConfigGetFollowsTheTargetsEnvironment(t *testing.T) {
	const src = `function main(): i32 { match (config_get("GREETING")) { Some(v) => { return 0; }, None => { return 1; } } }`
	for _, tc := range []struct{ target, callee string }{
		{"", "env"},
		{"x86-64-linux", "env"},
		{"wasm32-wasi", "env"},
		{"wasm32-wasi-http", "config_get"},
	} {
		t.Run(tc.target, func(t *testing.T) {
			prog, err := parser.Parse(src)
			if err != nil {
				t.Fatalf("parse: %v", err)
			}
			info, err := CheckTarget(prog, tc.target)
			if err != nil {
				t.Fatalf("check: %v", err)
			}
			if info.Target != tc.target {
				t.Fatalf("info.Target = %q, want %q", info.Target, tc.target)
			}
			var got string
			ast.WalkProgram(prog, func(n ast.Node) bool {
				if call, ok := n.(*ast.Call); ok {
					if id, ok := call.Callee.(*ast.Ident); ok && (id.Name == "env" || id.Name == "config_get") {
						got = id.Name
					}
				}
				return true
			})
			if got != tc.callee {
				t.Fatalf("config_get on %q became %q, want %q", tc.target, got, tc.callee)
			}
		})
	}
}

func TestSynthesisedHandleMainRunsInitFirst(t *testing.T) {
	prog, err := parser.Parse(serveStubDecls + `function init(): void { print("starting"); }
function handle(req: HttpRequest, plat: platform__Host): HttpResponse {
    return HttpResponse { status: 200, body: BodyText("ok"), headers: HeaderMap { names: [], values: [] }, trailers: HeaderMap { names: [], values: [] }, background: None };
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
		t.Fatalf("synth main has %d stmts, want 2 (init + the serve return)", len(main.Body.Stmts))
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

func TestSynthesisedHandleMainNoInitElidesPrepend(t *testing.T) {
	prog, err := parser.Parse(serveStubDecls + `function handle(req: HttpRequest, plat: platform__Host): HttpResponse {
    return HttpResponse { status: 200, body: BodyText("ok"), headers: HeaderMap { names: [], values: [] }, trailers: HeaderMap { names: [], values: [] }, background: None };
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
		t.Errorf("synth main has %d stmts, want 1 (just the serve return; no init prepended)", len(main.Body.Stmts))
	}
}

func TestSynthesisedHandleMainThreadsInitState(t *testing.T) {
	prog, err := parser.Parse(serveStubDecls + `function init(): i32 { return 7; }
function handle(hits: i32, req: HttpRequest, plat: platform__Host): (i32, HttpResponse) {
    return (hits + 1, HttpResponse { status: 200, body: BodyText("ok"), headers: HeaderMap { names: [], values: [] }, trailers: HeaderMap { names: [], values: [] }, background: None });
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
		t.Fatalf("synth main has %d stmts, want 1 (the serve return; init is an argument, not a statement)",
			len(main.Body.Stmts))
	}
	ret, ok := main.Body.Stmts[0].(*ast.Return)
	if !ok {
		t.Fatalf("stmt = %T, want *ast.Return", main.Body.Stmts[0])
	}
	call, ok := ret.Value.(*ast.Call)
	if !ok {
		t.Fatalf("returned expression = %T, want *ast.Call", ret.Value)
	}
	id, ok := call.Callee.(*ast.Ident)
	if !ok || id.Name != "serve__supervise_with" {
		t.Fatalf("synth main calls %v, want serve__supervise_with", call.Callee)
	}
	if len(call.Args) != 4 {
		t.Fatalf("serve__supervise_with call has %d args, want 4 (port, config(), init(), handle)", len(call.Args))
	}
	initArg, ok := call.Args[2].(*ast.Call)
	if !ok {
		t.Fatalf("third argument = %T, want the *ast.Call to init", call.Args[2])
	}
	if id, ok := initArg.Callee.(*ast.Ident); !ok || id.Name != "init" {
		t.Errorf("third argument calls %v, want init", initArg.Callee)
	}
}

func TestSynthesisedHandleMainTakesInitOptions(t *testing.T) {
	const response = `HttpResponse { status: 200, body: BodyText("ok"), headers: HeaderMap { names: [], values: [] }, trailers: HeaderMap { names: [], values: [] }, background: None }`
	t.Run("options beside the state", func(t *testing.T) {
		prog, err := parser.Parse(serveStubDecls + `function init(plat: platform__Host): (serve__Config, i32) { return (serve__config(), 7); }
function handle(hits: i32, req: HttpRequest, plat: platform__Host): (i32, HttpResponse) { return (hits + 1, ` + response + `); }
function shutdown(reason: string, hits: i32): void { print(reason); }`)
		if err != nil {
			t.Fatalf("parse: %v", err)
		}
		if _, err := Check(prog); err != nil {
			t.Fatalf("check: %v", err)
		}
		main := findDecl(prog, "main")
		if main == nil || !main.IsSynthesisedHandlerMain || len(main.Body.Stmts) != 2 {
			t.Fatalf("want a synthesised main of 2 statements (the destructure, the return), got %v", main)
		}
		pair, ok := main.Body.Stmts[0].(*ast.Destructure)
		if !ok || len(pair.Names) != 2 || pair.Names[0] != "__fern_opts" || pair.Names[1] != "__fern_state" {
			t.Fatalf("first statement = %#v, want the (__fern_opts, __fern_state) destructure", main.Body.Stmts[0])
		}
		initCall, ok := pair.Init.(*ast.Call)
		if !ok || len(initCall.Args) != 1 {
			t.Fatalf("the destructure's value = %#v, want init(__init_platform())", pair.Init)
		}
		if plat, ok := initCall.Args[0].(*ast.Call); !ok {
			t.Fatalf("init's argument = %T, want the __init_platform() call", initCall.Args[0])
		} else if id, ok := plat.Callee.(*ast.Ident); !ok || id.Name != "serve____init_platform" {
			t.Fatalf("init's argument calls %v, want serve____init_platform", plat.Callee)
		}
		call := main.Body.Stmts[1].(*ast.Return).Value.(*ast.Call)
		if id, ok := call.Callee.(*ast.Ident); !ok || id.Name != "serve__supervise_with_shutdown" {
			t.Fatalf("synth main calls %v, want serve__supervise_with_shutdown", call.Callee)
		}
		if opts, ok := call.Args[1].(*ast.Ident); !ok || opts.Name != "__fern_opts" {
			t.Fatalf("the options argument is %#v, want __fern_opts", call.Args[1])
		}
		if state, ok := call.Args[2].(*ast.Ident); !ok || state.Name != "__fern_state" {
			t.Fatalf("the state argument is %#v, want __fern_state", call.Args[2])
		}
	})
	t.Run("options alone", func(t *testing.T) {
		prog, err := parser.Parse(serveStubDecls + `function init(): serve__Config { return serve__Config { backlog: 4 }; }
function handle(req: HttpRequest, plat: platform__Host): HttpResponse { return ` + response + `; }`)
		if err != nil {
			t.Fatalf("parse: %v", err)
		}
		if _, err := Check(prog); err != nil {
			t.Fatalf("check: %v", err)
		}
		main := findDecl(prog, "main")
		if main == nil || len(main.Body.Stmts) != 1 {
			t.Fatalf("want a synthesised main of 1 statement, got %v", main)
		}
		call := main.Body.Stmts[0].(*ast.Return).Value.(*ast.Call)
		if id, ok := call.Callee.(*ast.Ident); !ok || id.Name != "serve__supervise" {
			t.Fatalf("synth main calls %v, want serve__supervise", call.Callee)
		}
		if opts, ok := call.Args[1].(*ast.Call); !ok {
			t.Fatalf("the options argument is %T, want the init() call", call.Args[1])
		} else if id, ok := opts.Callee.(*ast.Ident); !ok || id.Name != "init" || len(opts.Args) != 0 {
			t.Fatalf("the options argument calls %v with %d args, want init()", opts.Callee, len(opts.Args))
		}
	})
	t.Run("the config under an alias", func(t *testing.T) {
		prog, _, err := modload.LoadSource(`import "std/http";
import "std/platform";
import "std/serve" as web;
function init(plat: platform.Platform): (web.Config, i32) { return (web.Config { ...web.config(), workers: 1 }, 7); }
function handle(hits: i32, req: HttpRequest, plat: platform.Platform): (i32, HttpResponse) { return (hits + 1, http.ok("ok")); }`)
		if err != nil {
			t.Fatalf("load: %v", err)
		}
		if err := constfold.Fold(prog, nil); err != nil {
			t.Fatalf("constfold: %v", err)
		}
		if _, err := Check(prog); err != nil {
			t.Fatalf("check: %v", err)
		}
		main := findDecl(prog, "main")
		if main == nil || len(main.Body.Stmts) != 2 {
			t.Fatalf("want a synthesised main of 2 statements (the destructure, the return), got %v", main)
		}
		if _, ok := main.Body.Stmts[0].(*ast.Destructure); !ok {
			t.Fatalf("first statement = %T, want the (config, state) destructure", main.Body.Stmts[0])
		}
	})
	t.Run("a Config of the program's own", func(t *testing.T) {
		prog, err := parser.Parse(serveStubDecls + `struct Config { n: i32 }
function init(): Config { return Config { n: 1 }; }
function handle(c: Config, req: HttpRequest, plat: platform__Host): (Config, HttpResponse) { return (c, ` + response + `); }`)
		if err != nil {
			t.Fatalf("parse: %v", err)
		}
		if _, err := Check(prog); err != nil {
			t.Fatalf("check: %v", err)
		}
		main := findDecl(prog, "main")
		call := main.Body.Stmts[len(main.Body.Stmts)-1].(*ast.Return).Value.(*ast.Call)
		if opts, ok := call.Args[1].(*ast.Call); !ok || opts.Callee.(*ast.Ident).Name != "serve__config" {
			t.Fatalf("the config argument is %#v, want the serve__config() defaults", call.Args[1])
		}
	})
	for name, src := range map[string]string{
		"a parameter that is not the platform": `function init(n: i32): i32 { return n; }`,
		"two parameters":                       `function init(plat: platform__Host, n: i32): i32 { return n; }`,
	} {
		t.Run(name, func(t *testing.T) {
			prog, err := parser.Parse(serveStubDecls + src + `
function handle(hits: i32, req: HttpRequest, plat: platform__Host): (i32, HttpResponse) { return (hits + 1, ` + response + `); }`)
			if err != nil {
				t.Fatalf("parse: %v", err)
			}
			_, err = Check(prog)
			if err == nil || !hasCode(err, "E075") || !strings.Contains(err.Error(), "it takes none, or the platform alone") {
				t.Fatalf("want E075 on init's parameters, got %v", err)
			}
		})
	}
}

func TestSynthesisedHandleMainWiresShutdown(t *testing.T) {
	const decls = serveStubDecls
	const response = `HttpResponse { status: 200, body: BodyText("ok"), headers: HeaderMap { names: [], values: [] }, trailers: HeaderMap { names: [], values: [] }, background: None }`
	cases := []struct{ name, src, entry, message string }{
		{"stateless", decls + `function handle(req: HttpRequest, plat: platform__Host): HttpResponse { return ` + response + `; }
function shutdown(reason: string): void { print(reason); }`, "serve__supervise_shutdown", ""},
		{"stateful", decls + `function init(): i32 { return 7; }
function handle(n: i32, req: HttpRequest, plat: platform__Host): (i32, HttpResponse) { return (n, ` + response + `); }
function shutdown(reason: string, n: i32): void { print(reason); }`, "serve__supervise_with_shutdown", ""},
		{"state hook without init", decls + `function handle(req: HttpRequest, plat: platform__Host): HttpResponse { return ` + response + `; }
function shutdown(reason: string, n: i32): void { print(reason); }`, "", "`shutdown` takes a state parameter, but no `init` produces the state to hand it"},
		{"stateless hook drops the state", decls + `function init(): i32 { return 7; }
function handle(n: i32, req: HttpRequest, plat: platform__Host): (i32, HttpResponse) { return (n, ` + response + `); }
function shutdown(reason: string): void { print(reason); }`, "", "`shutdown` takes no state parameter, but the handler threads a state it would drop"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			prog, err := parser.Parse(tc.src)
			if err != nil {
				t.Fatalf("parse: %v", err)
			}
			_, err = Check(prog)
			if tc.message != "" {
				if err == nil || !strings.Contains(err.Error(), tc.message) {
					t.Fatalf("want E075 %q, got %v", tc.message, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("check: %v", err)
			}
			main := findDecl(prog, "main")
			if main == nil || !main.IsSynthesisedHandlerMain {
				t.Fatal("no synthesised main")
			}
			ret := main.Body.Stmts[len(main.Body.Stmts)-1].(*ast.Return)
			call := ret.Value.(*ast.Call)
			if id, ok := call.Callee.(*ast.Ident); !ok || id.Name != tc.entry {
				t.Fatalf("synth main calls %v, want %s", call.Callee, tc.entry)
			}
			if last, ok := call.Args[len(call.Args)-1].(*ast.Ident); !ok || last.Name != "shutdown" {
				t.Fatalf("the last argument is %v, want the shutdown hook", call.Args[len(call.Args)-1])
			}
			if opts, ok := call.Args[1].(*ast.Call); !ok {
				t.Fatalf("the second argument is %T, want the config() call", call.Args[1])
			} else if id, ok := opts.Callee.(*ast.Ident); !ok || id.Name != "serve__config" {
				t.Fatalf("the second argument calls %v, want serve__config", opts.Callee)
			}
		})
	}
}

func TestResultHandlerIsAdapted(t *testing.T) {
	const decls = serveStubDecls + `function respond(r: Result[HttpResponse, string]): HttpResponse { return HttpResponse { status: 500, body: BodyText(""), headers: HeaderMap { names: [], values: [] }, trailers: HeaderMap { names: [], values: [] }, background: None }; }
function respond_with(pair: (i32, Result[HttpResponse, string])): (i32, HttpResponse) { return (pair.0, respond(pair.1)); }
`
	const dynDecls = serveStubDecls + `struct Oops { why: string }
impl err.Error for Oops { function message(self: Self): string { return self.why; } }
function respond_error(r: Result[HttpResponse, dyn err.Error], plat: platform__Host): HttpResponse { return HttpResponse { status: 500, body: BodyText(""), headers: HeaderMap { names: [], values: [] }, trailers: HeaderMap { names: [], values: [] }, background: None }; }
function respond_error_with(pair: (i32, Result[HttpResponse, dyn err.Error]), plat: platform__Host): (i32, HttpResponse) { return (pair.0, respond_error(pair.1, plat)); }
`
	const ownErrorDecls = serveStubDecls + `trait Error { function message(self: Self): string; }
struct Oops { why: string }
impl Error for Oops { function message(self: Self): string { return self.why; } }
function respond(r: Result[HttpResponse, dyn Error]): HttpResponse { return HttpResponse { status: 500, body: BodyText(""), headers: HeaderMap { names: [], values: [] }, trailers: HeaderMap { names: [], values: [] }, background: None }; }
function respond_error(r: Result[HttpResponse, dyn Error], plat: platform__Host): HttpResponse { return HttpResponse { status: 418, body: BodyText(""), headers: HeaderMap { names: [], values: [] }, trailers: HeaderMap { names: [], values: [] }, background: None }; }
`
	cases := []struct {
		name, src, adapter string
		params, args       int
		load               bool
	}{
		{"stateless", decls + `function handle(req: HttpRequest, plat: platform__Host): Result[HttpResponse, string] { return Err("no"); }`, "respond", 2, 1, false},
		{"stateful", decls + `function init(): i32 { return 0; }
function handle(n: i32, req: HttpRequest, plat: platform__Host): (i32, Result[HttpResponse, string]) { return (n + 1, Err("no")); }`, "respond_with", 3, 1, false},
		{"dyn-error", `import "std/error" as err;
` + dynDecls + `function handle(req: HttpRequest, plat: platform__Host): Result[HttpResponse, dyn err.Error] { return Err(Oops { why: "no" }); }`, "respond_error", 2, 2, true},
		{"dyn-error-stateful", `import "std/error" as err;
` + dynDecls + `function init(): i32 { return 0; }
function handle(n: i32, req: HttpRequest, plat: platform__Host): (i32, Result[HttpResponse, dyn err.Error]) { return (n + 1, Err(Oops { why: "no" })); }`, "respond_error_with", 3, 2, true},
		{"a program's own Error trait is not std/error's", ownErrorDecls + `function handle(req: HttpRequest, plat: platform__Host): Result[HttpResponse, dyn Error] { return Err(Oops { why: "no" }); }`, "respond", 2, 1, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var prog *ast.Program
			var err error
			if tc.load {
				prog, _, err = modload.LoadSource(tc.src)
			} else {
				prog, err = parser.Parse(tc.src)
			}
			if err != nil {
				t.Fatalf("load: %v", err)
			}
			if _, err := Check(prog); err != nil {
				t.Fatalf("check: %v", err)
			}
			if findDecl(prog, resultHandlerName) == nil {
				t.Fatalf("the Result-shaped handler was not renamed to %s", resultHandlerName)
			}
			handle := findDecl(prog, "handle")
			if handle == nil || len(handle.Params) != tc.params {
				t.Fatalf("no synthesised handle with %d params", tc.params)
			}
			ret, ok := handle.Body.Stmts[0].(*ast.Return)
			if !ok {
				t.Fatalf("handle's statement = %T, want *ast.Return", handle.Body.Stmts[0])
			}
			call, ok := ret.Value.(*ast.Call)
			if !ok {
				t.Fatalf("handle returns %T, want *ast.Call", ret.Value)
			}
			if id, ok := call.Callee.(*ast.Ident); !ok || id.Name != tc.adapter {
				t.Fatalf("handle calls %v, want %s", call.Callee, tc.adapter)
			}
			if len(call.Args) != tc.args {
				t.Fatalf("%s is handed %d args, want %d", tc.adapter, len(call.Args), tc.args)
			}
			inner, ok := call.Args[0].(*ast.Call)
			if !ok {
				t.Fatalf("%s's argument = %T, want the call to the renamed handler", tc.adapter, call.Args[0])
			}
			if id, ok := inner.Callee.(*ast.Ident); !ok || id.Name != resultHandlerName || len(inner.Args) != tc.params {
				t.Fatalf("%s is handed %v with %d args, want %s with %d", tc.adapter, inner.Callee, len(inner.Args), resultHandlerName, tc.params)
			}
			if findDecl(prog, "main") == nil {
				t.Fatal("no main was synthesised over the adapted handle")
			}
		})
	}
}

func TestHandlerInitStateMismatchIsRejected(t *testing.T) {
	const decls = serveStubDecls
	cases := map[string]string{
		"handler takes state nobody produces": decls + `function handle(hits: i32, req: HttpRequest, plat: platform__Host): (i32, HttpResponse) {
    return (hits + 1, HttpResponse { status: 200, body: BodyText("ok"), headers: HeaderMap { names: [], values: [] }, trailers: HeaderMap { names: [], values: [] }, background: None });
}`,
		"void init produces no state": decls + `function init(): void { print("starting"); }
function handle(hits: i32, req: HttpRequest, plat: platform__Host): (i32, HttpResponse) {
    return (hits + 1, HttpResponse { status: 200, body: BodyText("ok"), headers: HeaderMap { names: [], values: [] }, trailers: HeaderMap { names: [], values: [] }, background: None });
}`,
		"init produces state nobody takes": decls + `function init(): i32 { return 7; }
function handle(req: HttpRequest, plat: platform__Host): HttpResponse {
    return HttpResponse { status: 200, body: BodyText("ok"), headers: HeaderMap { names: [], values: [] }, trailers: HeaderMap { names: [], values: [] }, background: None };
}`,
	}
	for name, src := range cases {
		t.Run(name, func(t *testing.T) {
			err := checkSource(t, src)
			if err == nil {
				t.Fatal("want E075, got no error")
			}
			if !hasCode(err, "E075") {
				t.Errorf("want E075, got: %v", err)
			}
		})
	}
}

func TestHandlerInitStateMismatchAllowedUnderUserMain(t *testing.T) {
	err := checkSource(t, hostStubDecls+`function init(): i32 { return 7; }
function handle(req: HttpRequest, plat: platform__Host): HttpResponse {
    return HttpResponse { status: 200, body: BodyText("ok"), headers: HeaderMap { names: [], values: [] }, trailers: HeaderMap { names: [], values: [] }, background: None };
}
function main(): i32 { return init(); }`)
	if err != nil {
		t.Fatalf("a user-written main should be left alone, got: %v", err)
	}
}

func TestWasiHandleIsSynthesisedForHandlers(t *testing.T) {
	const decls = serveStubDecls
	const handler = `function handle(req: HttpRequest, plat: platform__Host): HttpResponse {
    return HttpResponse { status: 200, body: BodyText("ok"), headers: HeaderMap { names: [], values: [] }, trailers: HeaderMap { names: [], values: [] }, background: None };
}`
	cases := map[string]struct {
		src  string
		want bool
	}{
		"handler with synthesised main": {decls + handler, true},
		"handler with its own main":     {decls + handler + "\nfunction main(): i32 { return 0; }", true},
		"no handler at all":             {decls + "function main(): i32 { return 0; }", false},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			prog, err := parser.Parse(tc.src)
			if err != nil {
				t.Fatalf("parse: %v", err)
			}
			if _, err := Check(prog); err != nil {
				t.Fatalf("check: %v", err)
			}
			entry := findDecl(prog, WasiHandleName)
			if tc.want && entry == nil {
				t.Fatalf("no %s synthesised; got %v", WasiHandleName, funcNamesOf(prog))
			}
			if !tc.want && entry != nil {
				t.Fatalf("%s synthesised for a program with no handler", WasiHandleName)
			}
			if entry == nil {
				return
			}
			if st, ok := entry.ReturnType.(ast.StructType); !ok || st.Name != "HttpResponse" {
				t.Errorf("entry returns %v, want HttpResponse", entry.ReturnType)
			}
			if len(entry.Params) != 1 {
				t.Errorf("entry takes %d params, want the request alone", len(entry.Params))
			}
		})
	}
}

func TestWasiHandleNotSynthesisedOverUserDefinition(t *testing.T) {
	prog, err := parser.Parse(serveStubDecls + `function __fern_wasi_handle(req: HttpRequest): HttpResponse { return handle(req, platform__Host { reactor: 7 }); }
function handle(req: HttpRequest, plat: platform__Host): HttpResponse {
    return HttpResponse { status: 200, body: BodyText("ok"), headers: HeaderMap { names: [], values: [] }, trailers: HeaderMap { names: [], values: [] }, background: None };
}`)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if _, err := Check(prog); err != nil {
		t.Fatalf("check: %v", err)
	}
	n := 0
	for _, fn := range prog.Funcs {
		if fn.Name == WasiHandleName {
			n++
		}
	}
	if n != 1 {
		t.Errorf("%d %s declarations, want 1 (the user's)", n, WasiHandleName)
	}
}

func funcNamesOf(prog *ast.Program) []string {
	out := make([]string, 0, len(prog.Funcs))
	for _, fn := range prog.Funcs {
		out = append(out, fn.Name)
	}
	return out
}

func TestUserDefinedMainSkipsSynthEvenWithInit(t *testing.T) {
	prog, err := parser.Parse(hostStubDecls + `function init(): void { print("starting"); }
function handle(req: HttpRequest, plat: platform__Host): HttpResponse {
    return HttpResponse { status: 200, body: BodyText("ok"), headers: HeaderMap { names: [], values: [] }, trailers: HeaderMap { names: [], values: [] }, background: None };
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

func TestTupleReturnWidensBareEnumElement(t *testing.T) {
	src := `function read(pos: i32, n: i32): (Option[i32], i32) {
    if (pos >= n) { return (None, pos); }
    return (Some(pos), pos + 1);
}
function main(): i32 {
    let (b, p) = read(0, 1);
    match (b) { Some(v) => { return v; }, None => { return -1; } }
}`
	if err := checkSource(t, src); err != nil {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestTupleReturnStillRejectsElementMismatch(t *testing.T) {
	src := `function f(): (i32, string) {
    return (1, 2);
}`
	if err := checkSource(t, src); err == nil {
		t.Errorf("expected a type error for (i32, i32) returned as (i32, string)")
	}
}

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
    let (v, c2) = mk(0, Cur { pos: 0 });
    match (v) { A(a) => { return a.x; }, B(b) => { return b.y + c2.pos; } }
}`
	if err := checkSource(t, src); err != nil {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestObjectSafetyReachesNestedVarDecls(t *testing.T) {
	const trait = "trait Eq { function eq(self: Self, other: Self): boolean; }\n"
	for name, src := range map[string]string{
		"else if branch": `function pick(n: i32): i32 {
    if (n == 0) {
        return 7;
    } else if (n == 1) {
        let b: dyn Eq[] = [];
        return b.len();
    }
    return 0;
}
function main(): i32 { return pick(1); }`,
		"lambda body": `function pick(n: i32): i32 {
    let f = (k: i32): i32 => {
        let b: dyn Eq[] = [];
        return b.len() + k;
    };
    return f(n);
}
function main(): i32 { return pick(1); }`,
	} {
		t.Run(name, func(t *testing.T) {
			err := checkSource(t, trait+src)
			if err == nil {
				t.Fatalf("want E021, got none")
			}
			if code := firstErrCode(err); code != "E021" {
				t.Errorf("code = %q, want E021: %v", code, err)
			}
			if !strings.Contains(err.Error(), "not object-safe") {
				t.Errorf("want the object-safety diagnostic, got: %v", err)
			}
		})
	}
}

func TestStructFieldSubPatternChecks(t *testing.T) {
	const decls = "enum E { A(i32), B }\nstruct P { x: i32, y: i32 }\nstruct W { e: E, n: i32 }\n"
	bad := map[string]struct{ src, code string }{
		"refutable field needs a fallback": {`function f(p: P): i32 {
  match (p) { P { x: 0, y } => { return y; } }
}`, "E030"},
		"variant field needs a fallback": {`function f(w: W): i32 {
  match (w) { W { e: A(v), n } => { return v + n; } }
}`, "E030"},
		"field literal type mismatch": {`function f(p: P): i32 {
  match (p) { P { x: "s", y } => { return y; }, _ => { return 0; } }
}`, "E035"},
	}
	for name, c := range bad {
		t.Run(name, func(t *testing.T) {
			err := checkSource(t, decls+c.src)
			if err == nil {
				t.Fatalf("want %s, got none", c.code)
			}
			if code := firstErrCode(err); code != c.code {
				t.Errorf("code = %q, want %s: %v", code, c.code, err)
			}
		})
	}

	ok := map[string]string{
		"literal field with an irrefutable sibling": `function f(p: P): i32 {
  match (p) { P { x: 0, y } => { return y; }, P { x, y } => { return x + y; } }
}`,
		"variant field covering every variant plus a fallback": `function f(w: W): i32 {
  match (w) { W { e: A(v), n } => { return v + n; }, W { e: B(), n } => { return n; } }
}`,
		"sub-pattern binds in the arm scope": `function f(w: W): i32 {
  match (w) { W { e: A(v), n } => { return v + n; }, W { e, n } => { return n; } }
}`,
		"expression form": `function f(p: P): i32 {
  return match (p) { P { x: 0, y } => y, P { x, y } => x + y };
}`,
	}
	for name, body := range ok {
		t.Run(name, func(t *testing.T) {
			if err := checkSource(t, decls+body); err != nil {
				t.Errorf("want accepted, got: %v", err)
			}
		})
	}
}

func TestStructFieldSubPatternAcceptsWildcardFallback(t *testing.T) {
	const decls = "enum E { A(i32), B }\nstruct W { e: E, n: i32 }\nstruct P { x: i32, y: i32 }\n"
	for name, body := range map[string]string{
		"statement form": `function f(w: W): i32 {
  match (w) { W { e: A(v), n } => { return v + n; }, _ => { return 0; } }
}`,
		"expression form": `function f(p: P): i32 {
  return match (p) { P { x: 0, y } => y, _ => 0 };
}`,
	} {
		t.Run(name, func(t *testing.T) {
			if err := checkSource(t, decls+body); err != nil {
				t.Errorf("the `_` fallback should be accepted, got: %v", err)
			}
		})
	}

	err := checkSource(t, decls+`function f(p: P): i32 {
  match (p) { P { x, y } => { return x + y; }, _ => { return 0; } }
}`)
	if err == nil {
		t.Fatal("want E026 for a `_` after a plain irrefutable struct arm, got none")
	}
	if code := firstErrCode(err); code != "E026" {
		t.Errorf("code = %q, want E026: %v", code, err)
	}
}

func TestMergedArmGuardSeesSiblingSlots(t *testing.T) {
	const decls = "enum Res { Ok2(i32), Err2(i32) }\nenum W { V(Res, i32), None2 }\nstruct P { x: i32, y: i32 }\n"
	for name, body := range map[string]string{
		"payload slot, statement form": `function f(w: W): i32 {
  match (w) {
    V(Ok2(n), m) when n > m => { return n; },
    V(x, m) => { return m; },
    None2 => { return 0; },
  }
}`,
		"payload slot, expression form": `function f(w: W): i32 {
  return match (w) { V(Ok2(n), m) when n > m => n, V(x, m) => m, None2 => 0 };
}`,
		"struct field": `function f(w: W): i32 {
  match (w) { V(Ok2(n), m) when m > 0 => { return n; }, _ => { return 0; } }
}`,
	} {
		t.Run(name, func(t *testing.T) {
			if err := checkSource(t, decls+body); err != nil {
				t.Errorf("guard should see the sibling slot, got: %v", err)
			}
		})
	}

	if err := checkSource(t, decls+`function f(w: W): i32 {
  match (w) {
    V(Ok2(n), m) when ((m: i32): boolean => m > 100)(n) => { return n; },
    V(x, m) => { return m; },
    None2 => { return 0; },
  }
}`); err != nil {
		t.Errorf("a lambda param should still shadow the slot, got: %v", err)
	}
}

func TestTraitConformanceAccepted(t *testing.T) {
	src := `trait Display { function to_string(self: Self): string; }
struct Point { x: i32, y: i32 }
impl Display for Point {
    function to_string(self: Self): string { return "p"; }
}
function main(): i32 { let p: Point = Point { x: 1, y: 2 }; let s: string = p.to_string(); return 0; }`
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
    let d: Dog = Dog { age: 1 };
    let c: Cat = Cat { age: 2 };
    let a: string = d.greeting();
    let b: string = c.greeting();
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
	if info.Methods["Dog.greeting"] == "" {
		t.Errorf("inherited default `greeting` should be registered for Dog, got %+v", info.Methods["Dog.greeting"])
	}
	if info.Methods["Cat.greeting"] == "" {
		t.Errorf("overriding `greeting` should be registered for Cat")
	}
}

func TestTraitDefaultMethodViaBound(t *testing.T) {
	src := `trait Greet {
    function name(self: Self): string;
    function greeting(self: Self): string { return "hi " + self.name(); }
}
struct Dog { age: i32 }
impl Greet for Dog { function name(self: Self): string { return "rex"; } }
function announce[T: Greet](x: T): string { return x.greeting(); }
function main(): i32 { let d: Dog = Dog { age: 1 }; let s: string = announce(d); return 0; }`
	if err := checkSource(t, src); err != nil {
		t.Errorf("default method via bound should typecheck: %v", err)
	}
}

func TestGenericTraitBound(t *testing.T) {
	const hdr = `trait From[T] { function from(v: T): Self; }
struct Celsius { deg: i32 }
impl From[i32] for Celsius { function from(v: i32): Self { return Celsius { deg: v }; } }
`
	if err := checkSource(t, hdr+`function describe[T: From[i32]](proto: T, v: i32): T { return T.from(v); }
function main(): i32 {
  let z: Celsius = Celsius { deg: 0 };
  return describe(z, 20).deg;
}`); err != nil {
		t.Errorf("bounded generic over a generic trait should type-check, got: %v", err)
	}
	err := checkSource(t, hdr+`function describe[T: From[i32, i64]](proto: T): T { return proto; }
function main(): i32 { return 0; }`)
	if err == nil || !strings.Contains(err.Error(), "takes 1 type argument") {
		t.Errorf("wrong bound-arg arity should be rejected, got: %v", err)
	}
	err = checkSource(t, hdr+`struct Plain { x: i32 }
function describe[T: From[i32]](proto: T, v: i32): T { return T.from(v); }
function main(): i32 { let p: Plain = Plain { x: 0 }; return describe(p, 5).x; }`)
	if err == nil || !strings.Contains(err.Error(), "does not implement trait From") {
		t.Errorf("non-implementing type argument should be rejected, got: %v", err)
	}
	err = checkSource(t, hdr+`function describe[T: From[i64]](proto: T, v: i64): T { return T.from(v); }
function main(): i32 {
  let z: Celsius = Celsius { deg: 0 };
  return describe(z, 20).deg;
}`)
	if err == nil || !strings.Contains(err.Error(), "the bound requires From[i64]") {
		t.Errorf("mismatched bound args should be rejected, got: %v", err)
	}
}

func TestGenericTraitConformance(t *testing.T) {
	ok := []string{
		`trait Container[T] { function get(self: Self): T; }
struct B { v: i32 }
impl Container[i32] for B { function get(self: Self): i32 { return self.v; } }
function main(): i32 { let b: B = B { v: 7 }; return b.get(); }`,
		`trait From[T] { function from(v: T): Self; }
struct C { d: i32 }
impl From[i32] for C { function from(v: i32): Self { return C { d: v }; } }
function main(): i32 { return C.from(9).d; }`,
		`trait Pair[A, B] { function fst(self: Self): A; function snd(self: Self): B; }
struct P { a: i32, b: i32 }
impl Pair[i32, i32] for P { function fst(self: Self): i32 { return self.a; } function snd(self: Self): i32 { return self.b; } }
function main(): i32 { let p: P = P { a: 1, b: 2 }; return p.fst() + p.snd(); }`,
	}
	for _, src := range ok {
		if err := checkSource(t, src); err != nil {
			t.Errorf("generic-trait program should type-check, got: %v\nsrc:\n%s", err, src)
		}
	}
	err := checkSource(t, `trait Container[T] { function get(self: Self): T; }
struct B { v: i32 }
impl Container for B { function get(self: Self): i32 { return self.v; } }
function main(): i32 { return 0; }`)
	if err == nil || !strings.Contains(err.Error(), "takes 1 type argument") {
		t.Errorf("generic-trait impl with wrong arity should be rejected, got: %v", err)
	}
	err = checkSource(t, `trait Container[T] { function get(self: Self): T; }
struct B { v: i32 }
impl Container[i32] for B { function get(self: Self): boolean { return true; } }
function main(): i32 { return 0; }`)
	if err == nil || !strings.Contains(err.Error(), "wrong signature") {
		t.Errorf("generic-trait impl with a mismatched method should be rejected, got: %v", err)
	}
}

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
    let b: B = B { v: 9 };
    let x: i32 = b.next();
    let y: i32 = first(b);
    return x + y;
}`
	if err := checkSource(t, src); err != nil {
		t.Errorf("associated-types program should type-check: %v", err)
	}
}

func TestAssociatedTypesGenericImplBinding(t *testing.T) {
	cases := []struct {
		name string
		src  string
	}{
		{"binds the impl's own type parameter", `trait Carrier { type Ok; function get(self: Self): Self::Ok; }
struct Box[T] { v: T }
impl[T] Carrier for Box[T] {
    type Ok = T;
    function get(self: Self): Self::Ok { return self.v; }
}
function main(): i32 {
    let b: Box[i32] = Box { v: 7 };
    let x: i32 = b.get();
    return x;
}`},
		{"projection through a bounded generic", `trait Carrier { type Ok; function get(self: Self): Self::Ok; }
struct Box[T] { v: T }
impl[T] Carrier for Box[T] {
    type Ok = T;
    function get(self: Self): Self::Ok { return self.v; }
}
function unwrap[C: Carrier](c: C): C::Ok { return c.get(); }
function main(): i32 {
    let b: Box[i32] = Box { v: 7 };
    let x: i32 = unwrap(b);
    return x;
}`},
		{"explicit projection on a concrete base", `trait Carrier { type Ok; function get(self: Self): Self::Ok; }
struct Box[T] { v: T }
impl[T] Carrier for Box[T] {
    type Ok = T;
    function get(self: Self): Self::Ok { return self.v; }
}
function twice(x: Box[i32]::Ok): i32 { return x + x; }
function main(): i32 {
    let b: Box[i32] = Box { v: 7 };
    return twice(b.get());
}`},
		{"binding is a composite of the parameter", `trait Holder { type Item; function take(self: Self): Self::Item; }
struct Box[T] { v: T }
impl[T] Holder for Box[T] {
    type Item = Option[T];
    function take(self: Self): Self::Item { return Some(self.v); }
}
function main(): i32 {
    let b: Box[i32] = Box { v: 7 };
    let o: Option[i32] = b.take();
    return 0;
}`},
		{"binds parameters other than the first, out of order", `trait Two { type A; type B; function fst(self: Self): Self::A; function snd(self: Self): Self::B; }
struct P[X, Y] { a: X, b: Y }
impl[X, Y] Two for P[X, Y] {
    type A = Y;
    type B = X;
    function fst(self: Self): Self::A { return self.b; }
    function snd(self: Self): Self::B { return self.a; }
}
function main(): i32 {
    let p: P[string, i32] = P { a: "hi", b: 40 };
    let n: i32 = p.fst();
    let s: string = p.snd();
    return n + s.len();
}`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if err := checkSource(t, c.src); err != nil {
				t.Errorf("should type-check: %v", err)
			}
		})
	}
}

func TestAssociatedTypesGenericImplBindingObservable(t *testing.T) {
	cases := []struct {
		src  string
		want string
	}{
		{`trait Carrier { type Ok; function get(self: Self): Self::Ok; }
struct Box[T] { v: T }
impl[T] Carrier for Box[T] {
    type Ok = T;
    function get(self: Self): Self::Ok { return self.v; }
}
function main(): i32 {
    let b: Box[i32] = Box { v: 7 };
    let s: string = b.get();
    return 0;
}`, "got i32"},
		{`trait Carrier { type Ok; function get(self: Self): Self::Ok; }
struct Box[T] { v: T }
impl[T] Carrier for Box[T] {
    type Ok = T;
    function get(self: Self): Self::Ok { return self.v; }
}
function unwrap[C: Carrier](c: C): C::Ok { return c.get(); }
function main(): i32 {
    let b: Box[string] = Box { v: "hi" };
    let n: i32 = unwrap(b);
    return 0;
}`, "got string"},
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

func TestAssociatedTypesProjectionInParameter(t *testing.T) {
	cases := []struct {
		name string
		src  string
	}{
		{"concrete impl", `trait Holder { type Item; function get(self: Self, d: Self::Item): Self::Item; }
struct IntBox { v: i32 }
impl Holder for IntBox {
    type Item = i32;
    function get(self: Self, d: Self::Item): Self::Item { return self.v; }
}
function pick[H: Holder](h: H, d: H::Item): H::Item { return h.get(d); }
function main(): i32 {
    let b: IntBox = IntBox { v: 7 };
    return pick(b, 0);
}`},
		{"parametric impl on a generic struct", `trait Holder { type Item; function get(self: Self, d: Self::Item): Self::Item; }
struct Box[T] { v: T }
impl[T] Holder for Box[T] {
    type Item = T;
    function get(self: Self, d: Self::Item): Self::Item { return self.v; }
}
function pick[H: Holder](h: H, d: H::Item): H::Item { return h.get(d); }
function main(): i32 {
    let b: Box[i32] = Box { v: 7 };
    return pick(b, 0);
}`},
		{
			name: "parametric impl on a generic enum",
			src: `trait Holder { type Item; function get(self: Self, d: Self::Item): Self::Item; }
enum E[T] { A(T), B }
impl[T] Holder for E[T] {
    type Item = T;
    function get(self: Self, d: Self::Item): Self::Item {
        match (self) { A(x) => { return x; }, B => { return d; } }
    }
}
function pick[H: Holder](h: H, d: H::Item): H::Item { return h.get(d); }
function main(): i32 {
    let e: E[i32] = A(7);
    return pick(e, 0) + e.get(1);
}`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if err := checkSource(t, c.src); err != nil {
				t.Errorf("should type-check: %v", err)
			}
		})
	}
}

func TestAssociatedTypesProjectionInParameterObservable(t *testing.T) {
	const prelude = `trait Holder { type Item; function get(self: Self, d: Self::Item): Self::Item; }
struct Box[T] { v: T }
impl[T] Holder for Box[T] {
    type Item = T;
    function get(self: Self, d: Self::Item): Self::Item { return self.v; }
}
function pick[H: Holder](h: H, d: H::Item): H::Item { return h.get(d); }
`
	cases := []struct{ src, want string }{
		{prelude + `function main(): i32 {
    let b: Box[i32] = Box { v: 7 };
    return pick(b, "no");
}`, "expected i32"},
		{prelude + `function main(): i32 {
    let b: Box[string] = Box { v: "hi" };
    return pick(b, 0);
}`, "expected string"},
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

func TestArrayLiteralElementsAreChecked(t *testing.T) {
	cases := []struct{ name, src, want string }{
		{"let destination", `function main(): i32 { let xs: i32[] = ["ab", "cd"]; return xs.len(); }`,
			"expected i32[], got string[]"},
		{"argument", `function total(xs: i32[]): i32 { return xs.len(); }
function main(): i32 { return total(["ab", "cd"]); }`,
			"expected i32[], got string[]"},
		{"argument, numeric literals into string[]", `function take(xs: string[]): i32 { return xs.len(); }
function main(): i32 { return take([1, 2, 3]); }`,
			"expected string[], got i32[]"},
		{"float literals into i64[]", `function main(): i32 { let xs: i64[] = [1.5, 2.5]; return xs.len(); }`,
			"expected i64[], got f64[]"},
		{"float literal into u8[]", `function main(): i32 { let xs: u8[] = [1.5]; return xs.len(); }`,
			"expected u8[], got f64[]"},
		{"float literals into an i64[] argument", `function total(xs: i64[]): i32 { return xs.len(); }
function main(): i32 { return total([1.5, 2.5]); }`,
			"expected i64[], got f64[]"},
		{"i32 variable into u8[]", `function f(b: i32): i32 { let xs: u8[] = [b]; return xs.len(); }
function main(): i32 { return f(300); }`,
			"expected u8[], got i32[]"},
		{"i32 variable into a u8[] argument", `function g(b: i32): i32 { return string_from_bytes_unchecked([b]).len(); }
function main(): i32 { return g(65); }`,
			"expected u8[], got i32[]"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := checkSource(t, c.src)
			if err == nil {
				t.Fatalf("expected an error")
			}
			if !strings.Contains(err.Error(), c.want) {
				t.Errorf("error %q does not contain %q", err.Error(), c.want)
			}
		})
	}
}

func TestArrayLiteralSettlingStillWorks(t *testing.T) {
	srcs := []string{
		`function main(): i32 { let xs: i64[] = [1, 2, 3]; return xs.len(); }`,
		`function main(): i32 { let xs: u8[] = [1, 2, 3]; return xs.len(); }`,
		`function main(): i32 { let xs: f64[] = [1.5, 2.5]; return xs.len(); }`,
		`function main(): i32 { let xs: f64[] = [1, 2]; return xs.len(); }`,
		`function main(): i32 { let xs: f32[] = [1.5, 2.5]; return xs.len(); }`,
		`function main(): i32 { let xs: i32[] = []; return xs.len(); }`,
		`function main(): i32 { let xs: string[] = ["a", "b"]; return xs.len(); }`,
		`function total(xs: i64[]): i32 { return xs.len(); }
function main(): i32 { return total([1, 2, 3]); }`,
		`function main(): i32 { let xs: f64[][] = [[1], [2]]; return 0; }`,
		`function main(): i32 { let xs: f64[][][] = [[[1]], [[2]]]; return 0; }`,
		`function count[T](xs: T[]): i32 { return xs.len(); }
function main(): i32 { return count([1, 2, 3]); }`,
		`function main(): i32 { let k = 5; let xs: u8[] = [k, 1]; return xs.len(); }`,
		`function f(b: i32): i32 { let xs: u8[] = [b as u8, 2]; return xs.len(); }
function main(): i32 { return f(3); }`,
	}
	for _, src := range srcs {
		if err := checkSource(t, src); err != nil {
			t.Errorf("should type-check: %v\n%s", err, src)
		}
	}
	bad := `function build[T](): i32 { let local: T[] = [1, 2, 3]; return local.len(); }
function main(): i32 { return 0; }`
	if err := checkSource(t, bad); err == nil || !strings.Contains(err.Error(), "expected T[], got i32[]") {
		t.Errorf("want E003 expected T[], got i32[]; got %v", err)
	}
}

func TestAssociatedTypesErrors(t *testing.T) {
	cases := []struct {
		src  string
		want string
	}{
		{`trait It { type Item; function next(self: Self): Self::Item; }
struct B { v: i32 }
impl It for B { function next(self: Self): Self::Item { return self.v; } }
function main(): i32 { return 0; }`, "must bind associated type"},
		{`trait It { type Item; function next(self: Self): Self::Item; }
struct B { v: i32 }
impl It for B { type Item = i32; type Extra = i32; function next(self: Self): Self::Item { return self.v; } }
function main(): i32 { return 0; }`, "does not declare"},
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

func TestTraitSupertraitAccepted(t *testing.T) {
	src := `trait Eq { function eq(self: Self, other: Self): boolean; }
trait Ord: Eq { function lt(self: Self, other: Self): boolean; }
struct P { x: i32 }
impl Eq for P { function eq(self: Self, other: Self): boolean { return self.x == other.x; } }
impl Ord for P { function lt(self: Self, other: Self): boolean { return self.x < other.x; } }
function cmp[T: Ord](a: T, b: T): boolean { if (a.eq(b)) { return true; } return a.lt(b); }
function main(): i32 { let p: P = P { x: 1 }; if (cmp(p, p)) { return 1; } return 0; }`
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
		{`trait Eq { function eq(self: Self, other: Self): boolean; }
trait Ord: Eq { function lt(self: Self, other: Self): boolean; }
struct P { x: i32 }
impl Ord for P { function lt(self: Self, other: Self): boolean { return self.x < other.x; } }
function main(): i32 { return 0; }`, "supertrait of Ord"},
		{`trait Ord: Nope { function lt(self: Self, other: Self): boolean; }
struct P { x: i32 }
impl Ord for P { function lt(self: Self, other: Self): boolean { return true; } }
function main(): i32 { return 0; }`, "unknown supertrait"},
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

func TestTraitImplForBuiltinType(t *testing.T) {
	src := `trait Tag { function tag(self: Self): string; }
impl Tag for i32 { function tag(self: Self): string { return "i32"; } }
function main(): i32 { let n: i32 = 5; let s: string = n.tag(); return 0; }`
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

func TestBoundedGenericAccepted(t *testing.T) {
	src := `trait Display { function to_string(self: Self): string; }
struct Point { x: i32 }
impl Display for Point { function to_string(self: Self): string { return "p"; } }
function show[T: Display](v: T): string { return v.to_string(); }
function main(): i32 { let p: Point = Point { x: 1 }; let s: string = show(p); return 0; }`
	if err := checkSource(t, src); err != nil {
		t.Errorf("bounded generic should typecheck: %v", err)
	}
}

func TestBoundedGenericErrors(t *testing.T) {
	cases := []struct {
		src  string
		want string
	}{
		{`trait Display { function to_string(self: Self): string; }
struct A { x: i32 }
struct B { y: i32 }
impl Display for A { function to_string(self: Self): string { return "a"; } }
function show[T: Display](v: T): string { return v.to_string(); }
function main(): i32 { let b: B = B { y: 1 }; let s: string = show(b); return 0; }`,
			"does not implement trait Display"},
		{`trait Display { function to_string(self: Self): string; }
function show[T: Display](v: T): string { return v.bogus(); }
function main(): i32 { return 0; }`,
			"no method \"bogus\" on type parameter T"},
		{`function show[T](v: T): string { return v.to_string(); }
function main(): i32 { return 0; }`,
			"add a trait bound"},
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

func TestDeriveErrors(t *testing.T) {
	cases := []struct {
		src  string
		want string
	}{
		{`trait Foo { function bar(self: Self): i32; }
@derive(Foo)
struct S { x: i32 }
function main(): i32 { return 0; }`, "only Eq, Display, Debug, Ord, Hash, Json, FromJson, and Default are derivable"},
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

func TestDeriveFieldConformancePreCheck(t *testing.T) {
	const ordTrait = "trait Ord { function cmp(self: Self, other: Self): i32; }\n"
	const ordI32 = "impl Ord for i32 { function cmp(self: Self, other: Self): i32 { if (self < other) { return 0 - 1; } if (self > other) { return 1; } return 0; } }\n"

	err := checkSource(t, ordTrait+"@derive(Ord)\nstruct Foo { x: i32, y: i32 }\nfunction main(): i32 { return 0; }")
	if err == nil {
		t.Fatal("expected E021 for underivable @derive(Ord), got nil")
	}
	es, ok := err.(diag.Errors)
	if !ok {
		t.Fatalf("expected diag.Errors, got %T: %v", err, err)
	}
	if len(es) != 1 {
		t.Fatalf("expected ONE error for the two-field gap, got %d: %v", len(es), err)
	}
	ce, ok := es[0].(*Error)
	if !ok {
		t.Fatalf("expected *Error, got %T", es[0])
	}
	if ce.ErrCode != "E021" {
		t.Errorf("code = %q, want E021", ce.ErrCode)
	}
	const wantMsg = "cannot @derive(Ord) for Foo: field x of type i32 does not implement Ord — add `impl Ord for i32` (or remove the derive)"
	if !strings.Contains(ce.Error(), wantMsg) {
		t.Errorf("message %q does not contain %q", ce.Error(), wantMsg)
	}
	if ce.Pos.Line != 3 || ce.Pos.Col == 0 {
		t.Errorf("position = %d:%d, want line 3 with a non-zero column", ce.Pos.Line, ce.Pos.Col)
	}

	err = checkSource(t, "trait Eq { function eq(self: Self, other: Self): boolean; }\n@derive(Eq)\nenum E { A, B(i32) }\nfunction main(): i32 { return 0; }")
	if err == nil || !strings.Contains(err.Error(), "cannot @derive(Eq) for E: variant B payload of type i32 does not implement Eq") {
		t.Errorf("enum payload gap: got %v", err)
	}

	for _, src := range []string{
		ordTrait + ordI32 + "@derive(Ord)\nstruct Foo { x: i32 }\nfunction main(): i32 { return 0; }",
		ordTrait + ordI32 + "@derive(Ord)\nstruct Outer { inner: Inner }\n@derive(Ord)\nstruct Inner { a: i32 }\nfunction main(): i32 { return 0; }",
		ordTrait + "struct Bar { a: i32 }\nfunction (self: Bar) cmp(other: Bar): i32 { return 0; }\n@derive(Ord)\nstruct Foo { b: Bar }\nfunction main(): i32 { return 0; }",
		ordTrait + ordI32 + "@derive(Ord)\nstruct Box[T] { v: T, n: i32 }\nfunction main(): i32 { let b: Box[i32] = Box { v: 1, n: 2 }; if (b < b) { return 1; } return 0; }",
	} {
		if err := checkSource(t, src); err != nil {
			t.Errorf("expected clean derive for %q, got: %v", src, err)
		}
	}
}

func TestDeriveFieldPreCheckCoversEveryFieldWiseTrait(t *testing.T) {
	for _, c := range []struct{ kind, decl string }{
		{"Eq", "trait Eq { function eq(self: Self, other: Self): boolean; }"},
		{"Ord", "trait Ord { function cmp(self: Self, other: Self): i32; }"},
		{"Hash", "trait Hash { function hash(self: Self): i32; }"},
		{"Display", "trait Display { function to_string(self: Self): string; }"},
		{"Debug", "trait Debug { function to_debug(self: Self): string; }"},
		{"Json", "trait Json { function to_json(self: Self): string; }"},
	} {
		src := c.decl + "\nstruct Bare { n: i32 }\n@derive(" + c.kind + ")\nstruct Foo { b: Bare }\nfunction main(): i32 { return 0; }"
		err := checkSource(t, src)
		if err == nil {
			t.Errorf("@derive(%s) over a non-conforming field: expected E021, got nil", c.kind)
			continue
		}
		es, ok := err.(diag.Errors)
		if !ok {
			t.Errorf("@derive(%s): expected diag.Errors, got %T: %v", c.kind, err, err)
			continue
		}
		ce, ok := es[0].(*Error)
		if !ok {
			t.Errorf("@derive(%s): expected *Error, got %T", c.kind, es[0])
			continue
		}
		if ce.ErrCode != "E021" {
			t.Errorf("@derive(%s): code = %q, want E021 (a position-less E043 means the gate skipped this kind): %v", c.kind, ce.ErrCode, err)
		}
		want := "cannot @derive(" + c.kind + ") for Foo: field b of type Bare does not implement " + c.kind
		if !strings.Contains(ce.Error(), want) {
			t.Errorf("@derive(%s): message %q does not contain %q", c.kind, ce.Error(), want)
		}
		if ce.Pos.Line != 4 || ce.Pos.Col == 0 {
			t.Errorf("@derive(%s): position = %d:%d, want line 4 with a non-zero column", c.kind, ce.Pos.Line, ce.Pos.Col)
		}
	}

	err := checkSource(t, "trait Default { function default(): Self; }\n@derive(Default)\nstruct Foo { b: (i32, i32) }\nfunction main(): i32 { return 0; }")
	if err == nil || !strings.Contains(err.Error(), "which has no default; implement Default by hand") {
		t.Errorf("@derive(Default) keeps its own dedicated diagnostic, got: %v", err)
	}
}

func TestDeriveEnumErrors(t *testing.T) {
	cases := []struct{ src, want string }{
		{`trait Foo { function foo(self: Self): boolean; }
@derive(Foo)
enum E { A, B }
function main(): i32 { return 0; }`, "only Eq, Display, Debug, Ord, Hash, Json, FromJson, and Default are derivable"},
	}
	for _, c := range cases {
		err := checkSource(t, c.src)
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("src %q: got %v, want containing %q", c.src, err, c.want)
		}
	}
}

func TestDeriveGenericEnum(t *testing.T) {
	src := `trait Eq { function eq(self: Self, other: Self): boolean; }
impl Eq for i32 { function eq(self: i32, other: i32): boolean { return self == other; } }
@derive(Eq)
enum E[T] { A(T), B }
function main(): i32 {
	let x = A(1);
	if (x.eq(A(1))) { return 0; }
	return 1;
}`
	if err := checkSource(t, src); err != nil {
		t.Fatalf("generic-enum @derive should check: %v", err)
	}
}

func TestDeriveHash(t *testing.T) {
	const hashTrait = `trait Hash { function hash(self: Self): i32; }
impl Hash for i32 { function hash(self: Self): i32 { return self; } }
`
	ok := []string{
		hashTrait + `@derive(Hash) struct P { x: i32, y: i32 }
function main(): i32 { let p: P = P { x: 1, y: 2 }; return p.hash(); }`,
		hashTrait + `@derive(Hash) enum E { A, B(i32), C(i32, i32) }
function main(): i32 { let e: E = C(1, 2); return e.hash(); }`,
		hashTrait + `@derive(Hash) struct Box[T] { v: T }
function main(): i32 { let b: Box[i32] = Box { v: 7 }; return b.hash(); }`,
	}
	for _, src := range ok {
		if err := checkSource(t, src); err != nil {
			t.Errorf("@derive(Hash) should type-check, got: %v\nsrc: %s", err, src)
		}
	}

	bad := hashTrait + `struct NoHash { z: i32 }
@derive(Hash) struct S { n: NoHash }
function main(): i32 { return 0; }`
	if err := checkSource(t, bad); err == nil {
		t.Error("expected an error deriving Hash for a struct with a non-Hash field, got nil")
	}
}

func TestDeriveDefault(t *testing.T) {
	const defTrait = `trait Default { function default(): Self; }
`
	ok := []string{
		defTrait + `@derive(Default) struct P { x: i32, y: string, f: boolean }
function main(): i32 { let p: P = P.default(); return p.x + p.y.len(); }`,
		defTrait + `@derive(Default) struct Inner { n: i32 }
@derive(Default) struct Outer { i: Inner, k: i32 }
function main(): i32 { let o: Outer = Outer.default(); return o.i.n + o.k; }`,
		defTrait + `@derive(Default) enum E { A, B(i32) }
function main(): i32 { let e: E = E.default(); match (e) { A => { return 0; }, B(n) => { return n; } } }`,
		defTrait + `@derive(Default) enum E { First(i32, i32), Second }
function main(): i32 { let e: E = E.default(); match (e) { First(a, b) => { return a + b; }, Second => { return 9; } } }`,
		defTrait + `@derive(Default) struct Inner { n: i32 }
@derive(Default) struct Box[T] { v: T }
function main(): i32 { let b: Box[Inner] = Box.default(); return b.v.n; }`,
	}
	for _, src := range ok {
		if err := checkSource(t, src); err != nil {
			t.Errorf("@derive(Default) should type-check, got: %v\nsrc: %s", err, src)
		}
	}

	bad := []struct{ src, want string }{
		{defTrait + `@derive(Default) struct S { xs: i32[] }
function main(): i32 { return 0; }`, "no default"},
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

func TestDeriveJson(t *testing.T) {
	const jsonTrait = `trait Json { function to_json(self: Self): string; }
impl Json for i32 { function to_json(self: Self): string { return "0"; } }
impl Json for string { function to_json(self: Self): string { return self; } }
`
	ok := []string{
		jsonTrait + `@derive(Json) struct P { x: i32, y: i32 }
function main(): i32 { let p: P = P { x: 1, y: 2 }; let s: string = p.to_json(); return 0; }`,
		jsonTrait + `@derive(Json) enum E { A, B(i32), C(i32, i32) }
function main(): i32 { let e: E = C(1, 2); let s: string = e.to_json(); return 0; }`,
		jsonTrait + `@derive(Json) struct Box[T] { v: T }
function main(): i32 { let b: Box[string] = Box { v: "hi" }; let s: string = b.to_json(); return 0; }`,
		jsonTrait + `@derive(Json) struct Inner { n: i32 }
@derive(Json) struct Outer { a: Inner, tag: string }
function main(): i32 { let o: Outer = Outer { a: Inner { n: 5 }, tag: "x" }; let s: string = o.to_json(); return 0; }`,
	}
	for _, src := range ok {
		if err := checkSource(t, src); err != nil {
			t.Errorf("@derive(Json) should type-check, got: %v\nsrc: %s", err, src)
		}
	}

	bad := jsonTrait + `struct NoJson { z: i32 }
@derive(Json) struct S { n: NoJson }
function main(): i32 { return 0; }`
	if err := checkSource(t, bad); err == nil {
		t.Error("expected an error deriving Json for a struct with a non-Json field, got nil")
	}
}

func TestDeriveFromJson(t *testing.T) {
	const fromJsonTrait = `enum JsonValue { JNull }
trait FromJson { function from_json_value(v: JsonValue): Result[Self, string]; }
`
	cases := []struct{ src, want string }{
		{fromJsonTrait + `@derive(FromJson) enum E { A, B }
function main(): i32 { return 0; }`, "the decoder is std/json's"},
		{fromJsonTrait + `@derive(FromJson) struct P { x: i32 }
function main(): i32 { return 0; }`, "the decoder is std/json's"},
	}
	for _, c := range cases {
		err := checkSource(t, c.src)
		if err == nil {
			t.Errorf("expected an E021 for %s, got nil", c.src)
			continue
		}
		if !strings.Contains(err.Error(), c.want) {
			t.Errorf("error %q does not contain %q", err.Error(), c.want)
		}
	}
}

func TestAssociatedFunctions(t *testing.T) {
	ok := []string{
		`trait Zero { function zero(): Self; }
struct Point { x: i32, y: i32 }
impl Zero for Point { function zero(): Self { return Point { x: 0, y: 0 }; } }
function main(): i32 { let p: Point = Point.zero(); return p.x + p.y; }`,
		`trait Ctor { function make(a: i32, b: i32): Self; }
struct Point { x: i32, y: i32 }
impl Ctor for Point { function make(a: i32, b: i32): Self { return Point { x: a, y: b }; } }
function main(): i32 { let p: Point = Point.make(3, 4); return p.x + p.y; }`,
		`trait Empty { function empty(): Self; }
enum Opt { Nothing, Just(i32) }
impl Empty for Opt { function empty(): Self { return Nothing; } }
function main(): i32 { let o: Opt = Opt.empty(); match (o) { Nothing => { return 0; }, Just(n) => { return n; } } }`,
		`trait Zero { function zero(): Self; }
struct P { x: i32 }
impl Zero for P { function zero(): Self { return P { x: 9 }; } }
function main(): i32 { return P.zero().x; }`,
		`trait Zero { function zero(): Self; }
struct P { x: i32 }
impl Zero for P { function zero(): Self { return P { x: 0 }; } }
function mk[T: Zero](): T { return T.zero(); }
function main(): i32 { let p: P = mk(); return p.x; }`,
		`trait From { function of(n: i32): Self; }
struct Box { v: i32 }
impl From for Box { function of(n: i32): Self { return Box { v: n }; } }
function build[T: From](n: i32): T { return T.of(n); }
function main(): i32 { let b: Box = build(7); return b.v; }`,
		`enum V { A(i32), B }
trait Mk { function mk(v: V): Result[Self, string]; }
struct Box { v: i32 }
impl Mk for Box { function mk(v: V): Result[Self, string] { match (v) { A(n) => { return Ok(Box { v: n }); }, B => { return Err("b"); } } return Err(""); } }
function build[T: Mk](v: V): Result[T, string] { return T.mk(v); }
function main(): i32 { let b: Result[Box, string] = build[Box](A(7)); return 0; }`,
	}
	for _, src := range ok {
		if err := checkSource(t, src); err != nil {
			t.Errorf("associated function should type-check, got: %v\nsrc: %s", err, src)
		}
	}

	bad := []struct{ src, want string }{
		{`struct Point { x: i32 }
function (p: Point) getx(): i32 { return p.x; }
function main(): i32 { return Point.getx(); }`, "is a method; call it on a value"},
		{`trait Zero { function zero(): Self; }
struct Point { x: i32 }
impl Zero for Point { }
function main(): i32 { return 0; }`, "missing method"},
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

func TestDynTraitChecker(t *testing.T) {
	const prelude = `trait Shape { function area(self: Self): i32; }
struct Circle { r: i32 }
impl Shape for Circle { function area(self: Self): i32 { return self.r; } }
`
	if err := checkSource(t, prelude+`
function f(d: dyn Shape): i32 { return d.area(); }
function main(): i32 { let d: dyn Shape = Circle { r: 3 }; return f(d); }`); err != nil {
		t.Fatalf("valid dyn use should check: %v", err)
	}

	if err := checkSource(t, prelude+`struct Holder { shape: dyn Shape, tag: i32 }
function main(): i32 { let h: Holder = Holder { shape: Circle { r: 4 }, tag: 1 }; return h.shape.area() + h.tag; }`); err != nil {
		t.Fatalf("implicit concrete->dyn coercion in a struct field should check: %v", err)
	}

	if err := checkSource(t, prelude+`enum Box { Wrap(dyn Shape), Empty }
function main(): i32 { let b: Box = Wrap(Circle { r: 4 }); match (b) { Wrap(d) => { return d.area(); }, Empty => { return 0; } } }`); err != nil {
		t.Fatalf("implicit concrete->dyn coercion in a variant payload should check: %v", err)
	}

	cases := []struct{ name, src, want string }{
		{"non-impl coercion",
			prelude + `struct NoShape { z: i32 }
function main(): i32 { let d: dyn Shape = NoShape { z: 1 }; return 0; }`,
			"got NoShape"},
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
function main(): i32 { let ds: dyn Shape[] = [Circle { r: 1 }, NoShape { z: 2 }]; return 0; }`,
			"does not implement Shape"},
		{"non-impl in dyn struct field",
			prelude + `struct NoShape { z: i32 }
struct Holder { shape: dyn Shape }
function main(): i32 { let h: Holder = Holder { shape: NoShape { z: 1 } }; return 0; }`,
			"expected dyn Shape, got NoShape"},
		{"non-impl in dyn variant payload",
			prelude + `struct NoShape { z: i32 }
enum Box { Wrap(dyn Shape) }
function main(): i32 { let b: Box = Wrap(NoShape { z: 1 }); return 0; }`,
			"payload 0 type NoShape, expected dyn Shape"},
		{"str in dyn struct field",
			`trait Size { function size(self: Self): i32; }
impl Size for str { function size(self: str): i32 { return self.len(); } }
struct Holder { d: dyn Size }
function main(): i32 { let s: string = "abc"; let v: str = slice_unchecked(s, 0, 2); let h: Holder = Holder { d: v }; return h.d.size(); }`,
			"expected dyn Size, got str"},
		{"str in dyn variant payload",
			`trait Size { function size(self: Self): i32; }
impl Size for str { function size(self: str): i32 { return self.len(); } }
enum Box { Wrap(dyn Size) }
function main(): i32 { let s: string = "abc"; let v: str = slice_unchecked(s, 0, 2); let b: Box = Wrap(v); return 0; }`,
			"payload 0 type str, expected dyn Size"},
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

func TestDynGenericTraitChecker(t *testing.T) {
	const prelude = `trait Container[T] { function get(self: Self): T; }
struct BoxI { v: i32 }
impl Container[i32] for BoxI { function get(self: Self): i32 { return self.v; } }
struct BoxS { v: i32 }
impl Container[string] for BoxS { function get(self: Self): string { return "x"; } }
`
	if err := checkSource(t, prelude+`
function take(d: dyn Container[i32]): i32 { return d.get(); }
function main(): i32 { let d: dyn Container[i32] = BoxI { v: 7 }; return take(d); }`); err != nil {
		t.Fatalf("valid generic dyn use should check: %v", err)
	}

	cases := []struct{ name, src, want string }{
		{"argument mismatch",
			prelude + `function main(): i32 { let d: dyn Container[string] = BoxI { v: 1 }; return 0; }`,
			"got BoxI"},
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
function main(): i32 { let d: dyn Container[string] = BoxS { v: 1 }; return 0; }`,
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

func TestDynAssocTypeChecker(t *testing.T) {
	const prelude = `trait Producer { type Item; function get(self: Self): Self::Item; }
struct IntBox { v: i32 }
impl Producer for IntBox { type Item = i32; function get(self: Self): i32 { return self.v; } }
`
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
			prelude + `function main(): i32 { let d: dyn Producer[Item = string] = IntBox { v: 1 }; return 0; }`,
			"got IntBox"},
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

func TestDynMultiTraitChecker(t *testing.T) {
	const prelude = `trait Show { function show(self: Self): i32; }
trait Sized { function size(self: Self): i32; }
struct Both { v: i32 }
impl Show for Both { function show(self: Self): i32 { return self.v; } }
impl Sized for Both { function size(self: Self): i32 { return 1; } }
struct OnlyShow { v: i32 }
impl Show for OnlyShow { function show(self: Self): i32 { return self.v; } }
`
	if err := checkSource(t, prelude+`
function f(d: dyn Show + Sized): i32 { return d.show() + d.size(); }
function main(): i32 { let d: dyn Show + Sized = Both { v: 3 }; return f(d); }`); err != nil {
		t.Fatalf("valid multi-trait dyn use should check: %v", err)
	}
	if err := checkSource(t, prelude+`
function main(): i32 { let d: dyn Sized + Show = Both { v: 3 }; return d.show() + d.size(); }`); err != nil {
		t.Fatalf("order-insensitive multi-trait dyn should check: %v", err)
	}

	cases := []struct{ name, src, want string }{
		{"missing one trait",
			prelude + `function main(): i32 { let d: dyn Show + Sized = OnlyShow { v: 1 }; return 0; }`,
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

func TestDowncastChecker(t *testing.T) {
	const prelude = `trait Shape { function area(self: Self): i32; }
struct Circle { r: i32 }
struct Rect { w: i32, h: i32 }
impl Shape for Circle { function area(self: Self): i32 { return self.r; } }
impl Shape for Rect { function area(self: Self): i32 { return self.w * self.h; } }
`
	if err := checkSource(t, prelude+`
function describe(s: dyn Shape): i32 {
    let c: Option[Circle] = s as? Circle;
    return match (c) { Some(x) => x.r, None => 0 };
}
function main(): i32 { return describe(Circle { r: 7 }); }`); err != nil {
		t.Fatalf("valid downcast should check: %v", err)
	}

	prog, err := parser.Parse(prelude + `
function describe(s: dyn Shape): Option[Circle] { return s as? Circle; }
function main(): i32 { return 0; }`)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if _, err := Check(prog); err != nil {
		t.Fatalf("downcast result-type flow should check: %v", err)
	}
	ret := prog.Funcs[len(prog.Funcs)-2].Body.Stmts[0].(*ast.Return)
	if dc, ok := ret.Value.(*ast.DowncastExpr); !ok {
		t.Fatalf("expected DowncastExpr, got %T", ret.Value)
	} else if dc.Trait != "Shape" {
		t.Errorf("DowncastExpr.Trait = %q, want Shape", dc.Trait)
	}

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
			prelude + `function main(): i32 { let c: string = "1"; let d: Option[Circle] = c as? Circle; return 0; }`,
			"requires a 'dyn Trait' value or an integer on the left"},
		{"integer to a non-integer",
			prelude + `function main(): i32 { let c: i32 = 1; let d: Option[Circle] = c as? Circle; return 0; }`,
			"'as?' from i32 converts only to an integer type, got Circle"},
		{"target does not implement trait",
			prelude + `struct NoShape { z: i32 }
function describe(s: dyn Shape): i32 { let c: Option[NoShape] = s as? NoShape; return 0; }
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

func TestDynCoercionRecorded(t *testing.T) {
	const src = `trait Shape { function area(self: Self): i32; }
struct Circle { r: i32 }
impl Shape for Circle { function area(self: Self): i32 { return self.r; } }
function f(d: dyn Shape): i32 { return d.area(); }
function main(): i32 {
    let d: dyn Shape = Circle { r: 3 };
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
		if _, ok := holder.(*ast.StructLit); !ok {
			t.Errorf("holder = %T, want *ast.StructLit (the Circle literal)", holder)
		}
	}
}

func TestDynCoercionNotRecordedForNonBoxing(t *testing.T) {
	const src = `trait Shape { function area(self: Self): i32; }
struct Circle { r: i32 }
impl Shape for Circle { function area(self: Self): i32 { return self.r; } }
function passthrough(d: dyn Shape): i32 {
    let e: dyn Shape = d;
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
	if len(info.DynCoercions) != 1 {
		t.Fatalf("want exactly 1 coercion (the Circle arg), got %d: %+v", len(info.DynCoercions), info.DynCoercions)
	}
}

func TestExternImportTypeChecks(t *testing.T) {
	src := `@import("wasi:random/random@0.2.0", "get-random-u64")
function get_random(): u64;
function main(): i32 {
	let r: u64 = get_random();
	return 0;
}`
	if err := checkSource(t, src); err != nil {
		t.Fatalf("extern @import call should type-check: %v", err)
	}
}

func TestResourceHandleChecker(t *testing.T) {
	const prelude = `@import("wasi:io/poll@0.2.0", "pollable")
resource Pollable;

@import("wasi:clocks/monotonic-clock@0.2.0", "subscribe-duration")
function subscribe(ns: u64): own Pollable;

@import("wasi:io/poll@0.2.0", "[method]pollable.ready")
function ready(h: borrow Pollable): boolean;
`
	if err := checkSource(t, prelude+`
function main(): i32 {
	let p: own Pollable = subscribe(0 as u64);
	if (ready(p)) { return 1; }
	return 0;
}`); err != nil {
		t.Fatalf("valid resource-handle use should check: %v", err)
	}
	if err := checkSource(t, prelude+`
function main(): i32 {
	let p: Pollable = subscribe(0 as u64);
	if (ready(p)) { return 1; }
	return 0;
}`); err != nil {
		t.Fatalf("bare resource name (owned handle) should check: %v", err)
	}

	cases := []struct{ name, src, want string }{
		{"unknown resource",
			`function f(h: borrow Bogus): i32 { return 0; }
function main(): i32 { return 0; }`,
			"`Bogus` is not a resource, so `borrow Bogus` is not a handle type; a parameter borrows by default, written `name: Bogus`"},
		{"struct is not a resource",
			`struct Foo { v: i32 }
function f(xs: own Foo): i32 { return 0; }
function main(): i32 { return 0; }`,
			"`Foo` is not a resource, so `own Foo` is not a handle type; a consuming parameter is written `own name: Foo`"},
		{"contextual type name is not a resource",
			"function f(s: own str): i32 { return 0; }\nfunction main(): i32 { return 0; }",
			"`str` is not a resource"},
		{"type parameter is not a resource",
			"function f[T](x: own T): i32 { return 0; }\nfunction main(): i32 { return 0; }",
			"`T` is not a resource"},
		{"struct field",
			"struct Foo { v: i32 }\nstruct S { h: own Foo }\nfunction main(): i32 { return 0; }",
			"2:1: `Foo` is not a resource"},
		{"enum payload",
			"struct Foo { v: i32 }\nenum E { A(own Foo) }\nfunction main(): i32 { return 0; }",
			"2:1: `Foo` is not a resource"},
		{"let annotation",
			"struct Foo { v: i32 }\nfunction main(): i32 {\n\tlet x: own Foo = Foo { v: 1 };\n\treturn 0;\n}",
			"3:2: `Foo` is not a resource"},
		{"plain i32 is not a handle",
			prelude + `function main(): i32 {
	let n: i32 = 7;
	if (ready(n)) { return 1; }
	return 0;
}`,
			"expected borrow Pollable, got i32"},
		{"handle is not a plain i32",
			prelude + `function main(): i32 {
	let p: own Pollable = subscribe(0 as u64);
	let n: i32 = p;
	return n;
}`,
			"expected i32, got own Pollable"},
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

func TestUnusedCollectionResultE055(t *testing.T) {
	err := checkSource(t, `function main(): i32 {
	let a: i32[] = [1];
	a.append(2);
	return a[0];
}`)
	if err == nil || !strings.Contains(err.Error(), "is unused") {
		t.Errorf("bare append should be E055, got %v", err)
	}
	if err := checkSource(t, `function main(): i32 {
	let a: i32[] = [1];
	a = a.append(2);
	return a[0];
}`); err != nil {
		t.Errorf("reassigned append should check, got %v", err)
	}
	if err := checkSource(t, `function main(): i32 {
	let a: i32[] = [1];
	let _ = a.append(2);
	return a.len();
}`); err != nil {
		t.Errorf("let _ discard should check, got %v", err)
	}
	if err := checkSource(t, `function main(): i32 {
	let a: i32[] = [1, 2];
	a = a.with(0, 9);
	return a[0];
}`); err != nil {
		t.Errorf("a = a.with(...) must check clean, got %v", err)
	}
	if err := checkSource(t, `function main(): i32 {
	let a: i32[] = [1];
	return a.append(2)[0];
}`); err != nil {
		t.Errorf("used append result must not trip E055, got %v", err)
	}
}

func TestArrayElementImmutabilityE056(t *testing.T) {
	err := checkSource(t, `function main(): i32 {
	let a: i32[] = [1, 2, 3];
	a[0] = 9;
	return a[0];
}`)
	if err == nil || !strings.Contains(err.Error(), "subscripts are read-only after construction") {
		t.Errorf("expected E056 for subscript assignment, got: %v", err)
	}
	err = checkSource(t, `function main(): i32 {
	let a: i32[] = [1, 2, 3];
	a[1] += 5;
	return a[1];
}`)
	if err == nil || !strings.Contains(err.Error(), "subscripts are read-only after construction") {
		t.Errorf("expected E056 for compound subscript assignment, got: %v", err)
	}
	if err := checkSource(t, `function main(): i32 {
	let a: i32[] = [1, 2, 3];
	a = a.with(0, 9);
	return a[0];
}`); err != nil {
		t.Errorf("a = a.with(...) must check clean, got: %v", err)
	}
}

const cellElemRefusal = "a cell's element must be built from scalars, strings, arrays, tuples, maps, structs and enums"

func TestCellElemTypeE057(t *testing.T) {
	const decls = `import "core/map";
struct Point { x: i32, name: string }
struct Session { user: string, seen: Map[string, i32], last: Option[Point], trail: Point[] }
struct Box[T] { v: T }
struct Thunk { run: () => i32 }
enum Tree { Leaf(i32), Node(Tree[]) }
enum Slot { Empty, Full(Cell[i32]) }
trait Shape { function area(self: Self): i32; }
`
	cases := []struct {
		name, body string
		risk       string
	}{
		{"scalar", `let c: Cell[i32] = cell_new(0); c.set(c.get() + 1);`, ""},
		{"string", `let c = cell_new("x"); c.set("yy");`, ""},
		{"char", `let c = cell_new('a');`, ""},
		{"struct", `let c: Cell[Point] = cell_new(Point { x: 1, name: "p" });`, ""},
		{"string-array", `let c: Cell[string[]] = cell_new(["a", "b"]);`, ""},
		{"nested-array", `let c: Cell[i32[][]] = cell_new([[1], []]);`, ""},
		{"tuple", `let c = cell_new(("a", 1));`, ""},
		{"map-by-destination", `let c: Cell[Map[string, i32]] = cell_new(map_new(8)); c.set(c.get().insert("k", 1));`, ""},
		{"empty-array-by-destination", `let c: Cell[Point[]] = cell_new([]);`, ""},
		{"none-by-destination", `let c: Cell[Option[Point]] = cell_new(None);`, ""},
		{"nested-struct", `let s: Session = Session { user: "u", seen: map_new(4), last: None, trail: [] }; let c = cell_new(s);`, ""},
		{"recursive-enum", `let c: Cell[Tree] = cell_new(Node([Leaf(1)]));`, ""},
		{"generic-struct", `let c: Cell[Box[string]] = cell_new(Box { v: "s" });`, ""},
		{"function", `let c: Cell[() => i32] = cell_new(() => 1);`, "() => i32"},
		{"function-field", `let c = cell_new(Thunk { run: () => 1 });`, "() => i32"},
		{"function-arg", `let b: Box[() => i32] = Box { v: () => 1 }; let c = cell_new(b);`, "() => i32"},
		{"function-in-map", `let c: Cell[Map[string, () => i32]] = cell_new(map_new(4));`, "() => i32"},
		{"cell", `let c = cell_new(cell_new(1));`, "Cell[i32]"},
		{"cell-payload", `let c = cell_new(Empty);`, "Cell[i32]"},
		{"dyn", `let c: Cell[(dyn Shape)[]] = cell_new([]);`, "dyn Shape"},
		{"view", `let c: Cell[str] = cell_new("v");`, "str"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := checkSource(t, decls+"function main(): i32 { "+tc.body+" return 0; }")
			if tc.risk == "" {
				if err != nil {
					t.Fatalf("should check, got %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), cellElemRefusal) || !strings.Contains(err.Error(), tc.risk) {
				t.Fatalf("want E057 naming %s, got %v", tc.risk, err)
			}
		})
	}
}

func TestCellElemTypeParamE057(t *testing.T) {
	for _, src := range []string{
		"function mk[T](x: T): Cell[T] { return cell_new(x); }",
		"function mk[T](x: T): i32 { let c = cell_new(Some(x)); return 0; }",
		"struct Holder[T] { c: Cell[T[]] }",
		"enum E[T] { A(Cell[T]), B }",
	} {
		err := checkSource(t, src+"\nfunction main(): i32 { return 0; }")
		if err == nil || !strings.Contains(err.Error(), cellElemRefusal) || !strings.Contains(err.Error(), "T") {
			t.Errorf("%s: want E057 naming T, got %v", src, err)
		}
	}
}

func TestCellElemTypeE057AnnotationPosition(t *testing.T) {
	cases := []struct {
		name, src string
		line, col int
	}{
		{"field", "struct Point { x: () => i32 }\nstruct Holder {\n    c: Cell[Point],\n}\nfunction main(): i32 { return 0; }", 3, 5},
		{"param", "struct Point { x: () => i32 }\nfunction f(c: Cell[Point]): i32 { return 0; }\nfunction main(): i32 { return 0; }", 2, 12},
		{"let", "struct Point { x: () => i32 }\nfunction main(): i32 {\n    let c: Cell[Point] = cell_new(Point { x: () => 1 });\n    return 0;\n}", 3, 5},
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
			rendered := diag.Format("", tc.src, found)
			if !strings.Contains(rendered, "error[E057]") {
				t.Errorf("rendered diagnostic missing error[E057] prefix:\n%s", rendered)
			}
		})
	}
}

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

func TestCheckPrintDisplayAccepted(t *testing.T) {
	srcStruct := `trait Display { function to_string(self: Self): string; }
struct Point { x: i32, y: i32 }
impl Display for Point { function to_string(self: Self): string { return "p"; } }
function main(): i32 {
    let p: Point = Point { x: 1, y: 2 };
    print(p);
    write(p);
    eprint(p);
    return 0;
}`
	if err := checkSource(t, srcStruct); err != nil {
		t.Errorf("print(struct: Display) should typecheck: %v", err)
	}
	srcStr := `function main(): i32 { print("hi"); write("x"); eprint("y"); return 0; }`
	if err := checkSource(t, srcStr); err != nil {
		t.Errorf("print(string) should still typecheck: %v", err)
	}
}

func TestCheckBoundDrivenInference(t *testing.T) {
	src := `trait Iterator[T] { function next(self: Self): Option[(T, Self)]; }
struct RangeIter { cur: i32, end: i32 }
impl Iterator[i32] for RangeIter { function next(self: Self): Option[(i32, Self)] { if (self.cur >= self.end) { return None; } return Some((self.cur, RangeIter { cur: self.cur + 1, end: self.end })); } }
function last[T, I: Iterator[T]](it: I, dflt: T): T { let acc = dflt; let cur = it; let go = true; while (go) { match (cur.next()) { Some(t) => { acc = t.0; cur = t.1; }, None => { go = false; }, } } return acc; }
function main(): i32 { return last(RangeIter { cur: 0, end: 5 }, -1); }`
	if err := checkSource(t, src); err != nil {
		t.Errorf("bound-driven inference of T from the impl should typecheck: %v", err)
	}
}

func TestCheckBoundDrivenInferenceBeatsLiteral(t *testing.T) {
	src := `trait Iterator[T] { function next(self: Self): Option[(T, Self)]; }
struct I64Iter { cur: i64 }
impl Iterator[i64] for I64Iter { function next(self: Self): Option[(i64, Self)] { return None; } }
function has[T, I: Iterator[T]](it: I, target: T): T { return target; }
function main(): i32 { let v = has(I64Iter { cur: 0 }, 2); let w: i64 = v; if (w == 2) { return 0; } return 1; }`
	if err := checkSource(t, src); err != nil {
		t.Errorf("the bound's impl should decide T over the literal: %v", err)
	}
}

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

func TestCheckPrintNonDisplayRejected(t *testing.T) {
	cases := []struct{ src, want string }{
		{`struct Point { x: i32 }
function main(): i32 { let p: Point = Point { x: 1 }; print(p); return 0; }`,
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

func TestBlockExprChecker(t *testing.T) {
	if err := checkSource(t, `function main(): i32 {
		let e = 5;
		let x: i32 = if (e > 0) { let k = e + 1; k } else { 0 };
		return x;
	}`); err != nil {
		t.Errorf("if-block tail-type: unexpected error: %v", err)
	}

	if err := checkSource(t, `function main(): i32 {
		let t = 0;
		let label: string = if (t == 0) { let s = "a"; s } else { "b" };
		return label.len();
	}`); err != nil {
		t.Errorf("string-tail unification: unexpected error: %v", err)
	}

	if err := checkSource(t, `function main(): i32 {
		let tag = 0;
		let r: i32 = match (tag) { 0 => { let s = tag + 5; s }, _ => 99 };
		return r;
	}`); err != nil {
		t.Errorf("match-arm block tail: unexpected error: %v", err)
	}
}

func TestCheckDeferBlockVoidAction(t *testing.T) {
	if err := checkSource(t, `function f(): Result[i32, i32] {
		let x: i32 = 0;
		defer { x = x + 1; }
		errdefer { x = x + 2; }
		return Ok(x);
	}`); err != nil {
		t.Errorf("defer/errdefer void block action: unexpected error: %v", err)
	}

	err := checkSource(t, `function main(): i32 {
		let y: i32 = { let z = 1; };
		return y;
	}`)
	if err == nil || !strings.Contains(err.Error(), "no trailing value") {
		t.Errorf("value-position void block should still error E061, got: %v", err)
	}
}

func TestBlockExprCheckerNeverDiverges(t *testing.T) {
	cases := []struct {
		name string
		src  string
	}{
		{
			"general-block-all-return",
			`function f(n: i32): i32 {
				let x: i32 = { if (n < 0) { return 1; } return 2; };
				return x;
			}
			function main(): i32 { return f(5); }`,
		},
		{
			"if-expr-both-arms-diverge",
			`function f(n: i32): i32 {
				let x: i32 = if (n < 0) { return 1; } else { return 2; };
				return x;
			}
			function main(): i32 { return f(5); }`,
		},
		{
			"match-arm-diverges",
			`function f(n: i32): i32 {
				let x: i32 = match (n) { 0 => { return 100; }, _ => { n * 2 } };
				return x;
			}
			function main(): i32 { return f(5); }`,
		},
		{
			"no-annotation",
			`function f(n: i32): i32 {
				let x = { if (n < 0) { return 1; } return 2; };
				return x;
			}
			function main(): i32 { return f(5); }`,
		},
		{
			"break-continue-in-loop",
			`function main(): i32 {
				let s: i32 = 0; let i: i32 = 0;
				while (i < 5) {
					i = i + 1;
					let d: i32 = { if (i == 4) { break; } if (i == 2) { continue; } i };
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
				let x: i32 = if (true) { let k = 1; k } else { 0 };
				return k;
			}`,
			"cannot find",
		},
		{
			"value-less-block-in-value-position",
			`function main(): i32 {
				let x: i32 = if (true) { let k = 1; } else { 0 };
				return x;
			}`,
			"block-expression has no trailing value",
		},
		{
			"mismatched-branch-tails",
			`function main(): i32 {
				let x = if (true) { let k = 1; k } else { let s = "x"; s };
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

func TestDynAssocFnCallIsRejected(t *testing.T) {
	assoc := `struct Box { v: i32 }
trait Mk { function make(own b: Box): i32; }
struct P { v: i32 }
impl Mk for P { function make(own b: Box): i32 { return b.v; } }
function twice(own m: dyn Mk, b1: Box, b2: Box): i32 { return m.make(b1) + m.make(b2); }
function main(): i32 { return 0; }`
	err := checkSource(t, assoc)
	if err == nil {
		t.Fatalf("assoc fn called through dyn: want an error, got none")
	}
	if !hasCode(err, "E021") {
		t.Errorf("assoc fn called through dyn: want E021, got %v", err)
	}
	for _, want := range []string{`"make" is an associated function of trait Mk`, "dyn Mk"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("assoc fn called through dyn: want the message to contain %q, got %v", want, err)
		}
	}
	if strings.Contains(err.Error(), "after it was consumed") {
		t.Errorf("assoc fn's `own` argument read as an `own` receiver: got a use-after-move, %v", err)
	}

	consuming := `trait Mk { function take(own self: Self): i32; }
struct P { v: i32 }
impl Mk for P { function take(own self: Self): i32 { return 1; } }
function twice(own m: dyn Mk): i32 { return m.take() + m.take(); }
function main(): i32 { return 0; }`
	err = checkSource(t, consuming)
	if err == nil || !strings.Contains(err.Error(), "after it was consumed") {
		t.Errorf("own self method used twice: want the E050 use-after-move, got %v", err)
	}
}

func TestBoundedParamAssocFnCallNoPanic(t *testing.T) {
	for _, tc := range []struct {
		name, src, want string
	}{
		{
			name: "no-arg",
			src: `trait Show { function show(): i32; }
struct P { v: i32 }
impl Show for P { function show(): i32 { return 5; } }
function pick[T: Show](a: T): i32 { return a.show(); }
function main(): i32 { return pick(P { v: 42 }); }`,
			want: "associated function",
		},
		{
			name: "with-arg",
			src: `trait Show { function show(x: i32): i32; }
struct P { v: i32 }
impl Show for P { function show(x: i32): i32 { return x + 1; } }
function pick[T: Show](a: T): i32 { return a.show(1); }
function main(): i32 { return pick(P { v: 41 }); }`,
			want: "associated function",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := checkSource(t, tc.src)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Errorf("want an error mentioning %q, got %v", tc.want, err)
			}
		})
	}
}

func TestBoundedParamTraitCallsStillCheck(t *testing.T) {
	for _, tc := range []struct{ name, src string }{
		{
			name: "method-on-value",
			src: `trait Show { function show(self: Self): i32; }
struct P { v: i32 }
impl Show for P { function show(self: Self): i32 { return self.v; } }
function pick[T: Show](a: T): i32 { return a.show(); }
function main(): i32 { return pick(P { v: 42 }); }`,
		},
		{
			name: "assoc-fn-on-type-param",
			src: `trait Zero { function zero(): Self; }
struct P { v: i32 }
impl Zero for P { function zero(): Self { return P { v: 7 }; } }
function mk[T: Zero](): i32 { let z: T = T.zero(); return 1; }
function main(): i32 { return 42; }`,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := checkSource(t, tc.src); err != nil {
				t.Errorf("want no error, got %v", err)
			}
		})
	}
}

func TestDynMethodCallNoSelfParamIsRejected(t *testing.T) {
	for _, tc := range []struct{ name, src, want string }{
		{
			name: "no-arg",
			src: `trait Sh { function area(): i32; }
struct Sq { s: i32 }
function (x: Sq) area(): i32 { return x.s * x.s; }
function via(sh: dyn Sh): i32 { return sh.area(); }
function main(): i32 { return 0; }`,
			want: `"area" is an associated function of trait Sh`,
		},
		{
			name: "with-arg",
			src: `trait Sh { function scaled(f: i32): i32; }
struct Sq { s: i32 }
function (x: Sq) scaled(f: i32): i32 { return x.s * f; }
function via(sh: dyn Sh): i32 { return sh.scaled(1); }
function main(): i32 { return 0; }`,
			want: `"scaled" is an associated function of trait Sh`,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := checkSource(t, tc.src)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("want an error containing %q, got %v", tc.want, err)
			}
			if !hasCode(err, "E021") {
				t.Errorf("want E021, got %v", err)
			}
		})
	}
}

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

func TestSliceEscapeRejected(t *testing.T) {
	for _, src := range []string{
		`function f(): [i32] { let xs: i32[] = [1, 2, 3]; return xs[0:2]; }`,
		`function f(): [i32] { return [1, 2, 3][0:2]; }`,
		`function f(): [i32] { let xs: i32[] = [1, 2, 3]; let s = xs[0:2]; return s; }`,
		`function f(): [i32] { let xs: i32[] = [1, 2, 3]; let s = xs[0:3]; return s[0:2]; }`,
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

func TestSliceEscapeAllowed(t *testing.T) {
	for _, src := range []string{
		`function f(xs: i32[]): [i32] { return xs[0:2]; }`,
		`function f(xs: i32[]): [i32] { let s = xs[0:2]; return s; }`,
		`function f(s: string): str { return slice_unchecked(s, 0, 2); }`,
		`function f(s: string): Option[str] { return s[0:2]; }`,
		`function f(s: string): string { return slice_unchecked(s, 0, 2) + ""; }`,
		`function f(): i32[] { let xs: i32[] = [1, 2, 3]; return xs; }`,
		`function (xs: T[]) head(): [T] { return xs[0:2]; }
function main(): i32 { let a: i32[] = [1,2,3]; return a.head()[0]; }`,
	} {
		if err := checkSource(t, src); err != nil {
			t.Errorf("%q: unexpected error %v", src, err)
		}
	}
}

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

func TestStrViewBorrowAllowed(t *testing.T) {
	for _, src := range []string{
		`function f(): void { let s: string = "x"; let v: str = s; }`,
		`function g(v: str): i32 { return v.len(); } function f(): i32 { return g("abc"); }`,
		`function t(o: string): i32 { return o.len(); } function f(v: str): i32 { return t(v); }`,
		`function f(): i32 { let vs: str[] = ["ab"]; return vs[0].len(); }`,
		`function f(v: str): str { return v; }`,
	} {
		if err := checkSource(t, src); err != nil {
			t.Errorf("%q: expected OK, got %v", src, err)
		}
	}
}

func TestStrViewPromoteRejected(t *testing.T) {
	for _, src := range []string{
		`function f(v: str) { let o: string = v; }`,
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

func TestStrEscapeRejected(t *testing.T) {
	for _, src := range []string{
		`function mk(): string { return "a" + "b"; } function f(): str { let s: string = mk(); return s; }`,
		`function mk(): string { return "a" + "b"; } function f(): str { let s: string = mk(); let v: str = s; return v; }`,
		`function mk(): string { return "a" + "b"; } function f(): str { let s: string = mk(); let v: str = s; let w: str = v; return w; }`,
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

func TestStrEscapeAllowed(t *testing.T) {
	for _, src := range []string{
		`function f(v: str): str { return v; }`,
		`function f(p: str): str { let v: str = p; return v; }`,
		`function f(): str { return "s"; }`,
		`function f(): str { let v: str = "s"; return v; }`,
		`function mk(): string { return "a" + "b"; } function f(): str { return mk(); }`,
	} {
		if err := checkSource(t, src); err != nil {
			t.Errorf("%q: expected OK, got %v", src, err)
		}
	}
}

func TestStrSliceProducesOption(t *testing.T) {
	for _, src := range []string{
		`function f(): void { let s: string = "abcd"; let o: Option[str] = s[1:3]; }`,
		`function f(): void { let s: string = "abcd"; let o = s[1:3]; }`,
		`function f(s: string): Option[str] { return s[1:3]; }`,
		`function f(v: str): Option[str] { return v[0:1]; }`,
		`function f(s: string): Option[str] { return s[:3]; }`,
		`function f(s: string): Option[str] { return s[3:]; }`,
		`function f(s: string): i32 { match (s[1:3]) { Some(v) => { return v.len(); }, None => { return 0; } } }`,
		`function f(s: string): Option[str] { let v: str = s[1:3]?; return Some(v); }`,
		`function f(s: string): i32 { return slice_unchecked(s, 1, 3).len(); }`,
		`function f(s: string): void { print(slice_unchecked(s, 1, 3)); }`,
	} {
		if err := checkSource(t, src); err != nil {
			t.Errorf("%q: expected OK, got %v", src, err)
		}
	}
	for _, src := range []string{
		`function f(): void { let s: string = "abcd"; let v: str = s[1:3]; }`,
		`function f(s: string): str { return s[1:3]; }`,
		`function f(s: string): string { return s[1:3]; }`,
		`function f(s: string): i32 { return s[1:3].len(); }`,
		`function f(s: string): boolean { return s[0:2] == "ab"; }`,
		`function f(s: string): void { print(s[1:3]); }`,
	} {
		err := checkSource(t, src)
		if err == nil {
			t.Errorf("%q: expected rejection, got nil", src)
			continue
		}
		if !strings.Contains(err.Error(), "Option[str]") {
			t.Errorf("%q: want the Option[str] type named in the diagnostic, got %v", src, err)
		}
	}
}

func TestStrSliceTypesAsOptionStr(t *testing.T) {
	want := ast.EnumType{Name: "Option", Args: []ast.Type{ast.StrType{}}}
	for name, src := range map[string]string{
		"string source": `function f(s: string): void { let v = s[1:3]; }`,
		"str source":    `function f(s: str): void { let v = s[1:3]; }`,
	} {
		t.Run(name, func(t *testing.T) {
			prog, err := parser.Parse(src)
			if err != nil {
				t.Fatalf("parse: %v", err)
			}
			info, err := Check(prog)
			if err != nil {
				t.Fatalf("check: %v", err)
			}
			found := false
			for v, ty := range info.VarTypes {
				if v.Name != "v" {
					continue
				}
				found = true
				if !reflect.DeepEqual(ty, want) {
					t.Errorf("type of v = %#v, want %#v", ty, want)
				}
			}
			if !found {
				t.Fatalf("no VarTypes entry for the slice binding")
			}
		})
	}
}

func TestStrSliceEscapeRejected(t *testing.T) {
	for _, src := range []string{
		`function mk(): string { return "a" + "b"; } function f(): str { let s: string = mk(); return slice_unchecked(s, 0, 1); }`,
		`function mk(): string { return "a" + "b"; } function f(): str { let s: string = mk(); let v: str = slice_unchecked(s, 0, 1); return v; }`,
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

func TestStrMatchArmEscapeRejected(t *testing.T) {
	for _, src := range []string{
		`function mk(): string { return "a" + "b"; } function f(): str { let s: string = mk(); match (s[0:1]) { Some(v) => { return v; }, None => { return ""; } } }`,
		`function mk(): string { return "a" + "b"; } function f(): str { let s: string = mk(); let t = s[0:1]; match (t) { Some(v) => { return v; }, None => { return ""; } } }`,
		`function mk(): string { return "a" + "b"; } function f(): str { let s: string = mk(); match (s[0:1]) { Some(v) => { let w: str = v; return w; }, None => { return ""; } } }`,
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

func TestStrMatchArmEscapeAllowed(t *testing.T) {
	for _, src := range []string{
		`function f(p: string): str { match (p[0:1]) { Some(v) => { return v; }, None => { return ""; } } }`,
		`function f(): str { match ("hello"[0:1]) { Some(v) => { return v; }, None => { return ""; } } }`,
		`function f(p: string): str { match (p[0:1]) { Some(p) => { return p; }, None => { return ""; } } }`,
	} {
		if err := checkSource(t, src); err != nil {
			t.Errorf("%q: expected acceptance, got %v", src, err)
		}
	}
}

func TestStrCalleeEscapeRejected(t *testing.T) {
	const decls = `function mk(): string { return "a" + "b"; }
function id_view(s: str): str { return s; }
function hop2(s: str): str { return id_view(s); }
function rec(s: str, n: i32): str { if (n <= 0) { return s; } return rec(s, n - 1); }
function (s: string) view(): str { return s; }
`
	for _, src := range []string{
		`function f(): str { let s: string = mk(); return id_view(slice_unchecked(s, 0, 1)); }`,
		`function f(): str { let s: string = mk(); return hop2(slice_unchecked(s, 0, 1)); }`,
		`function f(): str { let s: string = mk(); return rec(slice_unchecked(s, 0, 1), 3); }`,
		`function f(): str { let s: string = mk(); return s.view(); }`,
		`function f(): str { let s: string = mk(); match (s[0:1]) { Some(v) => { return id_view(v); }, None => { return ""; } } }`,
		`function f(): str { let s: string = mk(); let t: str = id_view(slice_unchecked(s, 0, 1)); return t; }`,
	} {
		err := checkSource(t, decls+src)
		if err == nil {
			t.Errorf("%q: expected E065, got nil", src)
			continue
		}
		if !hasCode(err, "E065") {
			t.Errorf("%q: want diagnostic stamped E065, got %v", src, err)
		}
	}
}

func TestStrCalleeEscapeAllowed(t *testing.T) {
	const decls = `function mk(): string { return "a" + "b"; }
function id_view(s: str): str { return s; }
function second(a: str, b: str): str { return b; }
function lit_only(a: str): str { return "x"; }
function (s: string) view(): str { return s; }
`
	for _, src := range []string{
		`function f(): str { let s: string = mk(); return second(slice_unchecked(s, 0, 1), "lit"); }`,
		`function f(): str { let s: string = mk(); return lit_only(slice_unchecked(s, 0, 1)); }`,
		`function f(p: string): str { return id_view(slice_unchecked(p, 0, 1)); }`,
		`function f(p: string): str { return p.view(); }`,
	} {
		if err := checkSource(t, decls+src); err != nil {
			t.Errorf("%q: expected acceptance, got %v", src, err)
		}
	}
}

func TestSliceCalleeEscapeRejected(t *testing.T) {
	const decls = `function id_sl(x: [i32]): [i32] { return x; }
function hop(x: [i32]): [i32] { return id_sl(x); }
function second(a: [i32], b: [i32]): [i32] { return b; }
function idarr(x: i32[]): i32[] { return x; }
function (xs: T[]) win(): [T] { return xs[0:1]; }
`
	for _, src := range []string{
		`function f(): [i32] { let a: i32[] = [1, 2, 3]; return id_sl(a[0:2]); }`,
		`function f(): [i32] { let a: i32[] = [1, 2, 3]; return hop(a[0:2]); }`,
		`function f(): [i32] { let a: i32[] = [1, 2, 3]; return id_sl(a)[0:1]; }`,
		`function f(): [i32] { let a: i32[] = [1, 2, 3]; return a.win(); }`,
		`function f(): [i32] { let a: i32[] = [1, 2, 3]; return second(a[0:2], [9]); }`,
		`function f(): [i32] { let a: i32[] = [1, 2, 3]; return idarr(a)[0:1]; }`,
	} {
		err := checkSource(t, decls+src)
		if err == nil {
			t.Errorf("%q: expected E063, got nil", src)
			continue
		}
		if !hasCode(err, "E063") {
			t.Errorf("%q: want diagnostic stamped E063, got %v", src, err)
		}
	}
}

func TestSliceCalleeEscapeAllowed(t *testing.T) {
	const decls = `function id_sl(x: [i32]): [i32] { return x; }
function second(a: [i32], b: [i32]): [i32] { return b; }
function mkarr(): i32[] { let a: i32[] = [1, 2, 3]; return a; }
function (xs: T[]) win(): [T] { return xs[0:1]; }
`
	for _, src := range []string{
		`function f(p: i32[]): [i32] { return id_sl(p[0:2]); }`,
		`function f(p: i32[]): [i32] { return p.win(); }`,
		`function f(p: i32[]): [i32] { let a: i32[] = [1, 2, 3]; return second(a[0:2], p[0:1]); }`,
		`function f(): i32[] { return mkarr(); }`,
		`function f(): i32[] { let a: i32[] = [1, 2, 3]; return a; }`,
	} {
		if err := checkSource(t, decls+src); err != nil {
			t.Errorf("%q: expected acceptance, got %v", src, err)
		}
	}
}

func TestSlicedTemporaryRejected(t *testing.T) {
	const decls = `function mkarr(): i32[] { let a: i32[] = [1, 2, 3]; return a; }
function fresharr(): i32[] { return [1, 2, 3]; }
function mkstr(): string { return "a" + "b"; }
function (s: string) own_copy(): string { return s + ""; }
`
	for _, src := range []string{
		`function f(): [i32] { return mkarr()[0:1]; }`,
		`function f(): [i32] { return fresharr()[0:1]; }`,
		`function f(): str { return slice_unchecked(mkstr(), 0, 1); }`,
		`function f(): str { let s: string = mkstr(); return slice_unchecked(s.own_copy(), 0, 1); }`,
	} {
		err := checkSource(t, decls+src)
		if err == nil {
			t.Errorf("%q: expected a dangling-view diagnostic, got nil", src)
			continue
		}
		if !hasCode(err, "E063") && !hasCode(err, "E065") {
			t.Errorf("%q: want E063 or E065, got %v", src, err)
		}
	}
}

func TestSlicedTemporaryAllowed(t *testing.T) {
	const decls = `function idarr(x: i32[]): i32[] { return x; }
function idstr(s: string): string { return s; }
function lit(): string { return "hello"; }
`
	for _, src := range []string{
		`function f(p: i32[]): [i32] { return idarr(p)[0:1]; }`,
		`function f(p: string): str { return slice_unchecked(idstr(p), 0, 1); }`,
		`function f(p: string): str { return slice_unchecked(p, 0, 1); }`,
		`function f(): str { return slice_unchecked(lit(), 0, 1); }`,
	} {
		if err := checkSource(t, decls+src); err != nil {
			t.Errorf("%q: expected acceptance, got %v", src, err)
		}
	}
}

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
	if n := strings.Count(err.Error(), "expected string, got str"); n != 1 {
		t.Errorf("want exactly 1 str-arg mismatch (own position only), got %d in %v", n, err)
	}
}

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
	mustCode(`function f(k: i32): string { return k as string; } function main(): i32 { return 0; }`, "E069")
	mustCode(`function f(k: i32): i32[] { return k as i32[]; } function main(): i32 { return 0; }`, "E069")
	mustCode(`struct P { x: i32 } function f(k: i32): P { return k as P; } function main(): i32 { return 0; }`, "E069")
	mustCode(`function f(k: u32): string { return k as string; } function main(): i32 { return 0; }`, "E069")
	mustOK(`function f(k: usize): string { return k as string; } function main(): i32 { return 0; }`)
	mustOK(`function f(k: usize): i32[] { return k as i32[]; } function main(): i32 { return 0; }`)
	mustOK(`function main(): i32 { let a: i32 = 5; let b: usize = a as usize; return b as i32; }`)
	mustOK(`function main(): i32 { let s: string = "x"; return s as i32; }`)
}

func TestFloatDefaultWidth(t *testing.T) {
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
	mustErr(`function main(): i32 { let x: f64 = 1.5; let y: f32 = x; return 0; }`,
		"expected f32, got f64")
	mustErr(`function main(): i32 { let s: string = 1.5; return 0; }`,
		"expected string, got f64")
	mustErr(`function f(a: double): i32 { return 0; } function main(): i32 { return 0; }`,
		"did you mean `f64`?")
}

func TestRetiredNameSuggestsReplacement(t *testing.T) {
	prog, err := parser.Parse(`function f(): string {
		return string_from_bytes([65 as u8]);
	}`)
	if err != nil {
		t.Fatal(err)
	}
	_, err = Check(prog)
	if err == nil {
		t.Fatal("expected error: string_from_bytes is retired")
	}
	es, ok := err.(diag.Errors)
	if !ok || len(es) == 0 {
		t.Fatalf("expected diag.Errors, got %T", err)
	}
	ce, ok := es[0].(*Error)
	if !ok {
		t.Fatalf("expected *Error, got %T", es[0])
	}
	if ce.ErrCode != "E001" {
		t.Errorf("code = %q, want E001", ce.ErrCode)
	}
	if ce.Fix == nil {
		t.Fatal("expected a machine-applicable fix for a retired name")
	}
	if ce.Fix.Replacement != "string_from_bytes_unchecked" {
		t.Errorf("fix replacement = %q, want %q", ce.Fix.Replacement, "string_from_bytes_unchecked")
	}
	if !strings.Contains(ce.Fix.Title, "std/utf8.from_bytes") {
		t.Errorf("fix title = %q, want it to mention std/utf8.from_bytes", ce.Fix.Title)
	}
	if ce.Fix.Length != len("string_from_bytes") || ce.Fix.Pos != ce.Pos {
		t.Errorf("fix span = (%v, %d), want the error's own (%v, %d)",
			ce.Fix.Pos, ce.Fix.Length, ce.Pos, len("string_from_bytes"))
	}
}

func TestSliceUncheckedMisuseRejected(t *testing.T) {
	bad := []struct{ src, want string }{
		{`function f(s: string): str { return slice_unchecked(s, 0); }`,
			"takes 3 arguments but was given 2"},
		{`function f(s: string): str { return slice_unchecked(s, 0, 1, 2); }`,
			"takes 3 arguments but was given 4"},
		{`function f(): str { return slice_unchecked(7, 0, 1); }`,
			"argument 1 to \"slice_unchecked\": expected string"},
		{`function f(s: string): str { return slice_unchecked(s, "a", 1); }`,
			"argument 2 to \"slice_unchecked\": expected i32"},
		{`function f(s: string): str { return slice_unchecked(s, 0, "b"); }`,
			"argument 3 to \"slice_unchecked\": expected i32"},
	}
	for _, c := range bad {
		err := checkSource(t, c.src)
		if err == nil {
			t.Errorf("expected error for %s, got none", c.src)
			continue
		}
		if !strings.Contains(err.Error(), c.want) {
			t.Errorf("error %q does not contain %q\nsrc: %s", err.Error(), c.want, c.src)
		}
	}

	if err := checkSource(t, `function f(s: string): str { return slice_unchecked(s, 0, 1); }
function main(): i32 { let s: string = "hey"; return f(s).len(); }`); err != nil {
		t.Errorf("well-typed slice_unchecked call rejected: %v", err)
	}
}

func TestBareNullaryVariantUnifiesWithGenericEnumInstantiation(t *testing.T) {
	const decls = `enum Tree[K, V] { Leaf, Node(Tree[K, V], K, V, Tree[K, V]) }
struct TMap[K, V] { root: Tree[K, V], size: i32 }
`
	for _, tc := range []struct{ name, body string }{
		{"variant payload", `function one[K, V](k: K, v: V): Tree[K, V] { return Node(Leaf, k, v, Leaf); }
function main(): i32 { let t: Tree[i32, i32] = one(1, 2); return 0; }`},
		{"struct field", `function empty[K, V](): TMap[K, V] { return TMap { root: Leaf, size: 0 }; }
function main(): i32 { let m: TMap[i32, string] = empty(); return m.size; }`},
		{"concrete payload", `function main(): i32 { let t: Tree[i32, i32] = Node(Leaf, 1, 2, Leaf); return 0; }`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := checkSource(t, decls+tc.body); err != nil {
				t.Errorf("unexpected error: %v", err)
			}
		})
	}
	t.Run("other enum rejected", func(t *testing.T) {
		err := checkSource(t, decls+`enum Other { Nil }
function main(): i32 { let t: Tree[i32, i32] = Node(Nil, 1, 2, Leaf); return 0; }`)
		if err == nil || !strings.Contains(err.Error(), "payload 0 type Other, expected Tree[K, V]") {
			t.Fatalf("expected the payload mismatch, got %v", err)
		}
	})
}

func TestArglessBindingIsUpgradedByALaterArgument(t *testing.T) {
	const src = `import "core/map";
function apply[T](x: T, f: (T) => T): T { return f(x); }
function bump(m: Map[string, i32]): Map[string, i32] { return m; }
function main(): i32 {
    let r: Map[string, i32] = apply(map_new(8), bump);
    return 0;
}`
	if err := checkSource(t, src); err != nil {
		t.Errorf("argument 2 pins what argument 1 left argless:\n%v", err)
	}

	t.Run("mismatched instantiations still rejected", func(t *testing.T) {
		err := checkSource(t, `import "core/map";
function apply[T](x: T, f: (T) => T): T { return f(x); }
function bump(m: Map[string, i32]): Map[string, i32] { return m; }
function main(): i32 {
    let a: Map[i32, i32] = map_new(8);
    let r: Map[string, i32] = apply(a, bump);
    return 0;
}`)
		if err == nil || !strings.Contains(err.Error(), "expected (Map[i32, i32]) => Map[i32, i32]") {
			t.Fatalf("Map[i32, i32] and Map[string, i32] must still conflict, got %v", err)
		}
	})
}

func TestEmptyArrayArgumentLeavesTypeParameterOpen(t *testing.T) {
	for _, tc := range []struct{ name, src, wantErr string }{
		{"later argument pins it", `function fold[T](x: i32, own acc: T, f: (i32, own T) => T): T { return f(x, acc); }
function add(x: i32, own acc: string[]): string[] { return acc.append("a"); }
function main(): i32 {
    let r: string[] = fold(1, [], add);
    return r.len();
}`, ""},
		{"destination pins it", `function id[T](own x: T): T { return x; }
function main(): i32 {
    let r: i32[] = id([]);
    return r.len();
}`, ""},
		{"nothing pins it", `function first[T](own x: T, n: i32): i32 { return n; }
function main(): i32 { return first([], 3); }`, "could not infer type parameter T for first"},
		{"pinned to a scalar", `function pair[T](own a: T, own b: T): T { return b; }
function main(): i32 { return pair([], 3); }`, "argument 1 to \"pair\": expected i32, got []"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := checkSource(t, tc.src)
			if tc.wantErr == "" {
				if err != nil {
					t.Fatalf("want accepted, got %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("want %q, got %v", tc.wantErr, err)
			}
		})
	}
}

func TestCastDoesNotRetargetPinnedGenericCall(t *testing.T) {
	const decls = `struct Box[T] { x: T }
function get_or2[T](b: Box[T], i: i32, fallback: T): T { if (i == 0) { return b.x; } return fallback; }
pub function (b: Box[T]) get_or(i: i32, fallback: T): T { if (i == 0) { return b.x; } return fallback; }
function id[T](x: T): T { return x; }
`
	for _, tc := range []struct{ name, body string }{
		{"free function, literal fallback", `function main(): i32 { let b: Box[i32] = Box { x: 41 }; let t: i64 = get_or2(b, 0, 0) as i64; return t as i32; }`},
		{"method, typed literal fallback", `function main(): i32 { let b: Box[i32] = Box { x: 41 }; let t: i64 = b.get_or(0, 7i32) as i64; return t as i32; }`},
		{"let destination", `function main(): i32 { let b: Box[i32] = Box { x: 41 }; let t: i64 = (b.get_or(0, 0) as i64) + 1i64; return t as i32; }`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			prog, err := parser.Parse(decls + tc.body)
			if err != nil {
				t.Fatalf("parse: %v", err)
			}
			info, err := Check(prog)
			if err != nil {
				t.Fatalf("check: %v", err)
			}
			_ = info
			ast.WalkProgram(prog, func(n ast.Node) bool {
				c, ok := n.(*ast.Call)
				if !ok || len(c.TypeArgs) != 1 {
					return true
				}
				if id, ok := c.Callee.(*ast.Ident); ok && (id.Name == "get_or2" || strings.HasSuffix(id.Name, "get_or")) {
					if nt, ok := c.TypeArgs[0].(ast.NumberType); !ok || nt.NormalWidth() != 32 {
						t.Errorf("%s instantiated at %v, want i32", id.Name, c.TypeArgs[0])
					}
				}
				return true
			})
		})
	}
	t.Run("literal-only call still settles from the cast", func(t *testing.T) {
		prog, err := parser.Parse(decls + `function main(): i32 { let t: i64 = id(7) as i64; return t as i32; }`)
		if err != nil {
			t.Fatalf("parse: %v", err)
		}
		if _, err := Check(prog); err != nil {
			t.Fatalf("check: %v", err)
		}
		ast.WalkProgram(prog, func(n ast.Node) bool {
			if c, ok := n.(*ast.Call); ok {
				if id, ok := c.Callee.(*ast.Ident); ok && id.Name == "id" && len(c.TypeArgs) == 1 {
					if nt, ok := c.TypeArgs[0].(ast.NumberType); !ok || nt.NormalWidth() != 64 {
						t.Errorf("id instantiated at %v, want i64", c.TypeArgs[0])
					}
				}
			}
			return true
		})
	})
}

func TestGenericArglessEnumArgInMethodReceiver(t *testing.T) {
	const prelude = `struct Box { n: i32 }
function (b: Box) get(): i32 { return b.n; }
function id[T](x: T): T { return x; }
function g(p: Result[i32, i32]): Box { return Box { n: 1 }; }
`
	positions := []struct {
		name string
		body string
	}{
		{"method receiver", `function main(): i32 { return (g(id(Ok(8i32)))).get(); }`},
		{"let with annotation", `function main(): i32 { let x: Box = g(id(Ok(8i32))); return 0; }`},
		{"let without annotation", `function main(): i32 { let x = g(id(Ok(8i32))); return 0; }`},
		{"bare statement", `function main(): i32 { g(id(Ok(8i32))); return 0; }`},
		{"field access", `function main(): i32 { return (g(id(Ok(8i32)))).n; }`},
		{"call argument", `function h(v: Box): i32 { return 0; }
function main(): i32 { return h(g(id(Ok(8i32)))); }`},
	}
	for _, p := range positions {
		t.Run(p.name, func(t *testing.T) {
			if err := checkSource(t, prelude+p.body); err != nil {
				t.Errorf("%s: unexpected error: %v", p.name, err)
			}
		})
	}

	t.Run("genuine conflict still rejected", func(t *testing.T) {
		src := `function pair[T](a: T, b: T): i32 { return 0; }
function main(): i32 {
  let x: Result[i32, i32] = Ok(1i32);
  let y: Result[string, i32] = Ok("s");
  return pair(x, y);
}`
		if err := checkSource(t, src); err == nil {
			t.Errorf("expected a conflict between Result[i32,i32] and Result[string,i32], got nil")
		}
	})
}

func TestCastOnUntypedOperandDoesNotCascade(t *testing.T) {
	for _, tc := range []struct{ name, src, wantCode string }{
		{"cannot find",
			`function main(): i32 { return undefined_thing as i32; }`, "E001"},
		{"unknown call",
			`function main(): i32 { return nosuchfn() as i32; }`, "E001"},
		{"unknown struct field",
			`struct S { a: i32 }
			 function main(): i32 { let s: S = S { a: 1 }; return s.nofield as i32; }`, "E043"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := checkSource(t, tc.src)
			if err == nil {
				t.Fatalf("expected %s, got no error", tc.wantCode)
			}
			if !hasCode(err, tc.wantCode) {
				t.Errorf("want %s, got: %v", tc.wantCode, err)
			}
			if hasCode(err, "E033") {
				t.Errorf("E033 cascaded off an already-errored operand: %v", err)
			}
			if strings.Contains(err.Error(), "%!s(") {
				t.Errorf("a nil type reached a %%s verb: %v", err)
			}
		})
	}
}

func TestCastRejectionStillReported(t *testing.T) {
	err := checkSource(t, `function main(): i32 { let s: string = "x"; return s as boolean; }`)
	if err == nil {
		t.Fatalf("expected E033, got no error")
	}
	if !hasCode(err, "E033") {
		t.Errorf("want E033, got: %v", err)
	}
}

func TestOneSidedNumericCastsAreRejectedBeforeIR(t *testing.T) {
	for _, src := range []string{
		`function main(): i32 { let n: i64 = 1; let s = n as string; return 0; }`,
		`function main(): i32 { let n: i64 = 1; let a = n as i32[]; return 0; }`,
		`function main(): i32 { let f: f64 = 1.5; let s = f as string; return 0; }`,
		`struct S { v: i32 } function main(): i32 { let n: i64 = 1; let x = n as S; return 0; }`,
	} {
		err := checkSource(t, src)
		if err == nil {
			t.Errorf("expected E033 for %s, got no error", src)
			continue
		}
		if !hasCode(err, "E033") {
			t.Errorf("want E033 for %s, got: %v", src, err)
		}
	}
}

func TestUnionMemberTypeArgArity(t *testing.T) {
	for _, tc := range []struct{ name, src, want string }{
		{
			"generic member given no arguments",
			`struct Leaf[T] { v: T }
struct Lit { v: i32 }
type Tree = Leaf | Lit;
function main(): i32 { return 0; }`,
			"takes 1 type argument(s), got 0",
		},
		{
			"plain member given arguments",
			`struct A { v: i32 }
struct B { v: i32 }
type U = A[i32] | B;
function main(): i32 { return 0; }`,
			"is not generic but was given 1 type argument(s)",
		},
		{
			"wrong number of arguments",
			`struct P[A, B] { a: A, b: B }
struct Lit { v: i32 }
type U = P[i32] | Lit;
function main(): i32 { return 0; }`,
			"takes 2 type argument(s), got 1",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := checkSource(t, tc.src)
			if err == nil {
				t.Fatalf("expected E016, got no error")
			}
			if !hasCode(err, "E016") {
				t.Errorf("want E016, got: %v", err)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("message %q does not mention %q", err.Error(), tc.want)
			}
		})
	}
}

func TestUnionArityHintMatchesMemberArity(t *testing.T) {
	err := checkSource(t, `struct P[A, B] { a: A, b: B }
struct Lit { v: i32 }
type U = P[i32] | Lit;
function main(): i32 { return 0; }`)
	if err == nil {
		t.Fatal("expected E016")
	}
	if want := "`type U[A, B] = P[A, B] | …`"; !strings.Contains(err.Error(), want) {
		t.Errorf("message %q does not carry the two-parameter hint %s", err.Error(), want)
	}
}

func TestUnionRejectsTwoInstantiationsOfOneStruct(t *testing.T) {
	err := checkSource(t, `struct Box[T] { v: T }
struct Lit { v: i32 }
type X = Box[i32] | Box[boolean] | Lit;
function main(): i32 { return 0; }`)
	if err == nil {
		t.Fatal("expected E016")
	}
	if !hasCode(err, "E016") || !strings.Contains(err.Error(), "duplicate member") {
		t.Errorf("want a duplicate-member E016, got: %v", err)
	}
}

func TestByteDisplayGateAndDispatchAgree(t *testing.T) {
	const displayTrait = `trait Display { function to_string(self: Self): string; }
`
	// The primitive byte formatter remains available beside another receiver's
	// explicit method; that method must not replace the byte's default.
	if err := checkSource(t, displayTrait+
		`impl Display for u32 { function to_string(self: Self): string { return "u32"; } }
function main(): i32 { let s: string = "A"; let b: u8 = s[0]; print(b); return 0; }`); err != nil {
		t.Fatalf("primitive byte display beside a u32 impl: %v", err)
	}

	if err := checkSource(t, displayTrait+
		`impl Display for u8 { function to_string(self: Self): string { return "b"; } }
function main(): i32 { let s: string = "A"; let b: u8 = s[0]; print(b); return 0; }`); err != nil {
		t.Errorf("print(u8) with `impl Display for u8` in scope: %v", err)
	}
}

func TestUsizeHasItsOwnImplSurface(t *testing.T) {
	const tr = `trait Same { function same(self: Self, o: Self): boolean; }
function need[T: Same](a: T): boolean { return a.same(a); }
`
	err := checkSource(t, tr+`impl Same for u32 { function same(self: Self, o: Self): boolean { return true; } }
function main(): i32 { let u: usize = 3; if (need(u)) { return 1; } return 0; }`)
	if err == nil || !strings.Contains(err.Error(), "type argument T = usize does not implement trait Same required by need") {
		t.Errorf("a u32 impl met a bound at usize: %v", err)
	}
	if err := checkSource(t, tr+`impl Same for u32 { function same(self: Self, o: Self): boolean { return true; } }
impl Same for usize { function same(self: Self, o: Self): boolean { return self == o; } }
function main(): i32 { let u: usize = 3; let w: u32 = 4; if (need(u) && need(w) && u.same(u)) { return 1; } return 0; }`); err != nil {
		t.Errorf("separate impls for usize and u32: %v", err)
	}
}

func TestCheckValueBindingShadowsGenericFunc(t *testing.T) {
	const genericID = `function id[T](x: T): T { return x; }
function inc(x: i32): i32 { return x + 1; }
`
	cases := []struct {
		name string
		src  string
	}{
		{"fn-param", `function apply(v: i32, id: (i32) => i32): i32 { return id(v); }
function main(): i32 { return apply(7, inc); }`},
		{"local-var", `function main(): i32 { let id: (i32) => i32 = inc; return id(7); }`},
		{"capture", `function main(): i32 {
	let id: (i32) => i32 = inc;
	function call_it(v: i32): i32 { return id(v); }
	return call_it(7);
}`},
		{"shadow-and-generic", `function apply(v: i32, id: (i32) => i32): i32 { return id(v); }
function plain(v: i32): i32 { return id(v); }
function main(): i32 { return apply(7, inc) + plain(1); }`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if err := checkSource(t, genericID+tc.src); err != nil {
				t.Errorf("a value binding must shadow the generic %q: %v", "id", err)
			}
		})
	}

	const plainID = `function id(x: i32): i32 { return x; }
function inc(x: i32): i32 { return x + 1; }
`
	if err := checkSource(t, plainID+`function apply(v: i32, id: (i32) => i32): i32 { return id(v); }
function main(): i32 { return apply(7, inc); }`); err != nil {
		t.Errorf("non-generic control regressed: %v", err)
	}
}

func TestCheckValueBindingShadowsUseClauseCallee(t *testing.T) {
	const mods = `function withRes(cb: (i32) => i32): i32 { return cb(4); }
function taker(f: (string) => i32): i32 { return f("hi"); }
`
	if err := checkSource(t, mods+`function g(): i32 {
	let withRes = taker;
	use x <- withRes();
	if (x == "hi") { return 21; }
	return 0;
}
function main(): i32 { return g(); }`); err != nil {
		t.Errorf("a `use` source call must resolve to the shadowing binding: %v", err)
	}

	if err := checkSource(t, mods+`function run(withRes: ((string) => i32) => i32): i32 {
	use x <- withRes();
	if (x == "hi") { return 21; }
	return 0;
}
function main(): i32 { return run(taker); }`); err != nil {
		t.Errorf("a fn-typed param must shadow the `use` source callee: %v", err)
	}

	if err := checkSource(t, mods+`function g(): i32 { use x <- withRes(); return x + 1; }
function main(): i32 { return g(); }`); err != nil {
		t.Errorf("unshadowed `use` regressed: %v", err)
	}
}

func TestScalarModuleHintIsFollowable(t *testing.T) {
	cases := []struct {
		name    string
		decl    string
		wantMod string
	}{
		{"i32 radix points at std/i32", `let x: i32 = 5; print(x.to_string_radix(16));`, `std/i32`},
		{"i64 radix points at std/i64", `let x: i64 = 5; print(x.to_string_radix(16));`, `std/i64`},
		{"f32 precision points at std/f64", `let x: f32 = 1.5; print(x.to_string_prec(2));`, `std/f64`},
		{"f64 precision points at std/f64", `let x: f64 = 1.5; print(x.to_string_prec(2));`, `std/f64`},
		// A `str` view had NO hint at all: scalarModuleFor had no StrType case,
		// so the message fell through to the builtin list ("it has: as_bytes,
		// len") — the outcome that function's own comment forbids, and the one
		// a reader reaches by FOLLOWING E043 (TestStrToOwnedAdviceIsFollowable).
		{"str points at std/string", `let s: string = "abcdef"; let v: str = s[0:3]; print(v.to_owned());`, `std/string`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := checkSource(t, "function main(): i32 { "+tc.decl+" return 0; }")
			if err == nil {
				t.Fatalf("expected an unresolved-method error")
			}
			want := "add `import \"" + tc.wantMod + "\"`"
			if !strings.Contains(err.Error(), want) {
				t.Fatalf("wanted the hint to say %q, got:\n%s", want, err.Error())
			}
		})
	}
}

func TestOneErrorPerMistake(t *testing.T) {
	count := func(src, needle string) int {
		t.Helper()
		err := checkSource(t, src)
		if err == nil {
			return 0
		}
		return strings.Count(err.Error(), needle)
	}
	cases := []struct {
		name, src string
		want      int
	}{
		{"boolean comparison", `function main(): i32 {
    let a: boolean = true; let b: boolean = false;
    if (a < b) { return 1; }
    return 0;
}`, 1},
		{"integer plus string", `function main(): i32 {
    let a: i32 = 1; let s: string = "x";
    return a + s;
}`, 1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := count(tc.src, `operator "`); got != tc.want {
				t.Fatalf("wanted %d operator diagnostics for %s, got %d:\n%v",
					tc.want, tc.name, got, checkSource(t, tc.src))
			}
		})
	}
}

func TestMixedIntegerHintSurvives(t *testing.T) {
	err := checkSource(t, `function main(): i32 {
    let a: i32 = 1; let b: u64 = 2;
    if (a < b) { return 1; }
    return 0;
}`)
	if err == nil {
		t.Fatalf("expected E009 for i32 vs u32")
	}
	if !strings.Contains(err.Error(), "use `as` for explicit conversion") {
		t.Fatalf("the cast hint should survive for two integers, got:\n%v", err)
	}
}

func TestUnknownVariantHintDistinguishesTypoFromCatchAll(t *testing.T) {
	const decl = `enum Ord { Before, After, Equal, Concurrent }
`
	cases := []struct {
		name, arm, want, notWant string
	}{
		{"typo suggests the variant", "Befre", `did you mean "Before"?`, "bare name in arm position"},
		{"typo on a longer name", "Concurent", `did you mean "Concurrent"?`, "bare name in arm position"},
		{"catch-all is explained", "other", "for a catch-all use `_`", "did you mean"},
		{"unrelated name is explained", "zzzqqq", "a VARIANT, not a binding", "did you mean"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			src := decl + "function f(o: Ord): i32 { match (o) { " + tc.arm +
				" => { return 1; }, _ => { return 0; }, } }\nfunction main(): i32 { return f(Equal); }"
			err := checkSource(t, src)
			if err == nil {
				t.Fatalf("expected E014 for arm %q", tc.arm)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("arm %q: wanted %q in:\n%v", tc.arm, tc.want, err)
			}
			if strings.Contains(err.Error(), tc.notWant) {
				t.Fatalf("arm %q: did not want %q in:\n%v", tc.arm, tc.notWant, err)
			}
		})
	}
}

func TestVariantQualifierSameInStatementAndExpression(t *testing.T) {
	const enums = "enum Color { Red, Green }\nenum Status { Ok, Err }\n"

	mustOK := func(name, src string) {
		t.Helper()
		if err := checkSource(t, enums+src); err != nil {
			t.Errorf("%s: want accepted, got: %v", name, err)
		}
	}
	mustE029 := func(name, src, want string) {
		t.Helper()
		err := checkSource(t, enums+src)
		if err == nil {
			t.Fatalf("%s: want E029, got no error", name)
		}
		if !strings.Contains(err.Error(), want) {
			t.Errorf("%s: want an E029 reading %q, got: %v", name, want, err)
		}
		if strings.Contains(err.Error(), "names module") {
			t.Errorf("%s: reported the module-qualifier form, want the enum-mismatch one: %v", name, err)
		}
		if strings.Contains(err.Error(), "is not part of enum") {
			t.Errorf("%s: reported E014 beside the E029: %v", name, err)
		}
	}

	mustOK("stmt-enum-qualifier",
		"function f(c: Color): i32 { match (c) { Color.Red => { return 1; }, Color.Green => { return 2; } } return 0; }")
	mustOK("expr-enum-qualifier",
		"function f(c: Color): i32 { return match (c) { Color.Red => 1i32, Color.Green => 2i32 }; }")

	mustE029("stmt-wrong-enum",
		"function f(c: Color): i32 { match (c) { Status.Ok => { return 1; }, _ => { return 0; } } return 0; }",
		`qualifier "Status" does not match scrutinee enum Color`)
	mustE029("expr-wrong-enum",
		"function f(c: Color): i32 { return match (c) { Status.Ok => 1i32, _ => 0i32 }; }",
		`qualifier "Status" does not match scrutinee enum Color`)
}

func TestPayloadlessVariantNameAsPayloadBinderIsRejected(t *testing.T) {
	const decls = `enum Res { Ok2(i32), Err2 }
enum Nest { Wrap(Res), Bare }
enum Two { Pair(Res, i32), Nil }
`
	bad := map[string]string{
		"statement match": `function f(w: Nest): i32 {
  match (w) { Wrap(Err2) => { return 9; }, _ => { return 1; } }
  return 0;
}`,
		"expression match": `function f(w: Nest): i32 { return match (w) { Wrap(Err2) => 9i32, _ => 1i32 }; }`,
		"if let": `function f(w: Nest): i32 {
  if let Wrap(Err2) = w { return 9; }
  return 1;
}`,
		"second payload slot": `function f(t: Two): i32 {
  match (t) { Pair(Err2, k) => { return k; }, _ => { return 1; } }
  return 0;
}`,
		"merged sibling of a nested arm": `function f(w: Nest): i32 {
  match (w) {
    Wrap(Ok2(n)) => { return n; },
    Wrap(Err2) => { return 9; },
    _ => { return 1; },
  }
  return 0;
}`,
		"merged sibling, expression form": `function f(w: Nest): i32 {
  return match (w) { Wrap(Ok2(n)) => n, Wrap(Err2) => 9i32, _ => 1i32 };
}`,
	}
	for name, body := range bad {
		t.Run(name, func(t *testing.T) {
			err := checkSource(t, decls+body)
			if err == nil {
				t.Fatalf("want E015 for a payload-less variant name in a payload slot, got none")
			}
			if code := firstErrCode(err); code != "E015" {
				t.Errorf("code = %q, want E015: %v", code, err)
			}
			for _, want := range []string{"payload-less variant of enum Res", "write `Err2()`"} {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("want %q in:\n%v", want, err)
				}
			}
		})
	}

	good := map[string]string{
		"ordinary binder": `function f(w: Nest): i32 {
  match (w) { Wrap(x) => { return match (x) { Ok2(n) => n, Err2 => 0i32 }; }, Bare => { return 2; } }
  return 0;
}`,
		"wildcard slot": `function f(w: Nest): i32 {
  match (w) { Wrap(_) => { return 1; }, Bare => { return 2; } }
  return 0;
}`,
		"the nested spelling": `function f(w: Nest): i32 {
  match (w) { Wrap(Err2()) => { return 9; }, _ => { return 1; } }
  return 0;
}`,
		"two ordinary binders": `function f(t: Two): i32 {
  match (t) { Pair(r, k) => { return match (r) { Ok2(n) => n + k, Err2 => k }; }, Nil => { return 0; } }
  return 0;
}`,
		"variant of another enum": `function f(w: Nest): i32 {
  match (w) { Wrap(Bare) => { return 1; }, Bare => { return 2; } }
  return 0;
}`,
	}
	for name, body := range good {
		t.Run(name, func(t *testing.T) {
			if err := checkSource(t, decls+body); err != nil {
				t.Errorf("want accepted, got: %v", err)
			}
		})
	}
}

func firstErrCode(err error) string {
	var errs diag.Errors
	if errors.As(err, &errs) {
		for _, e := range errs {
			var ce *Error
			if errors.As(e, &ce) {
				return ce.ErrCode
			}
		}
		return ""
	}
	var ce *Error
	if errors.As(err, &ce) {
		return ce.ErrCode
	}
	return ""
}

func TestRejectedMethodReportsOneError(t *testing.T) {
	for name, src := range map[string]string{
		"same trait twice": `trait A { function add(self: Self, n: i32): i32; }
impl A for i32 { function add(self: Self, n: i32): i32 { return self - n; } }
impl A for i32 { function add(self: Self, n: i32): i32 { return self + n; } }
function main(): i32 { return 0; }`,
		"inherent then trait": `function (x: i32) add(n: i32): i32 { return x - n; }
trait A { function add(self: Self, n: i32): i32; }
impl A for i32 { function add(self: Self, n: i32): i32 { return self + n; } }
function main(): i32 { return 0; }`,
	} {
		err := checkSource(t, src)
		if err == nil {
			t.Fatalf("%s: expected a diagnostic, got nil", name)
		}
		got := err.Error()
		if strings.Contains(got, "E001") || strings.Contains(got, `cannot find "self" in this scope`) {
			t.Errorf("%s: the rejected declaration's body leaked a second diagnostic: %q", name, got)
		}
	}
}

func TestPayloadlessVariantNameAsTupleElemIsRejected(t *testing.T) {
	const decls = "enum E { A(i32), B }\n"
	bad := map[string]string{
		"only arm, statement match": `function f(t: (E, i32)): i32 {
  match (t) { (B, y) => { return 100 + y; } }
  return 0;
}`,
		"only arm, expression match": `function f(t: (E, i32)): i32 { return match (t) { (B, y) => 100i32 }; }`,
		"second element": `function f(t: (i32, E)): i32 {
  match (t) { (y, B) => { return y; } }
  return 0;
}`,
		"beside a literal element": `function f(t: (E, i32)): i32 {
  match (t) { (B, 1) => { return 5; }, _ => { return 0; } }
  return 0;
}`,
	}
	for name, body := range bad {
		t.Run(name, func(t *testing.T) {
			err := checkSource(t, decls+body)
			if err == nil {
				t.Fatalf("want E015 for a payload-less variant name in a tuple element, got none")
			}
			if code := firstErrCode(err); code != "E015" {
				t.Errorf("code = %q, want E015: %v", code, err)
			}
			for _, want := range []string{"payload-less variant of enum E", "matches every value", "write `B()`"} {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("want %q in:\n%v", want, err)
				}
			}
		})
	}

	good := map[string]string{
		"ordinary binder": `function f(t: (E, i32)): i32 {
  match (t) { (e, y) => { return match (e) { A(n) => n + y, B => y }; } }
  return 0;
}`,
		"wildcard element": `function f(t: (E, i32)): i32 {
  match (t) { (_, y) => { return y; } }
  return 0;
}`,
		"variant with a payload": `function f(t: (E, i32)): i32 {
  match (t) { (A, y) => { return y; } }
  return 0;
}`,
		"name collides on a non-enum element": `function f(t: (i32, i32)): i32 {
  match (t) { (B, y) => { return B + y; } }
  return 0;
}`,
	}
	for name, body := range good {
		t.Run(name, func(t *testing.T) {
			if err := checkSource(t, decls+body); err != nil {
				t.Errorf("want accepted, got: %v", err)
			}
		})
	}
}

func TestNestedTuplePatternChecks(t *testing.T) {
	const decls = "enum E { A(i32), B }\n"
	bad := map[string]struct{ src, code string }{
		"element is not a tuple": {`function f(t: (i32, i32)): i32 {
  match (t) { (a, (b, c)) => { return a; } }
  return 0;
}`, "E035"},
		"nested arity mismatch": {`function f(t: (i32, (i32, i32))): i32 {
  match (t) { (a, (b, c, d)) => { return a; } }
  return 0;
}`, "E035"},
		"nested literal type mismatch": {`function f(t: (i32, (i32, i32))): i32 {
  match (t) { (a, ("x", c)) => { return a; }, _ => { return 0; } }
  return 0;
}`, "E035"},
		"nested binder collides with an outer one": {`function f(t: (i32, (i32, i32))): i32 {
  match (t) { (a, (a, c)) => { return c; } }
  return 0;
}`, "E013"},
		"nested literal needs a wildcard arm": {`function f(t: (i32, (i32, i32))): i32 {
  match (t) { (a, (1, c)) => { return a + c; } }
  return 0;
}`, "E030"},
		"nested payload-less variant name is a binder": {`function f(t: (i32, (E, i32))): i32 {
  match (t) { (a, (B, c)) => { return a + c; } }
  return 0;
}`, "E015"},
	}
	for name, c := range bad {
		t.Run(name, func(t *testing.T) {
			err := checkSource(t, decls+c.src)
			if err == nil {
				t.Fatalf("want %s, got none", c.code)
			}
			if code := firstErrCode(err); code != c.code {
				t.Errorf("code = %q, want %s: %v", code, c.code, err)
			}
		})
	}

	ok := map[string]string{
		"nested binders are irrefutable": `function f(t: (i32, (i32, i32))): i32 {
  match (t) { (a, (b, c)) => { return a + b + c; } }
  return 0;
}`,
		"nesting to depth three": `function f(t: (i32, (i32, (i32, string)))): i32 {
  match (t) { (a, (b, (c, s))) => { return a + b + c + s.len(); } }
  return 0;
}`,
		"nested wildcards and literals with a fallback": `function f(t: ((i32, string), (i32, i32))): i32 {
  match (t) { ((1, "a"), (_, c)) => { return c; }, _ => { return 0; } }
  return 0;
}`,
		"guard sees the nested binds": `function f(t: (i32, (i32, i32))): i32 {
  match (t) { (a, (b, c)) when b > c => { return a; }, _ => { return 0; } }
  return 0;
}`,
		"expression form nests too": `function f(t: (i32, (i32, i32))): i32 {
  let v = match (t) { (a, (b, c)) => a + b + c };
  return v;
}`,
	}
	for name, body := range ok {
		t.Run(name, func(t *testing.T) {
			if err := checkSource(t, decls+body); err != nil {
				t.Errorf("want accepted, got: %v", err)
			}
		})
	}
}

func TestNestedTupleDestructureChecks(t *testing.T) {
	bad := map[string]struct{ src, code string }{
		"inner position is not a tuple": {`function f(t: (i32, i32)): i32 {
  let (a, (b, c)) = t;
  return a;
}`, "E024"},
		"inner arity mismatch": {`function f(t: (i32, (i32, i32))): i32 {
  let (a, (b, c, d)) = t;
  return a;
}`, "E024"},
		"outer arity mismatch": {`function f(t: (i32, (i32, i32))): i32 {
  let (a, (b, c), d) = t;
  return a;
}`, "E024"},
		"a binder collides across levels": {`function f(t: (i32, (i32, i32))): i32 {
  let (a, (a, c)) = t;
  return c;
}`, "E013"},
	}
	for name, c := range bad {
		t.Run(name, func(t *testing.T) {
			err := checkSource(t, c.src)
			if err == nil {
				t.Fatalf("want %s, got none", c.code)
			}
			if code := firstErrCode(err); code != c.code {
				t.Errorf("code = %q, want %s: %v", code, c.code, err)
			}
		})
	}

	ok := map[string]string{
		"depth three": `function f(t: (i32, (i32, (i32, string)))): i32 {
  let (a, (b, (c, s))) = t;
  return a + b + c + s.len();
}`,
		"two nested positions in one pattern": `function f(t: ((i32, i32), (i32, i32))): i32 {
  let ((a, b), (c, d)) = t;
  return a + b + c + d;
}`,
		"discards at two levels": `function f(t: (i32, (i32, i32))): i32 {
  let (_, (_, c)) = t;
  return c;
}`,
		"a destructuring parameter nests": `function f((a, (b, c)): (i32, (i32, i32))): i32 {
  return a + b + c;
}`,
		"let spelling nests too": `function f(t: (i32, (i32, i32))): i32 {
  let (a, (b, c)) = t;
  return a + b + c;
}`,
		"pointer-shaped elements at both levels": `function f(t: (string, (i32[], string))): i32 {
  let (s, (xs, s2)) = t;
  return s.len() + xs.len() + s2.len();
}`,
	}
	for name, body := range ok {
		t.Run(name, func(t *testing.T) {
			if err := checkSource(t, body); err != nil {
				t.Errorf("want accepted, got: %v", err)
			}
		})
	}
}

func TestTupleVariantPayloadSubPatternChecks(t *testing.T) {
	const decls = "enum Inner { Ok2(i32), Err2(i32) }\nenum Outer { A(Inner), B }\nenum Wrap { W((i32, i32)), Z(i32) }\n"
	bad := map[string]struct{ src, code string }{
		"payload is not an enum": {`function f(t: (Wrap, i32)): i32 {
  match (t) { (Z(Ok2(n)), y) => { return n + y; }, _ => { return 0; } }
  return 0;
}`, "E035"},
		"unknown variant in a payload": {`function f(t: (Outer, i32)): i32 {
  match (t) { (A(Nope(n)), y) => { return n + y; }, _ => { return 0; } }
  return 0;
}`, "E014"},
		"payload literal type mismatch": {`function f(t: (Outer, i32)): i32 {
  match (t) { (A("x"), y) => { return y; }, _ => { return 0; } }
  return 0;
}`, "E035"},
		"payload tuple arity": {`function f(t: (Wrap, i32)): i32 {
  match (t) { (W((a, b, c)), y) => { return y; }, _ => { return 0; } }
  return 0;
}`, "E035"},
		"payload binder collides with a sibling element's": {`function f(t: (Outer, i32)): i32 {
  match (t) { (A(Ok2(y)), y) => { return y; }, _ => { return 0; } }
  return 0;
}`, "E013"},
		"payload sub-pattern needs a wildcard arm": {`function f(t: (Outer, i32)): i32 {
  match (t) { (A(Ok2(n)), y) => { return n + y; } }
  return 0;
}`, "E030"},
	}
	for name, c := range bad {
		t.Run(name, func(t *testing.T) {
			err := checkSource(t, decls+c.src)
			if err == nil {
				t.Fatalf("want %s, got none", c.code)
			}
			if code := firstErrCode(err); code != c.code {
				t.Errorf("code = %q, want %s: %v", code, c.code, err)
			}
		})
	}

	ok := map[string]string{
		"nested variant in a payload": `function f(t: (Outer, i32)): i32 {
  match (t) { (A(Ok2(n)), y) => { return n + y; }, _ => { return 0; } }
  return 0;
}`,
		"sibling arms differ inside the payload": `function f(t: (Outer, i32)): i32 {
  match (t) { (A(Ok2(n)), y) => { return n + y; }, (A(Err2(n)), y) => { return n - y; }, _ => { return 0; } }
  return 0;
}`,
		"tuple in a payload": `function f(t: (Wrap, i32)): i32 {
  match (t) { (W((0, b)), y) => { return b + y; }, (W((a, b)), y) => { return a * b + y; }, _ => { return 0; } }
  return 0;
}`,
		"payload sub-pattern under a nested tuple element": `function f(t: ((Outer, i32), i32)): i32 {
  match (t) { ((A(Ok2(n)), b), y) => { return n + b + y; }, _ => { return 0; } }
  return 0;
}`,
		"guard sees a payload sub-pattern's binds and the sibling element's": `function f(t: (Outer, i32)): i32 {
  match (t) { (A(Ok2(n)), y) when n > y => { return n; }, _ => { return 0; } }
  return 0;
}`,
		"expression form takes payload sub-patterns too": `function f(t: (Outer, i32)): i32 {
  let v = match (t) { (A(Ok2(n)), y) => n + y, _ => 0 };
  return v;
}`,
	}
	for name, body := range ok {
		t.Run(name, func(t *testing.T) {
			if err := checkSource(t, decls+body); err != nil {
				t.Errorf("want accepted, got: %v", err)
			}
		})
	}
}

func TestTupleElemVariantPatternChecks(t *testing.T) {
	const decls = "enum E { A(i32), B, C(i32, string) }\n"
	bad := map[string]struct{ src, code string }{
		"payload arity": {`function f(t: (E, i32)): i32 {
  match (t) { (A(x, z), y) => { return y; }, _ => { return 0; } }
  return 0;
}`, "E015"},
		"unknown variant": {`function f(t: (E, i32)): i32 {
  match (t) { (Zz(x), y) => { return y; }, _ => { return 0; } }
  return 0;
}`, "E014"},
		"non-enum element": {`function f(t: (E, i32)): i32 {
  match (t) { (e, A(y)) => { return 0; }, _ => { return 0; } }
  return 0;
}`, "E035"},
		"binder collides with an element binder": {`function f(t: (E, i32)): i32 {
  match (t) { (A(x), x) => { return x; }, _ => { return 0; } }
  return 0;
}`, "E013"},
		"no wildcard arm": {`function f(t: (E, i32)): i32 {
  match (t) { (A(x), y) => { return x + y; } }
  return 0;
}`, "E030"},
	}
	for name, c := range bad {
		t.Run(name, func(t *testing.T) {
			err := checkSource(t, decls+c.src)
			if err == nil {
				t.Fatalf("want %s, got none", c.code)
			}
			if code := firstErrCode(err); code != c.code {
				t.Errorf("code = %q, want %s: %v", code, c.code, err)
			}
		})
	}

	ok := map[string]string{
		"payload binds at the element's type": `function f(t: (E, i32)): i32 {
  match (t) { (C(n, s), y) => { return n + y + s.len(); }, _ => { return 0; } }
  return 0;
}`,
		"a variant nests inside a nested tuple element": `function f(t: (i32, (E, i32))): i32 {
  match (t) { (n, (A(x), y)) => { return n + x + y; }, _ => { return 0; } }
  return 0;
}`,
		"payload-less spelled with empty parens": `function f(t: (E, i32)): i32 {
  match (t) { (B(), y) => { return y; }, _ => { return 0; } }
  return 0;
}`,
		"generic element substitutes the payload type": `function f(t: (Option[i32], i32)): i32 {
  match (t) { (Some(v), y) => { return v + y; }, _ => { return 0; } }
  return 0;
}`,
		"guard sees the element's payload binds": `function f(t: (E, i32)): i32 {
  match (t) { (A(x), y) when x > y => { return x; }, _ => { return 0; } }
  return 0;
}`,
	}
	for name, body := range ok {
		t.Run(name, func(t *testing.T) {
			if err := checkSource(t, decls+body); err != nil {
				t.Errorf("want accepted, got: %v", err)
			}
		})
	}
}

func TestBreakableLoopDoesNotDiverge(t *testing.T) {
	rejected := []struct{ name, src string }{
		{"value-falls-off-the-end", `function f(n: i32): i32 {
			let acc: i32 = 42;
			loop {
				acc = acc + n;
				if (n > 100) { return acc; }
				break;
			}
		}`},
		{"while-true-with-break", `function f(n: i32): i32 {
			while (true) { if (n > 0) { return n; } break; }
		}`},
		{"let-else-breaks-out", `enum Opt { Has(i32), Nil }
		function f(o: Opt): i32 {
			let Has(v) = o else { loop { break; } };
			return v;
		}`},
		{"let-else-while-true-breaks", `enum Opt { Has(i32), Nil }
		function f(o: Opt): i32 {
			let Has(v) = o else { while (true) { break; } };
			return v;
		}`},
		{"labelled-break-targets-this-loop", `function f(n: i32): i32 {
			outer: loop { if (n > 0) { return n; } break outer; }
		}`},
	}
	for _, c := range rejected {
		t.Run(c.name, func(t *testing.T) {
			if err := checkSource(t, c.src); err == nil {
				t.Errorf("accepted a function whose loop can break, so control falls through:\n%s", c.src)
			}
		})
	}
}

func TestNonBreakingLoopStillDiverges(t *testing.T) {
	accepted := []struct{ name, src string }{
		{"bare-loop", `function f(): i32 { loop { let x = 1; } }`},
		{"while-true", `function f(): i32 { while (true) { let x = 1; } }`},
		{"todo-stub", `function f(): i32 { todo; }`},
		{"inner-loop-breaks", `function f(n: i32): i32 {
			loop {
				let i: i32 = 0;
				while (i < n) { if (i == 3) { break; } i = i + 1; }
			}
		}`},
		{"labelled-break-targets-inner", `function f(n: i32): i32 {
			loop {
				inner: while (true) { break inner; }
			}
		}`},
		{"let-else-non-breaking-loop", `enum Opt { Has(i32), Nil }
		function f(o: Opt): i32 {
			let Has(v) = o else { loop { } };
			return v;
		}`},
		{"break-inside-a-lambda-body", `function f(n: i32): i32 {
			loop {
				let g: () => i32 = (): i32 => { while (true) { break; } return 1; };
				let x: i32 = g();
			}
		}`},
		{"inner-loop-break-inside-block-expr", `function f(): i32 {
			loop {
				let z: i32 = { while (true) { break; } 1 };
			}
		}`},
		{"lambda-break-inside-block-expr", `function f(): i32 {
			loop {
				let z: i32 = { let g: () => i32 = (): i32 => { while (true) { break; } return 1; }; g() };
			}
		}`},
	}
	for _, c := range accepted {
		t.Run(c.name, func(t *testing.T) {
			if err := checkSource(t, c.src); err != nil {
				t.Errorf("rejected a function whose loop cannot break: %v\n%s", err, c.src)
			}
		})
	}
}

func TestBreakInExpressionPositionDoesNotDiverge(t *testing.T) {
	rejected := []struct{ name, src string }{
		{"block-expr", `function f(): i32 {
			loop {
				let z: i32 = { break; 1 };
			}
		}`},
		{"if-expr", `function f(n: i32): i32 {
			loop {
				let z: i32 = if (n > 0) { break; 1 } else { 2 };
			}
		}`},
		{"match-expr", `function f(n: i32): i32 {
			loop {
				let z: i32 = match (n) { 0 => { break; 1 }, _ => 2 };
			}
		}`},
		{"labelled-break-in-block-expr", `function f(): i32 {
			outer: loop {
				let z: i32 = { break outer; 1 };
			}
		}`},
		{"block-expr-in-while-true", `function f(): i32 {
			while (true) {
				let z: i32 = { break; 1 };
			}
		}`},
		{"break-in-nested-while-condition", `function f(): i32 {
			loop {
				while ({ break; true }) { }
			}
		}`},
	}
	for _, c := range rejected {
		t.Run(c.name, func(t *testing.T) {
			err := checkSource(t, c.src)
			if err == nil {
				t.Fatalf("accepted a function whose loop breaks from expression position:\n%s", c.src)
			}
			ce := errorWithCode(err, "E052")
			if ce == nil {
				t.Fatalf("want E052, got %v", err)
			}
			if ce.Pos.Line != 1 || ce.Pos.Col != 1 {
				t.Errorf("E052 anchored at %d:%d, want 1:1 (the function declaration)", ce.Pos.Line, ce.Pos.Col)
			}
		})
	}
}

func errorWithCode(err error, code string) *Error {
	var errs diag.Errors
	if !errors.As(err, &errs) {
		errs = diag.Errors{err}
	}
	for _, e := range errs {
		var ce *Error
		if errors.As(e, &ce) && ce.ErrCode == code {
			return ce
		}
	}
	return nil
}

func TestMapIterIsAnOrdinaryUserType(t *testing.T) {
	if err := checkSource(t, `struct MapIter { n: i32 } function f(): i32 { return MapIter { n: 3 }.n; }`); err != nil {
		t.Fatal(err)
	}
}

func TestStructFieldMapNewTakesTheFieldType(t *testing.T) {
	prog, err := parser.Parse(`struct Box { m: Map[string, i32], tag: i32 }
function main(): i32 { let b: Box = Box { m: map_new(4), tag: 2 }; return b.tag; }`)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if _, err := Check(prog); err != nil {
		t.Fatalf("want no diagnostic, got %v", err)
	}
	var call *ast.Call
	ast.Walk(prog.Funcs[0].Body, func(n ast.Node) bool {
		if c, ok := n.(*ast.Call); ok {
			if id, ok := c.Callee.(*ast.Ident); ok && id.Name == "map_new" {
				call = c
			}
		}
		return true
	})
	if call == nil || len(call.TypeArgs) != 2 {
		t.Fatalf("map_new's type arguments were not stamped from the field: %+v", call)
	}
	if _, isStr := call.TypeArgs[0].(ast.StringType); !isStr {
		t.Errorf("key type argument = %v, want string", call.TypeArgs[0])
	}
}

func TestWasiHTTPStatefulHandlerIsE075(t *testing.T) {
	const response = `HttpResponse { status: 200, body: BodyText("ok"), headers: HeaderMap { names: [], values: [] }, trailers: HeaderMap { names: [], values: [] }, background: None }`
	check := func(src string) (*Info, error) {
		prog, err := parser.Parse(serveStubDecls + src)
		if err != nil {
			t.Fatalf("parse: %v", err)
		}
		info, err := CheckTarget(prog, "wasm32-wasi-http")
		if main := findDecl(prog, "main"); main != nil {
			t.Errorf("a main was synthesised for a wasm32-wasi-http handler")
		}
		return info, err
	}
	_, err := check(`function handle(n: i32, req: HttpRequest, plat: platform__Host): (i32, HttpResponse) { return (n, ` + response + `); }`)
	if err == nil || strings.Count(err.Error(), "\n")+1 != 1 || !strings.Contains(err.Error(), WasiHTTPStatefulHandler) {
		t.Errorf("stateful handler: got %v, want one E075", err)
	}
	if _, err := check(`function handle(req: HttpRequest, plat: platform__Host): HttpResponse { return ` + response + `; }`); err != nil {
		t.Errorf("stateless handler: %v", err)
	}
	_, err = check(`function handle(req: HttpRequest): HttpResponse { return ` + response + `; }`)
	if err == nil || strings.Count(err.Error(), "\n")+1 != 1 || !strings.Contains(err.Error(), "this `handle` takes 1 parameter(s)") {
		t.Errorf("one-parameter handler: got %v, want one E075", err)
	}
	_, err = check(`function init(port: i32): i32 { return port; }
function handle(req: HttpRequest, plat: platform__Host): HttpResponse { return ` + response + `; }`)
	if err == nil || !strings.Contains(err.Error(), "`init` takes 1 parameters") {
		t.Errorf("mispaired init on wasm32-wasi-http: got %v, want the pairing E075", err)
	}
}

func TestPayloadlessVariantCallIsE038(t *testing.T) {
	for _, tc := range []struct{ src, typ string }{
		{`enum C { R, B } function main(): i32 { let c: C = R(); return 0; }`, "C"},
		{`enum T[A] { L, N(A) } function main(): i32 { let x: T[i32] = L(); return 0; }`, "T"},
		{`function main(): i32 { let x: Option[i32] = None(); return 0; }`, "Option"},
	} {
		err := checkSource(t, tc.src)
		if err == nil || !strings.Contains(err.Error(), "calling non-function value of type "+tc.typ) {
			t.Errorf("%s: got %v, want a non-function call of %s", tc.src, err, tc.typ)
		}
	}
	for _, ok := range []string{
		`enum C { R, B } function main(): i32 { let c: C = R; return 0; }`,
		`enum C { R, B } function main(): i32 { let c: C = C.R(); return 0; }`,
	} {
		if err := checkSource(t, ok); err != nil {
			t.Errorf("%s: rejected, want accepted: %v", ok, err)
		}
	}
}

func TestGenericTraitDefaultTakesImplArgs(t *testing.T) {
	src := `trait Holder[T] {
  function get(self: Self): T;
  function get_or(self: Self, d: T): T { let v: T = self.get(); return v; }
}
struct IntBox { v: i32 }
impl Holder[i32] for IntBox { function get(self: IntBox): i32 { return self.v; } }
struct Wrap[S] { v: S }
impl[S] Holder[S] for Wrap[S] { function get(self: Wrap[S]): S { return self.v; } }
struct Pair[A, B] { a: A, b: B }
impl[A, B] Holder[B] for Pair[A, B] { function get(self: Pair[A, B]): B { return self.b; } }
function main(): i32 {
  let b: i32 = IntBox { v: 7 }.get_or(0);
  let w: string = Wrap { v: "w" }.get_or("x");
  let p: boolean = Pair { a: 1, b: true }.get_or(false);
  return 0;
}`
	if err := checkSource(t, src); err != nil {
		t.Errorf("inherited generic-trait default should typecheck: %v", err)
	}
}

func TestTraitDefaultOwnTypeParamRenamedApart(t *testing.T) {
	src := `trait Iter[T] {
  function next(self: Self): Option[(T, Self)];
  function fold[U](self: Self, init: U, f: (U, T) => U): U {
    let acc: U = init;
    let cur: Self = self;
    let go: boolean = true;
    while (go) {
      match (cur.next()) {
        Some(t) => { acc = f(acc, t.0); cur = t.1; },
        None => { go = false; }
      }
    }
    return acc;
  }
}
struct Pair[T, U] { a: T, b: U, done: boolean }
impl[T, U] Iter[U] for Pair[T, U] {
  function next(self: Pair[T, U]): Option[(U, Pair[T, U])] {
    if (self.done) { return None; }
    return Some((self.b, Pair { a: self.a, b: self.b, done: true }));
  }
}
function main(): i32 {
  let p: Pair[string, i32] = Pair { a: "x", b: 4, done: false };
  let n: boolean = p.fold(false, (acc: boolean, v: i32): boolean => acc || v == 4);
  return 0;
}`
	if err := checkSource(t, src); err != nil {
		t.Errorf("own type parameter of an inherited default should typecheck: %v", err)
	}
}

func TestTraitDefaultBodySelfIsImplType(t *testing.T) {
	src := `trait Count {
  function step(self: Self): Self;
  function twice(self: Self): Self {
    let a: Self = self.step();
    return a.step();
  }
}
struct C { n: i32 }
impl Count for C { function step(self: C): C { return C { n: self.n + 1 }; } }
function main(): i32 {
  let c: C = C { n: 0 };
  return c.twice().n;
}`
	if err := checkSource(t, src); err != nil {
		t.Errorf("Self in a default's body should be the impl type: %v", err)
	}
}

func TestTupleTraitArgumentReadsImplParams(t *testing.T) {
	src := `trait It[T] {
  function next(self: Self): Option[(T, Self)];
  function first_or(self: Self, d: T): T {
    match (self.next()) {
      Some(t) => { return t.0; },
      None => { return d; }
    }
  }
}
struct Arr[T] { xs: T[], idx: i32 }
impl[T] It[T] for Arr[T] {
  function next(self: Arr[T]): Option[(T, Arr[T])] {
    if (self.idx >= self.xs.len()) { return None; }
    return Some((self.xs[self.idx], Arr { xs: self.xs, idx: self.idx + 1 }));
  }
}
struct Numbered[I, T] { it: I, at: i32 }
impl[I: It[T], T] It[(i32, T)] for Numbered[I, T] {
  function next(self: Numbered[I, T]): Option[((i32, T), Numbered[I, T])] {
    match (self.it.next()) {
      Some(t) => { return Some(((self.at, t.0), Numbered[I, T] { it: t.1, at: self.at + 1 })); },
      None => { return None; }
    }
  }
}
function main(): i32 {
  let n: Numbered[Arr[string], string] = Numbered[Arr[string], string] { it: Arr { xs: ["a"], idx: 0 }, at: 0 };
  let p: (i32, string) = n.first_or((9, "none"));
  return p.0;
}`
	if err := checkSource(t, src); err != nil {
		t.Errorf("a tuple trait argument should read the impl's parameters: %v", err)
	}
}

func TestAmbiguousUnionMemberVarInitReportsOnce(t *testing.T) {
	src := `struct Hold { v: i32 }
	struct Blank { w: i32 }
	type Slot = Hold | Blank;
	type Wrap = Hold | Blank;
	function main(): i32 {
		let f: Slot = Hold { v: 30 };
		return 0;
	}`
	err := checkSource(t, src)
	var errs diag.Errors
	if !errors.As(err, &errs) || len(errs) != 1 {
		t.Fatalf("want exactly one diagnostic, got: %v", err)
	}
	var ce *Error
	if !errors.As(errs[0], &ce) || ce.Code() != "E036" || !strings.Contains(ce.Msg, "declared in multiple enums") {
		t.Fatalf("want E036 naming both unions, got: %v", errs[0])
	}
}

func TestMapUserMethodBesideBuiltins(t *testing.T) {
	const has = `import "core/map";
function (m: Map[string, i32]) has(k: string): boolean { return k.len() > 0; }
function main(): i32 { let m: Map[string, i32] = map_new(4); if (m.has("a")) { return 1; } return 0; }`
	if err := checkSource(t, has); err != nil {
		t.Errorf("a user `has` on Map is refused: %v", err)
	}
	const contains = `import "core/map";
function (m: Map[string, i32]) contains(k: string): boolean { return false; }
function main(): i32 { let m: Map[string, i32] = map_new(4); if (m.contains("a")) { return 1; } return 0; }`
	err := checkSource(t, contains)
	es, _ := err.(diag.Errors)
	if len(es) != 1 || es[0].(*Error).ErrCode != "E006" {
		t.Errorf("a user `contains` on Map should redeclare the builtin (E006), got %v", err)
	}
}

func TestBoundGenericParameterTakesWhatItsTypeTakes(t *testing.T) {
	src := `function identity[T](x: T): T { return x; }
function pair[T](a: T, b: T): i32 { return 2; }
function main(): i32 {
    let lit: str = identity[str]("de");
    let raw: str = "abc";
    return lit.len() + pair("x", raw);
}`
	if err := checkSource(t, src); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}
