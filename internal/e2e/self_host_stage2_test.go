package e2e

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/jakechampion/lang/internal/checker"
	"github.com/jakechampion/lang/internal/codegen/x86_64"
	"github.com/jakechampion/lang/internal/constfold"
	"github.com/jakechampion/lang/internal/modload"
)

// TestSelfHostStage2Bootstrap is the capstone of the self-host effort:
// a genuine TWO-STAGE bootstrap.
//
//	stage 0: the Go compiler builds bundle_run (the multi-module
//	         self-host driver).
//	stage 1: bundle_run is fed the compiler's OWN front end + back end
//	         — lexer.fern + parser.fern + asm.fern — plus a small entry
//	         that lexes → parses → emits a program. flatten.bundle
//	         merges all four into one flat Module; asm.emit_module
//	         lowers it to x86-64 asm; gcc links it into a SELF-HOSTED
//	         COMPILER binary (every compiler stage now Fern-authored).
//	stage 2: that self-hosted compiler is run. It emits asm for its
//	         embedded program `function main(): i32 { return 7; }`,
//	         which is assembled, linked, and run — and must exit 7.
//
// So a compiler written entirely in Fern (lexer + parser + x86-64
// emitter), compiled through its own module-flattening pipeline,
// compiles a program to a correct working binary.
func TestSelfHostStage2Bootstrap(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	for _, name := range []string{"lexer.fern", "parser.fern", "flatten.fern", "asm.fern", "bundle_run.fern"} {
		src, err := os.ReadFile(filepath.Join("../../examples/self_host", name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		if err := os.WriteFile(filepath.Join(dir, name), src, 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}

	// stage 0: build bundle_run.
	prog, _, err := modload.Load(filepath.Join(dir, "bundle_run.fern"))
	if err != nil {
		t.Fatalf("modload: %v", err)
	}
	if err := constfold.Fold(prog); err != nil {
		t.Fatalf("constfold: %v", err)
	}
	info, err := checker.Check(prog)
	if err != nil {
		t.Fatalf("check: %v", err)
	}
	asm, err := x86_64.Emit(prog, info)
	if err != nil {
		t.Fatalf("emit: %v", err)
	}
	driverAsm := filepath.Join(dir, "driver.s")
	driverBin := filepath.Join(dir, "driver")
	if err := os.WriteFile(driverAsm, []byte(asm), 0o644); err != nil {
		t.Fatalf("write driver asm: %v", err)
	}
	if out, err := exec.Command(gcc, "-static", "-nostdlib", "-no-pie", driverAsm, "-o", driverBin).CombinedOutput(); err != nil {
		t.Fatalf("driver gcc: %v\n%s", err, out)
	}

	// stage 1: bundle lexer + parser + asm + an emitting entry into a
	// self-hosted compiler binary.
	lexerSrc, _ := os.ReadFile("../../examples/self_host/lexer.fern")
	parserSrc, _ := os.ReadFile("../../examples/self_host/parser.fern")
	asmSrc, _ := os.ReadFile("../../examples/self_host/asm.fern")
	entry := "import \"./lexer\";\n" +
		"import \"./parser\";\n" +
		"import \"./asm\";\n" +
		"function main(): i32 {\n" +
		"    var src: string = \"function main(): i32 { return 7; }\";\n" +
		"    var out: string = asm.emit_module(parser.parse_module(lexer.tokenize(src)));\n" +
		"    print(out);\n" +
		"    return 0;\n" +
		"}\n"

	var bundle bytes.Buffer
	bundle.WriteString("///MODULE lexer\n")
	bundle.Write(lexerSrc)
	bundle.WriteString("\n///MODULE parser\n")
	bundle.Write(parserSrc)
	bundle.WriteString("\n///MODULE asm\n")
	bundle.Write(asmSrc)
	bundle.WriteString("\n///MODULE main\n")
	bundle.WriteString(entry)

	compilerAsm := runCapture(t, gcc, runner, driverBin, bundle.Bytes())
	if len(compilerAsm) == 0 {
		t.Fatal("stage 1: bundler produced 0 bytes for the self-hosted compiler")
	}
	t.Logf("stage 1: self-hosted compiler asm = %d bytes", len(compilerAsm))

	compilerAsmPath := filepath.Join(dir, "selfcc.s")
	compilerBin := filepath.Join(dir, "selfcc")
	if err := os.WriteFile(compilerAsmPath, compilerAsm, 0o644); err != nil {
		t.Fatalf("write compiler asm: %v", err)
	}
	if out, err := exec.Command(gcc, "-static", "-nostdlib", "-no-pie", compilerAsmPath, "-o", compilerBin).CombinedOutput(); err != nil {
		t.Fatalf("stage 1: gcc on self-hosted compiler: %v\n%s", err, out)
	}

	// stage 2: run the self-hosted compiler; it emits asm for its
	// embedded `return 7` program.
	stage2Asm := runCapture(t, gcc, runner, compilerBin, nil)
	if len(stage2Asm) == 0 {
		t.Fatal("stage 2: self-hosted compiler produced 0 bytes")
	}
	t.Logf("stage 2: emitted program asm = %d bytes", len(stage2Asm))

	stage2AsmPath := filepath.Join(dir, "prog.s")
	stage2Bin := filepath.Join(dir, "prog")
	if err := os.WriteFile(stage2AsmPath, stage2Asm, 0o644); err != nil {
		t.Fatalf("write stage-2 asm: %v", err)
	}
	if out, err := exec.Command(gcc, "-static", "-nostdlib", "-no-pie", stage2AsmPath, "-o", stage2Bin).CombinedOutput(); err != nil {
		t.Fatalf("stage 2: gcc on emitted program: %v\n%s", err, out)
	}
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
func runCapture(t *testing.T, gcc string, runner []string, bin string, stdin []byte) []byte {
	t.Helper()
	var cmd *exec.Cmd
	if len(runner) == 0 {
		cmd = exec.Command(bin)
	} else {
		cmd = exec.Command(runner[0], append(runner[1:], bin)...)
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
