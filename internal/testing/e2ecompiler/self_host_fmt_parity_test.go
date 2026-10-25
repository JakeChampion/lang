package e2ecompiler

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/check/checker"
	"github.com/jakechampion/lang/internal/check/constfold"
	"github.com/jakechampion/lang/internal/pkg/modload"
	goparser "github.com/jakechampion/lang/internal/syntax/parser"
	goprinter "github.com/jakechampion/lang/internal/syntax/printer"
	"github.com/jakechampion/lang/internal/testing/corpus"
)

var fmtParityCases = []struct {
	name string
	src  string
}{
	{"match-value-with-divergent-arm", `enum ProbeError { Missing }
function probe(value: Option[i32]): Result[i32, ProbeError] {
  let number: i32 = match (value) {
    Some(n) => n,
    None => { return Err(Missing); },
  };
  return Ok(number + 1);
}
function main(): i32 {
  match (probe(Some(7))) {
    Ok(n) => { if (n != 8) { return 1; } },
    Err(_) => { return 2; },
  }
  match (probe(None)) {
    Ok(_) => { return 3; },
    Err(Missing()) => {},
  }
  return 0;
}
`},
	{"nested-valueless-match-lambda", `function main(): i32 {
  let count = cell_new(0);
  let visit = (n: i32): void => {
    match (n) {
      0 => { match (n) { 0 => { count.set(1); }, _ => { count.set(2); } } },
      _ => { count.set(3); }
    }
  };
  visit(0);
  return count.get() - 1;
}
`},
	{"qualified-record-binding-sites", `import "./shapes";
function f(whole @ shapes.Open { n, .. }: shapes.Open): i32 { return whole.n + n; }
function main(): i32 {
  let shapes.Open { n, .. } = shapes.make();
  for shapes.Open { n: k, .. } in [shapes.make()] { if (k != n) { return 1; } }
  if let shapes.Open { n: k, .. } = shapes.make() { return k; }
  return match (shapes.make()) { shapes.Open { n: k, .. } => k };
}
`},
	{"generic-supertraits", `trait Child[U]: api.Parent[Option[U], (string, u8)] + Marker {}`},
	{"nested-match-value-early-return", `struct P { x: i32 }
enum H { S(P), N }
enum Outer { A(H), B }
function whole(h: H): i32 {
  return match (h) { S(w @ P { x }) => { return w.x + x; }, N => 0 };
}
function nested(o: Outer): i32 {
  let value = match (o) { A(S(p)) => { return p.x; }, _ => 0 };
  return value + 1;
}
function main(): i32 { return whole(H.S(P { x: 1 })) + nested(Outer.B); }
`},
	{"nested-whole-struct-pattern", `struct P { x: i32 }
enum H { S(P) }
function f(h: H): i32 { return match (h) { S(w @ P { x }) => w.x + x }; }
function main(): i32 { return 0; }
`},
	{"tuple-binding-trailing-comment", "function main(): i32 {\n  let (a, b) = (1, 2); // pair\n  return a + b;\n}\n"},
	{"checked-trait-cast", `trait Named {
  function name(self: Self): string;
}
struct Square { side: i32 }
impl Named for Square {
  function name(self: Self): string { return "square"; }
}
function recover(value: dyn Named): Option[Square] {
  return value as? Square;
}
`},
	{"checked-numeric-casts", `function narrow(value: i64): Option[u8] {
  return (value + 1i64) as? u8;
}
function chained(value: i32): Option[u8] {
  return (value as i64) as? u8;
}
function unwrap(value: i64): Option[u8] {
  let smaller = (value as? u32)?;
  return smaller as? u8;
}
`},
	// A binding's annotation types a lambda that writes no return type; the
	// compile parse stamps the return type onto the lambda, and -fmt must print
	// the lambda as written (#12359).
	{"lambda-return-as-written", fmtLambdaAsWrittenSrc},
	{"opaque-struct-kept", "pub opaque struct A { a: i32 }\npub(package) opaque struct B { b: i32 }\n@must_consume pub opaque struct C { c: i32 }\ntrait Default { function default(): Self; }\n@derive(Default) opaque struct D { d: i32 }\nfunction main(): i32 { return 0; }\n"},
	{"omitted-return-type-as-written", "function helper() {}\n\nfunction main(): i32 {\n  return 0;\n}\n"},
	{"assoc-const-as-written", "trait Lanes {\n  const LANES: i32;\n  const NAME: string = \"lanes\";\n  function width(self: Self): i32;\n}\n\nstruct V2 { a: f64 }\n\nimpl Lanes for V2 {\n  const LANES: i32 = 2;\n  function width(self: Self): i32 {\n    return Self.LANES;\n  }\n}\n\nfunction chunk[T: Lanes](n: i32): i32 {\n  return n / T.LANES + f64.LANES;\n}\n\nfunction main(): i32 {\n  return 0;\n}\n"},
	{"const-params-as-written", "function filled[const N: i32](x: f64): f64[] {\n  return [x];\n}\n\nfunction count[const N: i32, T](x: T): i32 {\n  return N;\n}\n\nfunction main(): i32 {\n  return filled[0x4](1.0).len() + count[1_000, string](\"a\") + count[0o17, i32](1);\n}\n"},
	{"value-block-statements-span-lines", `function g(c: boolean, n: i32): i32 {
  let total: i32 = 0;
  while (total < n) {
    let step: i32 = if (c) { let k = 1; while (k < 3) { k = k + 1; } k } else { 1 };
    let w: i32 = { let a = 2; if (a > 1) { a = a + step; } a };
    let z: i32 = if (c) { let q = if (!c) { 0 } else { let m = 2; for (let j = 0; j < 2; j = j + 1) { m = m + j; } m }; q } else { 0 };
    total = total + w + z;
  }
  let d: i32 = if (c) { 4 } else { if (n > 0) { return 9; } 5 };
  return total + d;
}
function main(): i32 { return g(true, 20); }
`},
	{"if-expression-written-branches", `function branches(n: i32): string {
  let chain = if (n == 0) { "zero" } else if (n == 1) { "one" } else { "other" };
  let leading = if (n == 0) { let a = "a"; a } else { let b = "b"; b };
  let first = if (n < 0) { return "negative"; } else { "positive" };
  let last = if (n < 2) { "small" } else { return "large"; };
  let explicit_tail = if (n == 9) { return "early"; "written unreachable" } else { "tail" };
  let lambda = ((): string => if (n == 3) { return "three"; } else { if (n == 4) { return "four"; } else { return "five"; } })();
  return chain + leading + first + last + explicit_tail + lambda;
}
function main(): i32 { return branches(0).len(); }
`},
	{"if-expression-precedence", `function choose(flag: boolean): i32 {
  let bare = if (flag) { 1 } else { 2 };
  let right = 10 + (if (flag) { 1 } else { 2 });
  let left = (if (flag) { 3 } else { 4 }) * 2;
  let unary = -(if (flag) { 5 } else { 6 });
  let nested = 1 + (if (flag) { 2 + (if (flag) { 3 } else { 4 }) } else { 5 });
  let cast = (if (flag) { 7 } else { 8 }) as i64;
  let index = (if (flag) { [9] } else { [10] })[0];
  let size = (if (flag) { "yes" } else { "no" }).len();
  let logic = !(if (flag) { true } else { false });
  return bare + right + left + unary + nested + (cast as i32) + index + size + (if (logic) { 1 } else { 0 });
}
`},
	{"nested-payloadless-variants", `enum Color { Red, Black }
enum Tree { Leaf, Node(Color, Tree, Tree) }

function classify(t: Tree): i32 {
  match (t) {
    Node(Red(), Node(Black(), Leaf(), Leaf()), _) => { return 1; },
    Node(Black(), _, _) => { return 2; },
    _ => { return 3; }
  }
}

function main(): i32 {
  if (classify(Node(Red, Node(Black, Leaf, Leaf), Leaf)) != 1) { return 1; }
  if (classify(Node(Red, Node(Red, Leaf, Leaf), Leaf)) != 3) { return 2; }
  if (classify(Node(Black, Leaf, Leaf)) != 2) { return 3; }
  return 0;
}
`},
	{"owned-receiver", `struct Counter { n: i32 }

function (own c: Counter) bump(): Counter {
  return Counter { ...c, n: c.n + 1 };
}

function (c: Counter) peek(): i32 {
  return c.n;
}

trait Consume {
function take(own self: Self): i32;
}

impl Consume for Counter {
function take(own self: Self): i32 {
return self.n;
}
}
`},
	{"assert-statements", `function check(x: i32, message: string): void {
  assert(x > 0);
  assert(!(x > 10), "x is " + x.to_string());
  assert(x != 7, message);
  assert(x != 8, "");
  if (!(x > 0)) { eprint("assertion failed"); exit(1); }
}
`},
	{"assert-comments", `function check(x: i32): void {
  // Keep the contract.
  assert(
    x > 0,
    "positive value required"
  ); // Keep the explanation.
  assert(x < 10);
}
`},
	{"assert-nested", `function check(x: i32): void {
  defer assert(x > 0);
  let f: (i32) => void = (n: i32): void => { assert(n > 0); };
  f(x);
}
`},
	{"assert-with-and-without-message", `function check(xs: i32[], n: i32): i32 {
  assert(xs.len() > 0);
  assert(n <= 2147483647 - xs.len(), "too long: " + int_to_string(n));
  assert(!(n < 0));
  return xs[0] + n;
}
function main(): i32 {
  return check([1, 2], 3);
}
`},
	{"at-binding-scalar-heads", `function classify(n: i32): i32 {
  match (n) {
    k @ 1..10 => { return k * 2; },
    k @ 20 => { return k + 1; },
    k @ 30..=31 when k > 30 => { return k; },
    _ => { return 0; },
  }
}
function main(): i32 {
  return classify(5) + classify(20) + classify(31) + classify(30) + classify(99);
}
function a(n: i32): i32 { if let k @ 1..10 = n { return k * 2; } return 0; }
function b(n: i32): i32 { let k @ 20..30 = n else { return 0; }; return k + 1; }
`},
	{"if-let-braceless-then", `enum Box { Full(i32), Empty }
function main(): i32 {
  let b: Box = Full(8);
  if let Full(v) = b return v;
  return 99;
}
`},
	{"else-if-let-variant", `enum Box { Full(i32), Empty }
function pick(flag: boolean, box: Box): i32 {
  if (flag) { return 1; } else if let Full(value) = box { return value; } else { return 0; }
}
function main(): i32 { return pick(false, Full(7)); }
`},
	{"else-if-let-patterns", `struct Pair { a: i32, b: i32 }
function tuple(flag: boolean, pair: (i32, i32)): i32 {
  if (flag) { return 1; } else if let (2, value) = pair { return value; } else { return 0; }
}
function record(flag: boolean, pair: Pair): i32 {
  if (flag) { return 1; } else if let Pair { a: 2, b } = pair { return b; } else { return 0; }
}
function literal(flag: boolean, value: i32): i32 {
  if (flag) { return 1; } else if let 2 | 3 = value { return value; } else { return 0; }
}
function main(): i32 { return tuple(false, (2, 7)) + record(false, Pair { a: 2, b: 7 }) + literal(false, 3); }
`},
	{"else-block-if-let-stays-braced", `enum Box { Full(i32), Empty }
function pick(flag: boolean, box: Box): i32 {
  if (flag) { return 1; } else { if let Full(value) = box { return value; } }
  return 0;
}
function main(): i32 { return pick(false, Full(7)); }
`},
	{"pub-package", `pub(package) struct S { n: i32 }
pub(package) enum E { A, B }
pub(package) type T = S | S;
pub(package) function helper(n: i32): i32 { return n + 1; }
pub function api(n: i32): i32 { return helper(n); }
function main(): i32 { return api(1); }
`},
	{"export-attribute", `@export("fern:test/math", "add")
pub function add(x: i32, y: i32): i32 { return x + y; }
@export("fern:test/math", "zero")
function zero(): i32 { return 0; }
`},
	{"inline-hints", `@inline
function inc(n: i32): i32 { return n + 1; }
@noinline
pub function slow(n: i32): i32 { return inc(n); }
function plain(): i32 { return 0; }
`},
	{"inline-hints-methods", `struct A { value: i32 }
struct B { value: i32 }
@inline function (a: A) get(): i32 { return a.value; } @noinline function (b: B) get(): i32 { return b.value; }
`},
	{"inline-hints-modifiers", `@noinline
pub fip function keep(own xs: i32[]): i32[] { return xs; }
@inline
function id[T](x: T): T { return x; }
`},
	{"nested-fn-recursive", `function main(): i32 {
function f(n: i32): i32 { if (n <= 0) { return 0; } return f(n - 1); }
return f(3);
}
`},
	{"precedence-arith", `function main(): i32 {
let a: i32 = 1;
let b: i32 = 2;
let c: i32 = 3;
let t1: i32 = a + b * c;
let t2: i32 = (a + b) * c;
let t3: i32 = a - b - c;
let t4: i32 = a - (b - c);
let t5: i32 = a * b / c % a;
let t6: i32 = a << b >> c;
return t1 + t2 + t3 + t4 + t5 + t6;
}
`},
	{"slice-bounds", `function f(s: string): string {
let a: string = s[1:];
let b: string = s[:3];
let c: string = s[:];
let d: string = s[1:3];
return a + b + c + d;
}
function main(): i32 {
print(f("abcdef"));
return 0;
}
`},
	{"pattern-struct-scrutinee", `struct P { x: i32, y: i32 }
struct Q { a: i32, b: i32, c: i32 }
enum In { Ok2(i32), Er2(i32) }
enum E { A(In), B }
struct W { e: E, n: i32 }
function plain(p: P): i32 {
match (p) { P { x, y } => { return x + y; } }
}
function lit(p: P): i32 {
match (p) { P { x: 0, y } => { return 100 + y; }, P { x, y } => { return x + y; } }
}
function rename(q: Q): i32 {
match (q) { Q { a: n, b, c: m } => { return n + b + m; } }
}
function nested(w: W): i32 {
match (w) { W { e: A(Ok2(v)), n } => { return v + n; }, _ => { return 0; } }
}
function payloadless(w: W): i32 {
match (w) { W { e: B(), n } => { return n; }, W { n, .. } => { return 0 - n; } }
}
function guarded(p: P): i32 {
match (p) { P { x, y } when x > y => { return x; }, P { x, y } => { return y; } }
}
function at_bound(p: P): i32 {
match (p) { w @ P { x, y } => { return w.x + x + y; } }
}
function main(): i32 {
return plain(P { x: 1, y: 2 }) + lit(P { x: 0, y: 3 }) + rename(Q { a: 1, b: 2, c: 3 })
+ nested(W { e: E.A(In.Ok2(4)), n: 5 }) + payloadless(W { e: E.B, n: 2 }) + guarded(P { x: 9, y: 1 }) + at_bound(P { x: 1, y: 1 });
}
`},
	{"param-pattern-struct", `struct P { x: i32, y: i32 }
struct R { w: i32, h: i32 }
function add(P { x, y }: P): i32 {
return x + y;
}
function ren(P { x: a, y: b }: P): i32 {
return a * 10 + b;
}
function part(R { w, .. }: R): i32 {
return w;
}
function whole(v @ P { x, y }: P): i32 {
return v.x + x + y;
}
function tup((a, b): (i32, i32)): i32 {
return a - b;
}
function main(): i32 {
let f = (P { x, y }: P): i32 => { return x * 10 + y; };
return add(P { x: 1, y: 2 }) + ren(P { x: 3, y: 4 }) + part(R { w: 1, h: 2 }) + whole(P { x: 1, y: 1 }) + tup((9, 4)) + f(P { x: 1, y: 1 });
}
`},
	{"pattern-tuple-scrutinee", `enum In { Ok2(i32), Er2(i32) }
enum E { A(In), B }
function plain(t: (i32, i32)): i32 {
match (t) { (a, b) => { return a + b; } }
}
function mixed(t: (E, i32)): i32 {
match (t) { (A(v), 0) => { return 1; }, (A(v), y) => { return y; }, (B(), y) => { return y; }, _ => { return 0; } }
}
function nested(t: (i32, (i32, i32))): i32 {
match (t) { (a, (b, c)) => { return a + b + c; } }
}
function strlit(t: (string, i32)): i32 {
match (t) { ("go", n) => { return 10 + n; }, (s, n) => { return s.len() + n; } }
}
function at_bound(t: (i32, i32)): i32 {
match (t) { w @ (a, b) => { return w.0 + a + b; } }
}
function main(): i32 {
return plain((1, 2)) + mixed((E.A(In.Ok2(1)), 5)) + nested((1, (2, 3)))
+ strlit(("go", 1)) + at_bound((1, 2));
}
`},
	{"pattern-if-let", `enum Opt { Sm(i32), Nn }
enum Inner { Ok2(i32), Err2(i32) }
enum Outer { A(Inner), B }
function plain(o: Opt): i32 {
if let Sm(n) = o { return n; }
return 0;
}
function with_else(o: Opt): i32 {
if let Sm(n) = o { return n; } else { return 5; }
return 0;
}
function nested_head(o: Outer): i32 {
if let A(Ok2(n)) = o { return n; }
return 0;
}
function main(): i32 {
return plain(Opt.Sm(1)) + with_else(Opt.Nn) + nested_head(Outer.A(Inner.Ok2(3)));
}
`},
	{"pattern-nested-expression-match", `enum Inner { Ok2(i32), Err2(i32) }
enum Outer { A(Inner), B }
enum Opt { Sm(i32), Nn }
function nested_expr(o: Outer): i32 {
let v = match (o) { A(Ok2(n)) => n, A(Err2(n)) => 0 - n, _ => 0 };
return v;
}
function guarded_expr(o: Opt): i32 {
let v = match (o) { Sm(n) when n > 2 => n, Sm(n) => 0 - n, Nn => 0 };
return v;
}
function main(): i32 {
return nested_expr(Outer.A(Inner.Err2(4))) + guarded_expr(Opt.Sm(9));
}
`},
	{"pattern-nested-payload", `enum Inner { Ok2(i32), Err2(i32) }
enum Outer { A(Inner), B }
function nested(o: Outer): i32 {
match (o) {
A(Ok2(n)) => { return n; },
A(Err2(n)) => { return 0 - n; },
_ => { return 0; }
}
}
function main(): i32 {
return nested(Outer.A(Inner.Ok2(1)));
}
`},
	{"pattern-literal-payload", `enum Sm { V(i32), W }
function pick(s: Sm): i32 {
match (s) {
V(0) => { return 100; },
V(x) => { return x; },
W => { return 1; }
}
}
function main(): i32 {
return pick(Sm.V(0)) + pick(Sm.V(7)) + pick(Sm.W);
}
`},
	{"pattern-if-let-tuple", `function pick(t: (i32, i32)): i32 {
if let (1, 2) | (3, 4) = t {
return 1;
}
return 0;
}
function whole(t: (i32, i32)): i32 {
if let w @ (a, b) = t {
return a + b;
} else {
return 0;
}
}
function main(): i32 {
return pick((3, 4)) + whole((1, 2));
}
`},
	{"pattern-if-let-literal", `function pick(n: i32): i32 {
if let 1 | 2 = n {
return 10;
}
if let 3..5 = n {
return 20;
}
if let 7..=9 = n {
return 30;
}
return 0;
}
function main(): i32 {
return pick(2) + pick(4) + pick(9);
}
`},
	{"pattern-if-let-struct", `struct Point { x: i32, y: i32 }
function sum(p: Point): i32 {
if let Point { x: a, y } = p {
return a + y;
} else {
return 0;
}
}
function main(): i32 {
return sum(Point { x: 1, y: 2 });
}
`},
	{"pattern-let-else-literal", `function pick(n: i32): i32 {
let 1 | 2 = n else {
return 0;
};
return 9;
}
function ranged(n: i32): i32 {
let 3..5 = n else {
return 0;
};
return 8;
}
function main(): i32 {
return pick(2) + ranged(4);
}
`},
	{"pattern-let-else-nested", `enum Inner { Ok2(i32), Err2(i32) }
enum Outer { A(Inner), B }
function unwrap(o: Outer): i32 {
let A(Ok2(n)) = o else {
return 0;
};
return n;
}
function main(): i32 {
return unwrap(Outer.A(Inner.Ok2(5)));
}
`},
	{"pattern-let-else-or", `enum E { A(i32), B(i32) }
function get(e: E): i32 {
let A(v) | B(v) = e else {
return 0;
};
return v;
}
function main(): i32 {
return get(E.B(4));
}
`},
	{"param-destructure-vs-body-let", `struct Point { x: i32, y: i32 }
function folds_struct(Point { x: a, y }: Point): i32 {
return a + y;
}
function folds_tuple((a, (b, c)): (i32, (i32, i32))): i32 {
return a + b + c;
}
function folds_at(p @ Point { x, y }: Point): i32 {
return x + y + p.x;
}
function body_let_struct(p: Point): i32 {
let Point { x: a, y } = p;
return a + y;
}
function body_let_tuple(t: (i32, (i32, i32))): i32 {
let (a, (b, c)) = t;
return a + b + c;
}
function body_let_discard(t: (i32, i32)): i32 {
let (a, _) = t;
return a;
}
function main(): i32 {
return folds_struct(Point { x: 1, y: 2 }) + folds_tuple((1, (2, 3))) + folds_at(Point { x: 1, y: 2 }) + body_let_struct(Point { x: 1, y: 2 }) + body_let_tuple((1, (2, 3))) + body_let_discard((1, 2));
}
`},
	{"precedence-bitwise-vs-compare", `function is_pow2(n: i32): boolean {
return (n & (n - 1)) == 0;
}
function main(): i32 {
let n: i32 = 8;
let x: boolean = n & 1 == 0;
let y: boolean = (n | 2) != 0;
let z: boolean = n ^ 1 > 0;
if (is_pow2(n) && x && y || z) {
return 1;
}
return 0;
}
`},
	{"precedence-logical-and-unary", `function main(): i32 {
let a: boolean = true;
let b: boolean = false;
let n: i32 = 5;
let p: boolean = a && b || !a;
let q: boolean = !(a && b);
let r: i32 = 0 - n;
let s: i32 = 0 - (n + 1);
if (p && q) {
return r + s;
}
return 0;
}
`},
	{"precedence-postfix-receivers", `struct P { x: i32, y: i32 }
function main(): i32 {
let s: string = "hello";
let xs: i32[] = [1, 2, 3];
let p: P = P { x: 1, y: 2 };
let a: i32 = (s + "x").len();
let b: i32 = xs[p.x + 1];
let c: string = slice_unchecked(s, p.x, p.x + 2);
let d: i32 = (p.x + p.y) * 2;
return a + b + c.len() + d;
}
`},
	{"indent-nesting", `function main(): i32 {
let total: i32 = 0;
let i: i32 = 0;
while (i < 4) {
if (i > 1) {
let j: i32 = 0;
while (j < i) {
total = total + j;
j = j + 1;
}
} else {
total = total + 1;
}
i = i + 1;
}
return total;
}
`},
	{"comments-and-blanks", `// file header, above the import
import "std/i32";

// doc comment for f
function f(a: i32): i32 {
  // leading comment on the let
  let t: i32 = a + 1;

  // leading comment on the if
  if (t > 0) {
    return t;  // trailing on a return
  }
  return 0;
}

function g(): i32 {
  let x: i32 = 1;  // trailing on a let


  let y: i32 = 2;
  return x + y;
}
`},
	{"comments-at-block-end", `function f(a: i32): i32 {
  if (a > 0) {
    return 1;
  }
  // between the if and the return
  return 0;
}

function g(): i32 {
  return 2;
}
// trailing comment past the last declaration
`},
	{"decl-source-order", `// about the first function
function one(): i32 {
  return 1;
}

// about the struct
struct P { x: i32, y: i32 }

// about the second function
function two(p: P): i32 {
  return p.x + p.y;
}
`},
	{"late-import", `import "core/map";

// about one
function one(): i32 {
  return 1;
}

// about the late imports
import "std/strings";
pub use "./util".{helper};
import "./lexer" as lx;

// about two
function two(): i32 {
  return 2;
}
`},
	{"section-comment-apart", `// --- section one ---

// about first
function first(): i32 {
  return 1;
}

// --- section two ---
//
// prose about the section

// about Pair
struct Pair { a: i32 }

// a note about what follows

const LIMIT: i32 = 3;

// a note above an attribute

@inline
function third(): i32 {
  return 3;
}
`},
	{"comment-gaps-below-top-level", `function f(): i32 {
  // section

  // doc of x
  let x: i32 = 1;
  return x;
}

struct S {
  // group

  // doc a
  a: i32,
}

// trailing a

// trailing b
`},
	{"modifiers-and-shapes", `import "std/i32" as ints;

struct Box[T] { item: T }

pub function widen(s: string, i: i32): i32 {
  return s[i] as i32;
}

pub function early(a: i32): void {
  if (a > 0) {
    return;
  }
}
`},
	{"written-str-spelling", `pub function head(s: string): str {
return slice_unchecked(s, 0, 1);
}
pub function tag(s: str, t: str): i32 {
return s.len() + t.len();
}
struct View { text: str, label: string }
function main(): i32 {
let t: str = "  hi  ";
let u: string = "owned";
return t.len() + u.len();
}
`},
	{"written-fn-type-spelling", `struct Rec { n: i32 }
pub function sort_by[T](arr: T[], cmp: (T, T) => i32): T[] {
return arr;
}
pub function each[T, U](xs: T[], f: (T) => U[], p: (T) => boolean, mk: () => Rec, sink: (T) => void): i32 {
return xs.len();
}
function main(): i32 {
return 0;
}
`},
	{"visibility-on-types", `pub struct Open { x: i32 }

struct Shut { y: i32 }

pub enum Colour { Red, Green }

enum Hidden { On, Off }

pub type Either = Open | Shut;

type Private = Open | Shut;

pub function use_them(o: Open, c: Colour): i32 {
return o.x;
}
`},
	{"destructuring-forms", `struct Point { x: i32, y: i32 }
function pair(): (i32, i32) {
return (1, 2);
}
function main(): i32 {
let (a, b) = pair();
let (q, r, s) = (1, 2, 3);
let Point { x, y } = Point { x: 4, y: 5 };
let Point { x: px, y: py } = Point { x: 6, y: 7 };
return a + b + q + r + s + x + y + px + py;
}
`},
	{"pattern-binding-sites", `struct Point { x: i32, y: i32 }
function main(): i32 {
let ps: Point[] = [Point { x: 1, y: 2 }];
let ts: (i32, i32)[] = [(3, 4)];
let acc = 0;
for Point { x, y } in ps {
acc = acc + x + y;
}
for Point { x: a, y: b } in ps {
acc = acc + a + b;
}
for w @ Point { x, y } in ps {
acc = acc + w.x + x + y;
}
for w @ (m, n) in ts {
acc = acc + w.0 + m + n;
}
let v @ Point { x, y } = ps[0];
let t @ (p, q) = ts[0];
return acc + v.x + x + y + t.0 + p + q;
}
`},
	{"struct-pattern-rest", `struct Point { x: i32, y: i32, z: i32 }
function param(Point { x, .. }: Point): i32 {
return x;
}
function main(): i32 {
let p: Point = Point { x: 1, y: 2, z: 3 };
let ps: Point[] = [p];
let acc = 0;
let Point { x, .. } = p;
let w @ Point { y, .. } = p;
for Point { z, .. } in ps {
acc = acc + z;
}
match (p) {
Point { x: mx, .. } => {
acc = acc + mx;
}
}
return acc + x + w.z + param(p);
}
`},
	{"type-parameter-lists", `import "core/cmp";

enum Opt[T] { Some(T), None }

pub function each[T, U](xs: T[], f: (T) => U): i32 {
return xs.len();
}

pub function sort_key[T, K: cmp.Ord](arr: T[], key: (T) => K): T[] {
return arr;
}

pub function [U] (o: Opt[T]) map(f: (T) => U): Opt[U] {
return None;
}
`},
	{"for-range", `function hi(): i32 {
  return 4;
}

function main(): i32 {
  let t: i32 = 0;
  for i in 0..4 {
    t = t + i;
  }
  for j in 0..=4 {
    t = t + j;
  }
  for k in 1..hi() {
    t = t + k;
  }
  return t;
}
`},
	{"for-array-and-map", `function main(m: map[string, i32], a: i32[]): i32 {
  let t: i32 = 0;
  for x in a {
    t = t + x;
  }
  for (k, v) in m {
    t = t + v + k.len();
  }
  return t;
}
`},
	{"loop-labels", `function walk(a: i32[]): i32 {
  let t: i32 = 0;
  outer: while (t < 10) {
    inner: for (let i: i32 = 0; i < 3; i = i + 1) {
      if (i == 2) {
        continue outer;
      }
      break inner;
    }
    t = t + 1;
  }
  each: for x in a {
    break each;
  }
  return t;
}
function main(): i32 {
  return walk([1, 2]);
}
`},
	{"trait-and-impl-member-comments", `trait Shape {
  // The area, in whatever unit the shape was measured in.
  function area(self: Self): i32;

  function name(self: Self): string;  // for the report
}

struct Sq { s: i32 }

impl Shape for Sq {
  function area(self: Sq): i32 {
    return self.s * self.s;
  }

  // A square is named after its side.
  function name(self: Sq): string {
    return "square";
  }
}

function main(): i32 {
  return 0;
}
`},
	{"trait-declarations", `pub trait Display {
function to_string(self: Self): string;
}

trait Empty {}

pub trait Ord: Display {
function cmp(self: Self, other: Self): i32;
}

pub trait Greet[T] {
function hi(self: Self, x: T): string;
function loud(self: Self, x: T): string {
return self.hi(x);
}
}

function main(): i32 {
return 0;
}
`},
	{"impl-blocks", `import "core/cmp";

trait Display {
function to_string(self: Self): string;
}

struct Box[T] { item: T }

impl Display for i32 {}

impl Display for boolean {
function to_string(self: Self): string {
if (self) {
return "true";
}
return "false";
}
}

impl[T: cmp.Eq] cmp.Eq for Box[T] {
function eq(self: Self, other: Self): boolean {
return self.item == other.item;
}
}

function main(): i32 {
return 0;
}
`},
	{"comments-inside-impl-bodies", `trait Display {
function to_string(self: Self): string;
}

impl Display for boolean {
function to_string(self: Self): string {
// leading comment inside the method
let s: string = "t";  // trailing on a statement
return s;
}
}
`},
	{"decls-and-literals", `struct P { x: i32, y: i32 }
function mk(x: i32, y: i32): P {
return P { x: x, y: y };
}
function main(): i32 {
let p: P = mk(1, 2);
let q: P = P { ...p, y: p.y + 1 };
let xs: i32[] = [1, 2, 3, 4];
let s: string = "a" + "b" + "c";
return p.x + q.y + xs[2] + s.len();
}
`},
	{"else-block-vs-else-if", `function f(c: i32): i32 {
  if (c == 1) {
    return 1;
  } else {
    if (c == 2) {
      return 2;
    }
  }
  return 0;
}

function g(c: i32): i32 {
  if (c == 1) {
    return 1;
  } else if (c == 2) {
    return 2;
  } else if (c == 3) {
    return 3;
  } else {
    return 4;
  }
}

function h(c: i32): i32 {
  if (c == 1) {
    return 1;
  } else {
    let x: i32 = c + 1;
    if (x == 3) {
      return 2;
    }
  }
  return 0;
}
`},
	{"c-style-for", `function main(): i32 {
let sum: i32 = 0;
for (let i: i32 = 1; i <= 10; i = i + 1) {
sum = sum + i;
}
return sum;
}
`},
	{"c-style-for-optional-clauses", `function main(): i32 {
let t: i32 = 0;
let j: i32 = 0;
for (; j < 3; j = j + 1) {
t = t + 1;
}
for (let m: i32 = 0; m < 4; ) {
m = m + 1;
t = t + 1;
}
for (let n: i32 = 0; n < 5; n = n + 1) {
if (n == 2) {
continue;
}
t = t + 1;
}
return t;
}
`},
	{"if-true-while-is-not-a-for", `function main(): i32 {
let acc: i32 = 0;
if (true) {
let q: i32 = 1;
let r = true;
while (true) {
acc = acc + q;
break;
}
}
return acc;
}
`},
	{"guard-try-slice-own-fnarray-hex-discard-ifexpr", `enum Shape { Circle(f64), Square(f64) }

function area(s: Shape): Result[f64, string] {
  match (s) {
    Circle(r) when r <= 0.0 => {
      return Err("non-positive radius");
    },
    Circle(r) => {
      return Ok(3.14159 * r * r);
    },
    Square(side) => {
      return Ok(side * side);
    }
  }
  return Err("unreachable");
}

function first(xs: i32[]): Result[i32, string] {
  if (xs.len() == 0) {
    return Err("empty");
  }
  return Ok(xs[0]);
}

function head_plus_one(xs: i32[]): Result[i32, string] {
  let v: i32 = first(xs)?;
  return Ok(v + 1);
}

function byte_count(s: string): i32 {
  let bs: [u8] = s.as_bytes();
  return bs.len();
}

fip function consume(own arr: i32[]): i32[] {
  arr = arr.with(0, 1);
  return arr;
}

function apply_all(fs: ((string) => string)[], seed: string): string {
  let acc: string = seed;
  for i in 0..fs.len() {
    acc = fs[i](acc);
  }
  return acc;
}

function mask(): i32 {
  return 0xFF00 | 0xdead;
}

function modes(): i32 {
  return 0o755 | 0O17 | 0b1010 | 0B11;
}

function drop_first(): i32 {
  let (_, b) = (1, 2);
  return b;
}

function widen(kk: i32): string {
  let ctor: string = if (kk == 1) { "wide" } else { "narrow" };
  return ctor;
}

function main(): i32 {
  let fs: ((string) => string)[] = [(s: string) => s + "!"];
  if (apply_all(fs, "x") != "x!") {
    return 1;
  }
  if (byte_count("hello") != 5) {
    return 2;
  }
  if (consume([0, 0]).len() != 2) {
    return 3;
  }
  if (widen(1) != "wide") {
    return 4;
  }
  if (drop_first() != 2) {
    return 5;
  }
  if (mask() == 0) {
    return 6;
  }
  match (head_plus_one([7])) {
    Ok(v) => {
      if (v != 8) {
        return 7;
      }
    },
    Err(_) => {
      return 8;
    }
  }
  match (area(Circle(0.0))) {
    Ok(_) => {
      return 9;
    },
    Err(_) => {}
  }
  return 0;
}
`},
	{"match-expression-in-value-position", `function pick(o: Option[string]): string {
  let s: string = match (o) { Some(v) => v, None => "none" };
  return s;
}

function main(): i32 {
  if (pick(Some("hi")) != "hi") {
    return 1;
  }
  if (pick(None) != "none") {
    return 2;
  }
  return 0;
}
`},
	{"parametrised-trait-bound", `import "core/iter";

pub function total[I: iter.Iterator[i32]](it: I): i32 {
  return iter.sum(it);
}

function main(): i32 {
  return 0;
}
`},
	{"qualified-type-in-generic-args", `import "std/test";

function tally(): i32 {
  let seen: Map[string, test.TestOutcome] = map_new(4);
  seen = seen.insert("a", test.pass());
  return seen.len();
}

function main(): i32 {
  if (tally() != 1) {
    return 1;
  }
  return 0;
}
`},
	{"struct-lit-type-args", `struct Box[T] {
  val: T
}

struct Stack[T] {
  items: T[]
}

function take(b: Box[i64]): i64 {
  return b.val;
}

function main(): i32 {
  let b = Box[i64] { val: 42 };
  let s = Stack[i32] { items: [] };
  return (take(b) as i32) + s.items.len();
}
`},
	{"call-type-args", `function empty[T](): T[] {
  return [];
}

function main(): i32 {
  let xs = empty[i32]();
  return xs.len();
}
`},
	{"map-literal-and-inner-comments", `import "core/map";

struct Layer {
  writes: Map[string, i32],  // only the keys THIS layer changed
  parent: i32,
}

enum Verdict {
  Balanced,
  Unclosed(i32),  // opener at pos never closed
}

struct Empty {}

function store(): Layer {
  return Layer { writes: Map { }, parent: -1 };
}

function two(): Map[string, i32] {
  return Map { "a": 1, "b": 2 };
}

function verdict(n: i32): Verdict {
  if (n == 0) {
    return Balanced;
  }
  return Unclosed(n);
}

function main(): i32 {
  if (store().parent != -1) {
    return 1;
  }
  if (two().len() != 2) {
    return 2;
  }
  match (verdict(1)) {
    Balanced => {
      return 3;
    },
    Unclosed(_) => {}
  }
  let e: Empty = Empty {};
  return 0;
}
`},
	{"lowering-bare-block", `function main(): i32 {
let s: i32 = 0;
{
let t: i32 = 3;
s = t;
}
defer {
s = 0;
}
return s;
}
`},
	{"impl-keeps-self", `struct Box[T] {
xs: T[],
}
trait Maker {
function make(): Self;
}
trait Sized {
function size(self: Self): i32;
function same(self: Self, o: Self): Self;
}
impl Maker for Box[i32] {
function make(): Self {
return Box { xs: [] };
}
}
impl[T] Sized for Box[T] {
function size(self: Self): i32 {
return self.xs.len();
}
function same(self: Box[T], o: Self): Box[T] {
return o;
}
}
function main(): i32 {
return 0;
}
`},
	{"lowering-chained-assign", `function main(): i32 {
let a: i32 = 0;
let b: i32 = 0;
let c: i32 = 0;
a = b = c = 7;
return a + b + c;
}
`},
	{"lowering-place-assign", `struct P { f: i32 }
function main(): i32 {
let a: i32[] = [0, 0];
let p: P = P { f: 0 };
let x: i32 = 0;
a[0] = 1;
a[1] += 2;
p.f = 3;
x = a[0] = p.f = 4;
return x;
}
`},
	{"lowering-literal-match", `function grade(n: i32): string {
match (n) {
0 => { return "zero"; },
2..5 => { return "mid"; },
6..=9 => { return "high"; },
10 when n > 9 => { return "ten"; },
_ => { return "other"; },
}
}
function words(s: string): i32 {
match (s) {
"a" => { return 1; },
_ => { return 0; },
}
}
function pick(n: i32): i32 {
let v: i32 = match (n) {
0 => 5,
_ => 7,
};
return v;
}
function main(): i32 {
return grade(3).len() + words("a") + pick(0);
}
`},
	{"lowering-block-expression", `function id(n: i32): i32 {
return n;
}
function shapes(n: i32): i32 {
let stmts: i32 = { let t: i32 = n * 2; t + 1 };
let tail: i32 = { n };
let operand: i32 = { n + 1 } * 3;
let nested: i32 = { let t: i32 = { let q: i32 = n; q + 1 }; t * 2 };
let arg: i32 = id({ let t: i32 = n; t + 1 });
return stmts + tail + operand + nested + arg;
}
function real_lambda(n: i32): i32 {
let f: fn = (): i32 => { let t: i32 = n; return t + 1; };
return f();
}
function main(): i32 {
return shapes(1) + real_lambda(2);
}
`},
	{"pattern-or-alternatives", `enum Col { R, G, B }
function variant(c: Col, n: i32): i32 {
match (c) {
Col.R | Col.G => { return 1; },
Col.B => { return 2; },
}
}
function guarded(c: Col, n: i32): i32 {
match (c) {
Col.R | Col.G when n > 0 => { return 1; },
_ => { return 2; },
}
}
function lits(n: i32): i32 {
match (n) {
0 | 1 => { return 10; },
2..4 | 8..=9 => { return 20; },
_ => { return 30; },
}
}
function strs(s: string): i32 {
match (s) {
"a" | "b" | "c" => { return 1; },
_ => { return 0; },
}
}
function tup(t: (i32, i32)): i32 {
match (t) {
(1, 2) | (3, 4) => { return 1; },
(a, b) => { return a + b; },
}
}
function expr_form(c: Col): i32 {
let v: i32 = match (c) { Col.R | Col.G => 1, Col.B => 2 };
return v;
}
function main(): i32 {
return variant(Col.R, 1) + guarded(Col.R, 1) + lits(3) + strs("b") + tup((3, 4)) + expr_form(Col.B);
}
`},
	{"lowering-value-local-match", `struct P { x: i32, y: i32 }
enum W { Pr((i32, i32)), None }
function tup(t: (i32, i32)): i32 {
let v: i32 = match (t) {
(0, b) => b,
(a, b) => a + b,
};
return v;
}
function strct(p: P): i32 {
let v: i32 = match (p) {
P { x, y } when x > 0 => x + y,
_ => 0,
};
return v;
}
function block_arm(t: (i32, i32)): i32 {
let v: i32 = match (t) {
(0, b) => { let k: i32 = b * 2; k + 1 },
(a, b) => a + b,
};
return v;
}
function sub_pattern_stmt(w: W): i32 {
match (w) {
Pr((a, b)) => { return a + b; },
None => { return 0; },
}
}
function sub_pattern_expr(w: W): i32 {
let v: i32 = match (w) {
Pr((a, b)) => a + b,
None => 0,
};
return v;
}
function main(): i32 {
return tup((1, 2)) + strct(P { x: 1, y: 2 }) + block_arm((0, 3)) + sub_pattern_stmt(W.Pr((1, 2))) + sub_pattern_expr(W.None);
}
`},

	{"block-end-comments", `function f(x: i32): i32 {
  if (x > 0) {
    x = x + 1;
    // end of then
  } else if (x < -5) {
    x = 0;
    // end of middle
  } else {
    x = x - 1;
    // end of else
  }
  while (x > 10) { x = x - 1; }  // shrink
  for i in 0..3 {
    x = x + i;
    // end of for
  }
  match (x) {
    0 => {
      x = 1;
      // end of arm
    },
    _ => {}
  }
  return x;
  // end of f
}
function g(): i32 { return 1; }  // one
function main(): i32 { return f(3) + g(); }
`},
	{"list-written-across-lines", `struct S {
  a: i32,
  b: i32
}
struct T { a: i32 }
enum E {
  A,
  B(i32)
}
const NAMES: string[] = [
  "a", "b",
  "c"
];
function g(a: i32, b: i32): i32 {
  return a + b;
}
function main(): i32 {
  let s: S = S { a: 1,
    b: 2 };
  let t: S = S {
    ...s,
    a: 3
  };
  let xs: i32[] = [1,
    2, 3];
  let flat: i32[] = [1, 2];
  let n: i32 = g(xs[0],
    t.a + s.b + flat[0]);
  return n + NAMES.len() + xs
    .len();
}
`},
	{"binary-written-across-lines", `function g(a: i32, b: boolean): i32 {
  if (b) { return a; }
  return 0;
}
function main(): i32 {
  let a: i32 = 1;
  let s: string = "x"
    + "y" + a.to_string()
    + "z";
  let t: i32 = a +
    2;
  let u: i32 = a + t;
  if (a > 0 &&
      t > 0) { u = u + 1; }
  return g(a,
    a == 1
      || t == 2) + s.len() + u;
}
`},
	{"union-written-across-lines", `struct A { n: i32 }
struct B { n: i32 }
struct C { n: i32 }
struct D { n: i32 }
pub type Lead = A | B
  | C | D;
type Trail = A | B |
  C |
  D;
type Flat = A | B | C;
function main(): i32 {
  let l: Lead = C { n: 1 };
  let t: Trail = D { n: 2 };
  let f: Flat = A { n: 3 };
  return 0;
}
`},
	{"list-interior-comments", `struct S { a: i32, b: i32 }
const NAMES: string[] = [
  // group one
  "a", "b",
  // group two
  "c",  // just c
];
function g(a: i32, b: i32): i32 {
  return a + b;
}
function main(): i32 {
  let s: S = S {
    // the a field
    a: 1,
    b: 2,  // trailing
  };
  let t: S = S {
    ...s,
    // override
    a: 3,
  };
  let xs: i32[] = [
    1, 2,
    // then
    3,
  ];
  g(
    xs[0],  // first
    // second
    t.a,
  );
  return g(
    s.a,
    // nested
    g(
      1,
      // innermost
      2,
    ),
  );
}
`},

	{"string-control-bytes", "function main(): i32 {\n" +
		"let s: string = \"a\\x00b\\x1fc\\x7fd\\0e\";\n" +
		"let f: string = f\"pre\\x01{s}\";\n" +
		"return s.len() + f.len();\n" +
		"}\n"},

	{"nested-fstring-placeholder", "import \"std/i32\";\nimport \"std/string\";\n\nfunction main(): i32 {\n  let x = 5;\n  let s = f\"a {f\"b {x}\"} c {\"}{\"}\";\n  return s.len();\n}\n"},

	{"string-unicode-escapes", "function main(): i32 {\n" +
		"let s: string = \"\\u{7}\\u{e9}é\\u{1F600}\";\n" +
		"let f: string = f\"\\u{e9}{s}\\u{7B}\";\n" +
		"return s.len() + f.len();\n" +
		"}\n"},

	{"use-sugar", `function maybe_double(n: i32, cb: (i32) => Option[i32]): Option[i32] {
return cb(n + n);
}
function chain(start: i32): Option[i32] {
// bind the doubled value
use a <- maybe_double(start);  // trailing

let k: i32 = a * 2;
use b: i32 <- maybe_double(k);
return Some(b + 1);
}
function main(): i32 {
match (chain(1)) {
Some(v) => { return v; },
None => { return 0; }
}
}
`},
	{"lambda-arrow-depth", `struct P { x: i32, y: i32 }
function chain(start: i32): i32 {
let f = (n: i32): i32 => {
let a: i32 = n + 1;
return a * 2;
};
let g = (P { x, y }: P): i32 => x * 10 + y;
let h = (n: i32) => { let t: i32 = n; return t + 1; };
let k = (): i32 => { return 7; }();
while (true) {
let inner = (m: i32): i32 => {
let q = (z: i32): i32 => {
let w: i32 = z;
return w;
};
return q(m);
};
return inner(start) + f(start) + g(P { x: 1, y: 2 }) + h(1) + k;
}
return 0;
}
function main(): i32 { return chain(1); }
`},
	{"discard-bindings", `struct P { x: i32, y: i32 }
function pair(): (i32, i32) { return (1, 2); }
function constant(_: i32, _: string): i32 { return 7; }
function main(): i32 {
let _ = 99;
let _ = 98;
let (a, _) = pair();
let (_, b) = pair();
let ((c, _), _) = ((3, 4), 5);
let P { x: _, y } = P { x: 1, y: 6 };
let g = (_: i32, n: i32): i32 => n;
let xs: (i32, i32)[] = [(1, 2)];
let s: i32 = 0;
for (k, _) in xs { s = s + k; }
return a + b + c + y + s + g(0, 1) + constant(1, "x");
}
`},
	{"lambda-return-annotation", `function main(): i32 {
let tup = (): (string, i32) => { return ("ab", 7); };
let grp = (): (i32) => { return 7; };
let fnp = (p: i32): ((i32) => i32) => (q: i32) => p + q;
let fnb = (p: i32): (i32) => i32 => (q: i32) => p + q;
let fnt = (p: i32): ((i32) => (i32, i32)) => (q: i32) => (p, q);
let r = fnt(1)(2);
return tup().1 + grp() + fnp(1)(2) + fnb(1)(2) + r.0 + r.1;
}
`},
	{"unit-value-and-type", `function sink(u: void): i32 { return 0; }
function fallible(): Result[void, i32] { return Ok(()); }
function thunk(): () => i32 { return (): i32 => { return 1; }; }
function main(): i32 {
let u: void = ();
let v = ();
let r: Result[void, i32] = fallible();
let o: Option[void] = None;
let t: (void, i32) = ((), 1);
return sink(u) + sink(v) + sink(t.0) + thunk()() + t.1;
}
`},
	{"generic-arg-grouping-parens", `function sink(p: Result[(i32), i32]): i32 { return 0; }
function nested(p: Result[Option[(i32)], i32]): i32 { return 0; }
function fnarg(p: Result[((i32) => i32), i32]): i32 { return 0; }
function fnsuf(p: Result[((i32) => i32)[], i32]): i32 { return 0; }
function tup(p: Result[(i32, i32), i32]): i32 { return 0; }
function multi(p: Result[(Result[i32, i32]), i32]): i32 { return 0; }
function multifn(p: Result[((Result[i32, i32]) => i32), i32]): i32 { return 0; }
function nestedfn(p: Result[(Option[(i32) => i32])[], i32]): i32 { return 0; }
function generictup(p: Result[(Option[i32], i32), i32]): i32 { return 0; }
function main(): i32 { return 0; }
`},
	{"resource-declaration", `@import("wasi:io/poll@0.2.0", "pollable")
resource Pollable;

resource Conn;

@import("wasi:io/poll@0.2.0", "[method]pollable.ready")
function ready(h: borrow Pollable): boolean;

function hold(c: own Conn): i32 { return 0; }

function main(): i32 { return 0; }
`},
}

