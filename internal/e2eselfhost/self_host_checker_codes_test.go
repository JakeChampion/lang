package e2eselfhost

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/checker"
	"github.com/jakechampion/lang/internal/diag"
	"github.com/jakechampion/lang/internal/modload"
)

// codeRE pulls stable diagnostic codes (E001…E0NN) out of a formatted
// diagnostic string.
var codeRE = regexp.MustCompile(`E\d{3}`)

// goCheckerCodes runs the production (Go) front end over src and returns
// the sorted, de-duplicated set of diagnostic codes it reports.
func goCheckerCodes(t *testing.T, dir, src string) []string {
	t.Helper()
	p := filepath.Join(dir, "gocheck_input.fern")
	if err := os.WriteFile(p, []byte(src), 0o644); err != nil {
		t.Fatalf("write gocheck input: %v", err)
	}
	prog, _, err := modload.Load(p)
	if err != nil {
		// A parse/load failure isn't a checker code; treat as none.
		return nil
	}
	_, err = checker.Check(prog)
	if err == nil {
		return nil
	}
	// The stable E0XX code lives in the diag formatting layer, not the
	// checker error's bare message — format it the way `fern -check` does.
	return uniqueSortedCodes(codeRE.FindAllString(diag.Format(p, src, err), -1))
}

func uniqueSortedCodes(in []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, c := range in {
		if !seen[c] {
			seen[c] = true
			out = append(out, c)
		}
	}
	sort.Strings(out)
	return out
}

// TestSelfHostCheckerCodesX86_64 is the differential gate for the
// self-host type-checker port: it compiles the diag-printing checker
// driver (checker_codes_run.fern) with the Go-built self-host bundle
// compiler, runs it over a corpus, and asserts the set of diagnostic
// CODES it prints matches what the production Go checker reports for the
// same source — the full unfiltered code set: the checker port covers
// every code the Go checker emits, so the historical
// selfHostImplementedCodes filter is deleted (freeze precondition 3,
// #4451).
// buildCheckerCodesBin builds the single-module checker-codes driver
// (checker_codes_run.fern). See buildCheckerDriverBin.
func buildCheckerCodesBin(t *testing.T) (checkerBin string, runner []string, dir string) {
	return buildCheckerDriverBin(t, "checker_codes_run.fern", false)
}

// buildCheckerDriverBin builds a self-host checker-codes driver binary: it
// compiles driverFile (bundled with lexer / parser / checker / util / io, plus
// flatten when withFlatten) with the Go-built self-host compiler, producing a
// binary that reads stdin and prints the diagnostic CODE of every diagnostic
// the self-host checker emits. Returns the binary path, the (possibly empty)
// qemu/exec runner prefix, and the temp project dir (which also holds
// goCheckerCodes' scratch input). The single-module driver reads one program;
// the bundle driver (withFlatten) reads a ///MODULE bundle. Shared so the
// expensive self-host bundle compile happens once per driver.
func buildCheckerDriverBin(t *testing.T, driverFile string, withFlatten bool) (checkerBin string, runner []string, dir string) {
	t.Helper()
	gcc, run, modDriverBin := buildModloadDriverX86(t)
	runner = run

	// Compile the self-hosted checker binary (driverFile = checker_run /
	// checker_codes_run, importing std/io + ./lexer + ./parser + ./checker)
	// with the file-based asm driver. The loader resolves `import "std/io"`
	// to the vendored flat io.fern (basename fallback), so the driver source
	// is used unmodified — no ///MODULE bundle, no import rewrite.
	files := map[string]string{}
	for _, m := range []string{"util", "lexer", "parser", "checker"} {
		src, err := os.ReadFile(filepath.Join("../../examples/self_host", m+".fern"))
		if err != nil {
			t.Fatalf("read %s.fern: %v", m, err)
		}
		files[m+".fern"] = string(src)
	}
	if withFlatten {
		src, err := os.ReadFile("../../examples/self_host/flatten.fern")
		if err != nil {
			t.Fatalf("read flatten.fern: %v", err)
		}
		files["flatten.fern"] = string(src)
	}
	ioSrc, err := os.ReadFile("../../internal/stdlib/std/io.fern")
	if err != nil {
		t.Fatalf("read std/io.fern: %v", err)
	}
	files["io.fern"] = string(ioSrc)
	runSrc, err := os.ReadFile(filepath.Join("../../examples/self_host", driverFile))
	if err != nil {
		t.Fatalf("read %s: %v", driverFile, err)
	}
	files["main.fern"] = string(runSrc)

	checkerAsm, progDir := compileFilesModload(t, runner, modDriverBin, files)
	if len(checkerAsm) == 0 {
		t.Fatal("self-host compiler emitted 0 bytes for the codes driver")
	}
	checkerBin = buildBin(t, gcc, progDir, "codes", checkerAsm)
	return checkerBin, runner, progDir
}

