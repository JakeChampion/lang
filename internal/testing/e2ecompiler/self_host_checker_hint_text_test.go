package e2ecompiler

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/check/checker"
	"github.com/jakechampion/lang/internal/pkg/modload"
	"github.com/jakechampion/lang/internal/syntax/diag"
)

var typeErrPrefixRE = regexp.MustCompile(`^type error at \d+:\d+: `)

func goCheckerDiags(t *testing.T, dir, src string) []driverDiag {
	t.Helper()
	p := filepath.Join(dir, "gocheck_input.fern")
	if err := os.WriteFile(p, []byte(src), 0o644); err != nil {
		t.Fatalf("write gocheck input: %v", err)
	}
	prog, _, err := modload.Load(p)
	if err != nil {
		t.Fatalf("load gocheck input: %v", err)
	}
	_, err = checker.Check(prog)
	return checkErrDiags(err)
}

func checkErrDiags(err error) []driverDiag {
	if err == nil {
		return nil
	}
	errs := []error{err}
	if es, ok := err.(diag.Errors); ok {
		errs = es
	}
	var out []driverDiag
	for _, e := range errs {
		code := ""
		if cd, ok := e.(diag.Coded); ok {
			code = cd.Code()
		}
		out = append(out, driverDiag{code, typeErrPrefixRE.ReplaceAllString(e.Error(), "")})
	}
	return out
}

func byCode(ds []driverDiag) map[string][]string {
	out := map[string][]string{}
	for _, d := range ds {
		out[d.code] = append(out[d.code], d.msg)
	}
	for code, msgs := range out {
		sort.Strings(msgs)
		out[code] = slicesCompact(msgs)
	}
	return out
}

func slicesCompact(in []string) []string {
	var out []string
	for i, s := range in {
		if i == 0 || s != in[i-1] {
			out = append(out, s)
		}
	}
	return out
}

type hintTextCase struct {
	name     string
	src      string
	code     string
	spelling string
}

