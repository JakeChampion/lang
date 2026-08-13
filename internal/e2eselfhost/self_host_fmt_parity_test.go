package e2eselfhost

import (
	"os"
	"os/exec"
	"path/filepath"
	"slices"
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
// Corpus-wide, `fern -fmt` and `fern-selfhost -fmt` now agree byte for byte on
// 177 of the 244 files under examples/ + internal/stdlib, against 0 before
// #6762. What still differs is information the PARSER does not keep, so no
// printer can recover it: `str` is canonicalised to `string`, a function-typed
// parameter to `fn`, `trait` declarations have no Module field at all, struct
// visibility is not carried, and a `let (a, b) = …` destructuring loses its
// shape. Those are #6773.
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
	//
	// The struct here is deliberately NOT `pub`: only a FuncDecl's is_pub
	// records the source keyword, while a struct's is hardcoded true to keep it
	// unrestricted, so printing from it would stamp `pub` onto every struct in
	// the file. Struct visibility is parser surface, filed with the rest.
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
	// Both `for` forms are parse-time DESUGARS, and each compiler leaked its own
	// (#6770 native, #6771 self-host). Native expanded `for i in 0..4` into a
	// block around a synthesised `__range_hi_N` binding; the self-host printed
	// the parser-internal `for i in __range(0, 4)` call, which does not even
	// re-parse as the same loop. The C-style `for` was the self-host's alone: it
	// came back as `if (true) { … while (true) { … } }` around a `__forc_5_7`
	// flag keyed to the loop's original line and column.
	//
	// Both are now reconstructed, so this is also the case that unblocked
	// examples/sum_for.fern — the last corpus file diverging for a reason other
	// than parser-discarded information.
	{"for-loop-desugars", `function ranges(n: i32): i32 {
  var t: i32 = 0;
  for i in 0..4 {
    t = t + i;
  }
  for j in 1..=3 {
    t = t + j;
  }
  for k in (n - 4)..n {
    t = t + k;
  }
  return t;
}

function cstyle(n: i32): i32 {
  var sum: i32 = 0;
  for (var i: i32 = 1; i <= 10; i = i + 1) {
    sum = sum + i;
  }
  var j: i32 = 0;
  for (; j < 3; j = j + 1) {
    sum = sum + n;
  }
  return sum;
}

function main(): i32 {
  return ranges(5) + cstyle(2);
}
`},
	// A hand-written `if (true) { … }` around a `while (true)` must NOT be
	// mistaken for a C-style-for expansion. The reconstruction keys on the
	// synthesised `__forc_` flag and requires the whole shape, so this prints as
	// what it is.
	{"true-if-is-not-a-cfor", `function main(): i32 {
  var t: i32 = 0;
  if (true) {
    var i: i32 = 0;
    while (true) {
      if (!(i < 3)) {
        break;
      }
      t = t + i;
      i = i + 1;
    }
  }
  return t;
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
