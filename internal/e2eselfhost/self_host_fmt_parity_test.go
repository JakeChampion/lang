package e2eselfhost

import (
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

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
// Corpus-wide, `fern -fmt` and `fern-selfhost -fmt` now agree byte for byte on
// 210 of the 244 files under examples/ + internal/stdlib, against 0 before
// #6762. What still differs: `impl` blocks lose their block shape the same way
// traits did (#6783), `else { if … }` collapses to `else if` (#6779), and an
// ARROW lambda — native reprints `() => e` as `function(): T { return e; }`,
// filling in a return type from the callee's signature that the self-host
// printer has no way to know.
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