func TestSelfHostCheckerCodesX86_64(t *testing.T) {
	checkerBin, runner, dir := buildCheckerCodesBin(t)

	cases := []struct {
		name string
		src  string
		want []string // codes the self-host checker should print
	}{
		{"clean", "function main(): i32 { return 1 + 2; }\n", nil},
		// Bare no-payload enum variant used as a value (#4346 piece 2). A unit
		// variant (`Red`) types to its enum's union, so a matching declared
		// type is clean and a MISMATCHED one draws E003 (Color not assignable
		// to i32) — the same code the Go checker emits. Before the slice the
		// self-host typed `Red` as unknown, so E003 never fired here (the
		// mismatch went silently un-reported, invisible to this codes gate);
		// the mismatch case is what actively verifies the variant now types to
		// its union. The clean-accept cases (no over-reject) are pinned by the
		// self_host_cli_test `-check` exit-code tests, since a silent
		// over-reject also emits no codes and would pass this gate regardless.
		{"enum-value-mismatch", "enum Color { Red, Green }\nfunction main(): i32 { var x: i32 = Red; return x; }\n", []string{"E003"}},
		{"enum-value-assign-clean", "enum Color { Red, Green }\nfunction main(): i32 { var c: Color = Red; return 0; }\n", nil},
		{"enum-value-return-clean", "enum Color { Red, Green }\nfunction pick(): Color { return Green; }\nfunction main(): i32 { var c: Color = pick(); return 0; }\n", nil},
		// Builtin Option/Result as values (#4346 piece 2, second slice). The
		// generic annotation `Option[i32]` resolves to a name-only union, the
		// constructor call `Some(3)` / `Ok(3)` types to that union, and bare
		// `None` does too — so a matching declared type is clean and a
		// MISMATCHED one (`var x: i32 = Some(3)`) draws E003, the same code the
		// Go checker emits. Before the slice all three collapsed to unknown, so
		// E003 never fired (the mismatch was silently un-reported). The
		// mismatch cases verify the value now types to its union; the clean
		// accepts are pinned by the self_host_cli_test `-check` exit-code tests.
		{"option-some-mismatch", "function main(): i32 { var x: i32 = Some(3); return x; }\n", []string{"E003"}},
		{"result-ok-mismatch", "function main(): i32 { var x: i32 = Ok(3); return x; }\n", []string{"E003"}},
		{"option-some-clean", "function main(): i32 { var o: Option[i32] = Some(3); return 0; }\n", nil},
		{"option-none-clean", "function main(): i32 { var o: Option[i32] = None; return 0; }\n", nil},
		{"result-ok-clean", "function main(): i32 { var r: Result[i32, i32] = Ok(3); return 0; }\n", nil},
		// Generic-call return-type inference (#4346 piece 2): a call to a
		// generic function whose return type NAMES a parameter's type
		// (`ident[T](v: T): T`) infers the concrete return from that argument,
		// so `ident(3)` types to i32 — a MISMATCHED destination (`string`) draws
		// E003, the same code the Go checker emits, where the pre-slice
		// self-host typed the call as unknown and E003 never fired. The clean
		// call path is pinned by the self_host_cli_test exit-code test.
		{"generic-call-mismatch", "function ident[T](v: T): T { return v; }\nfunction main(): i32 { var x: string = ident(3); return 0; }\n", []string{"E003"}},
		{"generic-call-clean", "function ident[T](v: T): T { return v; }\nfunction main(): i32 { return ident(3); }\n", nil},
		// User generic-struct instantiation (#4346 piece 2): a `Box[i32]`
		// annotation resolves to the name-only struct `Box`, and constructing
		// `Box { v: 3 }` type-checks (the opaque generic field `v: T` accepts any
		// value). A MISMATCHED initialiser (`= 5`) draws E003 — the same code the
		// Go checker emits — where the pre-slice self-host typed the annotation
		// as unknown and E003 never fired. The clean construction is pinned
		// end-to-end by the self_host_cli_test exit-code test. (Field access
		// `b.v` still yields unknown — the field's type parameter isn't
		// substituted yet — so it's a further slice, kept out of these cases.)
		{"generic-struct-mismatch", "struct Box[T] { v: T }\nfunction main(): i32 { var b: Box[i32] = 5; return 0; }\n", []string{"E003"}},
		{"generic-struct-clean", "struct Box[T] { v: T }\nfunction main(): i32 { var b: Box[i32] = Box { v: 3 }; return 0; }\n", nil},
		// Generic-struct FIELD-ACCESS substitution (#4346 piece 2): reading a
		// field typed by a type parameter off a concrete instantiation
		// (`Box[i32].v`) yields the substituted arg (i32), so assigning it to a
		// MISMATCHED destination (`string`) draws E003 — the same code the Go
		// checker emits — where the pre-slice self-host typed `b.v` as unknown
		// and E003 never fired. The clean read is pinned by the CLI exit test.
		{"generic-struct-field-mismatch", "struct Box[T] { v: T }\nfunction main(): i32 { var b: Box[i32] = Box { v: 3 }; var s: string = b.v; return 0; }\n", []string{"E003"}},
		// NESTED generic field spellings (#4346 piece 2): a type parameter INSIDE
		// a field's spelling is substituted throughout, so off a `Wrapper[i32]`
		// (`items: T[]`) the field `items` is i32[] and its element is i32 —
		// binding it to a MISMATCHED `string` draws E003, matching the Go
		// checker, where the pre-slice self-host typed the nested field unknown
		// and E003 never fired. The clean read is pinned by the CLI exit test.
		{"generic-nested-field-mismatch", "struct Wrapper[T] { items: T[] }\nfunction main(): i32 { var w: Wrapper[i32] = Wrapper { items: [1, 2, 3] }; var s: string = w.items[0]; return 0; }\n", []string{"E003"}},
		{"generic-nested-field-clean", "struct Wrapper[T] { items: T[] }\nfunction main(): i32 { var w: Wrapper[i32] = Wrapper { items: [1, 2, 3] }; return w.items[0]; }\n", nil},
		// Generic-receiver METHOD return substitution (#4346 piece 2): a method
		// whose declared return names a receiver type parameter (`(b: Box[T])
		// get(): T`) resolves to the instantiation arg off a concrete receiver,
		// so `Box[i32].get()` is i32 — assigning it to a MISMATCHED destination
		// draws E003 (matching the Go checker) where the pre-slice self-host
		// typed the call unknown and E003 never fired. The clean call is pinned
		// by the CLI exit test.
		{"generic-method-ret-mismatch", "struct Box[T] { v: T }\nfunction (b: Box[T]) get(): T { return b.v; }\nfunction main(): i32 { var b: Box[i32] = Box { v: 5 }; var s: string = b.get(); return 0; }\n", []string{"E003"}},
		{"generic-method-ret-clean", "struct Box[T] { v: T }\nfunction (b: Box[T]) get(): T { return b.v; }\nfunction main(): i32 { var b: Box[i32] = Box { v: 5 }; return b.get(); }\n", nil},
		// E064: a bare nominal annotation that names no declared type, in a
		// non-generic function parameter — both checkers flag it.
		{"unknown-param-type", "function f(a: Wibble): i32 { return 0; }\nfunction main(): i32 { return 0; }\n", []string{"E064"}},
		{"unknown-field-type", "struct S { v: Wibble }\nfunction main(): i32 { return 0; }\n", []string{"E064"}},
		// E064 in a body `var` annotation. The init `q()` is itself undefined
		// (E001), so there is no E003 init-mismatch cascade to diverge on.
		{"unknown-var-type", "function main(): i32 { var x: Wibble = q(); return 0; }\n", []string{"E001", "E064"}},
		// Sub-word integer keywords (u8/usize) the parser accepts but the
		// self-host name resolver doesn't model. They must NOT draw E064 in a
		// body `var` annotation — the Go oracle accepts them, and the stdlib uses
		// them (`var b: u8`, `var p: usize`), so a false E064 here would bail every
		// importing module off the IR path (the #3813 regression).
		{"subword-int-vars-clean", "function main(): i32 { var a: u8 = 1 as u8; var e: usize = 1 as usize; return 0; }\n", nil},
		// `byte` is NOT a parser keyword, so the Go checker flags it E064 too —
		// the self-host must keep flagging it (init `q()` is E001, avoiding an
		// E003 init-mismatch cascade, same as unknown-var-type above).
		{"unknown-byte-var-type", "function main(): i32 { var x: byte = q(); return 0; }\n", []string{"E001", "E064"}},
		// isize/i8/i16/u16 were retired (#4408): neither is a lexer keyword
		// any more, so a reference to one is an unknown nominal type — both
		// checkers must now flag E064 here, the mirror image of the
		// subword-int-vars-clean case above.
		{"unknown-retired-subword-var-type", "function main(): i32 { var x: i8 = q(); return 0; }\n", []string{"E001", "E064"}},
		// `float` is the width-unqualified f64 alias (#5363). The self-host
		// checker always resolved it; the Go checker used to reject it with
		// E064 (+ a "did you mean f64?" hint) — this fixture pins the
		// reconciled behavior: clean on BOTH checkers, including flowing
		// into an f64 destination.
		{"float-alias-ok", "function main(): i32 { var x: float = 1.5; var y: f64 = x; if (y > 1.0) { return 0; } return 1; }\n", nil},
		// ... and a `float` value in a mismatched destination draws the
		// same E003 both sides (it is a real float type, not unknown).
		{"float-alias-mismatch", "function main(): i32 { var x: float = 1.5; var s: string = x; return 0; }\n", []string{"E003"}},
		// E064 widening (#4363 item 3): an unknown nominal reached through an
		// array-element (`Nope[]`) or generic-argument (`Map[string, Nope]`)
		// spelling draws E064 just like a bare `Nope` — the check used to bail on
		// any non-identifier text, so these inner positions went unflagged. The
		// emitted message names the INNER unknown ("Nope"), matching the Go
		// oracle. Each shape is chosen with no init, so there's no E001/E003
		// cascade to diverge on.
		{"unknown-arrelem-param", "function f(a: Nope[]): i32 { return 0; }\nfunction main(): i32 { return 0; }\n", []string{"E064"}},
		{"unknown-arrelem-nested-param", "function f(a: Nope[][]): i32 { return 0; }\nfunction main(): i32 { return 0; }\n", []string{"E064"}},
		{"unknown-genarg-param", "function f(m: Map[string, Nope]): i32 { return 0; }\nfunction main(): i32 { return 0; }\n", []string{"E064"}},
		{"unknown-arrelem-field", "struct S { xs: Nope[] }\nfunction main(): i32 { return 0; }\n", []string{"E064"}},
		{"unknown-genarg-field", "struct S { m: Map[string, Nope] }\nfunction main(): i32 { return 0; }\n", []string{"E064"}},
		{"unknown-arrelem-var", "function main(): i32 { var xs: Nope[] = []; return 0; }\n", []string{"E064"}},
		// Negative controls: a valid array-element / generic-argument type must
		// NOT draw E064 — the widening only checks the INNER name, never the
		// builtin generic base (Map / Cell), so these stay clean like native.
		{"arrelem-ok-param", "function f(a: string[]): i32 { return a.len(); }\nfunction main(): i32 { return 0; }\n", nil},
		{"genarg-cell-ok-param", "function f(c: Cell[i32]): i32 { return 0; }\nfunction main(): i32 { return 0; }\n", nil},
		{"genarg-map-ok-param", "function f(m: Map[string, i32]): i32 { return 0; }\nfunction main(): i32 { return 0; }\n", nil},
		{"arrelem-ok-field", "struct S { xs: string[] }\nfunction main(): i32 { return 0; }\n", nil},
		// E021 (#4347): an impl that omits a REQUIRED (abstract) trait method.
		// A complete impl and a default-only trait (whose default is synthesised
		// onto the omitting impl) stay clean, matching the Go oracle.
		{"impl-missing-method", "trait Greet { function hello(): i32; }\nstruct Dog {}\nimpl Greet for Dog {}\nfunction main(): i32 { return 0; }\n", []string{"E021"}},
		{"impl-complete-ok", "trait Greet { function hello(): i32; }\nstruct Dog {}\nimpl Greet for Dog { function hello(): i32 { return 1; } }\nfunction main(): i32 { return 0; }\n", nil},
		{"impl-default-omitted-ok", "trait Greet { function hi(): i32 { return 9; } }\nstruct Dog {}\nimpl Greet for Dog {}\nfunction main(): i32 { return 0; }\n", nil},
		{"impl-missing-one-of-two", "trait Two { function a(): i32; function b(): i32; }\nstruct S {}\nimpl Two for S { function a(): i32 { return 1; } }\nfunction main(): i32 { return 0; }\n", []string{"E021"}},
		// E021 signature mismatch (#4347 slice 2): the impl provides the method
		// but with the wrong arity / param type / return type vs the trait's
		// declaration (Self resolves to the impl type). A correct impl is clean.
		{"impl-sig-arity", "trait T { function m(self: Self, x: i32): i32; }\nstruct S { v: i32 }\nimpl T for S { function m(self: Self): i32 { return 1; } }\nfunction main(): i32 { return 0; }\n", []string{"E021"}},
		{"impl-sig-ret", "trait T { function m(self: Self): i32; }\nstruct S { v: i32 }\nimpl T for S { function m(self: Self): string { return \"x\"; } }\nfunction main(): i32 { return 0; }\n", []string{"E021"}},
		{"impl-sig-paramtype", "trait T { function m(self: Self, x: i32): i32; }\nstruct S { v: i32 }\nimpl T for S { function m(self: Self, x: string): i32 { return 1; } }\nfunction main(): i32 { return 0; }\n", []string{"E021"}},
		{"impl-sig-correct-ok", "trait T { function m(self: Self, x: i32): i32; }\nstruct S { v: i32 }\nimpl T for S { function m(self: Self, x: i32): i32 { return x; } }\nfunction main(): i32 { return 0; }\n", nil},
		// E021 supertrait conformance (#4347 slice 3): `impl B for S` where
		// `trait B: A` requires a separate `impl A for S`. Missing it (or any one
		// of several supertraits) draws E021; providing all of them is clean.
		{"impl-supertrait-missing", "trait A { function a(self: Self): i32; }\ntrait B: A { function b(self: Self): i32; }\nstruct S { v: i32 }\nimpl B for S { function b(self: Self): i32 { return 1; } }\nfunction main(): i32 { return 0; }\n", []string{"E021"}},
		{"impl-supertrait-multi-missing", "trait A { function a(self: Self): i32; }\ntrait C { function c(self: Self): i32; }\ntrait B: A + C { function b(self: Self): i32; }\nstruct S { v: i32 }\nimpl A for S { function a(self: Self): i32 { return 2; } }\nimpl B for S { function b(self: Self): i32 { return 1; } }\nfunction main(): i32 { return 0; }\n", []string{"E021"}},
		{"impl-supertrait-satisfied-ok", "trait A { function a(self: Self): i32; }\ntrait B: A { function b(self: Self): i32; }\nstruct S { v: i32 }\nimpl A for S { function a(self: Self): i32 { return 2; } }\nimpl B for S { function b(self: Self): i32 { return 1; } }\nfunction main(): i32 { return 0; }\n", nil},
		// E021 generic-bound conformance AT CALL SITES (#4842): calling
		// `f[T: Tr](x)` with an argument whose concrete type doesn't implement
		// Tr draws E021 — the last unported E021 shape (the impl-side family
		// above is #4347). A struct or primitive with no `impl Tr` fails; an
		// explicit impl, an `@derive(Tr)` (whose synthesised methods witness
		// conformance), and an argument that stays generic (an opaque type
		// param — the checker can't know its instantiation, so it never fires)
		// are all clean. A `T: A + B` bound missing either trait fails. Each
		// verified against the Go oracle (the native checker emits the same set).
		{"bound-struct-no-impl", "trait Ord { function cmp(self: Self, other: Self): i32; }\nstruct Foo { x: i32 }\nfunction pick[T: Ord](a: T, b: T): T { return a; }\nfunction main(): i32 { var p: Foo = Foo { x: 1 }; var r: Foo = pick(p, p); return r.x; }\n", []string{"E021"}},
		{"bound-prim-no-impl", "trait Ord { function cmp(self: Self, other: Self): i32; }\nfunction pick[T: Ord](a: T): T { return a; }\nfunction main(): i32 { return pick(3); }\n", []string{"E021"}},
		{"bound-struct-impl-ok", "trait Ord { function cmp(self: Self, other: Self): i32; }\nstruct Foo { x: i32 }\nimpl Ord for Foo { function cmp(self: Self, other: Self): i32 { return 0; } }\nfunction pick[T: Ord](a: T): T { return a; }\nfunction main(): i32 { var p: Foo = Foo { x: 1 }; var r: Foo = pick(p); return r.x; }\n", nil},
		// bound-derive-ok carries `impl Ord for i32`: the derived `cmp`
		// dispatches per-field to the FIELD type's impl (synthOrd emits
		// `self.x.cmp(other.x)` — the ord_struct_enum e2e fixture is the
		// design reference), so without it the derive is rejected by the
		// E021 field-conformance pre-check below (#5392).
		{"bound-derive-ok", "trait Ord { function cmp(self: Self, other: Self): i32; }\nimpl Ord for i32 { function cmp(self: Self, other: Self): i32 { if (self < other) { return 0 - 1; } if (self > other) { return 1; } return 0; } }\n@derive(Ord)\nstruct Foo { x: i32 }\nfunction pick[T: Ord](a: T): T { return a; }\nfunction main(): i32 { var p: Foo = Foo { x: 1 }; var r: Foo = pick(p); return r.x; }\n", nil},
		// E021 @derive field conformance (#5392): deriving Eq / Ord / Hash
		// for a type whose field (or enum variant payload) type does not
		// implement the trait — no impl, no derive of its own, no method
		// set — draws ONE positioned E021 at the deriving decl instead of
		// the position-less per-field E043 garbage the ill-typed
		// synthesized body used to surface. With the impl present the
		// derive is clean; a two-field gap still reports a single error.
		{"derive-field-no-impl", "trait Ord { function cmp(self: Self, other: Self): i32; }\n@derive(Ord)\nstruct Foo { x: i32 }\nfunction main(): i32 { var p: Foo = Foo { x: 1 }; return p.x; }\n", []string{"E021"}},
		{"derive-field-impl-ok", "trait Ord { function cmp(self: Self, other: Self): i32; }\nimpl Ord for i32 { function cmp(self: Self, other: Self): i32 { if (self < other) { return 0 - 1; } if (self > other) { return 1; } return 0; } }\n@derive(Ord)\nstruct Foo { x: i32 }\nfunction main(): i32 { var p: Foo = Foo { x: 1 }; return p.x; }\n", nil},
		{"derive-enum-payload-no-impl", "trait Eq { function eq(self: Self, other: Self): boolean; }\n@derive(Eq)\nenum E { A, B(i32) }\nfunction main(): i32 { return 0; }\n", []string{"E021"}},
		{"derive-two-fields-no-impl", "trait Ord { function cmp(self: Self, other: Self): i32; }\n@derive(Ord)\nstruct P { x: i32, y: i32 }\nfunction main(): i32 { return 0; }\n", []string{"E021"}},
		// The pre-check covers every derivable kind whose synthesised body
		// calls the trait method on each field, not just Eq/Ord/Hash:
		// Display / Debug / Json compose identically (`self.f.to_string()`
		// / `to_debug()` / `to_json()`), so each draws the same positioned
		// E021 at the deriving decl instead of the position-less E043 that
		// names the trait's method as a missing field.
		//
		// The BROKEN path agrees too. It did not always: the self-host
		// synthesises derived bodies at PARSE time — before any `impl` is
		// known — so the ill-typed `self.f.<method>()` survived to its
		// checker and stacked a position-less E043 on top of the E021.
		// Native never has that body, because it skips synthesis once the
		// pre-check fires. The self-host now reaches the same end state
		// from the other side: e021_derive_field_diags hands back the
		// "Type.method" key of each derive it condemned, and the body loop
		// declines to check exactly those synthesised functions
		// (derive_body_suppressed). Only synthesised methods sit at 0:0, so
		// a user-written method of the same name is still checked.
		//
		// This spans all six field-wise kinds, not the three whose gate
		// #5948 widened. Eq/Ord/Hash were said to escape it because their
		// bodies render inline (`==` / `<`) — but that holds only for a
		// SCALAR field. Over a NOMINAL one, `Ord`'s body calls `.cmp()` and
		// diverged identically; `derive-ord-field-broken` pins it.
		{"derive-display-impl-ok", "trait Display { function to_string(self: Self): string; }\nimpl Display for i32 { function to_string(self: Self): string { return \"n\"; } }\n@derive(Display)\nstruct Foo { x: i32 }\nfunction main(): i32 { return 0; }\n", nil},
		// Debug uses a NOMINAL field: the self-host renders Debug
		// type-directed, sending scalars through `to_string` rather than
		// `to_debug`, so a scalar carrying only a Debug impl diverges for
		// reasons of its own. A nominal field routes through `to_debug` on
		// both sides.
		{"derive-debug-impl-ok", "trait Debug { function to_debug(self: Self): string; }\nstruct Bare { n: i32 }\nimpl Debug for Bare { function to_debug(self: Self): string { return \"b\"; } }\n@derive(Debug)\nstruct Foo { b: Bare }\nfunction main(): i32 { return 0; }\n", nil},
		{"derive-json-impl-ok", "trait Json { function to_json(self: Self): string; }\nimpl Json for i32 { function to_json(self: Self): string { return \"0\"; } }\n@derive(Json)\nstruct Foo { x: i32 }\nfunction main(): i32 { return 0; }\n", nil},
		// The broken path, one per field-wise kind: a nominal field with no
		// impl of the derived trait. Each is E021 ALONE — an E043 here means
		// the synthesised body escaped suppression.
		{"derive-display-field-broken", "trait Display { function to_string(self: Self): string; }\nstruct Bare { n: i32 }\n@derive(Display)\nstruct HasBare { b: Bare }\nfunction main(): i32 { return 0; }\n", []string{"E021"}},
		{"derive-debug-field-broken", "trait Debug { function to_debug(self: Self): string; }\nstruct Bare { n: i32 }\n@derive(Debug)\nstruct HasBare { b: Bare }\nfunction main(): i32 { return 0; }\n", []string{"E021"}},
		{"derive-json-field-broken", "trait Json { function to_json(self: Self): string; }\nstruct Bare { n: i32 }\n@derive(Json)\nstruct HasBare { b: Bare }\nfunction main(): i32 { return 0; }\n", []string{"E021"}},
		{"derive-ord-field-broken", "trait Ord { function cmp(self: Self, other: Self): i32; }\nstruct Bare { n: i32 }\n@derive(Ord)\nstruct HasBare { b: Bare }\nfunction main(): i32 { return 0; }\n", []string{"E021"}},
		// The ENUM path: derives are stamped on each variant, and the
		// condemned key is the ENUM's name, not a variant's.
		{"derive-debug-enum-broken", "trait Debug { function to_debug(self: Self): string; }\nstruct Bare { n: i32 }\n@derive(Debug)\nenum E { A(Bare), B(i32) }\nfunction main(): i32 { return 0; }\n", []string{"E021"}},
		// Suppression is keyed to the condemned derive, not to E043 at
		// large: a genuine bad field access is still reported, and so is a
		// USER-written method of a derived trait's name (it sits at a real
		// position, so it never matches a synthesised key).
		{"real-e043-still-reported", "struct S { n: i32 }\nfunction main(): i32 { var s: S = S { n: 1 }; return s.missing; }\n", []string{"E043"}},
		{"user-written-method-still-checked", "trait Debug { function to_debug(self: Self): string; }\nstruct Bare { n: i32 }\nstruct Foo { b: Bare }\nimpl Debug for Foo { function to_debug(self: Self): string { return self.nope; } }\nfunction main(): i32 { return 0; }\n", []string{"E043"}},
		// u8 / char are named by ast.ReceiverTypeName, so the pre-check must
		// reason about them too; e021_derive_field_known omitted both, which
		// left native reporting E021 where the self-host reported only the
		// E043 from the body it then failed to suppress.
		{"derive-u8-field-broken", "trait Debug { function to_debug(self: Self): string; }\n@derive(Debug)\nstruct Foo { b: u8 }\nfunction main(): i32 { return 0; }\n", []string{"E021"}},
		{"derive-char-field-broken", "trait Debug { function to_debug(self: Self): string; }\n@derive(Debug)\nstruct Foo { c: char }\nfunction main(): i32 { return 0; }\n", []string{"E021"}},
		{"bound-opaque-generic-ok", "trait Ord { function cmp(self: Self, other: Self): i32; }\nfunction inner[T: Ord](a: T): T { return a; }\nfunction outer[U](x: U): U { return inner(x); }\nfunction main(): i32 { return 0; }\n", nil},
		{"bound-multi-missing", "trait A { function fa(self: Self): i32; }\ntrait B { function fb(self: Self): i32; }\nstruct S { v: i32 }\nimpl A for S { function fa(self: Self): i32 { return self.v; } }\nfunction need[T: A + B](x: T): T { return x; }\nfunction main(): i32 { var s: S = S { v: 1 }; var r: S = need(s); return r.v; }\n", []string{"E021"}},
		// E021 object-safety (#4347 slice 4): a `dyn T` param whose trait T is not
		// object-safe draws E021 — T has an associated function (no self) or a
		// Self-returning method, neither of which can dispatch through a dyn
		// vtable. An object-safe trait (all methods take self, non-Self return)
		// is fine as `dyn T`.
		{"dyn-unsafe-assoc-fn", "trait T { function make(): Self; }\nfunction f(x: dyn T): i32 { return 0; }\nfunction main(): i32 { return 0; }\n", []string{"E021"}},
		{"dyn-unsafe-self-return", "trait T { function m(self: Self): Self; }\nfunction f(x: dyn T): i32 { return 0; }\nfunction main(): i32 { return 0; }\n", []string{"E021"}},
		// E060 (#4347): `d as? T` on a dyn-annotated local — T must be a
		// declared struct/enum implementing every trait in the dyn set. A
		// primitive target draws the target-shape arm; a declared struct
		// missing the impl draws the not-implementing arm; a correct
		// downcast is clean from both checkers.
		{"as-downcast-nonimpl-target", "trait Shape { function area(self: Self): i32; }\nstruct Circle { r: i32 }\nstruct Square { s: i32 }\nimpl Shape for Circle { function area(self: Self): i32 { return self.r; } }\nfunction main(): i32 {\n    var d: dyn Shape = Circle { r: 3 };\n    match (d as? Square) { Some(sq) => { return sq.s; }, None => { return 0; } }\n}\n", []string{"E060"}},
		{"as-downcast-prim-target", "trait Shape { function area(self: Self): i32; }\nstruct Circle { r: i32 }\nimpl Shape for Circle { function area(self: Self): i32 { return self.r; } }\nfunction main(): i32 {\n    var d: dyn Shape = Circle { r: 3 };\n    match (d as? i32) { Some(x) => { return x; }, None => { return 0; } }\n}\n", []string{"E060"}},
		{"as-downcast-impl-ok", "trait Shape { function area(self: Self): i32; }\nstruct Circle { r: i32 }\nimpl Shape for Circle { function area(self: Self): i32 { return self.r; } }\nfunction main(): i32 {\n    var d: dyn Shape = Circle { r: 3 };\n    match (d as? Circle) { Some(c) => { return c.r; }, None => { return 0; } }\n}\n", nil},
		// E062 (#4347): `d.m()` on `dyn A + B` where BOTH traits declare m is
		// ambiguous (the E006 rides along from the two impls both writing m on
		// S; the impl bodies avoid `self` in the clashing method so the Go
		// checker's post-E006 cascade emits no E001 and the sets match
		// exactly). Distinct method names dispatch cleanly.
		{"dyn-ambiguous-method", "trait A { function m(self: Self): i32; }\ntrait B { function m(self: Self): i32; }\nstruct S { v: i32 }\nimpl A for S { function m(self: Self): i32 { return self.v; } }\nimpl B for S { function m(self: Self): i32 { return 7; } }\nfunction main(): i32 {\n    var d: dyn A + B = S { v: 3 };\n    return d.m();\n}\n", []string{"E006", "E062"}},
		{"dyn-multi-trait-dispatch-ok", "trait A { function m(self: Self): i32; }\ntrait B { function n(self: Self): i32; }\nstruct S { v: i32 }\nimpl A for S { function m(self: Self): i32 { return self.v; } }\nimpl B for S { function n(self: Self): i32 { return 7; } }\nfunction main(): i32 {\n    var d: dyn A + B = S { v: 3 };\n    return d.m() + d.n();\n}\n", nil},
		{"dyn-object-safe-ok", "trait T { function m(self: Self): i32; }\nfunction f(x: dyn T): i32 { return 0; }\nfunction main(): i32 { return 0; }\n", nil},
		{"rec-local-ok", "function main(): i32 { function f(n: i32): i32 { if (n <= 0) { return 0; } return f(n - 1); } return f(3); }\n", nil},
		{"rec-local-capture-ok", "function main(): i32 { var base: i32 = 10; function f(n: i32): i32 { if (n <= 0) { return base; } return 1 + f(n - 1); } return f(3); }\n", nil},
		// Range-for `for i in LOW..HIGH` (#2699 self-host IR slice): the loop
		// var is an i32 over the half-open interval. A clean program draws no
		// codes from EITHER checker — the differential proves the self-host
		// checker binds the range var to i32 (no spurious E001 / E008) the
		// same way the Go checker does (it desugars to a C-style for).
		{"range-clean", "function main(): i32 { var s = 0; for i in 0..5 { s = s + i; } return s; }\n", nil},
		{"range-nested-clean", "function main(): i32 { var t = 0; for i in 0..3 { for j in 0..3 { t = t + i + j; } } return t; }\n", nil},
		{"range-expr-bounds-clean", "function main(): i32 { var n = 4; var s = 0; for i in 1..n + 1 { s = s + i; } return s; }\n", nil},
		// Type ascription `e as T` (#2669): a zero-cost annotation. An array /
		// string ascription draws no diagnostic from EITHER checker — the
		// self-host only flags E033 when both sides are scalar primitives, and
		// the Go checker accepts the cast as an upcast assignable to the target.
		{"asc-arr-clean", "function main(): i32 { var a = [] as i32[]; a = [1, 2]; return a[0] + a[1]; }\n", nil},
		{"asc-str-clean", "function main(): i32 { var s = \"x\" as string; return s.len(); }\n", nil},
		// Non-binding-position ascription (#2669) — arg / return / nested — is
		// also clean from both checkers.
		{"asc-arg-clean", "function id(a: i32[]): i32 { return a.len(); }\nfunction main(): i32 { var a = [1, 2]; return id(a as i32[]); }\n", nil},
		{"asc-ret-clean", "function mk(): i32[] { var a = [1, 2]; return a as i32[]; }\nfunction main(): i32 { return mk()[0]; }\n", nil},
		// break / continue inside `for` loops (#2788) — clean from both checkers.
		{"for-continue-clean", "function main(): i32 { var s = 0; for i in 0..5 { if (i == 2) { continue; } s = s + i; } return s; }\n", nil},
		{"for-break-clean", "function main(): i32 { var a = [1, 2, 3]; var s = 0; for x in a { if (x == 3) { break; } s = s + x; } return s; }\n", nil},
		// E058 (labeled break/continue names no enclosing loop, #2857): a
		// labeled `break L` / `continue L` whose `L` matches no enclosing loop
		// label is E058. A valid label (the enclosing loop's, or an outer one
		// from a nested loop) draws no code — matching the Go checker, which
		// tracks the enclosing-loop label stack.
		{"break-bad-label", "function main(): i32 { var c = 0; outer: while (c < 5) { c = c + 1; if (c == 2) { break nope; } } return c; }\n", []string{"E058"}},
		{"continue-bad-label", "function main(): i32 { var c = 0; outer: while (c < 5) { c = c + 1; if (c == 2) { continue nope; } } return c; }\n", []string{"E058"}},
		{"break-good-label-clean", "function main(): i32 { var c = 0; outer: while (c < 5) { c = c + 1; var j = 0; while (j < 5) { j = j + 1; if (j == 2) { break outer; } } } return c; }\n", nil},
		{"continue-good-label-clean", "function main(): i32 { var c = 0; outer: while (c < 3) { c = c + 1; var j = 0; while (j < 3) { j = j + 1; if (j == 1) { continue outer; } } } return c; }\n", nil},
		// A labeled break that targets the INNERMOST loop's own label is in
		// scope (depth 0) — clean.
		{"break-self-label-clean", "function main(): i32 { var c = 0; inner: while (c < 5) { c = c + 1; if (c == 2) { break inner; } } return c; }\n", nil},
		// E011 still wins for an out-of-loop labeled break (the two never both
		// fire) — a labeled break with no enclosing loop at all is E011.
		{"labeled-break-no-loop", "function main(): i32 { break nope; return 0; }\n", []string{"E011"}},
		// E061 (value-position block has no trailing value, #2857): an if/match
		// used as a value whose branch ends in a `;`-terminated statement (no
		// tail expression) has no result. parse_branch_body tags that branch
		// with a marker the checker turns into E061 — matching the Go checker.
		// Both branches value-less (so they agree as void → no E031) and an
		// un-annotated var (→ no E003) isolate E061 as the only code.
		{"if-branch-no-tail", "function f(): i32 { return 0; }\nfunction main(): i32 { var x = if (true) { f(); } else { f(); }; return 0; }\n", []string{"E061"}},
		{"match-arm-no-tail", "function f(): i32 { return 0; }\nfunction main(): i32 { var a = match (1) { 1 => { f(); }, _ => { f(); } }; return 0; }\n", []string{"E061"}},
		// A value if/match WITH trailing values (incl. leading statements before
		// the tail) draws no E061 from either checker.
		{"if-branch-tail-ok", "function main(): i32 { var x = if (true) { 1 } else { 2 }; return x; }\n", nil},
		{"if-branch-leading-then-tail-ok", "function main(): i32 { var x = if (true) { var k = 1; k + 1 } else { 0 }; return x; }\n", nil},
		// E059 (`as?` downcast requires a `dyn Trait` value on the left, #2857):
		// the operand of `x as? T` must be a `dyn Trait` value. A concrete
		// scalar (i32 / string) on the left can never be dyn, so it's E059 —
		// matching the Go checker. (A real dyn value types to `unknown` in the
		// self-host and is left alone, like the E033/E042 conservatism.) The
		// regular `as` cast is unaffected (it's the `as_` op, not `as?_`).
		{"as-downcast-i32-left", "struct P { x: i32 }\nfunction main(): i32 { var a = 5; var b = a as? P; return 0; }\n", []string{"E059"}},
		{"as-downcast-string-left", "struct P { x: i32 }\nfunction main(): i32 { var s = \"hi\"; var b = s as? P; return 0; }\n", []string{"E059"}},
		{"regular-as-cast-ok", "function main(): i32 { var a = 5; var b = a as i32; return b; }\n", nil},
		// `for x in <EXPR>` over a non-ident array iterable — clean from both checkers.
		{"for-literal-clean", "function main(): i32 { var s = 0; for x in [1, 2, 3] { s = s + x; } return s; }\n", nil},
		{"for-call-clean", "function mk(): i32[] { return [1, 2]; }\nfunction main(): i32 { var s = 0; for x in mk() { s = s + x; } return s; }\n", nil},
		// Unannotated struct-array literal (`var ps = [P{..}, ..]`) — element type
		// inferred, clean from both checkers.
		{"inferred-struct-array-clean", "struct P { v: i32 }\nfunction main(): i32 { var ps = [P { v: 3 }, P { v: 4 }]; return ps[0].v + ps[1].v; }\n", nil},
		// Tuple literal with an i32[] element — clean from both checkers.
		{"tuple-arr-elem-clean", "function main(): i32 { var t = ([10, 20], 9); var a = t.0; return a[0] + t.1; }\n", nil},
		{"dup-field", "struct P { x: i32, x: i32 }\nfunction main(): i32 { return 0; }\n", []string{"E007"}},
		{"dup-param", "function f(a: i32, a: i32): i32 { return a; }\nfunction main(): i32 { return 0; }\n", []string{"E018"}},
		{"dup-field-and-param", "struct P { y: i32, y: i32 }\nfunction g(b: i32, b: i32): i32 { return b; }\nfunction main(): i32 { return 0; }\n", []string{"E007", "E018"}},
		{"clean-struct-and-func", "struct Q { a: i32, b: string }\nfunction h(x: i32, y: i32): i32 { return x + y; }\nfunction main(): i32 { return 0; }\n", nil},
		{"func-redeclared", "function f(): i32 { return 1; }\nfunction f(): i32 { return 2; }\nfunction main(): i32 { return 0; }\n", []string{"E006"}},
		{"method-redeclared", "struct P { x: i32 }\nfunction (p: P) m(): i32 { return 1; }\nfunction (p: P) m(): i32 { return 2; }\nfunction main(): i32 { return 0; }\n", []string{"E006"}},
		{"free-and-method-same-name-ok", "struct P { x: i32 }\nfunction m(): i32 { return 1; }\nfunction (p: P) m(): i32 { return 2; }\nfunction main(): i32 { return 0; }\n", nil},
		{"return-mismatch", "function main(): i32 { var s: string = \"x\"; return s; }\n", []string{"E002"}},
		{"return-mismatch-nested", "function f(): i32 { if (true) { return \"no\"; } return 1; }\nfunction main(): i32 { return 0; }\n", []string{"E002"}},
		{"return-ok", "function f(): string { var s: string = \"x\"; return s; }\nfunction main(): i32 { return 0; }\n", nil},
		{"struct-missing-field", "struct P { x: i32, y: i32 }\nfunction main(): i32 { var p: P = P { x: 1 }; return p.x; }\n", []string{"E005"}},
		{"struct-nested-missing", "struct Q { a: i32 }\nstruct P { q: Q }\nfunction main(): i32 { var p: P = P { q: Q {} }; return 0; }\n", []string{"E005"}},
		// E043 (unknown-field-in-literal): a struct literal naming a field the
		// struct doesn't declare (incl. update literals). All-declared is clean.
		{"struct-extra-field", "struct P { x: i32 }\nfunction main(): i32 { var p = P { x: 1, y: 2 }; return p.x; }\n", []string{"E043"}},
		{"struct-extra-update", "struct P { x: i32, y: i32 }\nfunction f(p: P): P { return P { ...p, z: 9 }; }\nfunction main(): i32 { return 0; }\n", []string{"E043"}},
		{"struct-all-fields-ok", "struct P { x: i32, y: i32 }\nfunction main(): i32 { var p = P { x: 1, y: 2 }; return p.x + p.y; }\n", nil},
		{"struct-complete-ok", "struct P { x: i32, y: i32 }\nfunction main(): i32 { var p: P = P { x: 1, y: 2 }; return p.x; }\n", nil},
		{"struct-field-type-mismatch", "struct P { x: i32, y: i32 }\nfunction main(): i32 { var p: P = P { x: 1, y: \"no\" }; return 0; }\n", []string{"E043"}},
		{"struct-field-type-string-ok", "struct P { x: i32, name: string }\nfunction main(): i32 { var p: P = P { x: 1, name: \"hi\" }; return p.x; }\n", nil},
		{"struct-field-array-mismatch", "struct P { xs: i32[] }\nfunction main(): i32 { var p: P = P { xs: 5 }; return 0; }\n", []string{"E043"}},
		{"struct-field-array-ok", "struct P { xs: i32[] }\nfunction main(): i32 { var p: P = P { xs: [1, 2, 3] }; return 0; }\n", nil},
		// E034 (typed composite-array element): an element of a `var x: Elem[]`
		// literal must be assignable to Elem. A union element type widens
		// (members ok); a non-member, a wrong struct, or a primitive is E034.
		{"typed-arr-struct-bad-prim", "struct P { x: i32 }\nfunction main(): i32 { var a: P[] = [P { x: 1 }, 5]; return 0; }\n", []string{"E034"}},
		{"typed-arr-struct-bad-struct", "struct P { x: i32 }\nstruct Q { y: i32 }\nfunction main(): i32 { var a: P[] = [P { x: 1 }, Q { y: 2 }]; return 0; }\n", []string{"E034"}},
		{"typed-arr-struct-ok", "struct P { x: i32 }\nfunction main(): i32 { var a: P[] = [P { x: 1 }, P { x: 2 }]; return 0; }\n", nil},
		{"typed-arr-union-ok", "struct P { x: i32 }\nstruct Q { y: i32 }\ntype U = P | Q;\nfunction main(): i32 { var a: U[] = [P { x: 1 }, Q { y: 2 }]; return 0; }\n", nil},
		{"typed-arr-union-bad", "struct P { x: i32 }\nstruct Q { y: i32 }\nstruct R { z: i32 }\ntype U = P | Q;\nfunction main(): i32 { var a: U[] = [P { x: 1 }, R { z: 3 }]; return 0; }\n", []string{"E034"}},
		// E034 in non-var positions: the same composite-array element check at
		// a `T[]` return, a `T[]` call argument, and a reassignment to a `T[]`
		// variable. Union element types still widen (members ok).
		{"arr-elem-return-bad", "struct P { x: i32 }\nfunction f(): P[] { return [P { x: 1 }, 5]; }\nfunction main(): i32 { return 0; }\n", []string{"E034"}},
		{"arr-elem-return-ok", "struct P { x: i32 }\nstruct Q { y: i32 }\ntype U = P | Q;\nfunction f(): U[] { return [P { x: 1 }, Q { y: 2 }]; }\nfunction main(): i32 { return 0; }\n", nil},
		{"arr-elem-arg-bad", "struct P { x: i32 }\nfunction f(a: P[]): i32 { return 0; }\nfunction main(): i32 { return f([P { x: 1 }, 5]); }\n", []string{"E034"}},
		{"arr-elem-assign-bad", "struct P { x: i32 }\nfunction main(): i32 { var a: P[] = [P { x: 1 }]; a = [P { x: 1 }, 5]; return 0; }\n", []string{"E034"}},
		// E034 at a struct-literal field of composite-array type: the field
		// value's elements are checked against the field's element type (plain
		// and `...base` literals). Union fields widen; the whole-value scalar
		// mismatch stays E043.
		{"arr-elem-field-bad", "struct Q { n: i32 }\nstruct P { xs: Q[] }\nfunction main(): i32 { var p = P { xs: [Q { n: 1 }, 5] }; return 0; }\n", []string{"E034"}},
		{"arr-elem-field-update-bad", "struct Q { n: i32 }\nstruct P { xs: Q[] }\nfunction f(p: P): P { return P { ...p, xs: [Q { n: 1 }, 5] }; }\nfunction main(): i32 { return 0; }\n", []string{"E034"}},
		{"arr-elem-field-union-ok", "struct A { a: i32 }\nstruct B { b: i32 }\ntype U = A | B;\nstruct P { xs: U[] }\nfunction main(): i32 { var p = P { xs: [A { a: 1 }, B { b: 2 }] }; return 0; }\n", nil},
		// E038 (builtin array-method arg type): `.append(elem)` and the value of
		// `.with(i32, elem)` must match the array's element type. A union
		// element widens; a correctly-typed call stays clean.
		{"arr-append-arg-bad", "function main(): i32 { var a: i32[] = [1]; a = a.append(\"x\"); return 0; }\n", []string{"E038"}},
		{"arr-append-arg-ok", "function main(): i32 { var a: i32[] = [1]; a = a.append(2); return 0; }\n", nil},
		{"arr-with-arg-bad", "function main(): i32 { var a: i32[] = [1]; a = a.with(0, \"x\"); return 0; }\n", []string{"E038"}},
		{"arr-append-union-ok", "struct P { x: i32 }\nstruct Q { y: i32 }\ntype U = P | Q;\nfunction main(): i32 { var a: U[] = [P { x: 1 }]; a = a.append(Q { y: 2 }); return 0; }\n", nil},
		// E043 (method-call on a numeric scalar): i32 / f64 carry no methods,
		// so any `x.m(...)` on one is a field access on a non-struct. Valid
		// string / array / struct method calls stay clean (no false positive).
		{"method-on-i32", "function main(): i32 { var x: i32 = 3; x.foo(); return 0; }\n", []string{"E043"}},
		{"method-on-f64", "function main(): i32 { var f: f64 = 1.0; f.foo(); return 0; }\n", []string{"E043"}},
		{"method-on-string-ok", "function main(): i32 { var s: string = \"a\"; return s.len(); }\n", nil},
		{"method-on-i32-user-method-ok", "function (n: i32) twice(): i32 { return n * 2; }\nfunction main(): i32 { var x: i32 = 21; return x.twice(); }\n", nil},
		// E043 (array method existence): only append / with / len are
		// unconditional array builtins; everything else is an auto-discovered
		// std/array function, so `a.sum()` / `a.bogus()` without `import
		// "std/array"` is a call to a non-existent method. append / with / len
		// stay clean. (The import path — where the std/array functions ARE in
		// scope — is covered by TestSelfHostCheckerBundleDifferentialX86_64.)
		{"method-on-array-sum-noimp", "function main(): i32 { var a: i32[] = [1]; return a.sum(); }\n", []string{"E043"}},
		{"method-on-array-bogus", "function main(): i32 { var a: i32[] = [1]; return a.bogus(); }\n", []string{"E043"}},
		{"method-on-array-append-ok", "function main(): i32 { var a: i32[] = []; a = a.append(1); return a.len(); }\n", nil},
		{"method-on-array-with-ok", "function main(): i32 { var a: i32[] = [1, 2]; a = a.with(0, 9); return a.len(); }\n", nil},
		// E043 (string method existence): a string carries only the `len` /
		// `as_bytes` builtins; any other method must be user-defined (here,
		// none is in scope), else it's a call to a non-existent method.
		{"method-on-string-missing", "function main(): i32 { var s: string = \"a\"; var t = s.bogus(); return 0; }\n", []string{"E043"}},
		{"method-on-string-substr-missing", "function main(): i32 { var s: string = \"abc\"; var t = s.substr(0, 1); return 0; }\n", []string{"E043"}},
		{"method-on-string-as-bytes-ok", "function main(): i32 { var s: string = \"a\"; var b = s.as_bytes(); return 0; }\n", nil},
		{"method-on-string-user-method-ok", "function (s: string) shout(): string { return s; }\nfunction main(): i32 { var s = \"a\"; var t = s.shout(); return 0; }\n", nil},
		// E043 (struct method/field both missing): `p.m()` where struct P has
		// no method m and no field m. A declared method, or a present field
		// (closure-field call), is excluded — no false positive.
		{"method-on-struct-missing", "struct P { x: i32 }\nfunction main(): i32 { var p = P { x: 1 }; return p.nope(); }\n", []string{"E043"}},
		{"method-on-struct-defined-ok", "struct P { x: i32 }\nfunction (p: P) m(): i32 { return p.x; }\nfunction main(): i32 { var p = P { x: 1 }; return p.m(); }\n", nil},
		// E038 (struct non-function field called): `p.x()` where x is a field
		// whose type isn't a function. A function-typed field (closure call)
		// stays clean; a missing member is E043 (above), not E038.
		{"call-struct-field-i32", "struct P { x: i32 }\nfunction main(): i32 { var p = P { x: 1 }; return p.x(); }\n", []string{"E038"}},
		{"call-struct-field-string", "struct P { s: string }\nfunction main(): i32 { var p = P { s: \"a\" }; p.s(); return 0; }\n", []string{"E038"}},
		{"call-too-few-args", "function add(a: i32, b: i32): i32 { return a + b; }\nfunction main(): i32 { return add(1); }\n", []string{"E004"}},
		{"call-too-many-args", "function id(a: i32): i32 { return a; }\nfunction main(): i32 { return id(1, 2); }\n", []string{"E004"}},
		{"call-correct-arity-ok", "function add(a: i32, b: i32): i32 { return a + b; }\nfunction main(): i32 { return add(1, 2); }\n", nil},
		{"call-shadowed-local-ok", "function f(a: i32, b: i32): i32 { return a + b; }\nfunction main(): i32 { var f = function(x: i32): i32 { return x; }; return f(7); }\n", nil},
		{"method-too-few-args", "struct P { x: i32 }\nfunction (p: P) add(a: i32, b: i32): i32 { return p.x + a + b; }\nfunction main(): i32 { var p: P = P { x: 1 }; return p.add(5); }\n", []string{"E004"}},
		{"method-too-many-args", "struct P { x: i32 }\nfunction (p: P) one(a: i32): i32 { return p.x + a; }\nfunction main(): i32 { var p: P = P { x: 1 }; return p.one(5, 6); }\n", []string{"E004"}},
		{"method-correct-arity-ok", "struct P { x: i32 }\nfunction (p: P) add(a: i32, b: i32): i32 { return p.x + a + b; }\nfunction main(): i32 { var p: P = P { x: 1 }; return p.add(5, 6); }\n", nil},
		{"method-arg-type-mismatch", "struct P { x: i32 }\nfunction (p: P) add(a: i32): i32 { return p.x + a; }\nfunction main(): i32 { var p: P = P { x: 1 }; var s: string = \"n\"; return p.add(s); }\n", []string{"E038"}},
		{"method-arg-type-ok", "struct P { x: i32 }\nfunction (p: P) add(a: i32): i32 { return p.x + a; }\nfunction main(): i32 { var p: P = P { x: 1 }; return p.add(7); }\n", nil},
		{"method-arg-array-mismatch", "struct P { x: i32 }\nfunction (p: P) take(xs: string[]): i32 { return p.x; }\nfunction main(): i32 { var p: P = P { x: 1 }; var n: i32 = 5; return p.take(n); }\n", []string{"E038"}},
		{"method-arg-empty-array-ok", "struct P { x: i32 }\nfunction (p: P) take(xs: string[]): i32 { return p.x; }\nfunction main(): i32 { var p: P = P { x: 1 }; return p.take([]); }\n", nil},
		{"var-annotation-mismatch", "function main(): i32 { var x: i32 = \"no\"; return x; }\n", []string{"E003"}},
		{"assign-mismatch", "function main(): i32 { var x: i32 = 1; x = \"no\"; return x; }\n", []string{"E003"}},
		{"assign-ok", "function main(): i32 { var x: i32 = 1; x = 2; return x; }\n", nil},
		{"arg-type-mismatch", "function add(a: i32, b: i32): i32 { return a + b; }\nfunction main(): i32 { return add(1, \"no\"); }\n", []string{"E038"}},
		{"arg-type-ok", "function add(a: i32, b: i32): i32 { return a + b; }\nfunction main(): i32 { return add(1, 2); }\n", nil},
		// E038 (call-non-function variant): calling a value whose type isn't a
		// function. A free function / closure / fn-value local / method call is
		// fine; only a scalar / non-fn local callee is flagged.
		{"call-nonfn-i32", "function main(): i32 { var x = 5; return x(3); }\n", []string{"E038"}},
		{"call-nonfn-string", "function main(): i32 { var s = \"a\"; return s(3); }\n", []string{"E038"}},
		{"call-nonfn-noargs", "function main(): i32 { var x = 5; return x(); }\n", []string{"E038"}},
		{"call-closure-ok", "function main(): i32 { var g = function(x: i32): i32 { return x + 1; }; return g(41); }\n", nil},
		{"call-fnval-named-ok", "function dbl(n: i32): i32 { return n * 2; }\nfunction main(): i32 { var f = dbl; return f(21); }\n", nil},
		{"if-nonbool-cond", "function main(): i32 { if (5) { return 1; } return 0; }\n", []string{"E008"}},
		{"while-nonbool-cond", "function main(): i32 { while (\"x\") { return 1; } return 0; }\n", []string{"E008"}},
		{"if-bool-cond-ok", "function main(): i32 { if (1 < 2) { return 1; } return 0; }\n", nil},
		{"break-outside-loop", "function main(): i32 { break; return 0; }\n", []string{"E011"}},
		{"continue-outside-loop", "function main(): i32 { continue; return 0; }\n", []string{"E011"}},
		{"break-in-loop-ok", "function main(): i32 { while (1 < 2) { break; } return 0; }\n", nil},
		{"break-in-match-outside-loop", "enum E { A, B }\nfunction main(): i32 { var e: E = A; match (e) { A => { break; }, B => { } } return 0; }\n", []string{"E011"}},
		{"return-no-value-nonvoid", "function f(): i32 { return; }\nfunction main(): i32 { return 0; }\n", []string{"E012"}},
		{"return-no-value-void-ok", "function f(): void { return; }\nfunction main(): i32 { return 0; }\n", nil},
		{"return-no-value-nested", "function f(): i32 { if (1 < 2) { return; } return 0; }\nfunction main(): i32 { return 0; }\n", []string{"E012"}},
		{"dup-var-same-block", "function main(): i32 { var x: i32 = 1; var x: i32 = 2; return x; }\n", []string{"E013"}},
		{"dup-var-nested-shadow-ok", "function main(): i32 { var x: i32 = 1; if (1 < 2) { var x: i32 = 2; } return x; }\n", nil},
		{"var-shadows-param-ok", "function f(a: i32): i32 { var a: i32 = 1; return a; }\nfunction main(): i32 { return 0; }\n", nil},
		{"empty-array-no-annotation", "function main(): i32 { var x = []; return 0; }\n", []string{"E020"}},
		{"empty-array-annotated-ok", "function main(): i32 { var x: i32[] = []; return 0; }\n", nil},
		{"nonempty-array-ok", "function main(): i32 { var x = [1, 2]; return x[0]; }\n", nil},
		{"and-on-ints", "function main(): i32 { if (1 && 2) { return 1; } return 0; }\n", []string{"E009"}},
		{"not-on-int", "function main(): i32 { if (!5) { return 1; } return 0; }\n", []string{"E009"}},
		{"and-on-bools-ok", "function main(): i32 { if ((1 < 2) && (2 < 3)) { return 1; } return 0; }\n", nil},
		{"not-on-bool-ok", "function main(): i32 { if (!(1 < 2)) { return 1; } return 0; }\n", nil},
		// E009 (extended ops): unary `-`, shift / bitwise, and ordering on a
		// non-numeric operand. Numeric (i32 / f64) operands stay clean.
		{"neg-on-string", "function main(): i32 { var s = \"a\"; var n = -s; return 0; }\n", []string{"E009"}},
		{"shift-on-string", "function main(): i32 { var s = \"a\"; return s << 2; }\n", []string{"E009"}},
		{"bitand-on-string", "function main(): i32 { var s = \"a\"; return s & 1; }\n", []string{"E009"}},
		{"order-on-strings", "function main(): i32 { if (\"a\" < \"b\") { return 1; } return 0; }\n", []string{"E009"}},
		{"order-mismatch", "function main(): i32 { if (5 < \"x\") { return 1; } return 0; }\n", []string{"E009"}},
		{"order-i32-ok", "function main(): i32 { if (3 < 5) { return 1; } return 0; }\n", nil},
		{"order-f64-ok", "function main(): i32 { if (1.5 < 2.5) { return 1; } return 0; }\n", nil},
		{"neg-i32-ok", "function main(): i32 { var x = 5; return -x; }\n", nil},
		{"shift-i32-ok", "function main(): i32 { return 1 << 4; }\n", nil},
		{"eq-i32-string", "function main(): i32 { if (1 == \"x\") { return 1; } return 0; }\n", []string{"E041"}},
		{"eq-bool-i32", "function main(): i32 { if ((1 < 2) == 3) { return 1; } return 0; }\n", []string{"E041"}},
		{"eq-i32-i32-ok", "function main(): i32 { if (1 == 2) { return 1; } return 0; }\n", nil},
		{"eq-string-string-ok", "function main(): i32 { if (\"a\" == \"b\") { return 1; } return 0; }\n", nil},
		// E041 (composite ordering): `<` / `>` / `<=` / `>=` on two values of
		// the SAME composite type (struct / array / tuple) is E041, not E009.
		// Mixed composite/scalar or differing types stay E009; scalars are ok.
		{"order-struct-struct", "struct P { x: i32 }\nfunction main(): i32 { var a = P { x: 1 }; var b = P { x: 2 }; if (a < b) { return 1; } return 0; }\n", []string{"E041"}},
		{"order-array-array", "function main(): i32 { var a = [1]; var b = [2]; if (a <= b) { return 1; } return 0; }\n", []string{"E041"}},
		{"order-struct-i32-mixed", "struct P { x: i32 }\nfunction main(): i32 { var a = P { x: 1 }; if (a < 3) { return 1; } return 0; }\n", []string{"E009"}},
		{"field-unknown", "struct P { x: i32 }\nfunction main(): i32 { var p: P = P { x: 1 }; return p.y; }\n", []string{"E043"}},
		{"field-known-ok", "struct P { x: i32 }\nfunction main(): i32 { var p: P = P { x: 1 }; return p.x; }\n", nil},
		{"method-call-not-field-ok", "struct P { x: i32 }\nfunction (p: P) getx(): i32 { return p.x; }\nfunction main(): i32 { var p: P = P { x: 1 }; return p.getx(); }\n", nil},
		{"field-nested-unknown", "struct Q { a: i32 }\nstruct P { q: Q }\nfunction main(): i32 { var p: P = P { q: Q { a: 1 } }; return p.q.z; }\n", []string{"E043"}},
		// E043 (non-struct-value variant): a field READ on an i32 / string /
		// array (no fields). Method calls and struct/tuple field access stay ok.
		{"field-on-i32", "function main(): i32 { var x = 5; return x.foo; }\n", []string{"E043"}},
		{"field-on-string", "function main(): i32 { var s = \"a\"; return s.foo; }\n", []string{"E043"}},
		{"field-on-array", "function main(): i32 { var a = [1, 2, 3]; return a.foo; }\n", []string{"E043"}},
		{"str-method-not-field-ok", "function main(): i32 { var s = \"abc\"; return s.len(); }\n", nil},
		{"slice-low-non-i32", "function main(): i32 { var s: string = \"hello\"; var t: str = s[\"x\":3]; return 0; }\n", []string{"E037"}},
		{"slice-high-non-i32", "function main(): i32 { var s: string = \"hello\"; var t: str = s[1:\"y\"]; return 0; }\n", []string{"E037"}},
		{"slice-bounds-ok", "function main(): i32 { var s: string = \"hello\"; var t: str = s[1:3]; return 0; }\n", nil},
		{"slice-full-ok", "function main(): i32 { var s: string = \"hello\"; var t: string = s[:]; return 0; }\n", nil},
		{"tuple-field-non-numeric", "function main(): i32 { var t = (1, 2); return t.foo; }\n", []string{"E046"}},
		{"tuple-field-out-of-range", "function main(): i32 { var t = (1, 2); return t.5; }\n", []string{"E046"}},
		{"tuple-field-ok", "function main(): i32 { var t = (1, 2); return t.0; }\n", nil},
		// E003 (tuple var annotation): a tuple-literal init must match the
		// annotation element-wise (and in arity). Matching tuples — including
		// nested, union-element, and struct-element — stay clean.
		{"tuple-annot-elem-bad", "function main(): i32 { var t: (i32, string) = (1, 2); return 0; }\n", []string{"E003"}},
		{"tuple-annot-order-bad", "function main(): i32 { var t: (string, i32) = (1, 2); return 0; }\n", []string{"E003"}},
		{"tuple-annot-arity-bad", "function main(): i32 { var t: (i32, string) = (1, \"a\", 3); return 0; }\n", []string{"E003"}},
		{"tuple-annot-ok", "function main(): i32 { var t: (i32, string) = (1, \"a\"); return t.0; }\n", nil},
		{"tuple-annot-nested-ok", "function main(): i32 { var t: (i32, (string, i32)) = (1, (\"a\", 2)); return t.0; }\n", nil},
		{"tuple-annot-union-ok", "enum E { A, B }\nfunction main(): i32 { var t: (E, i32) = (A, 1); return 0; }\n", nil},
		{"arith-sub-string", "function main(): i32 { var n: i32 = 1 - \"x\"; return n; }\n", []string{"E009"}},
		{"arith-add-mismatch", "function main(): i32 { var s = 1 + \"x\"; return 0; }\n", []string{"E009"}},
		{"arith-mul-ok", "function main(): i32 { return 3 * 4; }\n", nil},
		{"string-concat-ok", "function main(): i32 { var s: string = \"a\" + \"b\"; return 0; }\n", nil},
		{"literal-too-big-i32", "function main(): i32 { var x: i32 = 3000000000; return 0; }\n", []string{"E047"}},
		{"literal-i32-max-ok", "function main(): i32 { var x: i32 = 2147483647; return 0; }\n", nil},
		{"literal-i32-maxplus1", "function main(): i32 { var x: i32 = 2147483648; return 0; }\n", []string{"E047"}},
		{"literal-fits-i32-ok", "function main(): i32 { var x: i32 = 2000000000; return 0; }\n", nil},
		{"enum-redeclared", "enum Opt { A, B }\nenum Opt { C, D }\nfunction main(): i32 { return 0; }\n", []string{"E006"}},
		{"enum-dup-variant", "enum Opt { A, A, B }\nfunction main(): i32 { return 0; }\n", []string{"E017"}},
		{"enum-clean-ok", "enum Opt { A, B }\nfunction main(): i32 { return 0; }\n", nil},
		// Struct form (#4363 item 4): the flat struct table also carries enum
		// variant payloads (enum_owner-tagged), which must not read as user
		// struct redeclarations — only two genuine `struct X` decls collide.
		{"struct-redeclared", "struct P { x: i32 }\nstruct P { y: i32 }\nfunction main(): i32 { return 0; }\n", []string{"E006"}},
		{"struct-beside-enum-payloads-ok", "enum A { X(i32) }\nenum B { Y(i32) }\nstruct S { n: i32 }\nfunction main(): i32 { return 0; }\n", nil},
		{"variant-multi-enum-ref", "enum A { X, Y }\nenum B { X, Z }\nfunction main(): i32 { var a: A = X; return 0; }\n", []string{"E036"}},
		{"variant-multi-enum-unref-ok", "enum A { X, Y }\nenum B { X, Z }\nfunction main(): i32 { return 0; }\n", nil},
		{"variant-disjoint-ref-ok", "enum A { P, Q }\nenum B { R, S }\nfunction main(): i32 { var a: A = P; return 0; }\n", nil},
		// E036 also covers a user-enum variant that collides with a BUILT-IN
		// enum variant (Option / Result / …): `enum O { Some(i32), None }`
		// shadows Option's Some / None, so a bare reference must be qualified.
		// Plain Option usage (no colliding user enum) stays clean.
		{"variant-builtin-collide-none", "enum O { Some(i32), None }\nfunction get(): O { return None; }\nfunction main(): i32 { return 0; }\n", []string{"E036"}},
		{"variant-builtin-collide-result", "enum E { Ok(i32), Bad }\nfunction get(): E { return Ok(1); }\nfunction main(): i32 { return 0; }\n", []string{"E036"}},
		{"variant-builtin-no-collide-ok", "function get(): Option[i32] { return None; }\nfunction main(): i32 { return 0; }\n", nil},
		// A QUALIFIED variant reference `Enum.Variant` is valid — the enum-name
		// qualifier is not a bare value, so it must not trip E001. Covers a
		// user enum, a collision resolved by qualifying, and a built-in enum.
		{"qualified-variant-user-ok", "enum Color { Red, Green }\nfunction get(): Color { return Color.Red; }\nfunction main(): i32 { return 0; }\n", nil},
		{"qualified-variant-collide-ok", "enum A { X, Y }\nenum B { X, Z }\nfunction get(): A { return A.X; }\nfunction main(): i32 { return 0; }\n", nil},
		{"qualified-variant-builtin-ok", "enum O { Some(i32), None }\nfunction get(): O { return O.None; }\nfunction main(): i32 { return 0; }\n", nil},
		// A qualified reference to a NON-existent variant is E036 ("enum X has
		// no variant Y") — for a user enum, a union alias, and a built-in enum.
		{"qualified-variant-enum-bad", "enum Color { Red, Green }\nfunction main(): i32 { return Color.Blue; }\n", []string{"E036"}},
		{"qualified-variant-union-bad", "struct A { x: i32 }\nstruct B { y: i32 }\ntype U = A | B;\nfunction main(): i32 { var u: U = U.Nope; return 0; }\n", []string{"E036"}},
		{"qualified-variant-builtin-bad", "function main(): i32 { var o: Option[i32] = Option.Foo; return 0; }\n", []string{"E036"}},
		{"match-wildcard-not-last", "enum Opt { Has(i32), Nil }\nfunction main(): i32 { var o: Opt = Nil; match (o) { _ => { return 0; }, Has(n) => { return n; } } }\n", []string{"E026"}},
		{"match-variant-twice", "enum Opt { Has(i32), Nil }\nfunction main(): i32 { var o: Opt = Nil; match (o) { Has(n) => { return n; }, Has(m) => { return m; }, Nil => { return 0; } } }\n", []string{"E028"}},
		{"match-clean-ok", "enum Opt { Has(i32), Nil }\nfunction main(): i32 { var o: Opt = Nil; match (o) { Has(n) => { return n; }, Nil => { return 0; } } }\n", nil},
		{"match-wildcard-last-ok", "enum Opt { Has(i32), Nil }\nfunction main(): i32 { var o: Opt = Nil; match (o) { Has(n) => { return n; }, _ => { return 0; } } }\n", nil},
		// E026 on a LITERAL match (i32 / string scrutinee), where the
		// non-enum arms desugar to an if/else chain. A non-last wildcard
		// must still be E026 — and ONLY E026 — for any `_` position
		// (#3612): wildcard-first (where every arm returns, so the old
		// variant-path mis-parse used to add a spurious E052) and
		// wildcard-in-the-middle (which the old desugar silently swallowed,
		// dropping the diagnostic entirely). Native (the oracle) emits a
		// lone E026 in both. The wildcard-last / no-wildcard forms stay
		// clean and still lower through build_literal_match unchanged.
		{"match-lit-wildcard-first", "function main(): i32 { var x = 1; match (x) { _ => { return 0; }, 1 => { return 1; } } }\n", []string{"E026"}},
		{"match-lit-wildcard-middle", "function main(): i32 { var x = 1; match (x) { 1 => { return 1; }, _ => { return 9; }, 2 => { return 2; } } }\n", []string{"E026"}},
		{"match-lit-wildcard-first-3arm", "function main(): i32 { var x = 1; match (x) { _ => { return 0; }, 1 => { return 1; }, 2 => { return 2; } } }\n", []string{"E026"}},
		{"match-str-wildcard-middle", "function f(s: string): i32 { match (s) { \"a\" => { return 1; }, _ => { return 0; }, \"b\" => { return 2; } } }\nfunction main(): i32 { return f(\"a\"); }\n", []string{"E026"}},
		{"match-lit-wildcard-last-ok", "function classify(x: i32): i32 { match (x) { 1 => { return 10; }, 2 => { return 20; }, _ => { return 99; } } }\nfunction main(): i32 { return classify(2); }\n", nil},
		{"type-arity-param", "struct Box[T] { v: T }\nfunction f(b: Box[i32, i32]): i32 { return 0; }\nfunction main(): i32 { return 0; }\n", []string{"E019"}},
		{"type-arity-field", "struct Box[T] { v: T }\nstruct W { b: Box[i32, i32] }\nfunction main(): i32 { return 0; }\n", []string{"E019"}},
		{"type-arity-param-ok", "struct Box[T] { v: T }\nfunction f(b: Box[i32]): i32 { return 0; }\nfunction main(): i32 { return 0; }\n", nil},
		{"array-elem-string-in-i32", "function main(): i32 { var a = [1, \"x\", 3]; return 0; }\n", []string{"E034"}},
		{"array-elem-i32-in-string", "function main(): i32 { var a = [\"a\", 1]; return 0; }\n", []string{"E034"}},
		{"array-elem-homogeneous-i32-ok", "function main(): i32 { var a = [1, 2, 3]; return a[0]; }\n", nil},
		{"array-elem-homogeneous-string-ok", "function main(): i32 { var a = [\"p\", \"q\"]; return 0; }\n", nil},
		// E034 (index variant): an array / string index must be an i32.
		{"index-string", "function main(): i32 { var a = [1, 2, 3]; return a[\"x\"]; }\n", []string{"E034"}},
		{"index-string-on-string", "function main(): i32 { var s = \"abc\"; return s[\"x\"] as i32; }\n", []string{"E034"}},
		{"index-bool", "function main(): i32 { var a = [1, 2, 3]; var b = true; return a[b]; }\n", []string{"E034"}},
		{"index-i32-ok", "function main(): i32 { var a = [1, 2, 3]; var i = 1; return a[i]; }\n", nil},
		// E034 / E037 (non-array/string source): indexing or slicing a value
		// that isn't an array or string. Arrays / strings stay ok.
		{"index-non-array", "function main(): i32 { var x = 5; return x[0]; }\n", []string{"E034"}},
		{"index-struct", "struct P { x: i32 }\nfunction main(): i32 { var p = P { x: 1 }; return p[0]; }\n", []string{"E034"}},
		{"slice-non-array", "function main(): i32 { var x = 5; var y = x[1:2]; return 0; }\n", []string{"E037"}},
		{"index-string-source-ok", "function main(): i32 { var s = \"ab\"; return s[0] as i32; }\n", nil},
		{"slice-array-source-ok", "function main(): i32 { var a = [1, 2, 3, 4]; var b = a[1:3]; return b[0]; }\n", nil},
		{"slice-string-source-ok", "function main(): i32 { var s = \"abcd\"; var t = s[1:3]; return t.len(); }\n", nil},
		{"match-variant-on-i32", "enum E { A, B }\nfunction main(): i32 { var n: i32 = 5; match (n) { A => { return 1; }, _ => { return 0; } } }\n", []string{"E035"}},
		{"match-variant-on-string", "enum E { A, B }\nfunction main(): i32 { var s: string = \"x\"; match (s) { A => { return 1; }, _ => { return 0; } } }\n", []string{"E035"}},
		{"match-i32-wildcard-only-ok", "function main(): i32 { var n: i32 = 5; match (n) { _ => { return 0; } } }\n", nil},
		{"union-match-non-exhaustive", "struct A { x: i32 }\nstruct B { y: i32 }\npub type U = A | B;\nfunction f(u: U): i32 { match (u) { A(a) => { return a.x; } } return 0; }\nfunction main(): i32 { return f(A { x: 1 }); }\n", []string{"E030"}},
		{"union-match-exhaustive-ok", "struct A { x: i32 }\nstruct B { y: i32 }\npub type U = A | B;\nfunction f(u: U): i32 { match (u) { A(a) => { return a.x; }, B(b) => { return b.y; } } return 0; }\nfunction main(): i32 { return f(A { x: 1 }); }\n", nil},
		{"union-match-wildcard-ok", "struct A { x: i32 }\nstruct B { y: i32 }\npub type U = A | B;\nfunction f(u: U): i32 { match (u) { A(a) => { return a.x; }, _ => { return 0; } } return 0; }\nfunction main(): i32 { return f(A { x: 1 }); }\n", nil},
		{"match-binding-field-ok", "struct A { x: i32 }\nstruct B { y: i32 }\npub type U = A | B;\nfunction f(u: U): i32 { match (u) { A(a) => { return a.x; }, B(b) => { return b.y; } } return 0; }\nfunction main(): i32 { return f(A { x: 1 }); }\n", nil},
		{"match-binding-bad-field", "struct A { x: i32 }\nstruct B { y: i32 }\npub type U = A | B;\nfunction f(u: U): i32 { match (u) { A(a) => { return a.nope; }, B(b) => { return b.y; } } return 0; }\nfunction main(): i32 { return f(A { x: 1 }); }\n", []string{"E043"}},
		{"enum-match-non-exhaustive", "enum E { A, B }\nfunction f(e: E): i32 { match (e) { A => { return 1; } } return 0; }\nfunction main(): i32 { return f(A); }\n", []string{"E030"}},
		{"enum-match-exhaustive-ok", "enum E { A, B }\nfunction f(e: E): i32 { match (e) { A => { return 1; }, B => { return 2; } } return 0; }\nfunction main(): i32 { return f(A); }\n", nil},
		{"enum-match-payload-exhaustive-ok", "enum Opt { Has(i32), Nil }\nfunction main(): i32 { var o: Opt = Nil; match (o) { Has(n) => { return n; }, Nil => { return 0; } } }\n", nil},
		{"union-match-foreign-variant", "struct A { x: i32 }\nstruct B { y: i32 }\nstruct C { z: i32 }\npub type U = A | B;\nfunction f(u: U): i32 { match (u) { A(a) => { return a.x; }, C(c) => { return c.z; }, _ => { return 0; } } return 0; }\nfunction main(): i32 { return f(A { x: 1 }); }\n", []string{"E001", "E014"}},
		{"match-qualifier-mismatch", "enum E { A, B }\nenum F { C, D }\nfunction f(e: E): i32 { match (e) { F.A => { return 1; }, _ => { return 0; } } return 0; }\nfunction main(): i32 { return f(A); }\n", []string{"E029"}},
		{"match-qualifier-correct-ok", "enum E { A, B }\nfunction f(e: E): i32 { match (e) { E.A => { return 1; }, E.B => { return 2; } } return 0; }\nfunction main(): i32 { return f(A); }\n", nil},
		{"union-struct-name-collision", "struct A { x: i32 }\nstruct B { y: i32 }\nstruct C { z: i32 }\npub type B = A | C;\nfunction main(): i32 { return 0; }\n", []string{"E016"}},
		{"union-distinct-name-ok", "struct A { x: i32 }\nstruct C { z: i32 }\npub type U = A | C;\nfunction main(): i32 { return 0; }\n", nil},
		{"missing-return", "function f(): i32 { var x = 1; }\nfunction main(): i32 { return 0; }\n", []string{"E052"}},
		{"missing-return-one-armed-if", "function f(c: boolean): i32 { if (c) { return 1; } }\nfunction main(): i32 { return 0; }\n", []string{"E052"}},
		{"return-while-true-ok", "function f(): i32 { while (true) { return 1; } }\nfunction main(): i32 { return 0; }\n", nil},
		{"return-loop-ok", "function f(): i32 { loop { return 1; } }\nfunction main(): i32 { return 0; }\n", nil},
		{"return-if-else-ok", "function f(c: boolean): i32 { if (c) { return 1; } else { return 2; } }\nfunction main(): i32 { return 0; }\n", nil},
		// void return type: an empty body is fine (no E052 — falling off the
		// end is the normal exit), a bare `return;` is fine, and returning a
		// value is E002. Mirrors the Go checker's special handling of void.
		{"void-empty-ok", "function f(): void { }\nfunction main(): i32 { return 0; }\n", nil},
		{"void-bare-return-ok", "function f(): void { return; }\nfunction main(): i32 { return 0; }\n", nil},
		{"void-returns-value", "function f(): void { return 3; }\nfunction main(): i32 { return 0; }\n", []string{"E002"}},
		{"method-unknown-receiver", "function (r: Nope) m(): i32 { return 0; }\nfunction main(): i32 { return 0; }\n", []string{"E021"}},
		{"method-struct-receiver-ok", "struct P { x: i32 }\nfunction (p: P) m(): i32 { return p.x; }\nfunction main(): i32 { return 0; }\n", nil},
		{"method-builtin-receiver-ok", "function (n: i32) twice(): i32 { return n * 2; }\nfunction main(): i32 { return 0; }\n", nil},
		{"tuple-destructure-non-tuple", "function main(): i32 { var n = 5; var (a, b) = n; return 0; }\n", []string{"E024"}},
		{"tuple-destructure-ok", "function main(): i32 { var t = (1, 2); var (a, b) = t; return a + b; }\n", nil},
		{"cast-bool-to-i32", "function main(): i32 { var b: boolean = true; return b as i32; }\n", []string{"E033"}},
		{"cast-i32-to-bool", "function main(): i32 { var x: i32 = 1; var b: boolean = x as boolean; return 0; }\n", []string{"E033"}},
		{"cast-bool-to-string", "function main(): i32 { var b: boolean = true; var s: string = b as string; return 0; }\n", []string{"E033"}},
		{"cast-string-to-bool", "function main(): i32 { var s: string = \"x\"; var b: boolean = s as boolean; return 0; }\n", []string{"E033"}},
		{"cast-numeric-ok", "function main(): i32 { var x: i32 = 1; var y: f64 = x as f64; return 0; }\n", nil},
		{"cast-string-to-i32-ok", "function main(): i32 { var s: string = \"x\"; return s as i32; }\n", nil},
		{"cast-i32-to-string-e069", "function main(): i32 { var x: i32 = 1; var s: string = x as string; return 0; }\n", []string{"E069"}},
		{"cast-bool-to-bool-ok", "function main(): i32 { var b: boolean = true; var c: boolean = b as boolean; return 0; }\n", nil},
		{"cast-f64-to-string", "function main(): i32 { var f: f64 = 1.0; var s: string = f as string; return 0; }\n", []string{"E033"}},
		{"cast-string-to-f64", "function main(): i32 { var s: string = \"x\"; var f: f64 = s as f64; return 0; }\n", []string{"E033"}},
		{"cast-f64-to-i32-ok", "function main(): i32 { var f: f64 = 1.0; var x: i32 = f as i32; return 0; }\n", nil},
		{"cast-i32-to-f64-ok", "function main(): i32 { var x: i32 = 1; var y: f64 = x as f64; return 0; }\n", nil},
		{"field-assign", "struct P { x: i32 }\nfunction main(): i32 { var p: P = P { x: 1 }; p.x = 5; return p.x; }\n", []string{"E048"}},
		{"field-compound-assign", "struct P { x: i32 }\nfunction main(): i32 { var p: P = P { x: 1 }; p.x += 5; return p.x; }\n", []string{"E048"}},
		{"nested-field-assign", "struct Q { a: i32 }\nstruct P { q: Q }\nfunction main(): i32 { var p: P = P { q: Q { a: 1 } }; p.q.a = 9; return 0; }\n", []string{"E048"}},
		{"index-assign-e056", "function main(): i32 { var a = [1, 2, 3]; a[0] = 9; return a[0]; }\n", []string{"E056"}},
		{"local-reassign-ok", "function main(): i32 { var x: i32 = 1; x = 5; return x; }\n", nil},
		{"struct-update-ok", "struct P { x: i32 }\nfunction main(): i32 { var p: P = P { x: 1 }; p = P { ...p, x: 5 }; return p.x; }\n", nil},
		{"value-undefined", "function main(): i32 { return z; }\n", []string{"E001"}},
		{"value-defined-ok", "function main(): i32 { var z: i32 = 5; return z; }\n", nil},
		// A bare STRUCT type name in value position is E001 (you construct with
		// `P { … }`); the struct literal and a field read stay clean.
		{"struct-name-as-value", "struct P { x: i32 }\nfunction main(): i32 { return P; }\n", []string{"E001"}},
		{"struct-name-as-value-var", "struct P { x: i32 }\nfunction main(): i32 { var q = P; return 0; }\n", []string{"E001"}},
		{"struct-literal-not-value-ok", "struct P { x: i32 }\nfunction main(): i32 { var p = P { x: 1 }; return p.x; }\n", nil},
		// A payload-bearing variant referenced bare (a union member, or an enum
		// variant with a payload) is E036 — it must be constructed/called. A
		// nullary variant, a constructor call, and a struct literal stay clean.
		{"payload-variant-bare", "enum E { A(i32), B }\nfunction f(): E { return A; }\nfunction main(): i32 { return 0; }\n", []string{"E036"}},
		{"union-member-bare", "struct P { x: i32 }\nstruct Q { y: i32 }\ntype U = P | Q;\nfunction f(): U { return P; }\nfunction main(): i32 { return 0; }\n", []string{"E036"}},
		{"payload-variant-call-ok", "enum E { A(i32), B }\nfunction f(): E { return A(5); }\nfunction main(): i32 { return 0; }\n", nil},
		{"nullary-variant-bare-ok", "enum E { A, B }\nfunction f(): E { return A; }\nfunction main(): i32 { return 0; }\n", nil},
		{"value-param-ok", "function f(a: i32): i32 { return a; }\nfunction main(): i32 { return f(1); }\n", nil},
		{"value-function-as-value-ok", "function g(): i32 { return 1; }\nfunction run(fn: () => i32): i32 { return fn(); }\nfunction main(): i32 { return run(g); }\n", nil},
		{"value-enum-variant-ok", "enum E { A, B }\nfunction main(): i32 { var e: E = A; return 0; }\n", nil},
		{"value-builtin-variant-none-ok", "function f(): Option[i32] { return None; }\nfunction main(): i32 { return 0; }\n", nil},
		{"value-loop-var-ok", "function main(): i32 { var xs = [1, 2, 3]; var t = 0; for x in xs { t = t + x; } return t; }\n", nil},
		{"value-match-payload-ok", "enum O { Has(i32), Nil }\nfunction main(): i32 { var o: O = Nil; match (o) { Has(v) => { return v; }, Nil => { return 0; } } }\n", nil},
		{"assign-undefined-target", "function main(): i32 { y = 5; return 0; }\n", []string{"E001"}},
		{"assign-defined-target-ok", "function main(): i32 { var x: i32 = 1; x = 5; return x; }\n", nil},
		{"assign-param-target-ok", "function f(a: i32): i32 { a = 9; return a; }\nfunction main(): i32 { return f(1); }\n", nil},
		{"assign-loop-var-target-ok", "function main(): i32 { var xs = [1, 2, 3]; for x in xs { x = 9; } return 0; }\n", nil},
		{"assign-match-payload-target-ok", "enum O { Has(i32), Nil }\nfunction main(): i32 { var o: O = Nil; match (o) { Has(v) => { v = 7; }, Nil => { } } return 0; }\n", nil},
		{"assign-destructure-target-ok", "function main(): i32 { var t = (1, 2); var (a, b) = t; a = 9; return a + b; }\n", nil},
		{"try-on-i32", "function f(): Option[i32] { var x: i32 = 5; return x?; }\nfunction main(): i32 { return 0; }\n", []string{"E042"}},
		{"try-on-string", "function f(): Option[i32] { var s: string = \"x\"; return s?; }\nfunction main(): i32 { return 0; }\n", []string{"E042"}},
		{"try-on-option-ok", "function g(): Option[i32] { return Some(1); }\nfunction f(): Option[i32] { var o: Option[i32] = g(); var v: i32 = o?; return Some(v); }\nfunction main(): i32 { return 0; }\n", nil},
		// E042 return-shape (#4363 item 1): `?` on a known Option/Result
		// operand inside a function whose declared return type is a known
		// primitive draws the return-shape E042 ("requires the surrounding
		// function to return Option[_]/Result[_, E]"), matching native. The
		// matching-return shapes stay clean, including inside a lambda whose
		// own declared return supplies the context (not the enclosing fn's).
		{"try-option-ret-i32", "function f(): i32 { var o: Option[i32] = Some(1); return o?; }\nfunction main(): i32 { return 0; }\n", []string{"E042"}},
		{"try-result-ret-i32", "function get(): Result[i32, string] { return Ok(3); }\nfunction f(): i32 { return get()?; }\nfunction main(): i32 { return 0; }\n", []string{"E042"}},
		{"try-option-ret-string", "function f(): string { return Some(3)?; }\nfunction main(): i32 { return 0; }\n", []string{"E042"}},
		{"try-option-ret-bool", "function f(): boolean { var o: Option[i32] = Some(1); var v: i32 = o?; return v > 0; }\nfunction main(): i32 { return 0; }\n", []string{"E042"}},
		{"try-result-ret-ok", "function get(): Result[i32, string] { return Ok(3); }\nfunction f(): Result[i32, string] { var v: i32 = get()?; return Ok(v + 1); }\nfunction main(): i32 { return 0; }\n", nil},
		{"try-lambda-ret-i32", "function f(): Option[i32] {\n    var g = function(): i32 { var o: Option[i32] = Some(1); var v: i32 = o?; return v; };\n    return Some(1);\n}\nfunction main(): i32 { return 0; }\n", []string{"E042"}},
		{"try-lambda-ret-option-ok", "function f(): i32 {\n    var g = function(): Option[i32] { var o: Option[i32] = Some(1); var v: i32 = o?; return Some(v); };\n    return 2;\n}\nfunction main(): i32 { return 0; }\n", nil},
		{"callee-undefined", "function main(): i32 { return foo(1); }\n", []string{"E001"}},
		{"callee-user-fn-ok", "function g(): i32 { return 1; }\nfunction main(): i32 { return g(); }\n", nil},
		{"callee-builtin-ok", "function main(): i32 { print(\"hi\"); return 0; }\n", nil},
		{"callee-variant-ctor-ok", "function f(): Option[i32] { return Some(1); }\nfunction main(): i32 { return 0; }\n", nil},
		{"callee-closure-ok", "function main(): i32 { var f = function(x: i32): i32 { return x; }; return f(7); }\n", nil},
		{"value-builtin-as-value-ok", "function main(): i32 { var w = write; return 0; }\n", nil},
		{"shadow-option", "enum Option { A, B }\nfunction main(): i32 { return 0; }\n", []string{"E010"}},
		{"shadow-result", "enum Result { A, B }\nfunction main(): i32 { return 0; }\n", []string{"E010"}},
		{"shadow-ioerror", "enum IoError { A, B }\nfunction main(): i32 { return 0; }\n", []string{"E010"}},
		{"shadow-jsonvalue", "enum JsonValue { A, B }\nfunction main(): i32 { return 0; }\n", []string{"E010"}},
		{"enum-non-reserved-ok", "enum Color { Red, Green }\nfunction main(): i32 { return 0; }\n", nil},
		// Generic functions: a concrete argument must NOT be flagged against
		// the opaque type parameter (E038 false-positive guard).
		{"generic-call-infer-ok", "function id[T](x: T): T { return x; }\nfunction main(): i32 { return id(5); }\n", nil},
		{"generic-call-two-params-ok", "function fst[A, B](a: A, b: B): A { return a; }\nfunction main(): i32 { return fst(1, 2); }\n", nil},
		// A non-generic argument-type mismatch still fires E038 (regression
		// guard that the fix didn't disable the check).
		{"nongeneric-arg-mismatch", "function f(a: string): i32 { return 0; }\nfunction main(): i32 { return f(5); }\n", []string{"E038"}},
		// E040: explicit generic call-site type-argument arity.
		{"type-arg-too-many", "function id[T](x: T): T { return x; }\nfunction main(): i32 { return id[i32, i32](5); }\n", []string{"E040"}},
		{"type-arg-too-few", "function pair[A, B](a: A, b: B): A { return a; }\nfunction main(): i32 { return pair[i32](5, 6); }\n", []string{"E040"}},
		{"type-arg-ok", "function id[T](x: T): T { return x; }\nfunction main(): i32 { return id[i32](5); }\n", nil},
		{"type-arg-nongeneric-ok", "function f(x: i32): i32 { return x; }\nfunction main(): i32 { return f[i32](5); }\n", nil},
		// E027: a match-arm guard (`Pat when <expr> =>`) must be boolean.
		{"match-guard-nonbool", "enum O { Has(i32), Nil }\nfunction main(): i32 { var o: O = Nil; match (o) { Has(n) when n => { return n; }, _ => { return 0; } } }\n", []string{"E027"}},
		{"match-guard-bool-ok", "enum O { Has(i32), Nil }\nfunction main(): i32 { var o: O = Nil; match (o) { Has(n) when n > 0 => { return n; }, _ => { return 0; } } }\n", nil},
		// E015: variant pattern binding count must match the variant's payload count.
		{"variant-too-many-bindings", "enum O { Has(i32), Nil }\nfunction main(): i32 { var o: O = Nil; match (o) { Has(a, b) => { return a; }, Nil => { return 0; } } }\n", []string{"E015"}},
		{"variant-missing-binding", "enum O { Has(i32), Nil }\nfunction main(): i32 { var o: O = Nil; match (o) { Has => { return 1; }, Nil => { return 0; } } }\n", []string{"E015"}},
		{"variant-binding-arity-ok", "enum O { Has(i32), Nil }\nfunction main(): i32 { var o: O = Nil; match (o) { Has(n) => { return n; }, Nil => { return 0; } } }\n", nil},
		// E054: an `@export(...)` world-export function cannot be generic
		// (a world export has one concrete ABI) and cannot be a method.
		{"export-generic", "@export(\"example:app/run\", \"run\") function run[T](x: T): i32 { return 0; }\nfunction main(): i32 { return 0; }\n", []string{"E054"}},
		{"export-method", "struct P { x: i32 }\n@export(\"example:app/run\", \"run\") function (p: P) run(): i32 { return 0; }\nfunction main(): i32 { return 0; }\n", []string{"E054"}},
		{"export-plain-ok", "@export(\"example:app/run\", \"run\") function run(x: i32): i32 { return x; }\nfunction main(): i32 { return 0; }\n", nil},
		// E050: use of an owned parameter after it was consumed (moved).
		{"own-call-then-use", "function sink(own xs: i32[]): i32 { return xs[0]; }\nfunction f(own xs: i32[]): i32 { var a: i32 = sink(xs); return sink(xs); }\nfunction main(): i32 { return 0; }\n", []string{"E050"}},
		{"own-double-in-stmt", "function sink(own xs: i32[]): i32 { return xs[0]; }\nfunction f(own xs: i32[]): i32 { return sink(xs) + sink(xs); }\nfunction main(): i32 { return 0; }\n", []string{"E050"}},
		{"own-match-then-use", "enum Lst { Cons(i32), Nil }\nfunction lsink(l: Lst): i32 { return 0; }\nfunction f(own l: Lst): i32 { var r: i32 = match (l) { Cons(h) => h, Nil => 0 }; return r + lsink(l); }\nfunction main(): i32 { return 0; }\n", []string{"E050"}},
		{"own-consume-in-loop", "function sink(own xs: i32[]): i32 { return xs[0]; }\nfunction f(own xs: i32[]): i32 { var i: i32 = 0; while (i < 3) { var a: i32 = sink(xs); i = i + 1; } return 0; }\nfunction main(): i32 { return 0; }\n", []string{"E050"}},
		{"own-borrow-only-ok", "function f(own xs: i32[]): i32 { return xs[0] + xs[1]; }\nfunction main(): i32 { return 0; }\n", nil},
		{"own-borrow-arg-ok", "function peek(xs: i32[]): i32 { return xs[0]; }\nfunction f(own xs: i32[]): i32 { var a: i32 = peek(xs); return a + peek(xs); }\nfunction main(): i32 { return 0; }\n", nil},
		{"own-single-consume-ok", "function sink(xs: i32[]): i32 { return xs[0]; }\nfunction f(own xs: i32[]): i32 { return sink(xs); }\nfunction main(): i32 { return 0; }\n", nil},
		// E051: argument to an owned parameter must be an owned value.
		{"own-arg-borrowed-param", "function consume(own xs: i32[]): i32 { return xs[0]; }\nfunction f(xs: i32[]): i32 { return consume(xs); }\nfunction main(): i32 { return 0; }\n", []string{"E051"}},
		{"own-arg-plain-local", "function consume(own xs: i32[]): i32 { return xs[0]; }\nfunction f(): i32 { var xs: i32[] = [1, 2]; return consume(xs); }\nfunction main(): i32 { return 0; }\n", []string{"E051"}},
		{"own-arg-fresh-ok", "function consume(own xs: i32[]): i32 { return xs[0]; }\nfunction f(): i32 { return consume([1, 2]); }\nfunction main(): i32 { return 0; }\n", nil},
		{"own-arg-forward-ok", "function consume(own xs: i32[]): i32 { return xs[0]; }\nfunction f(own ys: i32[]): i32 { return consume(ys); }\nfunction main(): i32 { return 0; }\n", nil},
		// E049: assigning to a reference-typed variable captured by a closure.
		{"cap-assign-string", "function main(): i32 { var s: string = \"x\"; var f = function(): i32 { s = \"y\"; return 0; }; return f(); }\n", []string{"E049"}},
		{"cap-assign-array", "function main(): i32 { var a: i32[] = [1]; var f = function(): i32 { a = [2]; return 0; }; return f(); }\n", []string{"E049"}},
		{"cap-assign-struct", "struct P { x: i32 }\nfunction main(): i32 { var p: P = P { x: 1 }; var f = function(): i32 { p = P { x: 2 }; return 0; }; return f(); }\n", []string{"E049"}},
		{"cap-assign-param", "function g(s: string): i32 { var f = function(): i32 { s = \"y\"; return 0; }; return f(); }\nfunction main(): i32 { return 0; }\n", []string{"E049"}},
		{"cap-assign-scalar-ok", "function main(): i32 { var n: i32 = 1; var f = function(): i32 { n = 2; return n; }; return f(); }\n", nil},
		{"cap-read-ref-ok", "function main(): i32 { var s: string = \"x\"; var f = function(): i32 { return s.len(); }; return f(); }\n", nil},
		{"cap-assign-local-ok", "function main(): i32 { var f = function(): i32 { var t: string = \"a\"; t = \"b\"; return 0; }; return f(); }\n", nil},
		// #4410: the closure-capture contract (docs/CLOSURE-CAPTURE.md). The
		// scalar/reference split must be BYTE-identical to native's
		// ast.IsPointerType. These pin the two former divergences the parity
		// review surfaced: (1) the unsigned widths u8/u32/u64/usize are scalars
		// (native never flagged them; the self-host used to), and (2) an
		// unannotated var bound to a pointer-shaped LITERAL is reference-typed
		// (native infers it; the self-host used to skip unannotated captures).
		{"cap-assign-u32-ok", "function main(): i32 { var n: u32 = 1; var f = function(): i32 { n = 2; return 0; }; return f(); }\n", nil},
		{"cap-assign-u64-ok", "function main(): i32 { var n: u64 = 1; var f = function(): i32 { n = 2; return 0; }; return f(); }\n", nil},
		{"cap-assign-u8-ok", "function main(): i32 { var n: u8 = 1; var f = function(): i32 { n = 2; return 0; }; return f(); }\n", nil},
		{"cap-assign-usize-ok", "function main(): i32 { var n: usize = 1; var f = function(): i32 { n = 2; return 0; }; return f(); }\n", nil},
		{"cap-assign-f64-ok", "function main(): i32 { var x: f64 = 1.5; var f = function(): i32 { x = 2.5; return 0; }; return f(); }\n", nil},
		{"cap-assign-bool-ok", "function main(): i32 { var b: boolean = true; var f = function(): i32 { b = false; return 0; }; return f(); }\n", nil},
		{"cap-assign-unann-string", "function main(): i32 { var s = \"x\"; var f = function(): i32 { s = \"y\"; return 0; }; return f(); }\n", []string{"E049"}},
		{"cap-assign-unann-array", "function main(): i32 { var a = [1]; var f = function(): i32 { a = [2]; return 0; }; return f(); }\n", []string{"E049"}},
		{"cap-assign-unann-struct", "struct P { x: i32 }\nfunction main(): i32 { var p = P { x: 1 }; var f = function(): i32 { p = P { x: 2 }; return 0; }; return f(); }\n", []string{"E049"}},
		{"cap-assign-unann-tuple", "function main(): i32 { var t = (1, 2); var f = function(): i32 { t = (3, 4); return 0; }; return f(); }\n", []string{"E049"}},
		{"cap-assign-unann-scalar-ok", "function main(): i32 { var n = 5; var f = function(): i32 { n = 7; return 0; }; return f(); }\n", nil},
		// E002 inside lambda bodies: a lambda's `return` is checked against
		// the lambda's OWN declared return type, not the enclosing function's
		// (ret_diags stops at the lambda boundary). lret_stmts/lret_expr fill
		// that gap.
		{"lambda-ret-mismatch", "function main(): i32 { var f = function(): i32 { return \"x\"; }; return f(); }\n", []string{"E002"}},
		{"lambda-ret-ok", "function main(): i32 { var f = function(): i32 { return 5; }; return f(); }\n", nil},
		{"lambda-in-void-fn", "function g(): void { var f = function(): i32 { return \"x\"; }; }\nfunction main(): i32 { return 0; }\n", []string{"E002"}},
		{"lambda-nested-if-mismatch", "function main(): i32 { var f = function(): i32 { if (1 < 2) { return \"x\"; } return 1; }; return f(); }\n", []string{"E002"}},
		{"lambda-bare-return", "function main(): i32 { var f = function(): i32 { return; }; return f(); }\n", []string{"E012"}},
		{"lambda-arg-mismatch", "function run(fn: () => i32): i32 { return fn(); }\nfunction main(): i32 { return run(function(): i32 { return \"x\"; }); }\n", []string{"E002"}},
		{"lambda-nested-lambda-mismatch", "function main(): i32 { var f = function(): i32 { var g = function(): i32 { return \"x\"; }; return g(); }; return f(); }\n", []string{"E002"}},
		{"lambda-no-rettype-ok", "function main(): i32 { var f = function() { return; }; return 0; }\n", nil},
		{"rec-local-capture-ret-mismatch", "function main(): i32 { var base: string = \"x\"; function f(n: i32): i32 { if (n <= 0) { return base; } return f(n - 1); } return f(3); }\n", []string{"E002"}},
		// A `match` / `if` used in value position is desugared by the parser
		// into an IIFE — (function(): RT { … })() — whose RT is a coarse
		// heuristic tag (if_expr_rt, defaulting to "i32"). The lambda-body
		// E002 pass must NOT check those synthesized returns against that
		// tag, or a valid string-valued match/if-expression (whose first arm
		// isn't a string literal, so RT mis-tags as "i32") false-positives.
		{"match-expr-string-arms-ok", "enum O { Has(i32), Nil }\nfunction main(): i32 { var a: string = \"p\"; var b: string = \"q\"; var o: O = Nil; var s: string = match (o) { Has(n) => a, Nil => b }; return 0; }\n", nil},
		{"if-expr-string-arms-ok", "function main(): i32 { var a: string = \"p\"; var b: string = \"q\"; var s: string = if (1 < 2) { a } else { b }; return 0; }\n", nil},
		// A payload-bearing enum variant `V(T)` lowers to a struct `V` with a
		// marker field `__ev: T`; the pattern `V(n)` binds the PAYLOAD value
		// (type T), not the wrapper struct. Typing it as the wrapper struct
		// false-positived E038 when the payload was passed to a typed
		// function. variant_binding_type reads the real payload type.
		{"enum-payload-i32-arg-ok", "enum O { Has(i32), Nil }\nfunction f(n: i32): i32 { return n; }\nfunction main(): i32 { var o: O = Nil; match (o) { Has(n) => { var r: i32 = f(n); }, Nil => { } } return 0; }\n", nil},
		{"enum-payload-string-arg-ok", "enum S { Tag(string), Non }\nfunction h(s: string): i32 { return 0; }\nfunction main(): i32 { var x: S = Non; match (x) { Tag(t) => { var r: i32 = h(t); }, Non => { } } return 0; }\n", nil},
		// Regression direction: a real payload-type mismatch still fires E038
		// (n is i32, passed to a string parameter).
		{"enum-payload-arg-mismatch", "enum O { Has(i32), Nil }\nfunction g(s: string): i32 { return 0; }\nfunction main(): i32 { var o: O = Nil; match (o) { Has(n) => { var r: i32 = g(n); }, Nil => { } } return 0; }\n", []string{"E038"}},
		// A struct-union member still binds the whole struct (no `__ev`), so
		// field access on it stays clean.
		{"struct-union-member-field-ok", "struct A { x: i32 }\nstruct B { y: i32 }\npub type U = A | B;\nfunction f(u: U): i32 { match (u) { A(a) => { return a.x; }, B(b) => { return b.y; } } return 0; }\nfunction main(): i32 { return f(A { x: 1 }); }\n", nil},
		// E055: a bare value-returning collection mutator discards its result.
		{"unused-append-result", "function main(): i32 { var a: i32[] = [1]; a.append(2); return a[0]; }\n", []string{"E055"}},
		{"append-reassigned-ok", "function main(): i32 { var a: i32[] = [1]; a = a.append(2); return a[0]; }\n", nil},
		{"append-result-used-ok", "function main(): i32 { var a: i32[] = [1]; return a.append(2)[0]; }\n", nil},
		// E031: a `match` / `if` used in value position desugars to an IIFE,
		// so its arms' result types must be mutually compatible. The predicate
		// mirrors the Go checker's unifyIfArms — clear scalar mismatches fire;
		// numeric (f64/i32) widen; tuples/arrays unify element-wise; two
		// structs of the SAME enum family are compatible (but struct-union
		// members and unrelated structs are NOT); an unknown arm skips E031
		// (E001 owns it). Cross-checked against the Go checker.
		{"e031-if-i32-string", "function main(): i32 { var r = if (1 < 2) { 1 } else { \"x\" }; return 0; }\n", []string{"E031"}},
		{"e031-if-i32-i32-ok", "function main(): i32 { var r = if (1 < 2) { 1 } else { 2 }; return r; }\n", nil},
		{"e031-if-call-mismatch", "function a(): i32 { return 1; }\nfunction b(): string { return \"x\"; }\nfunction main(): i32 { var r = if (1 < 2) { a() } else { b() }; return 0; }\n", []string{"E031"}},
		{"e031-if-elseif-mismatch", "function main(): i32 { var r = if (1 < 2) { 1 } else if (2 < 3) { 2 } else { \"x\" }; return 0; }\n", []string{"E031"}},
		{"e031-if-f64-i32-ok", "function main(): i32 { var r = if (1 < 2) { 1.0 } else { 2 }; return 0; }\n", nil},
		{"e031-if-bool-arms-ok", "function main(): i32 { var r = if (1 < 2) { true } else { false }; return 0; }\n", nil},
		{"e031-if-struct-i32-mismatch", "struct P { x: i32 }\nfunction main(): i32 { var r = if (1 < 2) { P { x: 1 } } else { 2 }; return 0; }\n", []string{"E031"}},
		{"e031-if-struct-arms-ok", "struct P { x: i32 }\nfunction main(): i32 { var r = if (1 < 2) { P { x: 1 } } else { P { x: 2 } }; return r.x; }\n", nil},
		{"e031-if-arm-undefined", "function main(): i32 { var r = if (1 < 2) { undef } else { 1 }; return 0; }\n", []string{"E001"}},
		{"e031-match-i32-string", "enum O { A, B }\nfunction main(): i32 { var o: O = A; var r = match (o) { A => 1, B => \"x\" }; return 0; }\n", []string{"E031"}},
		{"e031-match-i32-i32-ok", "enum O { A, B }\nfunction main(): i32 { var o: O = A; var r = match (o) { A => 1, B => 2 }; return r; }\n", nil},
		{"e031-match-bool-i32", "enum O { A, B }\nfunction main(): i32 { var o: O = A; var r = match (o) { A => true, B => 1 }; return 0; }\n", []string{"E031"}},
		{"e031-match-payload-arm-ok", "enum O { Has(i32), Nil }\nfunction main(): i32 { var o: O = Nil; var r = match (o) { Has(n) => n, Nil => 0 }; return r; }\n", nil},
		{"e031-match-three-arms-last-bad", "enum O { A, B, C }\nfunction main(): i32 { var o: O = A; var r = match (o) { A => 1, B => 2, C => \"x\" }; return 0; }\n", []string{"E031"}},
		// A value if/match-expression has a real result type (the branches'
		// common type): a mismatched var annotation is E003, a mismatched
		// `return` is E002. A matching annotation, and a numeric-mix set (which
		// this port doesn't unify), stay clean.
		{"if-expr-value-assign-bad", "function main(): i32 { var x: string = if (1 < 2) { 1 } else { 2 }; return 0; }\n", []string{"E003"}},
		{"match-expr-value-assign-bad", "enum E { A, B }\nfunction main(): i32 { var e: E = A; var x: string = match (e) { A => 1, B => 2 }; return 0; }\n", []string{"E003"}},
		{"if-expr-value-assign-ok", "function main(): i32 { var x: i32 = if (1 < 2) { 1 } else { 2 }; return x; }\n", nil},
		{"if-expr-value-return-bad", "function f(): string { return if (1 < 2) { 1 } else { 2 }; }\nfunction main(): i32 { return 0; }\n", []string{"E002"}},
		// Same-enum-family / element-wise compatible arms are NOT flagged (Go
		// also reports nothing): Option Some/None, enum variants, a nested
		// if-expression arm, tuple arms with matching element types.
		{"e031-match-option-arms-ok", "enum O { A, B }\nfunction f(o: O): Option[i32] { var r = match (o) { A => Some(1), B => None }; return r; }\nfunction main(): i32 { return 0; }\n", nil},
		{"e031-if-enum-variant-arms-ok", "enum Sh { Circle(i32), Empty }\nfunction main(): i32 { var c = true; var r = if (c) { Circle(1) } else { Empty }; return 0; }\n", nil},
		{"e031-match-nested-if-arm-ok", "enum O { A, B }\nfunction main(): i32 { var o: O = A; var r = match (o) { A => if (1<2) { 1 } else { 2 }, B => 3 }; return r; }\n", nil},
		{"e031-match-tuple-arms-ok", "enum O { A, B }\nfunction main(): i32 { var o: O = A; var r = match (o) { A => (1,2), B => (3,4) }; return r.0; }\n", nil},
		// Faithful-precision cases (previously missed by the conservative
		// "all aggregates compatible" predicate; now match the Go checker):
		{"e031-two-unrelated-structs", "struct P { x: i32 }\nstruct Q { y: i32 }\nfunction main(): i32 { var c = true; var r = if (c) { P { x: 1 } } else { Q { y: 2 } }; return 0; }\n", []string{"E031"}},
		{"e031-tuple-elem-mismatch", "function main(): i32 { var c = true; var r = if (c) { (1, \"x\") } else { (1, 2) }; return 0; }\n", []string{"E031"}},
		{"e031-array-elem-mismatch", "function main(): i32 { var c = true; var r = if (c) { [\"x\"] } else { [1] }; return 0; }\n", []string{"E031"}},
		{"e031-union-members-mismatch", "struct A { x: i32 }\nstruct B { y: i32 }\npub type U = A | B;\nfunction main(): i32 { var c = true; var r = if (c) { A { x: 1 } } else { B { y: 2 } }; return 0; }\n", []string{"E031"}},
		{"e031-same-enum-diff-payload-ok", "enum E { A(i32), B(string) }\nfunction main(): i32 { var c = true; var r = if (c) { A(1) } else { B(\"y\") }; return 0; }\n", nil},
		{"e031-tuple-elems-ok", "function main(): i32 { var c = true; var r = if (c) { (1, \"x\") } else { (2, \"y\") }; return r.0; }\n", nil},
		// E045: a map literal's first key fixes the key type, which must be
		// i32 or string (the only key kinds the runtime hash/compare
		// supports). Map programs need `import "core/map";` (Go reports E001
		// otherwise — a Go-only rule the self-host doesn't model, so kept out
		// of the corpus). Cross-checked against the Go checker.
		{"e045-maplit-float-key", "import \"core/map\";\nfunction main(): i32 { var m = Map { 1.0: 10 }; return 0; }\n", []string{"E045"}},
		{"e045-maplit-string-key-ok", "import \"core/map\";\nfunction main(): i32 { var m = Map { \"a\": 1, \"b\": 2 }; return 0; }\n", nil},
		{"e045-maplit-i32-key-ok", "import \"core/map\";\nfunction main(): i32 { var m = Map { 1: 10, 2: 20 }; return 0; }\n", nil},
		{"e045-maplit-used-ok", "import \"core/map\";\nfunction main(): i32 { var m = Map { \"a\": 1 }; return m.get_or(\"a\", 0); }\n", nil},
		// E003 regression guard: an annotated map var assigned a `Map { … }`
		// literal must NOT false-positive E003. The literal desugars to
		// `map_new[_i32](n)…` whose key/value type the self-host leaves
		// `unknown`; type_assignable now treats an unknown side as a wildcard
		// into the annotated Map[K,V] (matching the empty-array rule).
		{"map-ann-empty-ok", "import \"core/map\";\nfunction main(): i32 { var m: Map[string,i32] = Map {}; return 0; }\n", nil},
		{"map-ann-nonempty-ok", "import \"core/map\";\nfunction main(): i32 { var m: Map[string,i32] = Map { \"a\": 1 }; return 0; }\n", nil},
		{"map-ann-i32keys-ok", "import \"core/map\";\nfunction main(): i32 { var m: Map[i32,i32] = Map { 1: 2 }; return 0; }\n", nil},
		// E022: `if let` / `let … else` carry dedicated pattern-binding
		// diagnostics. The self-host parser desugars both to a StmtMatch
		// tagged with `origin` ("if_let" / "let_else"); the checker reads
		// that tag to emit E022 (instead of the generic E035 the desugared
		// shape would otherwise draw) when the source isn't an enum, and —
		// for let-else — when the else branch doesn't diverge. The binding
		// is left unreferenced in the error cases so the Go checker's E001
		// for the now-unbound name (a separate rule) stays out of the set.
		// Cross-checked against the Go checker.
		{"iflet-source-nonenum", "function main(): i32 { var n: i32 = 5; if let Has(v) = n { return 0; } return 0; }\n", []string{"E022"}},
		{"letelse-source-nonenum", "function main(): i32 { var n: i32 = 5; let Has(v) = n else { return 0; }; return 0; }\n", []string{"E022"}},
		{"letelse-source-struct", "struct P { x: i32 }\nfunction main(): i32 { var p: P = P { x: 1 }; let Has(v) = p else { return 0; }; return 0; }\n", []string{"E022"}},
		{"letelse-else-nondiverge", "enum O { Has(i32), Nil }\nfunction main(): i32 { var o: O = Nil; let Has(v) = o else { var x: i32 = 1; }; return 0; }\n", []string{"E022"}},
		{"letelse-else-loop-diverge-ok", "enum O { Has(i32), Nil }\nfunction main(): i32 { var o: O = Nil; let Has(v) = o else { loop { } }; return v; }\n", nil},
		{"iflet-enum-ok", "enum O { Has(i32), Nil }\nfunction main(): i32 { var o: O = Nil; if let Has(v) = o { return v; } return 0; }\n", nil},
		{"letelse-enum-ok", "enum O { Has(i32), Nil }\nfunction main(): i32 { var o: O = Nil; let Has(v) = o else { return 0; }; return v; }\n", nil},
		{"iflet-bad-variant", "enum O { Has(i32), Nil }\nfunction main(): i32 { var o: O = Nil; if let Bogus(v) = o { return 0; } return 0; }\n", []string{"E014"}},
		{"iflet-bad-arity", "enum O { Has(i32), Nil }\nfunction main(): i32 { var o: O = Nil; if let Has(a, b) = o { return 0; } return 0; }\n", []string{"E015"}},
		// E057: `cell_new(v)` constructs a Cell[T]; T must be cycle-free —
		// a scalar (i32/i64/f64/bool) or string. A composite / reference
		// argument (struct, array, tuple, another cell) is E057, reported
		// at the argument. Cross-checked against the Go checker.
		{"cellnew-i32-ok", "function main(): i32 { var c = cell_new(5); return 0; }\n", nil},
		{"cellnew-string-ok", "function main(): i32 { var c = cell_new(\"x\"); return 0; }\n", nil},
		{"cellnew-bool-ok", "function main(): i32 { var c = cell_new(1 < 2); return 0; }\n", nil},
		{"cellnew-struct-bad", "struct P { x: i32 }\nfunction main(): i32 { var p: P = P { x: 1 }; var c = cell_new(p); return 0; }\n", []string{"E057"}},
		{"cellnew-array-bad", "function main(): i32 { var a: i32[] = [1]; var c = cell_new(a); return 0; }\n", []string{"E057"}},
		{"cellnew-tuple-bad", "function main(): i32 { var t = (1, 2); var c = cell_new(t); return 0; }\n", []string{"E057"}},
		{"cellnew-nested-bad", "function main(): i32 { var c = cell_new(cell_new(5)); return 0; }\n", []string{"E057"}},
		// E057 ANNOTATION form (#4363 item 2): `Cell[<composite>]` in a
		// param / field / body-var / return annotation — including a Cell
		// nested inside a generic argument, tuple element, or array element
		// spelling — draws E057, anchored at the annotation (the native
		// checker now reports the use site instead of the synthesised Cell
		// decl at 0:0, so the code is visible to this differential). A
		// generic's `Cell[T]` over an in-scope type parameter stays clean
		// (natively a ParamType element; the self-host scopes the walk to
		// non-generic decls). Cross-checked against the Go checker.
		{"cell-annot-param-bad", "struct P { x: i32 }\nfunction f(c: Cell[P]): i32 { return 0; }\nfunction main(): i32 { return 0; }\n", []string{"E057"}},
		{"cell-annot-field-bad", "struct P { x: i32 }\nstruct H { c: Cell[P] }\nfunction main(): i32 { return 0; }\n", []string{"E057"}},
		{"cell-annot-var-bad", "struct P { x: i32 }\nfunction main(): i32 { var c: Cell[P] = cell_new(P { x: 1 }); return 0; }\n", []string{"E057"}},
		{"cell-annot-array-bad", "function f(c: Cell[i32[]]): i32 { return 0; }\nfunction main(): i32 { return 0; }\n", []string{"E057"}},
		{"cell-annot-tuple-elem-bad", "struct P { x: i32 }\nfunction f(t: (i32, Cell[P])): i32 { return 0; }\nfunction main(): i32 { return 0; }\n", []string{"E057"}},
		{"cell-annot-generic-arg-bad", "struct P { x: i32 }\nfunction f(o: Option[Cell[P]]): i32 { return 0; }\nfunction main(): i32 { return 0; }\n", []string{"E057"}},
		{"cell-annot-cell-array-bad", "struct P { x: i32 }\nfunction f(a: Cell[P][]): i32 { return 0; }\nfunction main(): i32 { return 0; }\n", []string{"E057"}},
		{"cell-annot-str-bad", "function f(c: Cell[str]): i32 { return 0; }\nfunction main(): i32 { return 0; }\n", []string{"E057"}},
		// E051 self-reassign move admission (#4873 step 0): a LOCAL passed
		// exactly once, directly, in an `own` position of its OWN
		// reassignment's RHS is a transfer — admitted by both checkers
		// (native SelfReassignOwnMoveArg / self-host ow_self_move_admits).
		// Binding to a different name (old binding kept alive) or a second
		// read of the local in the same RHS stays E051.
		{"own-self-reassign-ok", "struct B { items: i32[] }\nfunction grow(own b: B, x: i32): B { return B { items: b.items.append(x) }; }\nfunction main(): i32 {\n    var a: B = B { items: [] };\n    a = grow(a, 1);\n    a = grow(a, 2);\n    return a.items.len();\n}\n", nil},
		{"own-kept-alive-bad", "struct B { items: i32[] }\nfunction grow(own b: B, x: i32): B { return B { items: b.items.append(x) }; }\nfunction main(): i32 {\n    var a: B = B { items: [] };\n    var c: B = grow(a, 1);\n    return c.items.len();\n}\n", []string{"E051"}},
		{"own-second-read-bad", "struct B { items: i32[] }\nfunction grow(own b: B, x: i32): B { return B { items: b.items.append(x) }; }\nfunction main(): i32 {\n    var a: B = B { items: [7] };\n    a = grow(a, a.items[0]);\n    return a.items.len();\n}\n", []string{"E051"}},
		{"cell-annot-i32-ok", "function f(c: Cell[i32]): i32 { return 0; }\nfunction main(): i32 { return 0; }\n", nil},
		{"cell-annot-string-ok", "function f(c: Cell[string]): i32 { return 0; }\nfunction main(): i32 { return 0; }\n", nil},
		{"cell-annot-generic-param-ok", "function f[T](c: Cell[T]): i32 { return 0; }\nfunction main(): i32 { return 0; }\n", nil},
		// `str` inside a generic ARGUMENT keeps its verbatim spelling in the
		// self-host (a bare `str` is erased to string at the parse
		// boundary) — a real native type, so no E064 (regression pin for
		// the generic-arg-widening false positive fixed alongside item 2).
		{"generic-arg-str-ok", "function f(o: Option[str]): i32 { return 0; }\nfunction main(): i32 { return 0; }\n", nil},
		// E063: returning a `[T]` slice that views function-local storage is a
		// use-after-free (the backing array dies with the frame). The check is
		// conservative — only slices provably viewing local storage fire.
		// String slices copy, slices of a parameter stay valid with the
		// caller's owner, and returning the owned array itself is a move.
		// Cross-checked against the Go checker.
		{"e063-slice-local-array", "function f(): [i32] { var xs: i32[] = [1, 2, 3]; return xs[0:2]; }\nfunction main(): i32 { return 0; }\n", []string{"E063"}},
		{"e063-slice-array-literal", "function f(): [i32] { return [1, 2, 3][0:2]; }\nfunction main(): i32 { return 0; }\n", []string{"E063"}},
		{"e063-slice-local-bound", "function f(): [i32] { var xs: i32[] = [1, 2, 3]; var s = xs[0:2]; return s; }\nfunction main(): i32 { return 0; }\n", []string{"E063"}},
		{"e063-slice-of-param-ok", "function f(xs: i32[]): [i32] { return xs[0:2]; }\nfunction main(): i32 { return 0; }\n", nil},
		{"e063-slice-of-param-bound-ok", "function f(xs: i32[]): [i32] { var s = xs[0:2]; return s; }\nfunction main(): i32 { return 0; }\n", nil},
		{"e063-string-slice-ok", "function f(s: string): str { return s[0:2]; }\nfunction main(): i32 { return 0; }\n", nil},
		{"e063-return-owned-array-ok", "function f(): i32[] { var xs: i32[] = [1, 2, 3]; return xs; }\nfunction main(): i32 { return 0; }\n", nil},
		{"e063-slice-local-not-returned-ok", "function f(): i32 { var xs: i32[] = [1, 2, 3]; var s = xs[0:2]; return s[0]; }\nfunction main(): i32 { return 0; }\n", nil},
		// E023 (unknown enum): an unknown-BASE generic annotation survives
		// type resolution as an "unknown enum" (native resolveType keeps
		// ast.EnumType), so a match / if-let on the value draws E023 at the
		// scrutinee — alongside the E064 the annotation itself draws. A
		// known enum / builtin Option match stays clean.
		{"e023-match-unknown-enum", "function main(s: Statuus[i32]): i32 {\n    match (s) {\n        _ => { return 0; }\n    }\n}\n", []string{"E023", "E064"}},
		{"e023-iflet-unknown-enum", "function main(s: Statuus[i32]): i32 {\n    if let Some(v) = s {\n        return 0;\n    }\n    return 0;\n}\n", []string{"E023", "E064"}},
		{"e023-known-enum-match-ok", "enum Color { Red, Green }\nfunction f(c: Color): i32 { match (c) { Red => { return 1; }, Green => { return 2; } } }\nfunction main(): i32 { return 0; }\n", nil},
		{"e023-option-match-ok", "function main(): i32 { var o: Option[i32] = Some(3); match (o) { Some(v) => { return v; }, None => { return 0; } } }\n", nil},
		// E044 (unsupported capture type): a lambda capturing a value with
		// no runtime representation — a void call result or an erased
		// generic type parameter — draws E044 (the two shapes the native
		// captureSink rejects). Scalar captures and shadowing lambda params
		// stay clean.
		{"e044-capture-generic-param", "function f[T](x: T): i32 {\n    var g = () => x;\n    return 0;\n}\nfunction main(): i32 { return f(1); }\n", []string{"E044"}},
		{"e044-capture-void", "function v(): void { return; }\nfunction main(): i32 {\n    var x = v();\n    var g = () => x;\n    return 0;\n}\n", []string{"E044"}},
		{"e044-capture-scalar-ok", "function main(): i32 {\n    var x = 5;\n    var g = () => x;\n    return g();\n}\n", nil},
		{"e044-capture-shadowed-ok", "function v(): void { return; }\nfunction main(): i32 {\n    var x = v();\n    var g = (x: i32) => x;\n    return g(1);\n}\n", nil},
		// E053 (`fip` no-allocation): array / struct literals, string
		// concatenation, and calls to non-fip functions are rejected inside
		// a `fip function`; scalar arithmetic and fip→fip calls are clean.
		{"e053-fip-array-lit", "fip function mk(): i32 {\n    var a = [1, 2];\n    return a[0];\n}\nfunction main(): i32 { return mk(); }\n", []string{"E053"}},
		{"e053-fip-struct-lit", "struct P { x: i32 }\nfip function mk(a: i32): i32 {\n    var p = P { x: a };\n    return p.x;\n}\nfunction main(): i32 { return mk(1); }\n", []string{"E053"}},
		{"e053-fip-concat", "fip function j(a: string, b: string): string {\n    return a + b;\n}\nfunction main(): i32 { return 0; }\n", []string{"E053"}},
		{"e053-fip-nonfip-call", "function g(): i32 { return 1; }\nfip function f(): i32 {\n    return g();\n}\nfunction main(): i32 { return f(); }\n", []string{"E053"}},
		{"e053-fip-arith-ok", "fip function add(a: i32, b: i32): i32 { return a + b; }\nfunction main(): i32 { return add(1, 2); }\n", nil},
		{"e053-fip-fipcall-ok", "fip function g(): i32 { return 1; }\nfip function f(): i32 { return g() + 1; }\nfunction main(): i32 { return f(); }\n", nil},
		// E065 (returning a `str` view of a function-local string): a local
		// owned string (annotated or inferred) escaping through a str return
		// draws E065, incl. through a local `str` binding chase. A literal,
		// a parameter view, and a str-of-param binding stay clean.
		{"e065-local-string", "function f(): str {\n    var s: string = \"hello\";\n    return s;\n}\nfunction main(): i32 { return 0; }\n", []string{"E065"}},
		{"e065-inferred-local", "function f(): str {\n    var s = \"hi\";\n    return s;\n}\nfunction main(): i32 { return 0; }\n", []string{"E065"}},
		{"e065-str-binding-chase", "function f(): str {\n    var s: string = \"hello\";\n    var t: str = s;\n    return t;\n}\nfunction main(): i32 { return 0; }\n", []string{"E065"}},
		{"e065-literal-ok", "function f(): str {\n    return \"hi\";\n}\nfunction main(): i32 { return 0; }\n", nil},
		{"e065-param-slice-ok", "function f(p: string): str {\n    return p[0:2];\n}\nfunction main(): i32 { return 0; }\n", nil},
		{"e065-str-of-param-ok", "function f(p: str): str {\n    var t: str = p;\n    return t;\n}\nfunction main(): i32 { return 0; }\n", nil},
		// E032 (`use` binding-type inference): an un-annotated `use` whose
		// callee has no signature (the E001 rides along) or whose last
		// parameter isn't a function draws E032 (the E038 arg-type mismatch
		// rides along on the desugared call); an inferrable or annotated
		// `use` is clean.
		{"e032-use-nosig", "function main(): i32 {\n    use n <- q(1);\n    return n;\n}\n", []string{"E001", "E032"}},
		{"e032-use-lastparam-not-fn", "function add(x: i32, y: i32): i32 { return x + y; }\nfunction main(): i32 {\n    use n <- add(1);\n    return n;\n}\n", []string{"E032", "E038"}},
		{"e032-use-ok", "function apply(x: i32, cb: (i32) => i32): i32 { return cb(x); }\nfunction main(): i32 {\n    use n <- apply(41);\n    return n + 1;\n}\n", nil},
		{"e032-use-annotated-ok", "function apply(x: i32, cb: (i32) => i32): i32 { return cb(x); }\nfunction main(): i32 {\n    use n: i32 <- apply(41);\n    return n + 1;\n}\n", nil},
		// @inline / @noinline function attributes (#4412 Rec §14): native
		// consults them in the IR inliner; the self-host parser
		// parse-tolerates and drops them. Both checkers must accept the
		// annotated program cleanly.
		{"inline-hints-ok", "@inline\nfunction fast(x: i32): i32 { return x + x; }\n@noinline\nfunction slow(x: i32): i32 { return x + 1; }\nfunction main(): i32 { return fast(3) + slow(4); }\n", nil},
		// E067 (@must_consume obligation, docs/MUST-CONSUME.md): a value of a
		// marked struct/enum type must be consumed on every control-flow path
		// before its binding leaves scope — call-argument, return, match,
		// transfer to another binding, or store into another marked type.
		// Laundering into an unmarked container (array/tuple/unmarked struct)
		// is a violation at the store site; loop bodies are opaque; `own`
		// params are exempt. Mirrors internal/checker/mustconsume_test.go;
		// cross-checked against the Go oracle by the differential leg.
		{"mc-plain-leak", "@must_consume\nstruct Ticket { id: i32 }\nfunction sink(t: Ticket): Ticket { return t; }\nfunction f(): void { var t: Ticket = Ticket { id: 1 }; }\nfunction main(): i32 { return 0; }\n", []string{"E067"}},
		{"mc-one-arm-leak", "@must_consume\nstruct Ticket { id: i32 }\nfunction sink(t: Ticket): Ticket { return t; }\nfunction f(n: i32): void { var t: Ticket = Ticket { id: 1 }; if (n > 0) { sink(t); } }\nfunction main(): i32 { return 0; }\n", []string{"E067"}},
		{"mc-early-return-leak", "@must_consume\nstruct Ticket { id: i32 }\nfunction sink(t: Ticket): Ticket { return t; }\nfunction f(n: i32): i32 { var t: Ticket = Ticket { id: 1 }; if (n > 5) { return 1; } sink(t); return 0; }\nfunction main(): i32 { return 0; }\n", []string{"E067"}},
		{"mc-both-arms-ok", "@must_consume\nstruct Ticket { id: i32 }\nfunction sink(t: Ticket): Ticket { return t; }\nfunction f(n: i32): void { var t: Ticket = Ticket { id: 1 }; if (n > 0) { sink(t); } else { sink(t); } }\nfunction main(): i32 { return 0; }\n", nil},
		{"mc-transfer-ok", "@must_consume\nstruct Ticket { id: i32 }\nfunction f(): Ticket { var t: Ticket = Ticket { id: 1 }; var u: Ticket = t; return u; }\nfunction main(): i32 { return 0; }\n", nil},
		{"mc-unannotated-leak", "@must_consume\nstruct Ticket { id: i32 }\nfunction f(): void { var t = Ticket { id: 1 }; }\nfunction main(): i32 { return 0; }\n", []string{"E067"}},
		{"mc-match-consumes-ok", "@must_consume\nenum Pending { Reply(string), Close }\nfunction f(p: Pending): i32 { match (p) { Reply(s) => { print(s); }, Close => { print(\"closed\"); } } return 0; }\nfunction main(): i32 { return 0; }\n", nil},
		{"mc-enum-leak", "@must_consume\nenum Pending { Reply(string), Close }\nfunction f(): void { var p: Pending = Close; }\nfunction main(): i32 { return 0; }\n", []string{"E067"}},
		{"mc-laundered-array", "@must_consume\nstruct Ticket { id: i32 }\nfunction f(): void { var t: Ticket = Ticket { id: 1 }; var arr: Ticket[] = [t]; }\nfunction main(): i32 { return 0; }\n", []string{"E067"}},
		{"mc-laundered-struct", "@must_consume\nstruct Ticket { id: i32 }\nstruct Box { inner: Ticket }\nfunction f(): void { var t: Ticket = Ticket { id: 1 }; var b: Box = Box { inner: t }; }\nfunction main(): i32 { return 0; }\n", []string{"E067"}},
		{"mc-laundered-tuple", "@must_consume\nstruct Ticket { id: i32 }\nfunction f(): (Ticket, i32) { var t: Ticket = Ticket { id: 1 }; return (t, 3); }\nfunction main(): i32 { return 0; }\n", []string{"E067"}},
		{"mc-marked-envelope-ok", "@must_consume\nstruct Ticket { id: i32 }\n@must_consume\nstruct Envelope { inner: Ticket }\nfunction open_env(e: Envelope): Envelope { return e; }\nfunction f(): void { var t: Ticket = Ticket { id: 1 }; var e: Envelope = Envelope { inner: t }; open_env(e); }\nfunction main(): i32 { return 0; }\n", nil},
		{"mc-closure-capture", "@must_consume\nstruct Ticket { id: i32 }\nfunction f(): void { var t: Ticket = Ticket { id: 1 }; var g = function(): i32 { return t.id; }; print(\"captured\"); }\nfunction main(): i32 { return 0; }\n", []string{"E067"}},
		{"mc-overwrite", "@must_consume\nenum Pending { Reply(string), Close }\nfunction f(): void { var p: Pending = Close; p = Reply(\"again\"); match (p) { Reply(s) => { }, Close => { } } }\nfunction main(): i32 { return 0; }\n", []string{"E067"}},
		{"mc-nonown-param-leak", "@must_consume\nstruct Ticket { id: i32 }\nfunction f(t: Ticket): void { print(\"ignored\"); }\nfunction main(): i32 { return 0; }\n", []string{"E067"}},
		{"mc-own-param-ok", "@must_consume\nstruct Ticket { id: i32 }\nfunction take(own t: Ticket): void { print(\"own\"); }\nfunction f(): void { take(Ticket { id: 9 }); }\nfunction main(): i32 { return 0; }\n", nil},
		{"mc-loop-consume-leak", "@must_consume\nstruct Ticket { id: i32 }\nfunction sink(t: Ticket): Ticket { return t; }\nfunction f(n: i32): void { var t: Ticket = Ticket { id: 1 }; var i: i32 = 0; while (i < n) { sink(t); i = i + 1; } }\nfunction main(): i32 { return 0; }\n", []string{"E067"}},
		{"mc-field-read-neutral-ok", "@must_consume\nstruct Ticket { id: i32 }\nfunction sink(t: Ticket): Ticket { return t; }\nfunction f(): i32 { var t: Ticket = Ticket { id: 7 }; var n: i32 = t.id; sink(t); return n; }\nfunction main(): i32 { return 0; }\n", nil},
		{"mc-match-expr-consumes-ok", "@must_consume\nenum Pending { Reply(string), Close }\nfunction f(p: Pending): i32 { var r: i32 = match (p) { Reply(s) => 1, Close => 0 }; return r; }\nfunction main(): i32 { return 0; }\n", nil},
		{"mc-nested-block-leak", "@must_consume\nstruct Ticket { id: i32 }\nfunction f(c: boolean): void { if (c) { var t: Ticket = Ticket { id: 1 }; } }\nfunction main(): i32 { return 0; }\n", []string{"E067"}},
		{"mc-unmarked-unaffected-ok", "struct Plain { id: i32 }\nfunction f(): void { var p: Plain = Plain { id: 1 }; }\nfunction main(): i32 { return 0; }\n", nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var cmd *exec.Cmd
			if len(runner) == 0 {
				cmd = exec.Command(checkerBin)
			} else {
				cmd = exec.Command(runner[0], append(runner[1:], checkerBin)...)
			}
			cmd.Stdin = bytes.NewReader([]byte(tc.src))
			out, _ := cmd.Output()
			got := uniqueSortedCodes(strings.Fields(string(out)))

			want := uniqueSortedCodes(tc.want)
			if !equalStrings(got, want) {
				t.Errorf("%s: self-host codes = %v, want %v", tc.name, got, want)
			}
			// Differential: the self-host codes must match what the Go
			// checker reports for the same source — the FULL, unfiltered
			// code set (the historical selfHostImplementedCodes filter is
			// gone; the port covers every code the Go checker emits).
			goCodes := goCheckerCodes(t, dir, tc.src)
			if !equalStrings(got, goCodes) {
				t.Errorf("%s: self-host codes %v disagree with Go checker %v (unfiltered)", tc.name, got, goCodes)
			}
		})
	}
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// TestSelfHostCheckerDifferentialX86_64 is the pure-differential verification
// harness for the self-host checker. For each program it runs BOTH the
// self-host checker (the codes driver) and the production Go checker, and
// asserts they agree on the diagnostic code SET (unfiltered). Unlike TestSelfHostCheckerCodesX86_64 it carries NO
// hardcoded expected codes — the Go checker is the sole oracle — so it catches
// a self-host FALSE POSITIVE (or missing diagnostic) on any construct in the
// corpus without the test author having to predict the codes.
//
// It is seeded with the real-world tuple / generic / union / method-chain
// constructs the stdlib leans on (annotated tuples, tuple-of-union, nested
// tuples, generic tuple containers, …). The whole-compiler fixpoint/bundle
// gate never runs the self-host CHECKER over the stdlib, so before teaching
// checker.fern a rule that touches these shapes (e.g. tuple-type assignability,
// builtin-method return types) add the relevant valid programs here: a
// regression then fails loudly in this differential instead of lurking as a
// latent false positive only triggered when someone runs the self-host checker
// over real code.
func TestSelfHostCheckerDifferentialX86_64(t *testing.T) {
	checkerBin, runner, dir := buildCheckerCodesBin(t)

	progs := []struct{ name, src string }{
		// Annotated tuple shapes — var binding, parameter, return, nested, and
		// a tuple whose element is a (builtin) enum/union. All well-typed: the
		// self-host must not invent a diagnostic the Go checker doesn't report.
		{"tuple-return", "function pair(): (i32, string) { return (1, \"a\"); }\nfunction main(): i32 { return 0; }\n"},
		{"tuple-var-annot", "function main(): i32 { var t: (i32, string) = (1, \"a\"); return t.0; }\n"},
		{"tuple-array-annot", "function main(): i32 { var out: (i32, string)[] = []; return 0; }\n"},
		{"tuple-nested", "function main(): i32 { var t: (i32, (string, i32)) = (1, (\"a\", 2)); return t.0; }\n"},
		{"tuple-union-elem", "enum E { A, B }\nfunction f(): (E, i32) { return (A, 1); }\nfunction main(): i32 { return 0; }\n"},
		{"tuple-param", "function f(p: (i32, string)): i32 { return p.0; }\nfunction main(): i32 { return f((1, \"a\")); }\n"},
		{"tuple-struct-elem", "struct P { x: i32 }\nfunction f(): (P, i32) { return (P { x: 1 }, 2); }\nfunction main(): i32 { return 0; }\n"},
		{"tuple-reassign", "function main(): i32 { var t: (i32, string) = (1, \"a\"); t = (2, \"b\"); return t.0; }\n"},
		// Generic tuple container (the std/array `zip` shape).
		{"tuple-generic-array", "function zip(a: i32[], b: string[]): (i32, string)[] { var out: (i32, string)[] = []; return out; }\nfunction main(): i32 { return 0; }\n"},
		// Lambda bodies see the LAMBDA's declared return type (not the
		// enclosing function's) — pins the call_diags lambda-scope ret_type
		// threading (#4363 item 1) on valid array-returning shapes.
		{"lambda-arr-ret-ok", "function f(): i32[] {\n    var g = function(): string[] { return [\"a\", \"b\"]; };\n    return [1, 2];\n}\nfunction main(): i32 { return 0; }\n"},
		{"lambda-try-ret-option-ok", "function f(): i32 {\n    var g = function(): Option[i32] { var o: Option[i32] = Some(1); var v: i32 = o?; return Some(v); };\n    return 2;\n}\nfunction main(): i32 { return 0; }\n"},
		// Method chains on string / array builtins (valid).
		{"method-chain-len", "function main(): i32 { var s = \"abc\"; var n = s.len(); return n; }\n"},
		{"method-chain-array", "function main(): i32 { var a = [1, 2, 3]; return a.len(); }\n"},
		// if/match-expression values flowing into typed positions (post-#3137).
		{"if-expr-typed-ok", "function main(): i32 { var x: i32 = if (1 < 2) { 1 } else { 2 }; return x; }\n"},
		{"match-expr-typed-ok", "enum E { A, B }\nfunction main(): i32 { var e: E = A; var x: i32 = match (e) { A => 1, B => 2 }; return x; }\n"},
		// Builtin array-method calls with element-typed args (valid): the
		// self-host uses .append / .with pervasively, so an arg-type rule over
		// them must not false-positive on a correctly-typed call.
		{"array-append-prim-ok", "function main(): i32 { var a: i32[] = [1]; a = a.append(2); return a.len(); }\n"},
		{"array-append-struct-ok", "struct P { x: i32 }\nfunction main(): i32 { var a: P[] = [P { x: 1 }]; a = a.append(P { x: 2 }); return 0; }\n"},
		{"array-append-union-ok", "struct P { x: i32 }\nstruct Q { y: i32 }\ntype U = P | Q;\nfunction main(): i32 { var a: U[] = [P { x: 1 }]; a = a.append(Q { y: 2 }); return 0; }\n"},
		{"array-with-ok", "function main(): i32 { var a: i32[] = [1, 2]; a = a.with(0, 9); return a.len(); }\n"},
		{"array-append-tuple-ok", "function main(): i32 { var a: (i32, string)[] = []; a = a.append((1, \"x\")); return 0; }\n"},
		// Literal-match wildcard position (#3612), proven against the Go
		// oracle: a non-last `_` on an i32/string scrutinee desugars to an
		// if/else chain, so the self-host must still surface E026 (and only
		// E026) for any `_` position, while the wildcard-last form stays
		// clean. Differential — no hardcoded codes, native is the oracle.
		{"lit-match-wildcard-first", "function main(): i32 { var x = 1; match (x) { _ => { return 0; }, 1 => { return 1; } } }\n"},
		{"lit-match-wildcard-middle", "function main(): i32 { var x = 1; match (x) { 1 => { return 1; }, _ => { return 9; }, 2 => { return 2; } } }\n"},
		{"str-match-wildcard-middle", "function f(s: string): i32 { match (s) { \"a\" => { return 1; }, _ => { return 0; }, \"b\" => { return 2; } } }\nfunction main(): i32 { return f(\"a\"); }\n"},
		{"lit-match-wildcard-last-ok", "function classify(x: i32): i32 { match (x) { 1 => { return 10; }, 2 => { return 20; }, _ => { return 99; } } }\nfunction main(): i32 { return classify(2); }\n"},
		// Sub-word / pointer-width integer builtins (u8 / usize) — the only two
		// left after i8/u16/i16/isize were retired (#4408): the Go checker
		// accepts them as real types (stdlib byte code uses `var b: u8` and
		// core/int uses `var p: usize` pervasively), so the self-host E064
		// unknown-type rule must not flag them — on a param or a body `var`
		// annotation (the #3813 body-var walk that imported them via the
		// stdlib bundle). `byte` is the negative control: not a keyword, so
		// the Go checker rejects it and both must report E064, proving the
		// allowlist didn't over-broaden.
		{"subword-int-params", "function f(a: u8, b: usize): i32 { return 0; }\nfunction main(): i32 { return 0; }\n"},
		{"subword-u8-var", "function main(): i32 { var n: u8 = 0 as u8; return 0; }\n"},
		{"usize-var", "function main(): i32 { var p: usize = 0 as usize; return 0; }\n"},
		{"unknown-byte-param", "function f(x: byte): i32 { return 0; }\nfunction main(): i32 { return 0; }\n"},
		// Multi-binding variant payload patterns (#4345): the self-host checker
		// only bound the FIRST payload name (pv.binding) and rejected the rest as
		// undefined (a false E001), so `Rect(w, h)` reading `h` in an arm body /
		// guard / assignment tripped a diagnostic the Go checker never emits.
		// These exercise every arm-scope binding site: expr-arm body, when-guard
		// scope, assignment-in-arm, and a `_`-first position that binds only the
		// second name. All well-typed → the self-host must stay silent.
		{"match-multi-binding-body", "enum Shape { Circle(i32), Rect(i32, i32) }\nfunction area(s: Shape): i32 { return match (s) { Circle(r) => r, Rect(w, h) => w * h }; }\nfunction main(): i32 { return area(Rect(3, 4)); }\n"},
		{"match-multi-binding-guard", "enum Shape { Circle(i32), Rect(i32, i32) }\nfunction area(s: Shape): i32 { match (s) { Rect(w, h) when h > 0 => { return w * h; }, _ => { return 0; } } }\nfunction main(): i32 { return area(Rect(3, 4)); }\n"},
		{"match-multi-binding-assign", "enum Shape { Circle(i32), Rect(i32, i32) }\nfunction area(s: Shape): i32 { match (s) { Rect(w, h) => { h = h + 1; return w * h; }, Circle(r) => { return r; } } }\nfunction main(): i32 { return area(Rect(3, 4)); }\n"},
		{"match-multi-binding-wild-first", "enum Shape { Circle(i32), Rect(i32, i32) }\nfunction area(s: Shape): i32 { return match (s) { Circle(r) => r, Rect(_, h) => h }; }\nfunction main(): i32 { return area(Rect(3, 4)); }\n"},
		// Guarded match arms (#4344). A guarded arm doesn't fully cover its
		// variant, so:
		//   - guarded-then-unguarded is VALID (no false E028) — the self-host
		//     used to add every variant to `seen` regardless of the guard.
		//   - a guarded-ONLY variant is NON-exhaustive (E030) — the self-host
		//     used to count it as covered (accepts-invalid). Native is the
		//     oracle: the first is clean, the second draws E030.
		{"match-guarded-then-unguarded-ok", "enum Color { Red, Green, Blue }\nfunction f(c: Color): i32 { match (c) { Red when 1 == 1 => { return 1; }, Red => { return 2; }, Green => { return 3; }, Blue => { return 4; } } }\nfunction main(): i32 { return f(Green); }\n"},
		{"match-guarded-only-nonexhaustive", "enum Color { Red, Green, Blue }\nfunction f(c: Color): i32 { match (c) { Red when 1 == 2 => { return 1; }, Green => { return 3; }, Blue => { return 4; } } }\nfunction main(): i32 { return f(Green); }\n"},
		// Nested generic field spellings (#4346 piece 2): reading a field whose
		// declared type nests a type parameter — `items: T[]`, `kv: (K, V)` — off
		// a concrete instantiation substitutes the arg throughout, so
		// `Wrapper[i32].items` is i32[] (element i32) and `Pair[i32, string].kv`
		// is (i32, string). Both well-typed against the Go oracle — the self-host
		// must stay silent where it once typed the nested field unknown and
		// either over- or under-flagged.
		{"generic-nested-array-field-ok", "struct Wrapper[T] { items: T[] }\nfunction main(): i32 { var w: Wrapper[i32] = Wrapper { items: [1, 2, 3] }; return w.items[0]; }\n"},
		{"generic-nested-tuple-field-ok", "struct Pair[K, V] { kv: (K, V) }\nfunction main(): i32 { var p: Pair[i32, string] = Pair { kv: (7, \"hi\") }; return p.kv.0; }\n"},
		// dyn Trait representation (#4346 piece 2): a `dyn Trait` annotation now
		// resolves to TypeDyn, so binding a value into a dyn slot type-checks
		// (assignment into dyn is lenient). Both are well-typed against the Go
		// oracle — a var-binding and a `dyn T[]` array of dyn — where the
		// pre-slice self-host bound them to unknown and silently over-rejected.
		// The E021 object-safety pass keys off the annotation text (Greet is
		// object-safe), so no code fires; the self-host must stay silent.
		{"dyn-bind-ok", "trait Greet { function hi(self: Self): i32; }\nstruct Dog { }\nimpl Greet for Dog { function hi(self: Self): i32 { return 7; } }\nfunction main(): i32 { var d: dyn Greet = Dog { }; return 0; }\n"},
		{"dyn-array-ok", "trait Greet { function hi(self: Self): i32; }\nstruct Dog { }\nimpl Greet for Dog { function hi(self: Self): i32 { return 7; } }\nfunction main(): i32 { var ds: dyn Greet[] = [Dog { }]; return ds.len(); }\n"},
		// Generic-receiver method return substitution (#4346 piece 2): a method
		// returning a receiver type parameter — bare `T` or nested `T[]` —
		// resolves to the instantiation off a concrete receiver. Both well-typed
		// against the Go oracle (`Box[i32].get()` → i32 feeds an i32 return;
		// `Wrapper[i32].all()` → i32[] indexes to i32) where the pre-slice
		// self-host typed the call unknown and silently over-rejected.
		{"generic-method-ret-ok", "struct Box[T] { v: T }\nfunction (b: Box[T]) get(): T { return b.v; }\nfunction main(): i32 { var b: Box[i32] = Box { v: 5 }; return b.get(); }\n"},
		{"generic-method-nested-ret-ok", "struct Wrapper[T] { items: T[] }\nfunction (w: Wrapper[T]) all(): T[] { return w.items; }\nfunction main(): i32 { var w: Wrapper[i32] = Wrapper { items: [1, 2] }; return w.all()[0]; }\n"},
	}

	for _, tc := range progs {
		t.Run(tc.name, func(t *testing.T) {
			var cmd *exec.Cmd
			if len(runner) == 0 {
				cmd = exec.Command(checkerBin)
			} else {
				cmd = exec.Command(runner[0], append(runner[1:], checkerBin)...)
			}
			cmd.Stdin = bytes.NewReader([]byte(tc.src))
			out, _ := cmd.Output()
			got := uniqueSortedCodes(strings.Fields(string(out)))
			want := goCheckerCodes(t, dir, tc.src)
			if !equalStrings(got, want) {
				t.Errorf("%s: self-host codes %v disagree with Go checker %v (unfiltered)\nsrc: %s", tc.name, got, want, tc.src)
			}
		})
	}
}