func typeChecks(src string) error {
	prog, _, err := modload.LoadSource(src)
	if err != nil {
		return err
	}
	if err := constfold.Fold(prog, nil); err != nil {
		return err
	}
	_, err = checker.Check(prog)
	return err
}

func fmtOutputTypeChecks(t *testing.T, label, src, formatted string) {
	t.Helper()
	if typeChecks(src) != nil {
		return
	}
	if err := typeChecks(formatted); err != nil {
		t.Errorf("%s: input compiles but its formatted output does not: %v\n%s", label, err, formatted)
	}
}

func TestSelfHostFmtNativeParityX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := writeSelfHostAsmProject(t)
	copySelfHostDriver(t, dir, "fern.fern")
	fernBin := buildSelfHostBin(t, gcc, dir, "fern.fern", "fern")

	runDriver := func(t *testing.T, args ...string) ([]byte, int) {
		t.Helper()
		if !slices.Contains(args, "-target") {
			args = append([]string{"-target", "x86-64-linux", "-emit", "asm"}, args...)
		}
		cmd := runX86_64Bin(runner, fernBin, args...)
		out, _ := cmd.Output()
		return out, cmd.ProcessState.ExitCode()
	}

	for _, tc := range fmtParityCases {
		t.Run(tc.name, func(t *testing.T) {
			prog, err := goparser.Parse(tc.src)
			if err != nil {
				t.Fatalf("native parse: %v", err)
			}
			want := goprinter.Format(prog)

			path := filepath.Join(dir, tc.name+".fern")
			if err := os.WriteFile(path, []byte(tc.src), 0o644); err != nil {
				t.Fatalf("write src: %v", err)
			}
			got, code := runDriver(t, "-fmt", path)
			if code != 0 {
				t.Fatalf("self-host -fmt exited %d, want 0 (out: %s)", code, got)
			}
			if string(got) != want {
				t.Errorf("self-host -fmt differs from native -fmt\n--- native ---\n%s\n--- self-host ---\n%s", want, got)
			}

			fmtOutputTypeChecks(t, "native -fmt", tc.src, want)
			fmtOutputTypeChecks(t, "self-host -fmt", tc.src, string(got))

			outPath := filepath.Join(dir, tc.name+"_fmt.fern")
			if err := os.WriteFile(outPath, got, 0o644); err != nil {
				t.Fatalf("write formatted: %v", err)
			}
			got2, code2 := runDriver(t, "-fmt", outPath)
			if code2 != 0 {
				t.Fatalf("second self-host -fmt exited %d, want 0", code2)
			}
			if string(got2) != string(got) {
				t.Errorf("self-host -fmt is not idempotent\n--- first ---\n%s\n--- second ---\n%s", got, got2)
			}
		})
	}
}

