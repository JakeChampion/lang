package e2ecompiler

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/syntax/parser"
)

// parseErrPosProbe is a driver over the self-host front end that prints the
// parse diagnostics `asmcore.parse_unknown_errors_module` raises for a
// program embedded as a literal. The source is embedded rather than read from
// stdin so the probe needs no `std/io` import and so stages as a plain
// single-directory module set.
const parseErrPosProbe = `import "./lexer";
import "./parser";
import "./asmcore";
import "./util";
function main(): i32 {
    let src: string = %s;
    let mod: parser.Module = parser.parse_module(lexer.tokenize(src));
    print(util.format_diags(asmcore.parse_unknown_errors_module(mod)));
    return 0;
}
`

// selfHostDiagPos matches the code and trailing `(line:col)` that
// util.format_diags renders, and nativeDiagPos the `file:line:col:` prefix and
// code native's diagnostic printer renders.
var (
	selfHostDiagPos = regexp.MustCompile(`error\[(P\d+)\]: (?:in fn '[^']*': |at top level: )?(.*) \((\d+):(\d+)\)`)
	nativeDiagPos   = regexp.MustCompile(`:(\d+):(\d+): error\[(P\d+)\]: (.*)`)
)

// parseErrPosCases are programs whose FIRST parse error is a parser-side
// sentinel — the shapes that reach `parse_unknown_errors_module`. Each one is
// a construct the permissive self-host parser recovers from by planting an
// ExprUnknown or a StmtUnknown rather than by failing outright.
var parseErrPosCases = []struct {
	name string
	src  string
}{
	// An expression sentinel, planted by parse_primary's punct arm. This is
	// the recovery that advances PAST the offending token so parse_program
	// cannot spin, so it is the one case where reading the cursor at the
	// plant site names the token AFTER the mistake.
	{"missing-init-expression", "function main(): i32 {\n  let x: i32 = ;\n  return 0;\n}\n"},
	// A statement sentinel: parse_stmt gives up on the `if` header and plants
	// a StmtUnknown, which carried no position at all before #2849/SH-041.
	{"if-without-paren", "function main(): i32 {\n  if x > 1) { return 1; }\n  return 0;\n}\n"},
	// A sentinel planted from a nested position — the diagnostic has to
	// survive the walk down through the enclosing statement.
	{"nested-bad-punct", "function main(): i32 {\n  while (true) {\n    let y: i32 = @;\n  }\n  return 0;\n}\n"},
	// Assignment is a statement, so an `=` where an expression is read is the
	// token both parsers stop at.
	{"assignment-in-let-init", "function main(): i32 {\n  let r = 0;\n  let a = r = 5;\n  return a;\n}\n"},
	{"assignment-in-condition", "function main(): i32 {\n  let r = 0;\n  while ((r = r + 1) < 3) { }\n  return r;\n}\n"},
	// An f-string placeholder holds one expression; the first token left over
	// is the error (#12648).
	{"fstring-two-operands", "function main(): i32 {\n  let x = 5;\n  let y = 7;\n  let s = f\"[{x y}]\";\n  return 0;\n}\n"},
	{"fstring-comma-list", "function main(): i32 {\n  let x = 5;\n  let y = 7;\n  let s = f\"[{x, y}]\";\n  return 0;\n}\n"},
	{"fstring-stray-paren", "function main(): i32 {\n  let x = 5;\n  let s = f\"[{x)}]\";\n  return 0;\n}\n"},
	{"fstring-format-spec", "function main(): i32 {\n  let x = 5;\n  let s = f\"{x:04}\";\n  return 0;\n}\n"},
	{"fstring-nested-sentinel-then-trailing", "function main(): i32 {\n  let x = 5;\n  let s = f\"{x + @} {x y}\";\n  return 0;\n}\n"},
	{"fstring-placeholder-missing-type", "function main(): i32 {\n  let x = 5;\n  let s = f\"{(a: ) => a}\";\n  return 0;\n}\n"},
	// A `pub use` names its list as `.{a, b}`: each entry is a name followed by
	// `,` or `}`, and the list holds at least one (#12692).
	{"pub-use-without-list", "pub use \"std/i32\";\nfunction main(): i32 {\n  return 0;\n}\n"},
	{"pub-use-without-braces", "pub use \"std/i32\".to_string;\nfunction main(): i32 {\n  return 0;\n}\n"},
	{"pub-use-empty-list", "pub use \"std/i32\".{};\nfunction main(): i32 {\n  return 0;\n}\n"},
	{"pub-use-names-without-comma", "pub use \"./m\".{a b};\nfunction main(): i32 {\n  return 0;\n}\n"},
	{"pub-use-leading-comma", "pub use \"./m\".{,a};\nfunction main(): i32 {\n  return 0;\n}\n"},
	{"pub-use-list-at-eof", "pub use \"./m\".{a,"},
	{"pub-use-open-brace-at-eof", "pub use \"./m\".{"},
	{"pub-use-without-semicolon", "pub use \"./m\".{a}"},
	// Parse errors with a code of their own rather than P001 (#12689).
	{"compound-in-chain-lead", "function main(): i32 {\n  let a = 0; let b = 0; let c = 0;\n  a += b = 1;\n  return a;\n}\n"},
	{"compound-in-chain-tail", "function main(): i32 {\n  let a = 0; let b = 0; let c = 0;\n  a = b += 5;\n  return a;\n}\n"},
	{"compound-in-chain-double", "function main(): i32 {\n  let a = 0; let b = 0; let c = 0;\n  a += b += 5;\n  return a;\n}\n"},
	{"compound-in-chain-deep", "function main(): i32 {\n  let a = 0; let b = 0; let c = 0;\n  a = b = c += 2;\n  return a;\n}\n"},
	{"assign-to-call-p003", "function main(): i32 {\n  foo() = 1;\n  return 0;\n}\n"},
	{"assign-to-literal-p003", "function main(): i32 {\n  42 = 1;\n  return 0;\n}\n"},
	{"chained-assign-inner-call-p003", "function foo(): i32 {\n  return 1;\n}\nfunction main(): i32 {\n  foo() = foo() = 1;\n  return 0;\n}\n"},
	{"chained-assign-middle-call-p003", "function foo(): i32 {\n  return 1;\n}\nfunction main(): i32 {\n  let x = 0;\n  x = foo() = 1;\n  return x;\n}\n"},
	{"chained-assign-outer-call-p003", "function main(): i32 {\n  let x = 0;\n  (x + 1) = x = 1;\n  return x;\n}\n"},
	{"compound-assign-to-binary-p003", "function main(): i32 {\n  let a = 1;\n  (a + 1) += 5;\n  return 0;\n}\n"},
	{"two-pipe-holes-p004", "function add(a: i32, b: i32): i32 {\n  return a + b;\n}\nfunction main(): i32 {\n  5 |> add(_, _);\n  return 0;\n}\n"},
	{"function-expression-p006", "function main(): i32 {\n  let f = function (x: i32): i32 { return x + 1; };\n  return f(41);\n}\n"},
	// A `return` whose value is refused at a `;` is not a bare `return;`
	// (#12915), in a valued function or a void one.
	{"return-refused-paren", "function main(): i32 {\n  return (;\n}\n"},
	{"return-refused-paren-void", "function f(): void {\n  return (;\n}\nfunction main(): i32 {\n  f();\n  return 0;\n}\n"},
	{"return-refused-unary", "function main(): i32 {\n  return -;\n}\n"},
	{"return-refused-array", "function main(): i32 {\n  return [;\n}\n"},
	{"defer-refused-paren", "function main(): i32 {\n  defer (;\n  return 0;\n}\n"},
	{"errdefer-refused-paren", "function main(): i32 {\n  errdefer (;\n  return 0;\n}\n"},
	{"expr-stmt-refused-paren", "function main(): i32 {\n  (;\n  return 0;\n}\n"},
	{"lambda-return-refused-paren", "function main(): i32 {\n  let f = (): i32 => { return (; };\n  return f();\n}\n"},
	{"arrow-lambda-refused-body", "function main(): i32 {\n  let f = () => ;\n  return 0;\n}\n"},
	{"return-refused-fstring", "function main(): i32 {\n  return f\"{;}\";\n}\n"},
	// A refused element stands for the construct whose closer is then
	// missing: it is the earlier error.
	{"array-refused-element", "function main(): i32 {\n  let x = [1, ;\n  return 0;\n}\n"},
	{"array-refused-nested-element", "function main(): i32 {\n  let x = [1 + ;\n  return 0;\n}\n"},
	{"call-refused-arg", "function main(): i32 {\n  let x = f(1, ;\n  return 0;\n}\n"},
	{"call-refused-named-arg", "function main(): i32 {\n  let x = f(a = ;\n  return 0;\n}\n"},
	{"index-refused", "function main(): i32 {\n  let x = a[;\n  return 0;\n}\n"},
	{"slice-refused-high", "function main(): i32 {\n  let x = a[1:;\n  return 0;\n}\n"},
	{"slice-refused-leading-colon", "function main(): i32 {\n  let x = a[:;\n  return 0;\n}\n"},
	{"struct-lit-refused-field", "struct Foo { x: i32 }\nfunction main(): i32 {\n  let x = Foo { x: ;\n  return 0;\n}\n"},
	{"struct-update-refused-field", "struct Foo { x: i32 }\nfunction main(): i32 {\n  let a = Foo { x: 1 };\n  let x = Foo { ...a, x: ;\n  return 0;\n}\n"},
	{"struct-update-refused-base", "struct Foo { x: i32 }\nfunction main(): i32 {\n  let x = Foo { ...(; ;\n  return 0;\n}\n"},
	// A missing closer after a well-formed element is still its own error.
	{"struct-lit-missing-brace", "struct Foo { x: i32 }\nfunction main(): i32 {\n  let x = Foo { x: 1 ;\n  return 0;\n}\n"},
	// `float` and `()` are not type spellings, wherever a type is read.
	{"float-let-annotation", "function main(): i32 {\n  let x: float = 1.0;\n  return 0;\n}\n"},
	{"float-generic-arg", "function f(x: Option[float]): i32 {\n  return 0;\n}\nfunction main(): i32 {\n  return 0;\n}\n"},
	{"float-bound-arg", "function f[T: Iterator[float]](x: T): T {\n  return x;\n}\nfunction main(): i32 {\n  return 0;\n}\n"},
	{"unit-bound-arg", "function f[T: Iterator[()]](x: T): T {\n  return x;\n}\nfunction main(): i32 {\n  return 0;\n}\n"},
	{"unit-return-type", "function f(): () {\n  return;\n}\nfunction main(): i32 {\n  return 0;\n}\n"},
	{"unit-generic-arg", "function f(): Result[(), i32] {\n  return Ok(0);\n}\nfunction main(): i32 {\n  return 0;\n}\n"},
	// `+` continues a `dyn` type only; a bound takes no associated-type pin.
	{"plus-outside-dyn-in-generic-arg", "trait A {}\nfunction f(x: Map[A + float, i32]): i32 {\n  return 0;\n}\nfunction main(): i32 {\n  return 0;\n}\n"},
	{"impl-names-float", "struct float { n: i32 }\nimpl float {\n  function get(): i32 { return 0; }\n}\nfunction main(): i32 {\n  return 0;\n}\n"},
	{"bound-assoc-pin", "trait Holder {\n  type Item;\n}\nfunction f[T: Holder[Item = i32]](x: T): i32 {\n  return 0;\n}\nfunction main(): i32 {\n  return 0;\n}\n"},
	{"array-missing-bracket", "function main(): i32 {\n  let x = [1 2];\n  return 0;\n}\n"},
	// A lambda's return annotation is followed by the lambda's own arrow, so
	// `() =>` there is the refused unit type, not a function type.
	{"lambda-return-unit-p001", "function main(): i32 {\n  let f = (x: i32): () => x;\n  return 0;\n}\n"},
	// `::` is only an associated-type projection; as a path it is a P001 (#12698).
	{"path-sep-call", "function main(): i32 {\n  return helpers::add5(10);\n}\n"},
	{"path-sep-const", "function main(): i32 {\n  let c = helpers::BONUS;\n  return c;\n}\n"},
	{"path-sep-type-keyword", "function main(): i32 {\n  let m = i32::MAX;\n  return 0;\n}\n"},
	{"path-sep-struct-literal", "function main(): i32 {\n  let b = m::Box { v: 1 };\n  return 0;\n}\n"},
	// An impl names a trait; `impl Type { … }` is a P001 at the type (#12699).
	{"inherent-impl", "struct P { x: i32 }\nimpl P {\n  function get(self: Self): i32 { return self.x; }\n}\nfunction main(): i32 {\n  return 0;\n}\n"},
	{"generic-inherent-impl", "struct B[T] { v: T }\nimpl[T] B[T] {\n  function get(self: Self): T { return self.v; }\n}\nfunction main(): i32 {\n  return 0;\n}\n"},
	// A type-parameter list ends at `,` or `]`, and a supertrait clause at the
	// trait's `{`: a `::` or any other token there is refused, not read on.
	{"bound-path-sep", "function f[T: cmp::Ord](x: T): i32 {\n  return 0;\n}\nfunction main(): i32 {\n  return 0;\n}\n"},
	{"type-param-stray-token", "function f[T x](x: T): i32 {\n  return 0;\n}\nfunction main(): i32 {\n  return 0;\n}\n"},
	{"supertrait-path-sep", "trait A : cmp::Ord {\n  function f(self: Self): i32;\n}\nfunction main(): i32 {\n  return 0;\n}\n"},
	{"trait-stray-token", "trait A x {\n  function f(self: Self): i32;\n}\nfunction main(): i32 {\n  return 0;\n}\n"},
	{"call-missing-paren", "function f(a: i32, b: i32): i32 {\n  return a;\n}\nfunction main(): i32 {\n  return f(1\n  2);\n}\n"},
	{"index-missing-bracket", "function main(): i32 {\n  let a = [1, 2];\n  return a[0;\n}\n"},
	{"slice-missing-bracket", "function main(): i32 {\n  let a = [1, 2];\n  let b = a[0:1;\n  return 0;\n}\n"},
	{"struct-update-missing-brace", "struct Foo { x: i32 }\nfunction main(): i32 {\n  let a = Foo { x: 1 };\n  let b = Foo { ...a x: 2 };\n  return 0;\n}\n"},
	{"field-name-missing-after-dot", "function main(): i32 {\n  let a = 1;\n  a.;\n  return 0;\n}\n"},
	{"let-name-without-equals", "function main(): i32 {\n  let x 1;\n  return 0;\n}\n"},
	{"let-annotated-without-equals", "function main(): i32 {\n  let x: i32 1;\n  return 0;\n}\n"},
	// `Name {` opens a struct literal wherever one may stand, whatever follows.
	{"struct-lit-field-name-missing", "struct P { a: i32 }\nfunction main(): i32 {\n  let p = P { 1 };\n  return 0;\n}\n"},
	{"struct-lit-colon-missing", "struct P { a: i32 }\nfunction main(): i32 {\n  let p = P { a 1 };\n  return 0;\n}\n"},
	// `for (` is C-style unless its `)` is followed by `in`.
	{"for-paren-without-in", "function main(): i32 {\n  for (x of [1]) { }\n  return 0;\n}\n"},
	// A refused return type stops the signature, and the body after it does
	// not leak to the top level; `Map {` is no literal where `{` opens a body.
	{"return-type-refused", "function f(): = {\n  return 0;\n}\nfunction main(): i32 {\n  return 0;\n}\n"},
	{"map-literal-in-if-let-source", "enum E { A(i32), B }\nfunction main(): i32 {\n  if let A(x) = Map { 1: 10 } { }\n  return 0;\n}\n"},
	{"for-paren-let-literal", "function main(): i32 {\n  for (let 1 in [1]) { }\n  return 0;\n}\n"},
}