// TestSelfHostCheckerBundleDifferentialX86_64 is the MULTI-MODULE differential
// verification harness. Unlike TestSelfHostCheckerDifferentialX86_64 (which
// checks a single self-contained module), it resolves each program's stdlib
// imports off disk and runs it through the file-based checker driver
// (checker_modload_run.fern → ./modloader → flatten.bundle → check_module),
// then asserts the self-host code set matches the Go checker's (modload +
// check) — unfiltered, with the Go checker as the sole oracle (no
// hardcoded expectations).
//
// This is the harness the method-table-dependent diagnostics need: it lets a
// rule keyed off an IMPORTED module's methods/types (string methods from
// std/string, etc.) be verified false-positive-free against real stdlib code
// before it ships. Seed it with valid stdlib-method programs; extend it when
// teaching the checker a rule that consults the imported table.
func TestSelfHostCheckerBundleDifferentialX86_64(t *testing.T) {
	_, runner, driverBin := buildCheckerModloadDriverX86(t)

	progs := []struct{ name, src string }{
		// `char` is NOT an integer. Both directions must be rejected without an
		// explicit cast — that distinctness is the type's whole point (#5629),
		// since a byte and a code point sharing i32 is what made
		// `s[i].to_upper()` and `to_upper_char(cp)` indistinguishable. The cast
		// itself stays legal, and the last case pins that: making char distinct
		// must not turn `n as char` / `c as i32` into E033.
		{"char-not-from-int-literal", "function main(): i32 { var c: char = 65; return 0; }\n"},
		{"char-not-to-i32-return", "function f(c: char): i32 { return c; }\nfunction main(): i32 { return 0; }\n"},
		{"char-not-from-i32-return", "function f(n: i32): char { return n; }\nfunction main(): i32 { return 0; }\n"},
		{"char-not-to-u8", "function main(): i32 { var cs: char[] = []; var b: u8 = cs[0]; return 0; }\n"},
		{"char-casts-both-ways-ok", "function main(): i32 { var c: char = 65 as char; var n: i32 = c as i32; return n; }\n"},
		// The NEGATIVE cases are the ones that discriminate. A bogus method on a
		// char must be E043 in both checkers; before #5922 the self-host reported
		// nothing at all, because `char` resolved in only one of five type-name
		// resolvers and an unknown receiver type skips every check — which made
		// the two positive cases below pass vacuously.
		{"char-method-bogus", "import \"std/utf8\";\nimport \"std/unicode\";\nfunction main(): i32 { var cs: char[] = utf8.codepoints(\"a\"); return cs[0].definitely_not_a_method(); }\n"},
		{"char-param-method-bogus", "function f(c: char): i32 { return c.definitely_not_a_method(); }\nfunction main(): i32 { return 0; }\n"},
		// A `char`-RECEIVER method call. std/unicode declares seven of them
		// ((c: char) to_upper / is_letter / ...), so if the self-host resolves a
		// char receiver to the i32 label while the declaration registered under
		// "char", every one of these is a spurious E043.
		{"char-method-to-upper", "import \"std/utf8\";\nimport \"std/unicode\";\nfunction main(): i32 { var cs: char[] = utf8.codepoints(\"a\"); return cs[0].to_upper() as i32; }\n"},
		{"char-method-is-letter", "import \"std/utf8\";\nimport \"std/unicode\";\nfunction main(): i32 { var cs: char[] = utf8.codepoints(\"a\"); if (cs[0].is_letter()) { return 1; } return 0; }\n"},
		// `<lit> as char` range/surrogate validation (E071, #5629 slice 5).
		// Both checkers must agree on which literals name a Unicode scalar
		// value. The self-host resolves `char` to i32, so it keys the rule on
		// the op text rather than the target type — these hold the two
		// implementations to the same answer, including the hex spelling and
		// both boundary values.
		{"char-lit-ok-ascii", "function main(): i32 { var c: char = 65 as char; return 0; }\n"},
		{"char-lit-ok-max", "function main(): i32 { var c: char = 1114111 as char; return 0; }\n"},
		{"char-lit-ok-below-surrogates", "function main(): i32 { var c: char = 55295 as char; return 0; }\n"},
		{"char-lit-ok-above-surrogates", "function main(): i32 { var c: char = 57344 as char; return 0; }\n"},
		{"char-lit-ok-hex", "function main(): i32 { var c: char = 0x10FFFF as char; return 0; }\n"},
		{"char-lit-e071-above-max", "function main(): i32 { var c: char = 1114112 as char; return 0; }\n"},
		{"char-lit-e071-huge", "function main(): i32 { var c: char = 2147483647 as char; return 0; }\n"},
		{"char-lit-e071-surrogate-lo", "function main(): i32 { var c: char = 55296 as char; return 0; }\n"},
		{"char-lit-e071-surrogate-hi", "function main(): i32 { var c: char = 57343 as char; return 0; }\n"},
		{"char-lit-e071-hex", "function main(): i32 { var c: char = 0x110000 as char; return 0; }\n"},
		// A runtime operand stays unchecked in BOTH — `as char` is the
		// reinterpret hatch, and a checker that flagged this would break
		// std/unicode's table lookups.
		{"char-runtime-cast-unchecked", "function main(): i32 { var n: i32 = 1114112; var c: char = n as char; return 0; }\n"},
		// Valid string-method calls (user-defined methods in std/string, not
		// builtins): both checkers must agree they are well-typed.
		{"string-contains-ok", "import \"std/string\";\nfunction main(): i32 { var s = \"abc\"; if (s.contains(\"b\")) { return 1; } return 0; }\n"},
		{"string-starts-with-ok", "import \"std/string\";\nfunction main(): i32 { var s = \"abc\"; if (s.starts_with(\"a\")) { return 1; } return 0; }\n"},
		{"string-is-empty-ok", "import \"std/string\";\nfunction main(): i32 { var s = \"\"; if (s.is_empty()) { return 1; } return 0; }\n"},
		{"string-trim-ok", "import \"std/string\";\nfunction main(): i32 { var s = \"  a \"; var t = s.trim(); return 0; }\n"},
		{"string-to-lower-ok", "import \"std/string\";\nfunction main(): i32 { var s = \"AB\"; var t = s.to_lower(); return 0; }\n"},
		// Auto-discovered std/array methods (__method_Array_*) must resolve once
		// std/array is imported — the import-side companion to the single-module
		// E043 corpus (a.sum() without import → E043). A codegen-intercepted
		// method (sum) and a non-intercepted one (sum_squared) both stay clean;
		// an unknown method (bogus) is still E043 even with std/array in scope.
		{"array-sum-import-ok", "import \"std/array\";\nfunction main(): i32 { var a: i32[] = [1, 2, 3]; return a.sum(); }\n"},
		{"array-sum-squared-import-ok", "import \"std/array\";\nfunction main(): i32 { var a: i32[] = [1, 2, 3]; return a.sum_squared(); }\n"},
		{"array-bogus-import-e043", "import \"std/array\";\nfunction main(): i32 { var a: i32[] = [1, 2, 3]; return a.bogus(); }\n"},
		// Array method RETURN-TYPING: `a.<m>()` resolves to the std/array
		// helper's declared return type, not `unknown`. So a result used in a
		// type-incompatible context surfaces the same E002/E003 the Go checker
		// reports (before, the unknown result was conservatively accepted and
		// the self-host stayed silent — a divergence). Scalar (sum→i32),
		// array (reversed→i32[]), bool (every_positive→boolean) and string
		// (join→string) returns are each covered on a clean and a mismatch path.
		{"array-sum-ret-i32-ok", "import \"std/array\";\nfunction main(): i32 { var a: i32[] = [1, 2, 3]; var x: i32 = a.sum(); return x; }\n"},
		{"array-sum-ret-string-mismatch", "import \"std/array\";\nfunction main(): i32 { var a: i32[] = [1, 2, 3]; var x: string = a.sum(); return 0; }\n"},
		{"array-reversed-ret-array-ok", "import \"std/array\";\nfunction main(): i32 { var a: i32[] = [1, 2, 3]; var b: i32[] = a.reversed(); return b[0]; }\n"},
		{"array-reversed-ret-string-mismatch", "import \"std/array\";\nfunction main(): i32 { var a: i32[] = [1, 2, 3]; var c: string = a.reversed(); return 0; }\n"},
		{"array-every-positive-ret-bool-ok", "import \"std/array\";\nfunction main(): i32 { var a: i32[] = [1, 2, 3]; if (a.every_positive()) { return 1; } return 0; }\n"},
		{"array-join-ret-string-ok", "import \"std/array\";\nfunction main(): i32 { var a: string[] = [\"x\", \"y\"]; var s: string = a.join(\",\"); return 0; }\n"},
		{"array-join-ret-i32-mismatch", "import \"std/array\";\nfunction main(): i32 { var a: string[] = [\"x\", \"y\"]; var n: i32 = a.join(\",\"); return 0; }\n"},
		// The string-method-existence E043 rule must still fire for a method
		// that no imported module defines, even WITH std/string in scope —
		// the bundle harness's reason for existing. `substr` isn't a std/string
		// method, so both checkers report E043.
		{"string-unknown-method-e043", "import \"std/string\";\nfunction main(): i32 { var s = \"abc\"; var t = s.substr(0, 1); return 0; }\n"},
		// #5205: a bundled helper that `match`es on an Option returned by a
		// method call (`result.checked_mul(base)` → Option[i32]) binding
		// `Some(v)` and assigning the payload to a concrete-typed local
		// (`result = v`). A built-in payload variant has no struct sig, so the
		// self-host used to bind `v` as the wrapper struct `Some` and spuriously
		// draw E003 on `result = v` — poisoning the whole bundle's code set for
		// every program that transitively imports the helper's module. The
		// helper's payload type is now bound unknown (unrecoverable in the
		// name-only type system), so both checkers agree the bundle is clean.
		{"match-method-option-payload-assign-ok", "import \"std/string\";\nfunction trig(base: i32): i32 { var result: i32 = 1; match (result.checked_mul(base)) { Some(v) => { result = v; }, None => { return 0; } } return result; }\nfunction main(): i32 { var s = \"abc\"; if (s.contains(\"b\")) { return 1; } return 0; }\n"},
		{"match-method-option-payload-assign-array-ok", "import \"std/array\";\nfunction trig(base: i32): i32 { var result: i32 = 1; match (result.checked_mul(base)) { Some(v) => { result = v; }, None => { return 0; } } return result; }\nfunction main(): i32 { var a: i32[] = [1, 2, 3]; return a.sum(); }\n"},
	}

	for _, tc := range progs {
		t.Run(tc.name, func(t *testing.T) {
			out := checkSourceModload(t, runner, driverBin, tc.src)
			got := uniqueSortedCodes(strings.Fields(out))
			want := goCheckerCodes(t, t.TempDir(), tc.src)
			if !equalStrings(got, want) {
				t.Errorf("%s: self-host file-loader codes %v disagree with Go checker %v (unfiltered)\nsrc: %s", tc.name, got, want, tc.src)
			}
		})
	}
}