func TestSelfHostFmtKeepsCStyleFor(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	if len(runner) != 0 {
		t.Skip("CLI driver test runs only natively (argv paths)")
	}
	dir := writeSelfHostAsmProject(t)
	copySelfHostDriver(t, dir, "fern.fern")
	fernBin := buildSelfHostBin(t, gcc, dir, "fern.fern", "fern")

	const src = `function main(): i32 {
  let sum: i32 = 0;
  for (let i: i32 = 1; i <= 10; i = i + 1) {
    sum = sum + i;
  }
  return sum;
}
`
	path := filepath.Join(dir, "keep_c_for.fern")
	if err := os.WriteFile(path, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	out, err := exec.Command(fernBin, "-fmt", path).Output()
	if err != nil {
		t.Fatalf("self-host -fmt: %v", err)
	}
	got := string(out)
	if strings.Contains(got, "__forc_") {
		t.Errorf("formatted output leaks the desugar's synthesised flag:\n%s", got)
	}
	if !strings.Contains(got, "for (let i: i32 = 1; i <= 10; i = i + 1)") {
		t.Errorf("the C-style for header did not survive formatting:\n%s", got)
	}
	if got != src {
		t.Errorf("already-formatted source was rewritten:\n--- in ---\n%s\n--- out ---\n%s", src, got)
	}
}

const fmtLambdaAsWrittenSrc = `function main(): i32 {
  let inc: (i32) => i32 = (x: i32) => x + 1;
  let three: () => i32 = (): i32 => 3;
  let fs: (() => i32)[] = [() => 1, (): i32 => 2];
  return inc(1) + three() + fs[0]() + fs[1]();
}
`

func TestSelfHostFmtKeepsLambdaAsWritten(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	if len(runner) != 0 {
		t.Skip("CLI driver test runs only natively (argv paths)")
	}
	dir := writeSelfHostAsmProject(t)
	copySelfHostDriver(t, dir, "fern.fern")
	fernBin := buildSelfHostBin(t, gcc, dir, "fern.fern", "fern")
	path := filepath.Join(dir, "lambda_as_written.fern")
	if err := os.WriteFile(path, []byte(fmtLambdaAsWrittenSrc), 0o644); err != nil {
		t.Fatal(err)
	}
	out, err := exec.Command(fernBin, "-fmt", path).Output()
	if err != nil {
		t.Fatalf("self-host -fmt: %v", err)
	}
	if got := string(out); got != fmtLambdaAsWrittenSrc {
		t.Errorf("already-formatted source was rewritten:\n--- in ---\n%s\n--- out ---\n%s", fmtLambdaAsWrittenSrc, got)
	}
}

var selfHostFmtKnownDivergences = map[string]string{}

func TestSelfHostFmtCorpusParityX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	root := formatterCorpusRoot(t)
	dir := writeSelfHostAsmProject(t)
	copySelfHostDriver(t, dir, "fern.fern")
	fernBin := buildSelfHostBin(t, gcc, dir, "fern.fern", "fern")

	files := corpusFernFiles(t, root)
	diverged := map[string]bool{}
	for _, rel := range files {
		src, err := os.ReadFile(filepath.Join(root, rel))
		if err != nil {
			t.Fatal(err)
		}
		prog, err := goparser.Parse(string(src))
		if err != nil {
			continue
		}
		want := goprinter.Format(prog)
		cmd := runX86_64Bin(runner, fernBin, "-fmt", filepath.Join(root, rel))
		got, err := cmd.Output()
		if err != nil {
			t.Errorf("%s: self-host -fmt failed: %v", rel, err)
			continue
		}
		if string(got) == want {
			continue
		}
		diverged[rel] = true
		if _, known := selfHostFmtKnownDivergences[rel]; !known {
			t.Errorf("%s: self-host -fmt differs from native and is not a known divergence\n%s",
				rel, firstDiffLines("native", want, "self-host", string(got)))
		}
	}
	for rel, why := range selfHostFmtKnownDivergences {
		if !diverged[rel] {
			t.Errorf("%s is listed as a known divergence (%s) but the two formatters now agree — delete the row", rel, why)
		}
	}
}

