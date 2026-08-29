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

	// Stage the front-end closure once; every case reuses it, differing only
	// in the embedded source of the generated main.fern.
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
