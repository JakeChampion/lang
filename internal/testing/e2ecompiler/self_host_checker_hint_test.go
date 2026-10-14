package e2ecompiler

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
)

// Keep the fallback's examples paired with programs that still need it.
// When the checker learns one of them, update the diagnostic as well as the
// expected exit code. The other rows guard previously unsupported categories.
func TestSelfHostCheckerFallbackHint(t *testing.T) {
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "fern.fern")
	var driver string
	var runner []string
	if runtime.GOOS == "darwin" && runtime.GOARCH == "arm64" {
		driver = buildSelfHostBinArm64Darwin(t, dir, "fern.fern", "fern")
	} else {
		var gcc string
		gcc, runner = x86_64Tooling(t)
		driver = buildSelfHostBin(t, gcc, dir, "fern.fern", "fern")
	}
	native := buildLangBinForInterp(t)
	stdlibRoot, err := filepath.Abs("../../stdlib")
	if err != nil {
		t.Fatal(err)
	}
	const dynPrelude = `trait Greet { function hi(self: Self): i32; }
struct Dog {}
impl Greet for Dog { function hi(self: Self): i32 { return 7; } }
`
	// fallback is the line the hint names: the innermost statement the
	// checker could not type, or 0 for a program it accepts.
	for _, tc := range []struct {
		name     string
		source   string
		fallback int
	}{
		{"enum", `enum Color { Red, Blue } function main(): i32 { let c: Color = Red; return match (c) { Red => 0, Blue => 1 }; }`, 0},
		{"generic", `function ident[T](v: T): T { return v; } function main(): i32 { return ident(0); }`, 0},
		{"option", `function main(): i32 { let o: Option[i32] = Some(3); return match (o) { Some(n) => n, None => 0 }; }`, 0},
		{"result", `function main(): i32 { let r: Result[i32, string] = Ok(3); return match (r) { Ok(n) => n, Err(_) => 0 }; }`, 0},
		// A match STATEMENT on a built-in enum: Option's and Result's variants
		// have no struct sig.
		{"option-match-stmt", `function f(o: Option[i32]): i32 { match (o) { Some(n) => { return n; }, None => { return 0; } } } function main(): i32 { return f(Some(3)); }`, 0},
		{"result-match-stmt", `function f(r: Result[i32, string]): i32 { match (r) { Ok(n) => { return n; }, Err(e) => { return e.len(); } } } function main(): i32 { return f(Ok(3)); }`, 0},
		// A user generic enum's payload is its spelling with the enum's
		// parameters replaced by the scrutinee's arguments.
		{"generic-enum-match-stmt", `enum Box[T, E] { Full(T), Blank(E) } function f(b: Box[i32, string]): i32 { match (b) { Full(n) => { return n; }, Blank(s) => { return s.len(); } } } function main(): i32 { let b: Box[i32, string] = Full(5); return f(b); }`, 0},
		// A derived method on a generic type is spelled on the bare name, and
		// its body reads the type's parameters off the declaration; it is a
		// generic body like any other (#11776).
		{"derived-on-generic-struct", `import "core/cmp"; @derive(cmp.Eq, cmp.Hash, cmp.Display) struct Pair[T] { a: T, b: T } function main(): i32 { let p: Pair[i32] = Pair[i32] { a: 1, b: 2 }; if (!p.eq(Pair[i32] { a: 1, b: 2 })) { return 1; } return p.hash() - p.hash() + p.to_string().len() - 18; }`, 0},
		{"derived-on-generic-enum", `import "core/cmp"; @derive(cmp.Eq, cmp.Hash, cmp.Display) enum Tag[T] { One(T), Two(T, T), Empty } function main(): i32 { let a: Tag[i32] = Tag.Two(1, 2); if (!a.eq(Tag.Two(1, 2))) { return 1; } return a.hash() - a.hash() + a.to_string().len() - 9; }`, 0},
		{"json-value-match-stmt", `import "std/json"; function f(v: json.JsonValue): i32 { match (v) { JNull => { return 1; }, JBool(b) => { return 2; }, _ => { return 0; } } } function main(): i32 { return f(JNull); }`, 0},
		{"map-iter-cursor", `import "core/map"; function f(m: Map[string, i32]): i32 { let it: MapIter[string, i32] = m.iter(); let n: i32 = 0; while (it.has_next()) { n = n + it.key().len() + it.value(); it.advance(); } return n; } function main(): i32 { return 0; }`, 0},
		{"option-literal-scrutinee", `function main(): i32 { match (Some(4)) { Some(v) => { return v + 1; }, None => { return 0; } } }`, 0},
		{"option-inferred-local", `function main(): i32 { let o = Some("ab"); match (o) { Some(v) => { return v.len(); }, None => { return 0; } } }`, 0},
		{"dyn-binding", dynPrelude + `function main(): i32 { let d: dyn Greet = Dog {}; return 0; }`, 0},
		{"dyn-method", dynPrelude + `function main(): i32 { let d: dyn Greet = Dog {}; return d.hi(); }`, 0},
		// A generic struct's callable field is called at the instance's type
		// arguments.
		{"generic-struct-callable-field-call", `struct S[T] { f: (T) => T } function main(): i32 { let s = S[i32] { f: (x: i32): i32 => x }; return s.f(3); }`, 0},
		// A lambda the program writes and calls in place is typed by what it
		// declares, in a loop body as anywhere else.
		{"iife", `function main(): i32 { return ((x: i32): i32 => { return x * 2; })(4); }`, 0},
		{"iife-in-loop", `function main(): i32 {
    let n: i32 = 0;
    while (n < 3) {
        n = n + ((x: i32): i32 => { return x; })(1);
    }
    return n;
}`, 0},
		// A function using a builtin struct (FileStat): the hint checks the
		// module check_module typed, builtins included (#10752).
		{"builtin-struct-field", `function isdir(p: string): boolean {
    let d: boolean = false;
    match (stat(p)) { Ok(st) => { d = st.is_dir; }, Err(e) => { } }
    return d;
}
function main(): i32 { if (isdir(".")) { return 0; } return ((x: i32): i32 => { return x * 2; })(4); }`, 0},
		// An empty literal at a type parameter leaves it to the function
		// value beside it, so iterating the result types its elements.
		{"generic-empty-literal-and-fn-value", `function fold[T](n: i32, acc: T, visit: (i32, T) => T): T { return visit(n, acc); }
function add(n: i32, acc: i32[]): i32[] { return acc.append(n); }
function main(): i32 { let t = 0; for u in fold(3, [], add) { t = t + u; } return t; }`, 0},
		// A variant assigned to a union-typed local widens, as it does in a
		// declaration or a return.
		{"union-variant-reassign", `struct Circle { r: i32 }
struct Square { s: i32 }
type Shape = Circle | Square;
function main(): i32 { let sh: Shape = Circle { r: 1 }; sh = Square { s: 2 }; return 0; }`, 0},
		// A type argument only the result names is the call's sole binding, so
		// it holds under `?` as it does for a whole init (#11329).
		{"written-type-arg-under-try", `struct Item { name: string }
enum E { Bad }
function get[T](x: i32): Result[T, E] { return Err(E.Bad); }
function create(): Result[i32, E] { let item: Item = get[Item](1)?; return Ok(0); }
function main(): i32 { return 0; }`, 0},
		// An empty array literal binds nothing (#10499), so the written list
		// is all that settles T.
		{"written-type-arg-sole-binding", `function pick[T](a: T[]): T { return a[0]; }
function use64(x: i64): i64 { return x; }
function main(): i32 { use64(pick[i64]([])); return 0; }`, 0},
		// A generic result only the destination binds reads at its type
		// (#12382).
		{"generic-result-bound-by-destination", `trait Ident { function one(): Self; }
struct A { v: i32 }
impl Ident for A { function one(): Self { return A { v: 10 }; } }
function unit[T: Ident](): T { return T.one(); }
function geta(): A { return unit(); }
function main(): i32 { let a: A = unit(); return a.v + geta().v; }`, 0},
		// An unsettled integer arm takes its sibling arm's type (#12381).
		{"value-if-literal-settles-at-float", `function main(): i32 { let x = if (true) { 1 } else { 2.5 }; return 0; }`, 0},
		{"value-if-literal-local-settles", `function main(): i32 { let q = 2; let x = if (true) { q } else { 2.5 }; return 0; }`, 0},
		{"value-if-literal-settles-at-i64", `function main(): i32 { let a: i64 = 5; let x = if (true) { a } else { -1 }; return 0; }`, 0},
		{"value-if-wide-array-arms", `function main(): i32 { let arr: i64[] = if (true) { [1234567890123] } else { [0, 0] }; return 0; }`, 0},
		{"value-match-literal-array-assigns", `function main(): i32 { let k: i32 = 1; let a: i64 = 2; let x = match (k) { 1 => [a], _ => [3] }; return 0; }`, 0},
		// So does a value block whose tail is one.
		{"value-if-block-arm-settles-at-float", `function main(): i32 { let c: boolean = true; let x = if (c) { ({ 1 }) } else { 2.5 }; if (x > 2.0) { return 1; } return 0; }`, 0},
		{"value-if-block-arm-leading-statement", `function main(): i32 { let x = if (true) { ({ let q: i32 = 3; 1 }) } else { 2.5 }; return 0; }`, 0},
		{"value-if-nested-block-arm", `function main(): i32 { let x = if (true) { ({ ({ 1 }) }) } else { 2.5 }; return 0; }`, 0},
		{"value-if-block-arm-after-float", `function main(): i32 { let x = if (true) { 2.5 } else { ({ 1 }) }; return 0; }`, 0},
		{"value-if-block-arm-literal-local", `function main(): i32 { let x = if (true) { ({ let q = 2; q }) } else { 2.5 }; return 0; }`, 0},
		{"value-if-nested-arm-literal-local", `function main(): i32 { let x = if (true) { if (false) { let q = 2; q } else { 2 } } else { 2.5 }; return 0; }`, 0},
		{"value-match-block-arm", `function main(): i32 { let k: i32 = 1; let x = match (k) { 1 => ({ 1 }), _ => 2.5 }; return 0; }`, 0},
		{"value-if-block-arm-return", `function g(c: boolean): f64 { return if (c) { ({ 1 }) } else { 2.5 }; }
function main(): i32 { return 0; }`, 0},
		{"value-block-return", `function g(): f64 { return ({ 1 }); }
function main(): i32 { return 0; }`, 0},
		{"value-if-block-arm-argument", `function take(v: f64): i32 { return 0; }
function main(): i32 { return take(if (true) { ({ 1 }) } else { 2.5 }); }`, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(dir, tc.name+".fern")
			if err := os.WriteFile(path, []byte(tc.source), 0o644); err != nil {
				t.Fatal(err)
			}
			if out, err := exec.Command(native, "-check", path).CombinedOutput(); err != nil {
				t.Fatalf("native checker rejected the corpus: %v\n%s", err, out)
			}
			cmd := runX86_64Bin(runner, driver)
			cmd.Args = append(cmd.Args, "-check", path, stdlibRoot)
			out, err := cmd.CombinedOutput()
			wantCode := 0
			if tc.fallback != 0 {
				wantCode = 1
			}
			if cmd.ProcessState == nil || cmd.ProcessState.ExitCode() != wantCode {
				t.Fatalf("self-host checker: %v, want exit %d\n%s", err, wantCode, out)
			}
			if tc.fallback != 0 {
				want := `error[type]: "main": the self-host checker could not infer the type of the statement at line ` +
					strconv.Itoa(tc.fallback) + " ("
				if !strings.Contains(string(out), want) {
					t.Errorf("diagnostic = %s, want %q", out, want)
				}
			} else if len(out) != 0 {
				t.Errorf("accepted program emitted a diagnostic: %s", out)
			}
		})
	}
}

// The self-host checker accepts the compiler it is part of (#10188). Every
// statement it cannot type is a place an ill-typed program would compile.
func TestSelfHostChecksItsOwnSources(t *testing.T) {
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "fern.fern")
	gcc, runner := x86_64Tooling(t)
	driver := buildSelfHostBin(t, gcc, dir, "fern.fern", "fern")
	src, err := filepath.Abs("../../../compiler/fern.fern")
	if err != nil {
		t.Fatal(err)
	}
	stdlib, err := filepath.Abs("../../stdlib")
	if err != nil {
		t.Fatal(err)
	}
	cmd := runX86_64Bin(runner, driver)
	cmd.Args = append(cmd.Args, "-check", src, stdlib)
	out, err := cmd.CombinedOutput()
	if err != nil || len(out) != 0 {
		t.Fatalf("fern-selfhost -check of its own sources: %v\n%s", err, out)
	}
}
