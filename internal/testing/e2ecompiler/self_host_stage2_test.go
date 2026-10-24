package e2ecompiler

import (
	"os/exec"
	"testing"
)

func TestSelfHostStage2Bootstrap(t *testing.T) {
	gcc, runner, driverBin := buildModloadDriverX86(t)

	entry := "import \"./lexer\";\n" +
		"import \"./parser\";\n" +
		"import \"./asm_ir\";\n" +
		"import \"./semlower\";\n" +
		"function main(): i32 {\n" +
		"    let src: string = \"function main(): i32 { return 7; }\";\n" +
		"    let d: semlower.Driven = semlower.driven(parser.parse_module(lexer.tokenize(src)), \"x86-64-linux\");\n" +
		"    let out: string = asm_ir.emit_module_or_error_sub(d.full, d.sub, 0 as usize);\n" +
		"    print(out);\n" +
		"    return 0;\n" +
		"}\n"
	files := map[string]string{"main.fern": entry}
	compilerAsm, progDir := compileFilesModload(t, runner, driverBin, files)
	if len(compilerAsm) == 0 {
		t.Fatal("stage 1: produced 0 bytes for the self-hosted compiler")
	}
	t.Logf("stage 1: self-hosted compiler asm = %d bytes", len(compilerAsm))
	compilerBin := buildBin(t, gcc, progDir, "selfcc", compilerAsm)

	stage2Asm := runCapture(t, gcc, runner, compilerBin, nil)
	if len(stage2Asm) == 0 {
		t.Fatal("stage 2: self-hosted compiler produced 0 bytes")
	}
	t.Logf("stage 2: emitted program asm = %d bytes", len(stage2Asm))
	stage2Bin := buildBin(t, gcc, progDir, "prog", string(stage2Asm))
	var pcmd *exec.Cmd
	if len(runner) == 0 {
		pcmd = exec.Command(stage2Bin)
	} else {
		pcmd = exec.Command(runner[0], append(runner[1:], stage2Bin)...)
	}
	_, _ = pcmd.CombinedOutput()
	if code := pcmd.ProcessState.ExitCode(); code != 7 {
		t.Errorf("stage 2: self-hosted-compiled program exited %d, want 7", code)
	}
}