func TestSelfHostFmtDiffCorpusParityX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	root := formatterCorpusRoot(t)
	dir := writeSelfHostAsmProject(t)
	copySelfHostDriver(t, dir, "fern.fern")
	fernBin := buildSelfHostBin(t, gcc, dir, "fern.fern", "fern")

	for _, rel := range corpusFernFiles(t, root) {
		if _, known := selfHostFmtKnownDivergences[rel]; known {
			continue
		}
		path := filepath.Join(root, rel)
		src, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		prog, err := goparser.Parse(string(src))
		if err != nil {
			continue
		}
		want := goprinter.UnifiedDiff(string(src), goprinter.Format(prog), path, path)
		out, err := runX86_64Bin(runner, fernBin, "-fmt", "-d", path).Output()
		if err != nil {
			var ee *exec.ExitError
			if !errors.As(err, &ee) || ee.ExitCode() != 1 {
				t.Errorf("%s: self-host -fmt -d failed: %v", rel, err)
				continue
			}
		}
		if got := string(out); got != want {
			t.Errorf("%s: self-host -fmt -d differs from native's\n%s", rel, firstDiffLines("native", want, "self-host", got))
		}
	}
}

func repoRootFromTest(t *testing.T) string {
	t.Helper()
	root, err := corpus.RepoRoot()
	if err != nil {
		t.Fatal(err)
	}
	return root
}