// TestSelfHostParseErrorPositions pins SH-041 (tracking #2849): a
// parser-side sentinel carries the position of the token the parser gave up
// on, and the diagnostic raised for it reports that position rather than 0:0,
// under the code and in the words native gives the same program.
//
// It is a differential, not a golden: the expectation is native's own reported
// position for the same program, so a case cannot be "fixed" by writing down
// whatever the self-host currently prints. fern.fern's printer gates on
// `line > 0`, so a sentinel without a position named no source location.
//
// The probe runs under the native interpreter rather than a compiled driver:
// the subject is the front end's diagnostic data, which no backend touches, so
// there is nothing here that needs gcc or an emitted binary.
func TestSelfHostParseErrorPositions(t *testing.T) {
	fernBin := buildLangBinForInterp(t)
	stage := stageParseProbeTree(t)

	for _, tc := range parseErrPosCases {
		t.Run(tc.name, func(t *testing.T) {
			wantCode, wantLine, wantCol, wantMsg := nativeParsePos(t, fernBin, tc.src)

			probe := filepath.Join(stage, "main.fern")
			if err := os.WriteFile(probe, []byte(fmt.Sprintf(parseErrPosProbe, fernStringLit(tc.src))), 0o644); err != nil {
				t.Fatalf("write probe: %v", err)
			}
			var out, errb bytes.Buffer
			cmd := exec.Command(fernBin, "-interp", probe)
			cmd.Stdout, cmd.Stderr = &out, &errb
			if err := cmd.Run(); err != nil {
				t.Fatalf("probe failed: %v\nstdout: %s\nstderr: %s", err, out.String(), errb.String())
			}
			got := out.String()
			m := earliestSelfHostDiag(got)
			if m == nil {
				t.Fatalf("self-host raised no positioned parse diagnostic for this program\ngot: %q", got)
			}
			if m[1] != wantCode {
				t.Errorf("code %s, want native's %s\nself-host: %q", m[1], wantCode, got)
			}
			// A sentinel with no position renders as 0:0 here and the
			// compiler's printer drops the location line entirely.
			if m[3] == "0" {
				t.Errorf("self-host diagnostic has no position (line 0)\ngot: %q", got)
			}
			if m[3] != wantLine || m[4] != wantCol {
				t.Errorf("position %s:%s, want native's %s:%s\nself-host: %q", m[3], m[4], wantLine, wantCol, got)
			}
			if m[2] != wantMsg {
				t.Errorf("message %q, want native's %q", m[2], wantMsg)
			}
		})
	}
}

