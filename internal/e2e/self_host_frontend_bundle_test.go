package e2e

import (
	"os"
	"os/exec"
	"testing"
)

// Real-frontend self-host milestone: bundle the ACTUAL lexer.fern +
// parser.fern (the compiler's own front end — both pure, importing
// nothing and using byte builtins, no stdlib) with a
// tiny entry that lexes + parses an embedded program and returns its
// function count. The bundle is fed through bundle_run; the merged
// asm is assembled + run and must return the function count (1).
//
// This exercises the full self-host pipeline on real compiler source:
// flatten + mangle + merge (flatten.fern) and lowering a program that
// uses cross-module structs with string / array fields (Lex { src:
// string, … }, Par { toks: lexer.Token[], … }) — the case that
// required the infer_expr_type struct-field-read fix in asm.fern.
func TestSelfHostFrontendBundleX86_64(t *testing.T) {
	gcc, runner, driverBin := buildModloadDriverX86(t)

	lexerSrc, _ := os.ReadFile("../../examples/self_host/lexer.fern")
	parserSrc, _ := os.ReadFile("../../examples/self_host/parser.fern")
	utilSrc, _ := os.ReadFile("../../examples/self_host/util.fern")
	entry := "import \"./lexer\";\n" +
		"import \"./parser\";\n" +
		"function main(): i32 {\n" +
		"    var toks: lexer.Token[] = lexer.tokenize(\"function f(): i32 { return 42; }\");\n" +
		"    var mod: parser.Module = parser.parse_module(toks);\n" +
		"    return mod.funcs.len();\n" +
		"}\n"

	// The loader follows main's ./lexer + ./parser imports (parser pulls
	// in lexer + util) and merges them — the file-based equivalent of the
	// hand-built ///MODULE util+lexer+parser bundle.
	mergedAsm, progDir := compileFilesModload(t, runner, driverBin, map[string]string{
		"util.fern":   string(utilSrc),
		"lexer.fern":  string(lexerSrc),
		"parser.fern": string(parserSrc),
		"main.fern":   entry,
	})
	if len(mergedAsm) == 0 {
		t.Fatal("driver emitted 0 bytes for the frontend bundle")
	}
	t.Logf("merged frontend asm: %d bytes", len(mergedAsm))

	mergedBin := buildBin(t, gcc, progDir, "merged", mergedAsm)
	var mcmd *exec.Cmd
	if len(runner) == 0 {
		mcmd = exec.Command(mergedBin)
	} else {
		mcmd = exec.Command(runner[0], append(runner[1:], mergedBin)...)
	}
	_, _ = mcmd.CombinedOutput()
	if code := mcmd.ProcessState.ExitCode(); code != 1 {
		t.Errorf("bundled lexer+parser frontend returned %d, want 1 (one function parsed)", code)
	}
}
