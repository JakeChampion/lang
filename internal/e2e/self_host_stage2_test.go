package e2e

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// TestSelfHostStage2Bootstrap is the capstone of the self-host effort:
// a genuine TWO-STAGE bootstrap.
//
//	stage 0: the Go compiler builds the file-based asm driver
//	         (asm_modload_run, via buildModloadDriverX86).
//	stage 1: that driver compiles the compiler's OWN front end + back end
//	         — lexer.fern + parser.fern + asm.fern (+ deps) — plus a small
//	         entry that lexes → parses → emits a program, loaded from FILES
//	         off disk (no ///MODULE bundle). asm.emit_module lowers the
//	         merged module to x86-64 asm; gcc links it into a SELF-HOSTED
//	         COMPILER binary (every compiler stage Fern-authored).
//	stage 2: that self-hosted compiler is run. It emits asm for its
//	         embedded program `function main(): i32 { return 7; }`,
//	         which is assembled, linked, and run — and must exit 7.
//
// So a compiler written entirely in Fern (lexer + parser + x86-64
// emitter), compiled through its own import-driven pipeline, compiles a
// program to a correct working binary.
func TestSelfHostStage2Bootstrap(t *testing.T) {
	gcc, runner, driverBin := buildModloadDriverX86(t)

	// The compiler's own front+back end plus a small entry that lexes →
	// parses → emits an embedded program, compiled from FILES by the
	// file-based asm driver.
	entry := "import \"./lexer\";\n" +
		"import \"./parser\";\n" +
		"import \"./asm\";\n" +
		"function main(): i32 {\n" +
		"    var src: string = \"function main(): i32 { return 7; }\";\n" +
		"    var out: string = asm.emit_module(parser.parse_module(lexer.tokenize(src)));\n" +
		"    print(out);\n" +
		"    return 0;\n" +
		"}\n"
	files := map[string]string{"main.fern": entry}
	for _, m := range []string{"util", "astwalk", "asmcore", "lexer", "parser", "ir", "irlower", "asm_ir", "asm"} {
		src, err := os.ReadFile(filepath.Join("../../examples/self_host", m+".fern"))
		if err != nil {
			t.Fatalf("read %s.fern: %v", m, err)
		}
		files[m+".fern"] = string(src)
	}
	compilerAsm, progDir := compileFilesModload(t, runner, driverBin, files)
	if len(compilerAsm) == 0 {
		t.Fatal("stage 1: produced 0 bytes for the self-hosted compiler")
	}
	t.Logf("stage 1: self-hosted compiler asm = %d bytes", len(compilerAsm))
	compilerBin := buildBin(t, gcc, progDir, "selfcc", compilerAsm)

	// stage 2: run the self-hosted compiler; it emits asm for its
	// embedded `return 7` program.
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

// runCapture runs bin (under the qemu runner if set), feeding stdin,
// and returns stdout.
// runCapture runs the built driver binary, pipes stdin in, and returns its
// stdout. extraArgs are appended after the binary — the arm64/darwin consumers
// pass "-target", "arm64" (etc.) to select the emit backend of the folded
// asm_run driver (#4398 part 1); x86 callers pass nothing and stay unchanged.
func runCapture(t *testing.T, gcc string, runner []string, bin string, stdin []byte, extraArgs ...string) []byte {
	t.Helper()
	var cmd *exec.Cmd
	if len(runner) == 0 {
		cmd = exec.Command(bin, extraArgs...)
	} else {
		args := append([]string{}, runner[1:]...)
		args = append(args, bin)
		args = append(args, extraArgs...)
		cmd = exec.Command(runner[0], args...)
	}
	if stdin != nil {
		cmd.Stdin = bytes.NewReader(stdin)
	}
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("run %s: %v", bin, err)
	}
	return out
}