// earliestSelfHostDiag is the submatch of the diagnostic in out that comes
// first in the source, the one native stops at and the CLI prints first. The
// pre-check lists a function's expectations after its body's sentinels.
func earliestSelfHostDiag(out string) []string {
	var best []string
	bl, bc := 0, 0
	for _, m := range selfHostDiagPos.FindAllStringSubmatch(out, -1) {
		l, _ := strconv.Atoi(m[3])
		c, _ := strconv.Atoi(m[4])
		if best == nil || l < bl || l == bl && c < bc {
			best, bl, bc = m, l, c
		}
	}
	return best
}

// stageParseProbeTree stages the self-host front-end closure (lexer / parser /
// asmcore / util and their imports) into a temp dir the parse-probe mains are
// written into.
func stageParseProbeTree(t *testing.T) string {
	t.Helper()
	stage := t.TempDir()
	for _, root := range []string{"lexer.fern", "parser.fern", "asmcore.fern", "util.fern"} {
		for _, p := range selfHostImportClosure(t, "../../../compiler", root) {
			base := filepath.Base(p)
			if _, err := os.Stat(filepath.Join(stage, base)); err == nil {
				continue
			}
			src, err := os.ReadFile(p)
			if err != nil {
				t.Fatalf("read %s: %v", p, err)
			}
			if err := os.WriteFile(filepath.Join(stage, base), src, 0o644); err != nil {
				t.Fatalf("stage %s: %v", base, err)
			}
		}
	}
	return stage
}