var hintTextCases = []hintTextCase{
	{name: "E043 matching IoError receiver", src: `function probe(x: Option[IoError]): void { x.failure(); } function main(): i32 { return 0; }`, code: "E043", spelling: `import "std/io_outcome"`},
	{name: "E043 matching nested Option receiver", src: `function probe(x: Option[Option[i32]]): void { x.flatten(); } function main(): i32 { return 0; }`, code: "E043", spelling: `import "std/option"`},
	{name: "E043 matching Option Result receiver", src: `function probe(x: Option[Result[i32, string]]): void { x.transpose(); } function main(): i32 { return 0; }`, code: "E043", spelling: `import "std/option"`},
	{name: "E043 matching nested Result receiver", src: `function probe(x: Result[Result[i32, string], string]): void { x.flatten(); } function main(): i32 { return 0; }`, code: "E043", spelling: `import "std/result"`},
	{name: "E043 matching Result failure receiver", src: `function probe(x: Result[void, IoError]): void { x.failure(); } function main(): i32 { return 0; }`, code: "E043", spelling: `import "std/io_outcome"`},
	{name: "E043 matching generic tuple receiver", src: `function probe[A, B](x: Option[(A, B)]): void { x.unzip(); } function main(): i32 { return 0; }`, code: "E043", spelling: `import "std/option"`},
	{name: "E043 matching restricted receiver near miss", src: `function probe(x: Option[IoError]): void { x.failur(); } function main(): i32 { return 0; }`, code: "E043", spelling: `did you mean "failure", which needs`},
	{name: "E043 mismatching repeated Result parameter", src: `function probe(x: Result[Result[i32, string], i32]): void { x.flatten(); } function main(): i32 { return 0; }`, code: "E043", spelling: `is declared for Result[Result[T, E], E] in "std/result"`},
	{name: "E043 generic receiver is not IoError", src: `function probe[A](x: Option[A]): void { x.failure(); } function main(): i32 { return 0; }`, code: "E043", spelling: `is declared for Option[IoError] in "std/io_outcome"`},
	{name: "E043 narrow Option receiver", src: `function main(): i32 { let x: Option[i32] = Some(1); x.failure(); return 0; }`, code: "E043", spelling: `"failure" is declared for Option[IoError]`},
	{name: "E043 nested Option receiver", src: `function main(): i32 { let x: Option[i32] = Some(1); x.unzip(); return 0; }`, code: "E043", spelling: `"unzip" is declared for Option[(T, U)]`},
	{name: "E021 unsafe object reason", src: `trait Bad { function copy(self: Self): Self; } function f(x: dyn Bad): i32 { return 0; } function main(): i32 { return 0; }`, code: "E021", spelling: `method "copy" returns Self, so it cannot be used as`},
	{name: "E032 empty use callback", src: `function take(f: () => i32): i32 { return f(); } function main(): i32 { use x <- take(); return 0; }`, code: "E032", spelling: `callback takes no arguments`},

	{name: "E052 unannotated try fallthrough", src: `function f(x: Option[i32]) { let y = x?; } function main(): i32 { return 0; }`, code: "E052", spelling: `"f" has return type Option[i32]`},

	{name: "E001 raw data is private", src: `function main(): i32 { let x = __raw_data("a"); return 0; }`, code: "E001", spelling: `cannot find "__raw_data"`},
	{name: "E001 raw store is private", src: `function main(): i32 { __raw_store8(0 as usize, 0, 0); return 0; }`, code: "E001", spelling: `cannot find "__raw_store8"`},
	{name: "E040 unbound array result", src: `function mk[T](): T[] { return []; } function main(): i32 { let z = mk(); return 0; }`, code: "E040", spelling: `could not infer type parameter T for mk`},
	{name: "E009 unary string", src: `function main(): i32 { let x = -"s"; return 0; }`, code: "E009", spelling: `operator "-" requires an integer type, got string`},
	{name: "E035 literal match type", src: `function main(): i32 { match (true) { 1 => { return 1; }, _ => { return 0; } } }`, code: "E035", spelling: `literal pattern of type i32 does not match scrutinee type boolean`},

	{
		name: "E040 method type argument count names the receiver",
		src: `struct B { v: i32 }
function (b: B) ap[U](f: (i32) => U): U { return f(b.v); }
function main(): i32 { let b = B { v: 1 }; let n = b.ap[i32, i32]((v: i32): i32 => v); return 0; }`,
		code:     "E040",
		spelling: "B.ap takes 1 type argument",
	},
	{
		name: "E043 generic struct literal clash names the later field",
		src: `struct Same[T] { a: T, b: T }
function main(): i32 { let q = Same { a: 1, b: "x" }; return 0; }`,
		code:     "E043",
		spelling: `field "b": expected i32`,
	},
	{
		name: "E043 struct update over a literal-bound base",
		src: `struct Same[T] { a: T, b: T }
function main(): i32 {
    let q = Same { a: 1, b: 2 };
    let y: i64 = 8589934592;
    let w = Same { ...q, b: y };
    return 0;
}`,
		code:     "E043",
		spelling: `field "b": expected i32, got i64`,
	},
	{
		name: "E040 uninferred struct type parameter",
		src: `struct Box[T] { xs: T[] }
function main(): i32 { let b = Box { xs: [] }; return b.xs.len(); }`,
		code:     "E040",
		spelling: "could not infer type parameter T for struct Box",
	},
	{
		name: "E041 equality",
		src: `struct P { x: i32 }
function main(): i32 {
    let a: P = P { x: 1 };
    let b: P = P { x: 1 };
    if (a == b) { return 1; }
    return 0;
}`,
		code:     "E041",
		spelling: "`@derive(cmp.Eq)`",
	},
	{
		name: "E041 ordering",
		src: `struct P { x: i32 }
function main(): i32 {
    let a: P = P { x: 1 };
    let b: P = P { x: 1 };
    if (a < b) { return 1; }
    return 0;
}`,
		code:     "E041",
		spelling: "`@derive(cmp.Ord)`",
	},
	{
		name:     "E056 array element assignment",
		src:      `function main(): i32 { let a: i32[] = [1, 2]; a[0] = 5; return a[0]; }`,
		code:     "E056",
		spelling: "`arr = arr.with(i, value)`",
	},
	{
		name: "E003 str view into owned string",
		src: `function main(): i32 {
    let t: string = "abcdef";
    let s: string = slice_unchecked(t, 0, 3);
    return s.len();
}`,
		code:     "E003",
		spelling: "add `.to_owned()`",
	},
	{
		name: "E003 array view into owned array",
		src: `function main(): i32 {
    let all: string[] = ["alpha", "beta", "gamma"];
    let mid: string[] = all[1:3];
    return mid.len();
}`,
		code:     "E003",
		spelling: "expected string[], got [string]",
	},
	{
		name: "E063 view of local storage returned",
		src: `function middle(): [string] {
    let all: string[] = ["alpha", "beta", "gamma"];
    return all[1:3];
}
function main(): i32 { return 0; }`,
		code:     "E063",
		spelling: "return an owned array (`T[]`) or slice a parameter instead",
	},
	{
		name: "E026 wildcard arm placement",
		src: `enum C { R, G }
function main(): i32 {
    let c: C = R;
    match (c) { _ => { return 0; }, R => { return 1; } }
    return 0;
}`,
		code:     "E026",
		spelling: "`_` arm must be last",
	},
	{
		name: "E030 inexhaustive match",
		src: `enum C { R, G }
function main(): i32 {
    let c: C = R;
    match (c) { R => { return 1; } }
    return 0;
}`,
		code:     "E030",
		spelling: "or a `_` arm",
	},
	{
		name: "E021 underivable field",
		src: `trait Ord { function cmp(self: Self, other: Self): i32; }
@derive(Ord)
struct Foo { x: i32 }
function main(): i32 { return 0; }`,
		code:     "E021",
		spelling: "`impl Ord for i32`",
	},
	{
		name: "E038 print needs Display",
		src: `struct P { x: i32 }
function main(): i32 {
    let a: P = P { x: 1 };
    print(a);
    return 0;
}`,
		code:     "E038",
		spelling: "`@derive(cmp.Display)`",
	},
	{
		name:     "E045 float map key",
		src:      `function main(): i32 { let m = Map { 1.5: 1 }; return 0; }`,
		code:     "E045",
		spelling: "`@derive(cmp.Eq, cmp.Hash)`",
	},
	{
		name: "E045 struct map key",
		src: `struct K { a: i32 }
function main(): i32 { let m = Map { K { a: 1 }: 1 }; return 0; }`,
		code:     "E045",
		spelling: "`@derive(cmp.Eq, cmp.Hash)`",
	},
	{
		name:     "E064 bool for boolean",
		src:      `function main(): i32 { let b: bool = true; return 0; }`,
		code:     "E064",
		spelling: "did you mean `boolean`?",
	},
	{
		name:     "E064 int for i32",
		src:      `function main(): i32 { let n: int = 1; return 0; }`,
		code:     "E064",
		spelling: "did you mean `i32`?",
	},
	{
		name:     "E064 String for string",
		src:      `function main(): i32 { let s: String = "x"; return 0; }`,
		code:     "E064",
		spelling: "did you mean `string`?",
	},
	{
		name:     "E043 unknown array method lists the API",
		src:      `function main(): i32 { let a: i32[] = [1]; return a.sum(); }`,
		code:     "E043",
		spelling: "it has: append, len, with",
	},
	{
		name:     "E043 retired collection spelling names the replacement",
		src:      `function main(): i32 { let a: i32[] = [1]; a.push(2); return a.len(); }`,
		code:     "E043",
		spelling: `use "append"`,
	},
	{
		name:     "E043 near-miss method name suggests the real one",
		src:      `function main(): i32 { let a: i32[] = [1]; return a.lenn(); }`,
		code:     "E043",
		spelling: `did you mean "len"?`,
	},
	{
		name:     "E043 f-string to_string names the import",
		src:      `function main(): i32 { let n: i32 = 1; let s: string = f"x{n}y"; return s.len(); }`,
		code:     "E043",
		spelling: "add `import \"std/i32\"`",
	},
	{
		name:     "E043 a string method from std/string names the import",
		src:      `function main(): i32 { let s: string = "ab"; return s.trim().len(); }`,
		code:     "E043",
		spelling: "— add `import \"std/string\"`",
	},
	{
		name:     "E043 a method no module declares names no import",
		src:      `function main(): i32 { let s: string = "ab"; return s.frobnicate(); }`,
		code:     "E043",
		spelling: `no method "frobnicate" on string, and no stdlib module declares one`,
	},
	{
		name:     "E043 field read on a non-struct gets the method advice",
		src:      `function main(): i32 { let s = "a"; return s.foo; }`,
		code:     "E043",
		spelling: `no method "foo" on string, and no stdlib module declares one`,
	},
	{
		name: "E042 non-Option operand names boolean",
		src: `function f(): i32 { let b: boolean = true; let c = b?; return 0; }
function main(): i32 { return 0; }`,
		code:     "E042",
		spelling: "got boolean",
	},
	{
		name: "E042 Option operand in a Result function names the return",
		src: `function a(): Result[i32, IoError] {
  let x: Option[i32] = Some(1);
  let n: i32 = x?;
  return Ok(n);
}
function main(): i32 { return 0; }`,
		code:     "E042",
		spelling: "got Result[i32, IoError]",
	},
	{
		name: "E004 Map get with no key",
		src: `import "core/map";
function main(): i32 { let m: Map[string, i32] = map_new(4); let z = m.get(); return 0; }`,
		code:     "E004",
		spelling: "takes 1 argument but was given 0",
	},
	{
		name: "E042 dyn residual names the missing impl",
		src: `import "std/error";
struct Oops { code: i32 }
function g(): Result[i32, Oops] { return Err(Oops { code: 1 }); }
function f(): Result[i32, dyn error.Error] { let v: i32 = g()?; return Ok(v); }
function main(): i32 { return 0; }`,
		code:     "E042",
		spelling: "`?` cannot box Oops into dyn error.Error: Oops does not implement error.Error",
	},
	{
		name: "E042 concrete residual names the from constructor",
		src: `struct A { code: i32 }
struct B { code: i32 }
function g(): Result[i32, A] { return Err(A { code: 1 }); }
function f(): Result[i32, B] { let v: i32 = g()?; return Ok(v); }
function main(): i32 { return 0; }`,
		code:     "E042",
		spelling: "B has no `from(A)` constructor to convert it",
	},
	{
		name: "E042 Option operand in a void function",
		src: `function f(o: Option[i32]): void { let v: i32 = o?; }
function main(): i32 { return 0; }`,
		code:     "E042",
		spelling: "got void",
	},
	{
		name: "E021 associated function called as a method",
		src: `trait Show { function show(): i32; }
struct P { v: i32 }
impl Show for P { function show(): i32 { return 5; } }
function pick[T: Show](a: T): i32 { return a.show(); }
function main(): i32 { return pick(P { v: 42 }); }`,
		code:     "E021",
		spelling: "call it as T.show(...)",
	},
	{
		name: "E021 associated function called through dyn",
		src: `struct Box { v: i32 }
trait Mk { function make(own b: Box): i32; }
struct P { v: i32 }
impl Mk for P { function make(own b: Box): i32 { return b.v; } }
function main(): i32 { let d: dyn Mk = P { v: 0 }; return d.make(Box { v: 3 }); }`,
		code:     "E021",
		spelling: "call it on a concrete type",
	},
	{
		name: "E021 receiver type not a valid receiver",
		src: `function (x: bool) foo(): i32 { return 0; }
function main(): i32 { return 0; }`,
		code:     "E021",
		spelling: "must be a struct, enum, array, slice, or built-in type",
	},
}

