package e2e

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestSelfHostRangeReachesIR guards that `for i in LO..HI` range-loops actually
// lower through the IR path, not the AST fallback. There is no lifted symbol to
// look for (range desugars to an ordinary while/if/break shape that the IR and
// AST backends emit identically), so the genuine-IR signal is eligibility:
// irlower.lift_lambdas desugars the range to a counting while-loop, which makes
// the module pass asm_ir.all_eligible — and the asm_ir_run driver only calls
// emit_module_ir (the IR path) when all_eligible is true. If the desugar were
// only in module_with_builtins (the AST path) and NOT in lift_lambdas, a
// range-for program would stay an unrecognised StmtFor iter, fail IR
// eligibility, and silently ride the AST fallback (the #2759 trap).
//
// The probe driver reports asm_ir.all_eligible(lift_lambdas(parse(stdin))) so we
// can assert a range program is IR-eligible directly, and that the probe
// discriminates (a builtin-calling program is NOT eligible).
func TestSelfHostRangeReachesIR(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	for _, name := range []string{"util.fern", "astwalk.fern", "asmcore.fern", "lexer.fern", "parser.fern", "ir.fern", "irlower.fern", "asm_ir.fern", "asm.fern"} {
		src, err := os.ReadFile(filepath.Join("../../examples/self_host", name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		if err := os.WriteFile(filepath.Join(dir, name), src, 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	// Probe: print "1" if the stdin program is IR-eligible, "0" otherwise.
	probe := `import "std/io";
import "./lexer";
import "./parser";
import "./asm_ir";

function main(): i32 {
  var src: string = io.read_all_stdin();
  var mod: parser.Module = parser.parse_module(lexer.tokenize(src));
  var lm: parser.Module = asm_ir.lift_lambdas(mod);
  if (asm_ir.all_eligible(lm)) { write("1"); } else { write("0"); }
  return 0;
}
`
	if err := os.WriteFile(filepath.Join(dir, "range_probe.fern"), []byte(probe), 0o644); err != nil {
		t.Fatalf("write probe: %v", err)
	}
	probeBin := buildSelfHostBin(t, gcc, dir, "range_probe.fern", "range_probe")

	run := func(src string) string {
		var cmd *exec.Cmd
		if len(runner) == 0 {
			cmd = exec.Command(probeBin)
		} else {
			cmd = exec.Command(runner[0], append(append([]string{}, runner[1:]...), probeBin)...)
		}
		cmd.Stdin = bytes.NewReader([]byte(src))
		out, err := cmd.Output()
		if err != nil {
			t.Fatalf("probe failed for %q: %v", src, err)
		}
		return strings.TrimSpace(string(out))
	}

	// A range-for program must be IR-eligible — i.e. genuinely reach the IR path.
	if got := run(`function main(): i32 { var t = 0; for i in 0..5 { t = t + i; } return t; }`); got != "1" {
		t.Errorf("range-for program is not IR-eligible (got %q): it would silently ride the AST fallback", got)
	}
	// Range with break/continue must also stay IR-eligible (the increment is at
	// the top of the desugared loop, so both work without bailing).
	if got := run(`function main(): i32 { var t = 0; for i in 0..10 { if (i == 5) { break; } t = t + i; } return t; }`); got != "1" {
		t.Errorf("range-for with break is not IR-eligible (got %q)", got)
	}
	// Sanity: the probe discriminates — a builtin-calling program is NOT eligible
	// (print has no __fn_ body in the IR path), so a "1" everywhere would be a
	// broken probe rather than proof.
	if got := run(`function main(): i32 { print("hi"); return 0; }`); got != "0" {
		t.Errorf("probe does not discriminate: a builtin-calling program reported eligible (got %q)", got)
	}
}
