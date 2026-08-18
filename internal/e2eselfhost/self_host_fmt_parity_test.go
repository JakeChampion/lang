package e2eselfhost

import (
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/checker"
	"github.com/jakechampion/lang/internal/constfold"
	"github.com/jakechampion/lang/internal/modload"
	goparser "github.com/jakechampion/lang/internal/parser"
	goprinter "github.com/jakechampion/lang/internal/printer"
)

// fmtParityCases pin #6762: both compilers accept `-fmt`, and until now they
// formatted the same source DIFFERENTLY — four-space indent against native's
// two, and every binary and unary expression parenthesised against native's
// precedence-driven minimum, so `if (x > 0)` reprinted as `if ((x > 0))`.
// Nothing compared the two, which is why it stayed invisible.
//
// It is not a cosmetic gap. `make fmt-check` gates every examples/*.fern file
// against NATIVE's formatter, so under a self-host-only toolchain a single
// `fern -fmt -w` would rewrite the tree into a form the project's own gate
// rejects — the "the two frontends disagree about what the language is" shape
// of #6576, one level up, at its written form.
//
// #6769 added the other half: comments and blank lines. Placing them needs a
// source line on every statement, so five Stmt kinds grew one and parse_stmt
// stamps it; the cases at the end of the list are that half's.
//
// #6773 closed four of the five parser-side losses that came after: `str` was
// canonicalised to `string` and a function type coarsed to the flat `fn` tag,
// both now held by Par.verbatim — a parse mode only `-fmt` selects, since those
// canonicalisations are exactly what the compile path's `type_name == "string"`
// / `== "fn"` dispatch sites read. Struct / enum / alias `pub` and both
// destructuring forms needed no parser work at all: the data was already on the
// node and the printer did not read it.
//
// #6773's last item closed the `trait` block: the compile path keeps two
// DERIVED views of one and neither can be printed from, so the written form is
// retained beside the Module — the way the comment and blank-line maps already
// ride beside it, rather than as a field on Module that 101 literals would have
// to carry and none would read.
//
// #6783 did the same for `impl` blocks, which were desugared into free-standing
// receiver methods on the way in.
//
// Corpus-wide, `fern -fmt` and `fern-selfhost -fmt` agree byte for byte on 210
// of the 244 files under examples/ + internal/stdlib, against 0 before #6762.
// That count is unchanged by #6783 even though every impl block now prints
// correctly: the files carrying one also carry the LAST structural divergence,
// `else { if … }` collapsing to `else if` (#6779) — `core/cmp.fern` differs by
// nothing else at all. The other remaining cause is an ARROW lambda: native
// reprints `() => e` as `function(): T { return e; }`, filling in a return type
// from the callee's signature that the self-host printer has no way to know.
var fmtParityCases = []struct {
	name string
	src  string
}{
	// Precedence is the substantive half — one wrong level silently
	// reassociates. `(n & (n - 1)) == 0` is the trap native's own table
	// documents: bitwise sits BELOW the comparison family in Fern's grammar,
	// so an and-then-compare that loses its parens re-parses as ANDing a
	// number with a boolean.
	{"precedence-arith", `function main(): i32 {
var a: i32 = 1;
var b: i32 = 2;
var c: i32 = 3;
var t1: i32 = a + b * c;
var t2: i32 = (a + b) * c;
var t3: i32 = a - b - c;
var t4: i32 = a - (b - c);
var t5: i32 = a * b / c % a;
var t6: i32 = a << b >> c;
return t1 + t2 + t3 + t4 + t5 + t6;
}
`},
	// A NESTED payload (`A(Ok2(n))`) and a LITERAL one (`V(0)`) are both
	// consumed by the lowering — the first into a merged arm plus an inner
	// match on a `__nest` temp, the second into a synthetic binder plus a
	// guard. Neither formatter had anything left to reprint but that
	// lowering, so `-fmt -w` rewrote the file into the desugar. Nothing in
	// the parity CORPUS uses either spelling, which is why the gate stayed
	// green through it; this fixture is what covers them.
	// `if let` and a match in EXPRESSION position are both parsed into
	// something else — a two-arm match with a wildcard else, and a 0-arg IIFE
	// — and the self-host reprinted those lowerings: the `if let` came back as
	// `match (o) { Sm(n) => {…}, _ => {} }`, and an expression match whose arms
	// nest came back as the whole `function(): i32 { … }()`. Nothing in the
	// parity CORPUS spells either (`if let` appears only inside string
	// literals and comments in the compiler's own sources), so these fixtures
	// are the only thing covering them.
	// A tuple- or struct-SCRUTINEE match desugars to a done-flag chain and
	// leaves no StmtMatch behind, so the self-host had only the chain to
	// reprint — `if (true) { var __sm4_5_d = false; … }` over the user's
	// match (#7065). Independent of pattern nesting: the plainest all-binder
	// `P { x, y }` reproduced it. Nothing in the parity CORPUS spells either
	// scrutinee, so these fixtures are the only cover.
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
function guarded(p: P): i32 {
match (p) { P { x, y } when x > y => { return x; }, P { x, y } => { return y; } }
}
function at_bound(p: P): i32 {
match (p) { w @ P { x, y } => { return w.x + x + y; } }
}
function main(): i32 {
return plain(P { x: 1, y: 2 }) + lit(P { x: 0, y: 3 }) + rename(Q { a: 1, b: 2, c: 3 })
+ nested(W { e: E.A(In.Ok2(4)), n: 5 }) + guarded(P { x: 9, y: 1 }) + at_bound(P { x: 1, y: 1 });
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
var v = match (o) { A(Ok2(n)) => n, A(Err2(n)) => 0 - n, _ => 0 };
return v;
}
function guarded_expr(o: Opt): i32 {
var v = match (o) { Sm(n) when n > 2 => n, Sm(n) => 0 - n, Nn => 0 };
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
	{"precedence-bitwise-vs-compare", `function is_pow2(n: i32): boolean {
return (n & (n - 1)) == 0;
}
function main(): i32 {
var n: i32 = 8;
var x: boolean = n & 1 == 0;
var y: boolean = (n | 2) != 0;
var z: boolean = n ^ 1 > 0;
if (is_pow2(n) && x && y || z) {
return 1;
}
return 0;
}
`},
	{"precedence-logical-and-unary", `function main(): i32 {
var a: boolean = true;
var b: boolean = false;
var n: i32 = 5;
var p: boolean = a && b || !a;
var q: boolean = !(a && b);
var r: i32 = 0 - n;
var s: i32 = 0 - (n + 1);
if (p && q) {
return r + s;
}
return 0;
}
`},
	// Postfix positions bind tighter than every operator, so an operand that
	// is a binary or unary expression keeps its parens under a call, an
	// index, a slice or a field read.
	{"precedence-postfix-receivers", `struct P { x: i32, y: i32 }
function main(): i32 {
var s: string = "hello";
var xs: i32[] = [1, 2, 3];
var p: P = P { x: 1, y: 2 };
var a: i32 = (s + "x").len();
var b: i32 = xs[p.x + 1];
var c: string = s[p.x:p.x + 2];
var d: i32 = (p.x + p.y) * 2;
return a + b + c.len() + d;
}
`},
	// Indentation depth: every nesting level differed by two spaces per level,
	// so a deeply nested block is where the two formatters were furthest apart.
	{"indent-nesting", `function main(): i32 {
var total: i32 = 0;
var i: i32 = 0;
while (i < 4) {
if (i > 1) {
var j: i32 = 0;
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
	// #6769: comments and blank lines. Leading comments attach to the statement
	// below them, a comment on a single-line statement's own line goes inline
	// after two spaces, and one blank line survives where the author left one
	// (runs collapse to one, and a blank just inside `{` is dropped).
	{"comments-and-blanks", `// file header, above the import
import "std/i32";

// doc comment for f
function f(a: i32): i32 {
  // leading comment on the var
  var t: i32 = a + 1;

  // leading comment on the if
  if (t > 0) {
    return t;  // trailing on a return
  }
  return 0;
}

function g(): i32 {
  var x: i32 = 1;  // trailing on a var


  var y: i32 = 2;
  return x + y;
}
`},
	// A comment after a block's LAST statement belongs to whatever follows it at
	// the OUTER indent, not to the block it trails — the cursor is monotonic, so
	// getting this wrong relocates the comment into an unrelated body.
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
	// Declarations emit in SOURCE order, not grouped by kind: a monotonic comment
	// cursor would otherwise drain a comment against whichever declaration was
	// printed first and reattach it to an unrelated one.
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
	// The modifiers and the shapes a formatter must not drop: `pub` on a
	// function, type parameters, an aliased import, a cast, a void `return;`.
	// The unexported struct pins the other half of the visibility rule that
	// `visibility-on-types` covers: an absent `pub` must stay absent.
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
	// #6773 item 1: `str` is a borrowed VIEW and `string` owned storage, so
	// erasing one to the other at the parse boundary means a reformat rewrites
	// the program's ownership. The return position is where the corpus has
	// almost all of them (every `std/string` trimmer returns `str`).
	{"written-str-spelling", `pub function head(s: string): str {
return s[0:1];
}
pub function tag(s: str, t: str): i32 {
return s.len() + t.len();
}
struct View { text: str, label: string }
function main(): i32 {
var t: str = "  hi  ";
var u: string = "owned";
return t.len() + u.len();
}
`},
	// #6773 item 2: the flat `fn` tag names neither what the callback takes nor
	// what it returns, so a formatted signature stopped stating its own
	// contract. Zero-arg, multi-arg, array-returning and qualified-return forms
	// all appear in the stdlib.
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
	// #6773 item 4: `pub` on a struct / enum / alias. Only FuncDecl.is_pub was
	// being read, so a formatted library file kept its function exports and
	// silently lost every type export.
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
	// #6773 item 5: both destructuring forms parse to a StmtVar — the tuple one
	// with its bindings comma-joined, the struct one additionally tagged on
	// type_name — and printing either as a plain `var` emitted `var a,b = …`,
	// which is not syntax the parser accepts back.
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
	// Surfaced by the two cases above: FuncDecl.type_params carried only the
	// BOUNDED parameters (the unbounded ones are erased and the monomorphiser
	// has no use for their names), so a formatted `each[T, U]` lost both and
	// left every T and U unbound. A METHOD's parameters are written before its
	// receiver — the spelling native emits, which the self-host parser did not
	// accept, so its own formatted output did not re-parse.
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
	// The rest of the loop family. The C-style form is #6771's, covered by the
	// `c-style-for` cases below; these are the ones with no NATIVE node (#6770),
	// where native wrote its own expansion — `__range_hi_1`, `__foreach_iter_1`,
	// the map iterator's cursor calls — back over the user's source.
	{"for-range", `function hi(): i32 {
  return 4;
}

function main(): i32 {
  var t: i32 = 0;
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
  var t: i32 = 0;
  for x in a {
    t = t + x;
  }
  for (k, v) in m {
    t = t + v + k.len();
  }
  return t;
}
`},
	// A loop label and the `break` / `continue` naming it are one fact written
	// twice: dropping either half retargets the jump at the innermost loop, so
	// the reformatted program leaves a different loop than the one written.
	{"loop-labels", `function main(a: i32[]): i32 {
  var t: i32 = 0;
  outer: while (t < 10) {
    inner: for (var i: i32 = 0; i < 3; i = i + 1) {
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
`},
	// #6773 item 3: a `trait` block reached the printer only as the two DERIVED
	// views the compile path keeps — TraitReq's abstract method set (names
	// simplified past any `mod.` qualifier) and TraitDefault's bodies — so the
	// declaration vanished from formatted output entirely and every `impl` of
	// one then named a trait the file no longer declared. The written form is
	// retained beside the Module now. Generic parameters, a supertrait list, an
	// empty block and a bodied default are each a piece the derived views drop.
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
	// #6783: an `impl` block is desugared on the way in — each method becomes an
	// ordinary receiver method on Module.funcs and the block reduces to the
	// ImplInfo the E021 conformance walk reads — so a formatted file re-declared
	// each method as a plain receiver method and the `impl` it belonged to was
	// gone, leaving the type no longer implementing the trait. The block is
	// retained beside the Module now, the way traits are.
	//
	// The methods print DESUGARED, which is also what native emits: `Self` reads
	// as the concrete impl type and the `self` receiver goes back to being the
	// first parameter. An empty impl adopts the trait's defaults and states no
	// methods; a parametric one states its bounds once, on the block.
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

impl Box[i32] {
function zero(): Box[i32] {
return Box { item: 0 };
}
function get(self: Self): i32 {
return self.item;
}
}

function main(): i32 {
return 0;
}
`},
	// A comment inside a trait or impl method body has to drain against the
	// SHARED comment cursor. Printing those bodies with a fresh one dropped
	// every such comment and flushed it at the end of the file, relocating it
	// onto an unrelated declaration.
	{"comments-inside-impl-bodies", `trait Display {
function to_string(self: Self): string;
}

impl Display for boolean {
function to_string(self: Self): string {
// leading comment inside the method
var s: string = "t";  // trailing on a statement
return s;
}
}
`},
	{"decls-and-literals", `struct P { x: i32, y: i32 }
function mk(x: i32, y: i32): P {
return P { x: x, y: y };
}
function main(): i32 {
var p: P = mk(1, 2);
var q: P = P { ...p, y: p.y + 1 };
var xs: i32[] = [1, 2, 3, 4];
var s: string = "a" + "b" + "c";
return p.x + q.y + xs[2] + s.len();
}
`},
	// #6771: the C-style `for` is desugared at parse time, so the printer sees
	// an `if (true)` wrapping a flag-driven `while` and used to print exactly
	// that — writing the desugar, synthesised `__forc_<line>_<col>` and all,
	// back over the user's loop. Native keeps the loop, so comparing against it
	// is what catches the leak; idempotence cannot, since the second pass has
	// nothing left to desugar.
	//
	// All three header clauses are optional, and an omitted one takes a
	// different path through the desugar, so each is covered: an empty init, an
	// empty step, and a `continue` (which is the reason the desugar runs STEP
	// at the TOP of the body rather than the bottom).
	// #6779: `else if` and `else { if … }` parse to the same one-element
	// else_body, and the printer rendered every such body as a chain — so the
	// block form was rewritten into the chained one. The reverse never
	// happened, which is why only one direction is asserted by construction:
	// both spellings must come back as they were written.
	//
	// `indent-nesting` above has an if/else but no NESTED else, which is why
	// nothing caught this. The third function is the case that is not merely
	// cosmetic: a binding declared before the inner `if` is scoped to the else
	// block, and the collapse has nowhere to put it.
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
    var x: i32 = c + 1;
    if (x == 3) {
      return 2;
    }
  }
  return 0;
}
`},
	{"c-style-for", `function main(): i32 {
var sum: i32 = 0;
for (var i: i32 = 1; i <= 10; i = i + 1) {
sum = sum + i;
}
return sum;
}
`},
	{"c-style-for-optional-clauses", `function main(): i32 {
var t: i32 = 0;
var j: i32 = 0;
for (; j < 3; j = j + 1) {
t = t + 1;
}
for (var m: i32 = 0; m < 4; ) {
m = m + 1;
t = t + 1;
}
for (var n: i32 = 0; n < 5; n = n + 1) {
if (n == 2) {
continue;
}
t = t + 1;
}
return t;
}
`},
	// The recogniser keys on the reserved `__forc_` flag name, so a hand-written
	// block of the same SHAPE must still print as itself. Without this the
	// reconstruction would rewrite ordinary code into a loop that never existed
	// — the bug's mirror image.
	{"if-true-while-is-not-a-for", `function main(): i32 {
var acc: i32 = 0;
if (true) {
var q: i32 = 1;
var r = true;
while (true) {
acc = acc + q;
break;
}
}
return acc;
}
`},
	// #6802 / #6803: the rows where a formatter emitted source that no longer
	// COMPILES, which is why fmtOutputTypeChecks below exists. Each was a live
	// divergence: the self-host dropped a `when` guard (two arms then collide,
	// E028), printed `?` as a prefix `try_` (E001), and wrote a match/if
	// expression back as its IIFE desugar carrying the return-type TAG the
	// desugar guessed; native dropped a `Map { … }` value outright
	// (unparseable), leaked its internal `__discard_` name, invented `: void`
	// over an arrow lambda, and canonicalised hex to decimal. `own` and
	// `((string) => string)[]` were found by the type-check property itself —
	// one drops a checked modifier (E053), the other loses parens the grammar
	// needs, since `(string) => string[]` is ONE function returning an array.
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
  var v: i32 = first(xs)?;
  return Ok(v + 1);
}

function byte_count(s: string): i32 {
  var bs: [u8] = s.as_bytes();
  return bs.len();
}

fip function consume(own arr: i32[]): i32[] {
  arr = arr.with(0, 1);
  return arr;
}

function apply_all(fs: ((string) => string)[], seed: string): string {
  var acc: string = seed;
  for i in 0..fs.len() {
    acc = fs[i](acc);
  }
  return acc;
}

function mask(): i32 {
  return 0xFF00 | 0xdead;
}

function drop_first(): i32 {
  let (_, b) = (1, 2);
  return b;
}

function widen(kk: i32): string {
  var ctor: string = if (kk == 1) { "wide" } else { "narrow" };
  return ctor;
}

function main(): i32 {
  var fs: ((string) => string)[] = [(s: string) => s + "!"];
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
  var s: string = match (o) { Some(v) => v, None => "none" };
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
	// A parametrised trait bound: the self-host's type-param scan consumed the
	// `[i32]` and kept only the base name, so `I: iter.Iterator[i32]` came back
	// as `I: iter.Iterator` — a weaker bound the checker then refuses.
	{"parametrised-trait-bound", `import "core/iter";

pub function total[I: iter.Iterator[i32]](it: I): i32 {
  return iter.sum(it);
}

function main(): i32 {
  return 0;
}
`},
	// A module-QUALIFIED generic argument. The self-host's generic-arg
	// reconstruction broke on the `.`, truncating the type and corrupting the
	// whole `var` into a StmtUnknown — which `-fmt` wrote back as
	// `/*unknown-stmt:missing = in var*/`, destroying the statement.
	{"qualified-type-in-generic-args", `import "std/test";

function tally(): i32 {
  var seen: Map[string, test.TestOutcome] = map_new(4);
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
	// #6812's two construction-site forms. Written type args must survive
	// `-fmt`: dropping them re-infers the literal, so `Box[i64]` silently
	// becomes `Box[i32]`, `Stack[i32] { items: [] }` stops compiling
	// altogether (E040), and `empty[i32]()` formatted to `empty()` — the
	// formatter deleting the syntax the diagnostic recommends. These were a
	// native-only list until the self-host grew a written-form carrier for
	// both (#6802): the type args ride on the literal's type name and on the
	// callee's written name, which is where its printer already reads from.
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
  var b = Box[i64] { val: 42 };
  var s = Stack[i32] { items: [] };
  return (take(b) as i32) + s.items.len();
}
`},
	{"call-type-args", `function empty[T](): T[] {
  return [];
}

function main(): i32 {
  var xs = empty[i32]();
  return xs.len();
}
`},
	// #6802's remaining rows. A `Map { … }` literal desugars to
	// `map_new(8).insert(…)`, which states no K/V — so a formatted
	// `var m: Map[string, i32] = Map { }` came back as `map_new(8)` and
	// stopped compiling (E043). A comment written INSIDE a struct or enum
	// forces the multi-line block form in both formatters; printing the
	// one-liner instead left every such comment queued and re-emitted it
	// above the NEXT declaration, where it documents the wrong thing.
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
  var e: Empty = Empty {};
  return 0;
}
`},
}

// typeChecks reports whether src is a program the checker accepts, running the
// same load → constfold → check chain `fern -check` does.
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

// fmtOutputTypeChecks is the parity gate's third property, beside byte-parity
// against native and self-host idempotence: formatting a program that compiles
// must yield a program that still compiles.
//
// The two older properties are both blind to a desugar leak. Byte-parity only
// catches one where native happens to be right, and #6803 is the list of shapes
// where it was not. Idempotence catches nothing at all here — the self-host
// formatter is perfectly stable on its own broken output, re-emitting the same
// unparseable `Layer { writes: , … }` or the same `try_first(xs)` every pass.
//
// Every correctness row in #6802 and #6803 fails this directly, and two more
// were found by adding it: a dropped `own` (E053 on the `fip` sort helpers) and
// a function-typed array element losing its parens.
//
// Conditional on the INPUT compiling, because many cases above are printer
// fixtures rather than programs — `precedence-bitwise-vs-compare` is `n & 1 ==
// 0` precisely so a lost paren shows up, and that is `i32 & boolean`. Those
// still carry their byte-parity and idempotence assertions.
func fmtOutputTypeChecks(t *testing.T, label, src, formatted string) {
	t.Helper()
	if typeChecks(src) != nil {
		return
	}
	if err := typeChecks(formatted); err != nil {
		t.Errorf("%s: input compiles but its formatted output does not: %v\n%s", label, err, formatted)
	}
}

// TestSelfHostFmtNativeParityX86_64 formats each case with BOTH formatters and
// compares bytes, then re-formats the self-host's own output and compares that
// too. The second half is the property native already tests
// (internal/printer/idempotence_test.go) and the self-host did not: a formatter
// that is byte-equal to native but unstable still rewrites a file on every
// pass.
func TestSelfHostFmtNativeParityX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	if len(runner) != 0 {
		t.Skip("CLI driver test runs only natively (argv paths)")
	}
	dir := writeSelfHostAsmProject(t)
	copySelfHostDriver(t, dir, "fern.fern")
	fernBin := buildSelfHostBin(t, gcc, dir, "fern.fern", "fern")

	runDriver := func(t *testing.T, args ...string) ([]byte, int) {
		t.Helper()
		if !slices.Contains(args, "-target") {
			args = append([]string{"-target", "x86-64-linux", "-emit", "asm"}, args...)
		}
		cmd := exec.Command(fernBin, args...)
		out, _ := cmd.Output()
		return out, cmd.ProcessState.ExitCode()
	}

	for _, tc := range fmtParityCases {
		t.Run(tc.name, func(t *testing.T) {
			// Native side: the same printer.Format the `fern -fmt` CLI calls.
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

			// Both outputs must still be programs. Stated per-formatter rather
			// than only on `got`, so a case where the two AGREE on something
			// broken still fails.
			fmtOutputTypeChecks(t, "native -fmt", tc.src, want)
			fmtOutputTypeChecks(t, "self-host -fmt", tc.src, string(got))

			// Idempotence: formatting the formatted output is a fixed point.
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

// TestSelfHostFmtKeepsCStyleFor states the property #6771 names, independently
// of native: formatting must not write a compiler-synthesised name into the
// user's source.
//
// The parity cases above catch the leak by comparing against native, which is
// the stronger check while native is right. This one survives native
// regressing — and it is the assertion a reader of #6771 would write, since
// `__forc_` appearing in formatted output is the bug, whatever native does.
// #6770 is the same class on native's side, for the range form.
func TestSelfHostFmtKeepsCStyleFor(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	if len(runner) != 0 {
		t.Skip("CLI driver test runs only natively (argv paths)")
	}
	dir := writeSelfHostAsmProject(t)
	copySelfHostDriver(t, dir, "fern.fern")
	fernBin := buildSelfHostBin(t, gcc, dir, "fern.fern", "fern")

	const src = `function main(): i32 {
  var sum: i32 = 0;
  for (var i: i32 = 1; i <= 10; i = i + 1) {
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
	if !strings.Contains(got, "for (var i: i32 = 1; i <= 10; i = i + 1)") {
		t.Errorf("the C-style for header did not survive formatting:\n%s", got)
	}
	// Source preservation is the point, so the formatted file must still be the
	// same program: `-fmt` is advertised as a round-trip, and a rewritten loop
	// that happens to compute the same thing is still a rewritten loop.
	if got != src {
		t.Errorf("already-formatted source was rewritten:\n--- in ---\n%s\n--- out ---\n%s", src, got)
	}
}

// selfHostFmtKnownDivergences is every corpus file the self-host formatter is
// known to print differently from native. The list is EXACT in both directions:
// a file that diverges without being here fails, and a file here that no longer
// diverges fails too, so it shrinks as fixes land instead of outliving them.
//
// It is EMPTY, and that is the state to defend. #6802 started at 40 divergent
// files of 425 and #6832 had already taken it from 71; the last ten classes went
// with a written-form carrier each (`Map { … }` and construction-site type args
// as new/reused nodes, `let … else` and `loop` and `|>` and the nested
// `function` decl and the f-string from data the nodes now carry) plus one fix on
// NATIVE's side, where `-fmt` was re-rendering every float literal from its value
// and rewriting `1e-6` into `1e-06`.
//
// Add a row only with the reason and the issue, never to make a red build green.
var selfHostFmtKnownDivergences = map[string]string{}

// TestSelfHostFmtCorpusParityX86_64 runs the byte-parity property over the whole
// corpus rather than over fmtParityCases.
//
// The fixture list is what a formatter bug hides behind: #6802 was found by a
// hand-run 425-file sweep, not by this suite, and every row it produced had to
// be transcribed into a fixture before anything gated it. A corpus run gates the
// files themselves, so the next divergence is a red build rather than an issue
// somebody has to notice and write up.
//
// Cheap enough to belong here: 425 `-fmt` invocations of the linked driver take
// ~16 s in total (the driver's fixed startup is ~12 ms; the cost is the ten
// 15-50 kloc self_host modules), against the driver build the parity test above
// already pays for and caches.
func TestSelfHostFmtCorpusParityX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	if len(runner) != 0 {
		t.Skip("CLI driver test runs only natively (argv paths)")
	}
	root := repoRootFromTest(t)
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
			// Not a parseable program: nothing for the two formatters to agree on.
			continue
		}
		want := goprinter.Format(prog)
		cmd := exec.Command(fernBin, "-fmt", filepath.Join(root, rel))
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
				rel, firstDiffLines(want, string(got)))
		}
	}
	for rel, why := range selfHostFmtKnownDivergences {
		if !diverged[rel] {
			t.Errorf("%s is listed as a known divergence (%s) but the two formatters now agree — delete the row", rel, why)
		}
	}
}

// repoRootFromTest finds the repository root by walking up to the go.mod.
func repoRootFromTest(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("go.mod not found above the test's working directory")
		}
		dir = parent
	}
}

// corpusFernFiles lists every `.fern` file under examples/ + internal/stdlib,
// repo-relative and sorted, with slash separators so the allowlist keys read the
// same on every platform.
func corpusFernFiles(t *testing.T, root string) []string {
	t.Helper()
	var out []string
	for _, sub := range []string{"examples", "internal/stdlib"} {
		err := filepath.WalkDir(filepath.Join(root, sub), func(path string, d os.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() || filepath.Ext(path) != ".fern" {
				return nil
			}
			rel, err := filepath.Rel(root, path)
			if err != nil {
				return err
			}
			out = append(out, filepath.ToSlash(rel))
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	if len(out) < 400 {
		t.Fatalf("corpus walk found only %d files; the tree moved and this gate stopped covering it", len(out))
	}
	slices.Sort(out)
	return out
}

// firstDiffLines reports the first differing line of two formatter outputs, with
// its neighbours — a whole 50 kloc module is not a test failure message.
func firstDiffLines(want, got string) string {
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
		b.WriteString("--- native ---\n")
		for j := lo; j < hi && j < len(w); j++ {
			b.WriteString(w[j] + "\n")
		}
		b.WriteString("--- self-host ---\n")
		for j := lo; j < hi && j < len(g); j++ {
			b.WriteString(g[j] + "\n")
		}
		return b.String()
	}
	return "outputs differ only in length"
}