// TestSelfHostParseUnknownDiagSequence pins the FULL output — order,
// multiplicity, message text, position — of the P001/P002 pre-check
// (asmcore.parse_unknown_errors_module). No other gate can: the checker-codes
// drivers run checker.fern's check_module, which never calls asmcore, and the
// position test above compares only the first diagnostic's position.
//
// Every `want` was captured from the hand-written collectors BEFORE they
// folded onto astwalk (#6993), so the fold is held to reproducing the exact
// sequence rather than to whatever it happens to print.
//
// The essential rows are the bare-return ones. A bare `return;` parses to
// its own sentinel, which is exempt; the `punct:;` a refused expression
// leaves stays diagnosed wherever it lands — in an init slot, nested inside a
// return's value, or AS a return's whole value (`return (;`, #12915).
func TestSelfHostParseUnknownDiagSequence(t *testing.T) {
	fernBin := buildLangBinForInterp(t)
	stage := stageParseProbeTree(t)

	cases := []struct {
		name string
		src  string
		// want is the probe's exact stdout, one formatted diagnostic per
		// line, "" for a clean program.
		want string
	}{
		{"bare-return-clean",
			"function f(): void {\n  return;\n}\nfunction main(): i32 {\n  f();\n  return 0;\n}\n",
			""},
		{"var-init-sentinel",
			"function main(): i32 {\n  let x: i32 = ;\n  return 0;\n}\n",
			"error[P001]: in fn 'main': expected an expression, got \";\" (2:16)"},
		{"refused-return-value",
			"function f(): void {\n  return (;\n}\nfunction main(): i32 {\n  f();\n  return 0;\n}\n",
			"error[P001]: in fn 'f': expected an expression, got \";\" (2:11)"},
		{"refused-arrow-lambda-body",
			"function main(): i32 {\n  let f = () => ;\n  return 0;\n}\n",
			"error[P001]: in fn 'main': expected an expression, got \";\" (2:17)"},
		{"sentinel-nested-in-return",
			"function main(): i32 {\n  return 1 + ;\n}\n",
			"error[P001]: in fn 'main': expected an expression, got \";\" (2:14)"},
		// Functions in declaration order, then top-level statements LAST —
		// parse_unknown_errors_module's own loop order — with a multi-error
		// function reporting in source order. Count and order both pinned.
		{"return-type-refused-one-error",
			"function f(): = {\n  return 0;\n}\nfunction main(): i32 {\n  return 0;\n}\n",
			"error[P001]: in fn 'f': expected a type, got \"=\" (1:15)"},
		// A refused `for` header takes its body with it: `arr {` is not
		// re-read as a struct literal whose `}` closes the function.
		{"for-header-refused-one-error",
			"function main(): i32 {\n  let arr: i32[] = [1];\n  for in arr { let q = 1; }\n  return 0;\n}\n",
			"error[P001]: in fn 'main': expected \"in\" after the `for` pattern (3:10)"},
		{"multi-error-two-fns-and-toplevel",
			"let g: i32 = ;\nfunction a(): i32 {\n  let x: i32 = ;\n  let y: i32 = @;\n  return 0;\n}\nfunction b(): i32 {\n  if x > 1) { return 1; }\n  return 1 + ;\n}\nfunction main(): i32 {\n  return a() + b() + g;\n}\n",
			"error[P001]: in fn 'a': expected an expression, got \";\" (3:16)\n" +
				"error[P001]: in fn 'a': expected an expression, got \"@\" (4:16)\n" +
				"error[P001]: in fn 'b': expected an expression, got \";\" (9:14)\n" +
				"error[P001]: in fn 'b': expected \"(\", got \"x\" (8:6)\n" +
				"error[P001]: at top level: expected an expression, got \";\" (1:14)"},
		// A `pub use` that stops early reports where it stops and nothing more.
		{"pub-use-empty-list-without-semicolon",
			"pub use \"./m\".{}",
			"error[P001]: at top level: expected \";\", got the end of the file (1:17)"},
		// A `::` path parses through as a `.` chain, so its P001 is the only
		// diagnostic it draws (#12698).
		{"path-sep-chain",
			"function main(): i32 {\n  return helpers::add5(10).x;\n}\n",
			"error[P001]: in fn 'main': `::` is not a path separator; write `.` (2:17)"},
		// The refused impl's members draw nothing of their own.
		{"inherent-impl-alone",
			"struct P { x: i32 }\nimpl P {\n  function get(self: Self): i32 { return self.x; }\n}\nfunction main(): i32 {\n  return 0;\n}\n",
			"error[P001]: at top level: `impl P` needs a trait, as in `impl cmp.Eq for P`; declare a method as `function (self: P) name(…)` and a constructor as a plain function (2:6)"},
		// A generic type is named with its arguments, as written.
		{"generic-inherent-impl-message",
			"struct B[T] { v: T }\nimpl[T] B[T] {\n  function get(self: B[T]): T { return self.v; }\n}\nfunction main(): i32 {\n  return 0;\n}\n",
			"error[P001]: at top level: `impl B[T]` needs a trait, as in `impl cmp.Eq for B[T]`; declare a method as `function (self: B[T]) name(…)` and a constructor as a plain function (2:9)"},
		// A sentinel inside a defer's action: the walk descends StmtDefer.
		{"defer-action-sentinel",
			"function main(): i32 {\n  defer print(;);\n  return 0;\n}\n",
			"error[P001]: in fn 'main': expected an expression, got \";\" (2:15)"},
		// A sentinel inside a lambda body, reachable only through expression
		// descent. Column 37 is the sentinel `;` itself, which is where native
		// points and where every sibling row above points; the 42 this pinned
		// until the braced arrow-lambda body became a function body (#8593) was
		// inside the following `return`.
		{"lambda-body-sentinel",
			"function main(): i32 {\n  let f = (): i32 => { let z: i32 = ;; return 0; };\n  return f();\n}\n",
			"error[P001]: in fn 'main': expected an expression, got \";\" (2:37)"},
		// A sentinel in a parameter DEFAULT. Defaults hang off the
		// declaration, not off any statement, so neither the body walk nor the
		// top-level one reaches them: until #8739 the only thing refusing this
		// was E076's non-constant rule, which named a rule the author had not
		// broken. Reported under the declaring function's `where`, like a body
		// sentinel, because that is where the author has to go and edit.
		{"param-default-sentinel",
			"function f(x: i32 = ;): i32 {\n  return x;\n}\nfunction main(): i32 {\n  return 0;\n}\n",
			"error[P001]: in fn 'f': expected an expression, got \";\" (1:21)"},
		// Nested inside the default rather than being all of it, so the
		// fold is held to descending a default and not merely to testing its
		// root node.
		{"param-default-nested-sentinel",
			"function f(x: i32 = 1 + ;): i32 {\n  return x;\n}\nfunction main(): i32 {\n  return 0;\n}\n",
			"error[P001]: in fn 'f': expected an expression, got \";\" (1:25)"},
		// A default is not a body: the bare-`return;` exemption that
		// unknown_error_stmt_at arms must not leak across into one, and a
		// second parameter's default is reached as well as the first.
		{"param-default-two-params",
			"function f(a: i32 = 1, b: i32 = @): i32 {\n  return a + b;\n}\nfunction main(): i32 {\n  return 0;\n}\n",
			"error[P001]: in fn 'f': expected an expression, got \"@\" (1:33)"},
		// A clean default stays clean — the walk adds no diagnostic of its own.
		{"param-default-clean",
			"function f(x: i32 = 41): i32 {\n  return x;\n}\nfunction main(): i32 {\n  return f();\n}\n",
			""},
		// The one shape that maps to P002 rather than P001 (#6842).
		{"float-range-p002",
			"function main(): i32 {\n  let big: f64 = 1e999;\n  return 0;\n}\n",
			"error[P002]: in fn 'main': invalid float literal \"1e999\": value out of range (2:18)"},
		// A nameless declaration reports its P001 at the expected name.
		{"malformed-fn-decl",
			"function (): i32 {\n  return 1;\n}\nfunction main(): i32 {\n  return 0;\n}\n",
			"error[P001]: at top level: expected a name, got \"(\" (1:10)"},
		// The parse errors with a code of their own, worded as native words
		// them (#12689).
		{"own-code-p003-p004-p006",
			"function add(a: i32, b: i32): i32 {\n  return a + b;\n}\nfunction main(): i32 {\n  42 = 1;\n  5 |> add(_, _);\n  let f = function (x: i32): i32 { return x; };\n  return 0;\n}\n",
			"error[P003]: in fn 'main': left-hand side of assignment is not assignable (5:6)\n" +
				"error[P004]: in fn 'main': at most one `_` placeholder in a piped call (6:15)\n" +
				"error[P006]: in fn 'main': `function` introduces a named declaration; write an anonymous function as an arrow lambda — `(x: T): R => …` (7:11)"},
		// A placeholder with tokens left over is one P001 per f-string, at the
		// first leftover token of the first bad placeholder, as native reports
		// it (#12648); a later f-string is reported on its own.
		{"fstring-trailing-tokens-once-per-fstring",
			"function main(): i32 {\n  let x = 5;\n  let y = 7;\n  let s = f\"[{x y}] [{x, y}] [{x)}]\";\n  let t = f\"{x} {x:04}\";\n  return 0;\n}\n",
			"error[P001]: in fn 'main': f-string interpolation: unexpected trailing tokens after expression (4:17)\n" +
				"error[P001]: in fn 'main': f-string interpolation: unexpected trailing tokens after expression (5:19)"},
		// A type error inside a placeholder reaches the module's diagnostics
		// rather than being dropped with the placeholder's sub-parse.
		{"fstring-placeholder-type-error",
			"function main(): i32 {\n  let x = 5;\n  let s = f\"{x as}\";\n  return 0;\n}\n",
			"error[P001]: in fn 'main': expected a type, got the end of the file (3:18)"},
		// A placeholder that fails by a type error or by a sentinel inside it
		// also ends the f-string: its own error is the f-string's one
		// diagnostic, and a later bad placeholder adds nothing.
		{"fstring-type-error-placeholder-ends-fstring",
			"function main(): i32 {\n  let x = 5;\n  let s = f\"{x as} {x as}\";\n  let t = f\"{x as} {x y}\";\n  return 0;\n}\n",
			"error[P001]: in fn 'main': expected a type, got the end of the file (3:18)\n" +
				"error[P001]: in fn 'main': expected a type, got the end of the file (4:18)"},
		{"fstring-nested-sentinel-placeholder-ends-fstring",
			"function main(): i32 {\n  let x = 5;\n  let s = f\"{x + @} {x y}\";\n  return 0;\n}\n",
			"error[P001]: in fn 'main': expected an expression, got \"@\" (3:18)"},
		// Placeholders that are whole expressions stay clean: nested calls, a
		// method chain, string literals holding braces, and a nested f-string.
		{"fstring-placeholders-clean",
			"function main(): i32 {\n  let s = \"ab\";\n  let t = f\"{s.repeat(s.len()).repeat(2).len()} {\"}{\".len()} {\"a{b}c\".index_of(\"}\")} [{f\"{s}\"}]\";\n  return 0;\n}\n",
			""},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			probe := filepath.Join(stage, "main.fern")
			if err := os.WriteFile(probe, []byte(fmt.Sprintf(parseErrPosProbe, fernStringLit(tc.src))), 0o644); err != nil {
				t.Fatalf("write probe: %v", err)
			}
			var out, errb bytes.Buffer
			cmd := exec.Command(fernBin, "-interp", probe)
			cmd.Stdout, cmd.Stderr = &out, &errb
			if err := cmd.Run(); err != nil {
				t.Fatalf("probe failed: %v\nstdout: %s\nstderr: %s", err, out.String(), errb.String())
			}
			got := strings.TrimRight(out.String(), "\n")
			if got != tc.want {
				t.Errorf("diagnostic sequence:\n%s\nwant:\n%s\n"+
					"    A change here means the pre-check's emission order, count, text or\n"+
					"    position moved. Decide whether the new sequence is right, then move\n"+
					"    this row.", got, tc.want)
			}
		})
	}
}