func formatterCorpusRoot(t *testing.T) string {
	t.Helper()
	root := repoRootFromTest(t)
	dst := t.TempDir()
	if err := corpus.Materialize(root, dst, corpus.Sources); err != nil {
		t.Fatal(err)
	}
	return dst
}

func corpusFernFiles(t *testing.T, root string) []string {
	t.Helper()
	files, err := corpus.Files(root, corpus.Sources)
	if err != nil {
		t.Fatal(err)
	}
	return files
}

func firstDiffLines(wantName, want, gotName, got string) string {
	w := strings.Split(want, "\n")
	g := strings.Split(got, "\n")
	for i := 0; i < len(w) && i < len(g); i++ {
		if w[i] == g[i] {
			continue
		}
		lo := i - 2
		if lo < 0 {
			lo = 0
		}
		hi := i + 3
		var b strings.Builder
		fmt.Fprintf(&b, "--- %s, first difference at line %d ---\n", wantName, i+1)
		for j := lo; j < hi && j < len(w); j++ {
			b.WriteString(w[j] + "\n")
		}
		fmt.Fprintf(&b, "--- %s ---\n", gotName)
		for j := lo; j < hi && j < len(g); j++ {
			b.WriteString(g[j] + "\n")
		}
		return b.String()
	}
	return "outputs differ only in length"
}