var hintTextDivergences = map[string]string{}

func TestSelfHostCheckerHintTextDifferential(t *testing.T) {
	interpBin := buildLangBinForInterp(t)
	driver, err := filepath.Abs("../../../compiler/drivers/checker_codes_run.fern")
	if err != nil {
		t.Fatalf("abs driver path: %v", err)
	}

	for _, tc := range hintTextCases {
		t.Run(tc.name, func(t *testing.T) {
			goDiags := byCode(goCheckerDiags(t, t.TempDir(), tc.src))
			goMsgs := goDiags[tc.code]
			if len(goMsgs) == 0 {
				t.Fatalf("native reported no %s for this case — the corpus row no longer exercises the hint it names", tc.code)
			}
			for _, m := range goMsgs {
				if !strings.Contains(m, tc.spelling) {
					t.Fatalf("native %s no longer names %q, so this row is not pinning advice any more: %s", tc.code, tc.spelling, m)
				}
			}

			cmd := exec.Command(interpBin, "-interp", driver)
			cmd.Stdin = strings.NewReader(tc.src)
			out, _ := cmd.Output()
			shMsgs := byCode(driverDiags(string(out)))[tc.code]

			key := tc.name + "/" + tc.code
			reason, listed := hintTextDivergences[key]
			switch {
			case listed && equalStrings(goMsgs, shMsgs):
				t.Errorf("%s no longer diverges — the two checkers now say the same thing, so drop %q from hintTextDivergences.\nlisted reason: %s\ntext: %s",
					tc.code, key, reason, strings.Join(goMsgs, "\n      "))
			case !listed && len(shMsgs) == 0:
				t.Errorf("the self-host checker reports no %s here, so it tells the reader nothing where native names %q. Fix the port, or list %q in hintTextDivergences with the reason.\nnative: %s",
					tc.code, tc.spelling, key, strings.Join(goMsgs, "\n        "))
			case !listed && !equalStrings(goMsgs, shMsgs):
				t.Errorf("%s message text has drifted between the two checkers.\nnative:    %s\nself-host: %s\n\nIf the difference is deliberate, add %q to hintTextDivergences with the reason; if it is not, move the two into step.",
					tc.code, strings.Join(goMsgs, "\n           "), strings.Join(shMsgs, "\n           "), key)
			}
		})
	}
}

func TestHintTextCorpusCoversTheDeriveHints(t *testing.T) {
	want := map[string]bool{"E038": false, "E041": false, "E045": false}
	for _, tc := range hintTextCases {
		if _, ok := want[tc.code]; ok {
			want[tc.code] = true
		}
	}
	for code, covered := range want {
		if !covered {
			t.Errorf("%s names a `@derive` spelling in internal/check/checker but no hintTextCases row reaches it", code)
		}
	}
}
