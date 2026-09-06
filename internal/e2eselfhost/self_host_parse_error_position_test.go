package e2eselfhost

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// parseErrPosProbe is a driver over the self-host front end that prints the
// P001/P002 diagnostics `asmcore.parse_unknown_errors_module` raises for a
// program embedded as a literal. The source is embedded rather than read from
// stdin so the probe needs no `std/io` import and so stages as a plain
// single-directory module set.
const parseErrPosProbe = `import "./lexer";
import "./parser";
import "./asmcore";
import "./util";
function main(): i32 {
    var src: string = %s;
    var mod: parser.Module = parser.parse_module(lexer.tokenize(src));
    print(util.format_diags(asmcore.parse_unknown_errors_module(mod)));
    return 0;
}
`

// diagPos matches the trailing `(line:col)` that util.format_diags renders,
// and the `file:line:col:` prefix native's diagnostic printer renders.
var (
	selfHostDiagPos = regexp.MustCompile(`\((\d+):(\d+)\)`)
	nativeDiagPos   = regexp.MustCompile(`:(\d+):(\d+): error\[P00[12]\]`)
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
	{"missing-init-expression", "function main(): i32 {\n  var x: i32 = ;\n  return 0;\n}\n"},
	// A statement sentinel: parse_stmt gives up on the `if` header and plants
	// a StmtUnknown, which carried no position at all before #2849/SH-041.
	{"if-without-paren", "function main(): i32 {\n  if x > 1) { return 1; }\n  return 0;\n}\n"},
	// A sentinel planted from a nested position — the diagnostic has to
	// survive the walk down through the enclosing statement.
	{"nested-bad-punct", "function main(): i32 {\n  while (true) {\n    var y: i32 = @;\n  }\n  return 0;\n}\n"},
}

// TestSelfHostParseErrorPositions pins SH-041 (tracking #2849): a
// parser-side sentinel carries the position of the token the parser gave up
// on, and the P001/P002 raised for it reports that position rather than 0:0.
//
// It is a differential, not a golden: the expectation is native's own reported
// position for the same program, so a case cannot be "fixed" by writing down
// whatever the self-host currently prints. The two engines already agreed on
// the diagnostic CODE here; only the position was missing, and
// fern.fern's printer gates on `line > 0` — so before this change every one of
// these diagnostics took the positionless branch and named no source location.
//
// The probe runs under the native interpreter rather than a compiled driver:
// the subject is the front end's diagnostic data, which no backend touches, so
// there is nothing here that needs gcc or an emitted binary.
func TestSelfHostParseErrorPositions(t *testing.T) {
	fernBin := buildLangBinForInterp(t)
	stage := stageParseProbeTree(t)

	for _, tc := range parseErrPosCases {
		t.Run(tc.name, func(t *testing.T) {
			wantLine, wantCol := nativeParsePos(t, fernBin, tc.src)

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
			if !strings.Contains(got, "error[P00") {
				t.Fatalf("self-host raised no P001/P002 for this program\ngot: %q", got)
			}
			m := selfHostDiagPos.FindStringSubmatch(got)
			if m == nil {
				t.Fatalf("no (line:col) in self-host diagnostic\ngot: %q", got)
			}
			// The whole point of the row: a sentinel with no position renders
			// as 0:0 here and the compiler's printer drops the location line
			// entirely.
			if m[1] == "0" {
				t.Errorf("self-host diagnostic has no position (line 0)\ngot: %q", got)
			}
			if m[1] != wantLine || m[2] != wantCol {
				t.Errorf("position %s:%s, want native's %s:%s\nself-host: %q", m[1], m[2], wantLine, wantCol, got)
			}
		})
	}
}

