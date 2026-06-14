package e2e

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

// selfHostImplementedCodes is the set of Go-checker codes the self-host
// checker (checker.fern) already emits. The differential gate below
// asserts parity ONLY on this set, so it stays green as the Go checker
// emits codes the self-host port hasn't reached yet. Each checker-port
// slice grows this set (see docs/SELFHOST-CHECKER-PORT.md).
var selfHostImplementedCodes = map[string]bool{
	"E002": true, // return-type mismatch
	"E003": true, // assignment / annotated-var type mismatch
	"E008": true, // non-boolean if/while condition
	"E009": true, // non-boolean operand of && / || / !
	"E011": true, // break / continue outside a loop
	"E012": true, // return without value in a non-void function
	"E013": true, // duplicate var in the same block
	"E017": true, // duplicate variant in an enum
	"E019": true, // generic-struct type-argument arity (param / field)
	"E020": true, // empty array literal needs a type annotation
	"E004": true, // free-function call arity
	"E026": true, // wildcard arm not last in a match
	"E028": true, // variant covered twice in a match
	"E036": true, // unqualified reference to a variant shared by 2+ enums
	"E037": true, // slice bound must be i32
	"E038": true, // free-function argument type
	"E041": true, // == / != on mismatched types
	"E043": true, // unknown struct field (read)
	"E046": true, // tuple field index (non-numeric / out of range)
	"E047": true, // integer literal doesn't fit i32
	"E005": true, // struct literal missing field
	"E006": true, // function / method redeclared
	"E007": true, // duplicate struct field
	"E018": true, // duplicate parameter
	"E034": true, // heterogeneous array element type (primitives)
	"E035": true, // variant pattern in a match on a non-enum scrutinee
	"E030": true, // non-exhaustive match on a union scrutinee
	"E014": true, // variant pattern not part of the scrutinee enum/union
	"E029": true, // variant pattern qualifier names the wrong enum/union
	"E016": true, // union alias collides with a struct of the same name
	"E052": true, // missing return (non-void body can fall off the end)
	"E021": true, // method receiver references an unknown type
	"E024": true, // tuple destructure of a non-tuple / wrong arity
	"E033": true, // invalid cast (bool ↔ non-bool scalar)
	"E048": true, // assignment to an immutable struct field
	"E001": true, // undefined name (value position)
	"E042": true, // `?` operator on a non-Option/Result operand
	"E010": true, // user enum shadowing a reserved built-in name
	"E027": true, // match guard must be boolean
	"E015": true, // variant pattern payload-binding arity
	"E040": true, // generic-call type-argument arity mismatch
	"E054": true, // @export function cannot be generic / a method
	"E050": true, // use of an owned parameter after it was consumed (move)
	"E051": true, // argument to an owned parameter must be an owned value
	"E049": true, // assignment to a reference-typed closure capture
	"E055": true, // discarded result of a value-returning collection mutator
	"E031": true, // match/if-expression arms have incompatible types
	"E045": true, // map literal key type must be i32 or string
	"E025": true, // switch on float / case value type mismatch
	"E022": true, // if-let / let-else source not an enum; let-else else must diverge
	"E057": true, // cell_new(v) element type must be a scalar or string
	"E056": true, // subscript assignment (arr[i] = v) is read-only
}

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