// nativeParsePos returns the code, line and col of native's first parse
// diagnostic for src — the oracle each self-host diagnostic is compared against.
func nativeParsePos(t *testing.T, fernBin, src string) (code, line, col, msg string) {
	t.Helper()
	dir := t.TempDir()
	p := filepath.Join(dir, "prog.fern")
	if err := os.WriteFile(p, []byte(src), 0o644); err != nil {
		t.Fatalf("write native case: %v", err)
	}
	var errb bytes.Buffer
	cmd := exec.Command(fernBin, "-check", p)
	cmd.Stderr = &errb
	_ = cmd.Run()
	m := nativeDiagPos.FindStringSubmatch(errb.String())
	if m == nil {
		t.Fatalf("native raised no positioned parse diagnostic for this program, so it cannot serve as the oracle\nstderr: %s", errb.String())
	}
	return m[3], m[1], m[2], m[4]
}

// fernStringLit renders src as a Fern string literal for embedding in the
// probe. Only the escapes these cases need — the corpus is test-local.
func fernStringLit(src string) string {
	r := strings.NewReplacer(`\`, `\\`, `"`, `\"`, "\n", `\n`, "\t", `\t`)
	return `"` + r.Replace(src) + `"`
}

// TestSelfHostCheckParseGatesLeadWithPositionX86_64 pins how `-check` prints
// a parse-marker diagnostic: position first, `path:L:C: error[P00x]: …`, as every
// other gate and native print theirs, at native's position and under its code. The parse gates
// alone used to print it last, `error[P001]: … (L:C)`, which a reader of the
// other lines — or the language server, which publishes the same findings —
// did not expect.
func TestSelfHostCheckParseGatesLeadWithPositionX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "fern.fern")
	driver := buildSelfHostBin(t, gcc, dir, "fern.fern", "fern")
	fernBin := buildLangBinForInterp(t)
	lead := regexp.MustCompile(`^(?:[^:\s]+:)?(\d+):(\d+): error\[(P\d+)\]: `)
	for _, tc := range parseErrPosCases {
		t.Run(tc.name, func(t *testing.T) {
			wantCode, wantLine, wantCol, _ := nativeParsePos(t, fernBin, tc.src)
			p := filepath.Join(t.TempDir(), "prog.fern")
			if err := os.WriteFile(p, []byte(tc.src), 0o644); err != nil {
				t.Fatal(err)
			}
			var errb bytes.Buffer
			cmd := runX86_64Bin(runner, driver, "-check", p)
			cmd.Stderr = &errb
			if err := cmd.Run(); err == nil {
				t.Fatalf("self-host -check accepted the program")
			}
			m := lead.FindStringSubmatch(errb.String())
			if m == nil {
				t.Fatalf("-check did not lead with the position\nstderr: %q", errb.String())
			}
			if m[1] != wantLine || m[2] != wantCol || m[3] != wantCode {
				t.Errorf("%s at %s:%s, want native's %s at %s:%s\nstderr: %q", m[3], m[1], m[2], wantCode, wantLine, wantCol, errb.String())
			}
		})
	}
}