// stageParseProbeTree stages the self-host front-end closure (lexer / parser /
// asmcore / util and their imports) into a temp dir the parse-probe mains are
// written into.
func stageParseProbeTree(t *testing.T) string {
	t.Helper()
	stage := t.TempDir()
	for _, root := range []string{"lexer.fern", "parser.fern", "asmcore.fern", "util.fern"} {
		for _, p := range selfHostImportClosure(t, "../../examples/self_host", root) {
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
// The load-bearing rows are the punct:; pair. A bare `return;` parses to the
// sentinel ExprUnknown("punct:;") in the return's value slot and is exempt
// there — and ONLY there: `var x: i32 = ;` plants the SAME sentinel in an
// init slot and must stay diagnosed, and `return 1 + ;` plants it nested
// INSIDE the return's value where it must also stay diagnosed. A collector
// that exempts the sentinel by kind alone, position-independent, passes every
// other gate and silently swallows both.
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
			"function main(): i32 {\n  var x: i32 = ;\n  return 0;\n}\n",
			"error[P001]: in fn 'main': parser-side unknown: punct:; (2:16)"},
		{"sentinel-nested-in-return",
			"function main(): i32 {\n  return 1 + ;\n}\n",
			"error[P001]: in fn 'main': parser-side unknown: punct:; (2:14)"},
		// Functions in declaration order, then top-level statements LAST —
		// parse_unknown_errors_module's own loop order — with a multi-error
		// function reporting in source order. Count and order both pinned.
		{"multi-error-two-fns-and-toplevel",
			"var g: i32 = ;\nfunction a(): i32 {\n  var x: i32 = ;\n  var y: i32 = @;\n  return 0;\n}\nfunction b(): i32 {\n  if x > 1) { return 1; }\n  return 1 + ;\n}\nfunction main(): i32 {\n  return a() + b() + g;\n}\n",
			"error[P001]: in fn 'a': parser-side unknown: punct:; (3:16)\n" +
				"error[P001]: in fn 'a': parser-side unknown: punct:@ (4:16)\n" +
				"error[P001]: in fn 'b': parser-side unknown: stmt: missing ( in if (8:6)\n" +
				"error[P001]: in fn 'b': parser-side unknown: punct:) (8:11)\n" +
				"error[P001]: in fn 'b': parser-side unknown: punct:; (9:14)\n" +
				"error[P001]: at top level: parser-side unknown: punct:; (1:14)"},
		// A sentinel inside a defer's action: the walk descends StmtDefer.
		{"defer-action-sentinel",
			"function main(): i32 {\n  defer print(;);\n  return 0;\n}\n",
			"error[P001]: in fn 'main': parser-side unknown: punct:; (2:15)"},
		// A sentinel inside a lambda body, reachable only through expression
		// descent.
		{"lambda-body-sentinel",
			"function main(): i32 {\n  var f = (): i32 => { var z: i32 = ;; return 0; };\n  return f();\n}\n",
			"error[P001]: in fn 'main': parser-side unknown: punct:; (2:42)"},
		// The one shape that maps to P002 rather than P001 (#6842).
		{"float-range-p002",
			"function main(): i32 {\n  var big: f64 = 1e999;\n  return 0;\n}\n",
			"error[P002]: in fn 'main': invalid float literal \"1e999\": value out of range (2:18)"},
		// The nameless-function P001 raised by parse_unknown_errors_module
		// itself, before any walk, followed by the top-level residue.
		{"malformed-fn-decl",
			"(): i32 => {\n  return 1;\n}\nfunction main(): i32 {\n  return 0;\n}\n",
			"error[P001]: malformed function declaration (1:1)\n" +
				"error[P001]: at top level: parser-side unknown: punct:: (1:12)"},
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

// nativeParsePos returns the line and col of native's first P001/P002 for src
// — the oracle each self-host position is compared against.
func nativeParsePos(t *testing.T, fernBin, src string) (line, col string) {
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
		t.Fatalf("native raised no positioned P001/P002 for this program, so it cannot serve as the oracle\nstderr: %s", errb.String())
	}
	return m[1], m[2]
}

// fernStringLit renders src as a Fern string literal for embedding in the
// probe. Only the escapes these cases need — the corpus is test-local.
func fernStringLit(src string) string {
	r := strings.NewReplacer(`\`, `\\`, `"`, `\"`, "\n", `\n`, "\t", `\t`)
	return `"` + r.Replace(src) + `"`
}