// filterImplemented keeps only the codes the self-host checker is
// expected to emit at this slice.
func filterImplemented(codes []string) []string {
	var out []string
	for _, c := range codes {
		if selfHostImplementedCodes[c] {
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
// same source — restricted to the codes the port has implemented so far
// (selfHostImplementedCodes). As later slices teach checker.fern more
// codes, the corpus + that set grow together.
func TestSelfHostCheckerCodesX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := writeSelfHostAsmProject(t) // lexer, parser, asm
	for _, name := range []string{"flatten.fern", "util.fern", "checker.fern", "bundle_run.fern"} {
		src, err := os.ReadFile(filepath.Join("../../examples/self_host", name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		if err := os.WriteFile(filepath.Join(dir, name), src, 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	driverBin := buildSelfHostBin(t, gcc, dir, "bundle_run.fern", "driver")

	lexerSrc, _ := os.ReadFile(filepath.Join(dir, "lexer.fern"))
	parserSrc, _ := os.ReadFile(filepath.Join(dir, "parser.fern"))
	checkerSrc, _ := os.ReadFile(filepath.Join(dir, "checker.fern"))
	utilSrc, _ := os.ReadFile(filepath.Join(dir, "util.fern"))
	ioSrc, err := os.ReadFile("../../internal/stdlib/std/io.fern")
	if err != nil {
		t.Fatalf("read std/io.fern: %v", err)
	}
	runSrc, err := os.ReadFile("../../examples/self_host/checker_codes_run.fern")
	if err != nil {
		t.Fatalf("read checker_codes_run.fern: %v", err)
	}
	driverMod := strings.ReplaceAll(string(runSrc), "import \"std/io\";", "import \"./io\";")
	var bundle bytes.Buffer
	bundle.WriteString("///MODULE util\n")
	bundle.Write(utilSrc)
	bundle.WriteString("///MODULE lexer\n")
	bundle.Write(lexerSrc)
	bundle.WriteString("\n///MODULE parser\n")
	bundle.Write(parserSrc)
	bundle.WriteString("\n///MODULE checker\n")
	bundle.Write(checkerSrc)
	bundle.WriteString("\n///MODULE io\n")
	bundle.Write(ioSrc)
	bundle.WriteString("\n///MODULE main\n")
	bundle.WriteString(driverMod)

	checkerAsm := runCapture(t, gcc, runner, driverBin, bundle.Bytes())
	if len(checkerAsm) == 0 {
		t.Fatal("self-host compiler emitted 0 bytes for the codes driver")
	}
	checkerBin := buildBin(t, gcc, dir, "codes", string(checkerAsm))

	cases := []struct {
		name string
		src  string
		want []string // codes the self-host checker should print
	}{
		{"clean", "function main(): i32 { return 1 + 2; }\n", nil},
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
		// E043 (method-call on a numeric scalar): i32 / f64 carry no methods,
		// so any `x.m(...)` on one is a field access on a non-struct. Valid
		// string / array / struct method calls stay clean (no false positive).
		{"method-on-i32", "function main(): i32 { var x: i32 = 3; x.foo(); return 0; }\n", []string{"E043"}},
		{"method-on-f64", "function main(): i32 { var f: f64 = 1.0; f.foo(); return 0; }\n", []string{"E043"}},
		{"method-on-string-ok", "function main(): i32 { var s: string = \"a\"; return s.len(); }\n", nil},
		{"method-on-i32-user-method-ok", "function (n: i32) twice(): i32 { return n * 2; }\nfunction main(): i32 { var x: i32 = 21; return x.twice(); }\n", nil},
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
		{"return-no-value-void-ok", "function f() { return; }\nfunction main(): i32 { return 0; }\n", nil},
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
		{"slice-low-non-i32", "function main(): i32 { var s: string = \"hello\"; var t: string = s[\"x\":3]; return 0; }\n", []string{"E037"}},
		{"slice-high-non-i32", "function main(): i32 { var s: string = \"hello\"; var t: string = s[1:\"y\"]; return 0; }\n", []string{"E037"}},
		{"slice-bounds-ok", "function main(): i32 { var s: string = \"hello\"; var t: string = s[1:3]; return 0; }\n", nil},
		{"slice-full-ok", "function main(): i32 { var s: string = \"hello\"; var t: string = s[:]; return 0; }\n", nil},
		{"tuple-field-non-numeric", "function main(): i32 { var t = (1, 2); return t.foo; }\n", []string{"E046"}},
		{"tuple-field-out-of-range", "function main(): i32 { var t = (1, 2); return t.5; }\n", []string{"E046"}},
		{"tuple-field-ok", "function main(): i32 { var t = (1, 2); return t.0; }\n", nil},
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
		{"variant-multi-enum-ref", "enum A { X, Y }\nenum B { X, Z }\nfunction main(): i32 { var a: A = X; return 0; }\n", []string{"E036"}},
		{"variant-multi-enum-unref-ok", "enum A { X, Y }\nenum B { X, Z }\nfunction main(): i32 { return 0; }\n", nil},
		{"variant-disjoint-ref-ok", "enum A { P, Q }\nenum B { R, S }\nfunction main(): i32 { var a: A = P; return 0; }\n", nil},
		{"match-wildcard-not-last", "enum Opt { Has(i32), Nil }\nfunction main(): i32 { var o: Opt = Nil; match (o) { _ => { return 0; }, Has(n) => { return n; } } }\n", []string{"E026"}},
		{"match-variant-twice", "enum Opt { Has(i32), Nil }\nfunction main(): i32 { var o: Opt = Nil; match (o) { Has(n) => { return n; }, Has(m) => { return m; }, Nil => { return 0; } } }\n", []string{"E028"}},
		{"match-clean-ok", "enum Opt { Has(i32), Nil }\nfunction main(): i32 { var o: Opt = Nil; match (o) { Has(n) => { return n; }, Nil => { return 0; } } }\n", nil},
		{"match-wildcard-last-ok", "enum Opt { Has(i32), Nil }\nfunction main(): i32 { var o: Opt = Nil; match (o) { Has(n) => { return n; }, _ => { return 0; } } }\n", nil},
		{"type-arity-param", "struct Box[T] { v: T }\nfunction f(b: Box[i32, i32]): i32 { return 0; }\nfunction main(): i32 { return 0; }\n", []string{"E019"}},
		{"type-arity-field", "struct Box[T] { v: T }\nstruct W { b: Box[i32, i32] }\nfunction main(): i32 { return 0; }\n", []string{"E019"}},
		{"type-arity-param-ok", "struct Box[T] { v: T }\nfunction f(b: Box[i32]): i32 { return 0; }\nfunction main(): i32 { return 0; }\n", nil},
		{"array-elem-string-in-i32", "function main(): i32 { var a = [1, \"x\", 3]; return 0; }\n", []string{"E034"}},
		{"array-elem-i32-in-string", "function main(): i32 { var a = [\"a\", 1]; return 0; }\n", []string{"E034"}},
		{"array-elem-homogeneous-i32-ok", "function main(): i32 { var a = [1, 2, 3]; return a[0]; }\n", nil},
		{"array-elem-homogeneous-string-ok", "function main(): i32 { var a = [\"p\", \"q\"]; return 0; }\n", nil},
		// E034 (index variant): an array / string index must be an i32.
		{"index-string", "function main(): i32 { var a = [1, 2, 3]; return a[\"x\"]; }\n", []string{"E034"}},
		{"index-string-on-string", "function main(): i32 { var s = \"abc\"; return s[\"x\"]; }\n", []string{"E034"}},
		{"index-bool", "function main(): i32 { var a = [1, 2, 3]; var b = true; return a[b]; }\n", []string{"E034"}},
		{"index-i32-ok", "function main(): i32 { var a = [1, 2, 3]; var i = 1; return a[i]; }\n", nil},
		// E034 / E037 (non-array/string source): indexing or slicing a value
		// that isn't an array or string. Arrays / strings stay ok.
		{"index-non-array", "function main(): i32 { var x = 5; return x[0]; }\n", []string{"E034"}},
		{"index-struct", "struct P { x: i32 }\nfunction main(): i32 { var p = P { x: 1 }; return p[0]; }\n", []string{"E034"}},
		{"slice-non-array", "function main(): i32 { var x = 5; var y = x[1:2]; return 0; }\n", []string{"E037"}},
		{"index-string-source-ok", "function main(): i32 { var s = \"ab\"; return s[0]; }\n", nil},
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
		{"return-if-else-ok", "function f(c: boolean): i32 { if (c) { return 1; } else { return 2; } }\nfunction main(): i32 { return 0; }\n", nil},
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
		{"cast-i32-to-string-ok", "function main(): i32 { var x: i32 = 1; var s: string = x as string; return 0; }\n", nil},
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
		// E002 inside lambda bodies: a lambda's `return` is checked against
		// the lambda's OWN declared return type, not the enclosing function's
		// (ret_diags stops at the lambda boundary). lret_stmts/lret_expr fill
		// that gap.
		{"lambda-ret-mismatch", "function main(): i32 { var f = function(): i32 { return \"x\"; }; return f(); }\n", []string{"E002"}},
		{"lambda-ret-ok", "function main(): i32 { var f = function(): i32 { return 5; }; return f(); }\n", nil},
		{"lambda-in-void-fn", "function g() { var f = function(): i32 { return \"x\"; }; }\nfunction main(): i32 { return 0; }\n", []string{"E002"}},
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
		// E025: `switch` keeps a real StmtSwitch node through the checker (it
		// desugars to an if-chain only at emit). A float scrutinee, or a case
		// value whose type isn't equal to the scrutinee's, is E025.
		{"e025-switch-float", "function main(): i32 { var f: f64 = 1.0; switch (f) { case 1.0: return 1; default: return 0; } }\n", []string{"E025"}},
		{"e025-switch-i32-ok", "function main(): i32 { var n: i32 = 1; switch (n) { case 1: return 1; default: return 0; } }\n", nil},
		{"e025-switch-case-type", "function main(): i32 { var n: i32 = 1; switch (n) { case \"x\": return 1; default: return 0; } }\n", []string{"E025"}},
		{"e025-switch-string-ok", "function main(): i32 { var s: string = \"a\"; switch (s) { case \"a\": return 1; default: return 0; } }\n", nil},
		{"e025-switch-multi-ok", "function main(): i32 { var n: i32 = 1; switch (n) { case 1, 2, 3: return 1; default: return 0; } }\n", nil},
		// E052 interaction: a switch exits only with a default whose arms all
		// return; without a default the function can fall through.
		{"e025-switch-no-default-e052", "function f(x: i32): i32 { switch (x) { case 1: return 1; } }\nfunction main(): i32 { return f(1); }\n", []string{"E052"}},
		{"e025-switch-all-return-ok", "function f(x: i32): i32 { switch (x) { case 1: return 1; default: return 0; } }\nfunction main(): i32 { return f(1); }\n", nil},
		// Switch-body diagnostics must still be caught (the body-walking passes
		// recurse into case / default bodies).
		{"e025-switch-body-undefined", "function main(): i32 { var n: i32 = 1; switch (n) { case 1: return undef; default: return 0; } }\n", []string{"E001"}},
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
		{"iflet-enum-ok", "enum O { Has(i32), Nil }\nfunction main(): i32 { var o: O = Nil; if let Has(v) = o { return v; } return 0; }\n", nil},
		{"letelse-enum-ok", "enum O { Has(i32), Nil }\nfunction main(): i32 { var o: O = Nil; let Has(v) = o else { return 0; }; return v; }\n", nil},
		{"iflet-bad-variant", "enum O { Has(i32), Nil }\nfunction main(): i32 { var o: O = Nil; if let Bogus(v) = o { return 0; } return 0; }\n", []string{"E014"}},
		{"iflet-bad-arity", "enum O { Has(i32), Nil }\nfunction main(): i32 { var o: O = Nil; if let Has(a, b) = o { return 0; } return 0; }\n", []string{"E015"}},
		// E057: `cell_new(v)` constructs a Cell[T]; T must be cycle-free —
		// a scalar (i32/i64/f64/bool) or string. A composite / reference
		// argument (struct, array, tuple, another cell) is E057, reported
		// at the argument. The Go checker's type-annotation form
		// (`Cell[T]` in a field/param) reports E057 at the synthesised Cell
		// builtin decl (position 0:0), which diag.Format renders without a
		// code, so the differential keys off the value form (cell_new),
		// whose diagnostic carries the argument position. Cross-checked
		// against the Go checker.
		{"cellnew-i32-ok", "function main(): i32 { var c = cell_new(5); return 0; }\n", nil},
		{"cellnew-string-ok", "function main(): i32 { var c = cell_new(\"x\"); return 0; }\n", nil},
		{"cellnew-bool-ok", "function main(): i32 { var c = cell_new(1 < 2); return 0; }\n", nil},
		{"cellnew-struct-bad", "struct P { x: i32 }\nfunction main(): i32 { var p: P = P { x: 1 }; var c = cell_new(p); return 0; }\n", []string{"E057"}},
		{"cellnew-array-bad", "function main(): i32 { var a: i32[] = [1]; var c = cell_new(a); return 0; }\n", []string{"E057"}},
		{"cellnew-tuple-bad", "function main(): i32 { var t = (1, 2); var c = cell_new(t); return 0; }\n", []string{"E057"}},
		{"cellnew-nested-bad", "function main(): i32 { var c = cell_new(cell_new(5)); return 0; }\n", []string{"E057"}},
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
			// checker reports for the same source, restricted to the
			// codes implemented so far.
			goCodes := filterImplemented(goCheckerCodes(t, dir, tc.src))
			if !equalStrings(got, goCodes) {
				t.Errorf("%s: self-host codes %v disagree with Go checker %v (implemented subset)", tc.name, got, goCodes)
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
