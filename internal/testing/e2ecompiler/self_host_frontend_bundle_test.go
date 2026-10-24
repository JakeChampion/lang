package e2ecompiler

import (
	"os/exec"
	"testing"
)

func TestSelfHostFrontendBundleX86_64(t *testing.T) {
	gcc, runner, driverBin := buildModloadDriverX86(t)

	entry := "import \"./lexer\";\n" +
		"import \"./parser\";\n" +
		"function main(): i32 {\n" +
		"    let toks: lexer.Token[] = lexer.tokenize(\"function f(): i32 { return 42; }\");\n" +
		"    let mod: parser.Module = parser.parse_module(toks);\n" +
		"    return mod.funcs.len();\n" +
		"}\n"

	mergedAsm, progDir := compileFilesModload(t, runner, driverBin, map[string]string{"main.fern": entry})
	if len(mergedAsm) == 0 {
		t.Fatal("driver emitted 0 bytes for the frontend bundle")
	}
	t.Logf("merged frontend asm: %d bytes", len(mergedAsm))

	assertNoDanglingLocalLabels(t, "frontend bundle merged asm", []byte(mergedAsm))
	assertNoDuplicateLocalLabels(t, "frontend bundle merged asm", []byte(mergedAsm))

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
