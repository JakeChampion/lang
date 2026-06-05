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
	"E020": true, // empty array literal needs a type annotation
	"E004": true, // free-function call arity
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
	for _, name := range []string{"flatten.fern", "checker.fern", "bundle_run.fern"} {
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
		{"struct-complete-ok", "struct P { x: i32, y: i32 }\nfunction main(): i32 { var p: P = P { x: 1, y: 2 }; return p.x; }\n", nil},
		{"call-too-few-args", "function add(a: i32, b: i32): i32 { return a + b; }\nfunction main(): i32 { return add(1); }\n", []string{"E004"}},
		{"call-too-many-args", "function id(a: i32): i32 { return a; }\nfunction main(): i32 { return id(1, 2); }\n", []string{"E004"}},
		{"call-correct-arity-ok", "function add(a: i32, b: i32): i32 { return a + b; }\nfunction main(): i32 { return add(1, 2); }\n", nil},
		{"call-shadowed-local-ok", "function f(a: i32, b: i32): i32 { return a + b; }\nfunction main(): i32 { var f = function(x: i32): i32 { return x; }; return f(7); }\n", nil},
		{"var-annotation-mismatch", "function main(): i32 { var x: i32 = \"no\"; return x; }\n", []string{"E003"}},
		{"assign-mismatch", "function main(): i32 { var x: i32 = 1; x = \"no\"; return x; }\n", []string{"E003"}},
		{"assign-ok", "function main(): i32 { var x: i32 = 1; x = 2; return x; }\n", nil},
		{"arg-type-mismatch", "function add(a: i32, b: i32): i32 { return a + b; }\nfunction main(): i32 { return add(1, \"no\"); }\n", []string{"E038"}},
		{"arg-type-ok", "function add(a: i32, b: i32): i32 { return a + b; }\nfunction main(): i32 { return add(1, 2); }\n", nil},
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
		{"eq-i32-string", "function main(): i32 { if (1 == \"x\") { return 1; } return 0; }\n", []string{"E041"}},
		{"eq-bool-i32", "function main(): i32 { if ((1 < 2) == 3) { return 1; } return 0; }\n", []string{"E041"}},
		{"eq-i32-i32-ok", "function main(): i32 { if (1 == 2) { return 1; } return 0; }\n", nil},
		{"eq-string-string-ok", "function main(): i32 { if (\"a\" == \"b\") { return 1; } return 0; }\n", nil},
		{"field-unknown", "struct P { x: i32 }\nfunction main(): i32 { var p: P = P { x: 1 }; return p.y; }\n", []string{"E043"}},
		{"field-known-ok", "struct P { x: i32 }\nfunction main(): i32 { var p: P = P { x: 1 }; return p.x; }\n", nil},
		{"method-call-not-field-ok", "struct P { x: i32 }\nfunction (p: P) getx(): i32 { return p.x; }\nfunction main(): i32 { var p: P = P { x: 1 }; return p.getx(); }\n", nil},
		{"field-nested-unknown", "struct Q { a: i32 }\nstruct P { q: Q }\nfunction main(): i32 { var p: P = P { q: Q { a: 1 } }; return p.q.z; }\n", []string{"E043"}},
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