// TestSelfHostStatementTerminators: a simple statement or declaration ends
// with `;`, as spec/grammar.ebnf has it and the Go parser enforces. The
// self-host took the `;` as optional, so `break 5;` parsed as `break; 5;`
// (#11851). Each row is held to the Go parser's verdict, and a `return;`, a
// C-style `for` step and an expression refused at its `;` add no second error.
func TestSelfHostStatementTerminators(t *testing.T) {
	fernBin := buildLangBinForInterp(t)
	stage := stageParseProbeTree(t)

	cases := []struct {
		name string
		src  string
		want string
	}{
		{"break-with-value", "function main(): i32 { while (true) { break 5; } return 0; }\n",
			`error[P001]: in fn 'main': expected ";", got "5" (1:45)`},
		{"continue-with-value", "function main(): i32 { while (false) { continue 5; } return 0; }\n",
			`error[P001]: in fn 'main': expected ";", got "5" (1:49)`},
		{"break-before-brace", "function main(): i32 { while (true) { break } return 0; }\n",
			`error[P001]: in fn 'main': expected ";", got "}" (1:45)`},
		{"return-before-brace", "function main(): i32 { return 0 }\n",
			`error[P001]: in fn 'main': expected ";", got "}" (1:33)`},
		{"let", "function main(): i32 { let x: i32 = 1 return x; }\n",
			`error[P001]: in fn 'main': expected ";", got "return" (1:39)`},
		{"assign", "function main(): i32 { let x: i32 = 1; x = 2 return x; }\n",
			`error[P001]: in fn 'main': expected ";", got "return" (1:46)`},
		{"compound-assign", "function main(): i32 { let x: i32 = 1; x += 2 return x; }\n",
			`error[P001]: in fn 'main': expected ";", got "return" (1:47)`},
		{"compound-in-chain", "function main(): i32 { let a = 0; let b = 0; a = b += (1; 2); return a; }\n",
			`error[P001]: in fn 'main': a compound assignment stands alone: it cannot be part of an assignment chain (1:52)`},
		{"expression-statement", "function f(): void { }\nfunction main(): i32 { f() return 0; }\n",
			`error[P001]: in fn 'main': expected ";", got "return" (2:28)`},
		{"tuple-destructure", "function main(): i32 { let (a, b) = (1, 2) return a + b; }\n",
			`error[P001]: in fn 'main': expected ";", got "return" (1:44)`},
		{"const", "const K: i32 = 1\nfunction main(): i32 { return K; }\n",
			`error[P001]: at top level: expected ";", got "function" (2:1)`},
		{"import", "import \"std/i32\"\nfunction main(): i32 { return 0; }\n",
			`error[P001]: at top level: expected ";", got "function" (2:1)`},
		{"tuple-type-trailing-comma", "function main(): i32 { let o: Option[(i32, i32,)] = None; return 0; }\n",
			`error[P001]: in fn 'main': expected a type, got ")" (1:48)`},
		// A defer block is a block of statements like any other: each ends
		// with `;`, and a statement match needs no arm values.
		{"defer-block-trailing-assign", "function main(): i32 { let r: i32 = 0; defer { r = 2 } return r; }\n",
			`error[P001]: in fn 'main': expected ";", got "}" (1:54)`},
		{"defer-block-match-statement-clean", "function main(): i32 { let r: i32 = 0; let c: i32 = 1; defer { match (c) { 1 => { r = 2; }, _ => {} } } return r; }\n", ""},
		{"defer-block-trailing-let", "function main(): i32 { defer { let z: i32 = 1 } return 0; }\n",
			`error[P001]: in fn 'main': expected ";", got "}" (1:47)`},
		{"bare-return-clean", "function f(): void { return; }\nfunction main(): i32 { f(); return 0; }\n", ""},
		{"c-for-step-clean", "function main(): i32 { let t: i32 = 0; for (let i: i32 = 0; i < 3; i = i + 1) { t = t + i; } return t; }\n", ""},
		{"refused-expression-one-error", "function main(): i32 { let x: i32 = 1; return x +; }\n",
			"error[P001]: in fn 'main': expected an expression, got \";\" (1:50)"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, goErr := parser.Parse(tc.src)
			if (goErr != nil) != (tc.want != "") {
				t.Fatalf("the Go parser's verdict (%v) disagrees with the row", goErr)
			}
			probe := filepath.Join(stage, "main.fern")
			if err := os.WriteFile(probe, []byte(fmt.Sprintf(parseErrPosProbe, fernStringLit(tc.src))), 0o644); err != nil {
				t.Fatalf("write probe: %v", err)
			}
			out, err := exec.Command(fernBin, "-interp", probe).Output()
			if err != nil {
				t.Fatalf("probe failed: %v\nstdout: %s", err, out)
			}
			if got := strings.TrimSpace(string(out)); got != tc.want {
				t.Errorf("got  %q\nwant %q", got, tc.want)
			}
		})
	}
}
