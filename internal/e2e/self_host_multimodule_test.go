package e2e

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/checker"
	"github.com/jakechampion/lang/internal/codegen/x86_64"
	"github.com/jakechampion/lang/internal/constfold"
	"github.com/jakechampion/lang/internal/modload"
)

// TestSelfHostMultiModuleCompiler builds a self-hosted compiler that
// understands IMPORTS, and uses it to compile a multi-module program.
//
// stage 1 bundles lexer + parser + asm + flatten + a driver (the
// bundle_run logic, reading stdin via the read_all_stdin builtin) into
// one self-hosted compiler binary. stage 2 feeds that compiler a
// marked TWO-module program — module `a` exporting `add`, an entry
// calling `a.add(19, 23)` — over stdin; the self-hosted compiler runs
// flatten.bundle internally to merge them, emits asm, and the result
// is assembled + run and must exit 42.
//
// This exercises flatten.fern compiled THROUGH the self-host pipeline
// — including its qualified struct-literal reconstruction
// (`parser.ExprBinary { … }`), the case that needed the parse_postfix
// qualified-struct-lit fix. (The full fixpoint — this compiler
// compiling its own ~460 KB source bundle — still faults at scale;
// tracked separately.)
func TestSelfHostMultiModuleCompiler(t *testing.T) {
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

	// stage 1: the self-hosted multi-module compiler. Its entry is
	// bundle_run's logic but reading stdin via the read_all_stdin
	// builtin (no std/io dependency, which the emitter can't lower).
	lexerSrc, _ := os.ReadFile("../../examples/self_host/lexer.fern")
	parserSrc, _ := os.ReadFile("../../examples/self_host/parser.fern")
	asmSrc, _ := os.ReadFile("../../examples/self_host/asm.fern")
	flattenMod, _ := os.ReadFile("../../examples/self_host/flatten.fern")
	bundleRun, _ := os.ReadFile("../../examples/self_host/bundle_run.fern")
	mmMain := strings.ReplaceAll(string(bundleRun), "import \"std/io\";", "")
	mmMain = strings.ReplaceAll(mmMain, "io.read_all_stdin()", "read_all_stdin()")

	var bundle bytes.Buffer
	bundle.WriteString("///MODULE lexer\n")
	bundle.Write(lexerSrc)
	bundle.WriteString("\n///MODULE parser\n")
	bundle.Write(parserSrc)
	bundle.WriteString("\n///MODULE asm\n")
	bundle.Write(asmSrc)
	bundle.WriteString("\n///MODULE flatten\n")
	bundle.Write(flattenMod)
	bundle.WriteString("\n///MODULE main\n")
	bundle.WriteString(mmMain)

	compilerAsm := runCapture(t, gcc, runner, driverBin, bundle.Bytes())
	if len(compilerAsm) == 0 {
		t.Fatal("stage 1: bundler produced 0 bytes for the multi-module compiler")
	}
	compilerAsmPath := filepath.Join(dir, "mmc.s")
	compilerBin := filepath.Join(dir, "mmc")
	if err := os.WriteFile(compilerAsmPath, compilerAsm, 0o644); err != nil {
		t.Fatalf("write compiler asm: %v", err)
	}
	if out, err := exec.Command(gcc, "-static", "-nostdlib", "-no-pie", compilerAsmPath, "-o", compilerBin).CombinedOutput(); err != nil {
		t.Fatalf("stage 1: gcc on multi-module compiler: %v\n%s", err, out)
	}
	t.Logf("stage 1: multi-module self-hosted compiler built (%d bytes asm)", len(compilerAsm))

	// stage 2: compile a 2-module program with the self-hosted compiler.
	prog2 := "///MODULE a\n" +
		"pub function add(x: i32, y: i32): i32 { return x + y; }\n" +
		"function main(): i32 { return 0; }\n" +
		"///MODULE main\n" +
		"import \"./a\";\n" +
		"function main(): i32 { return a.add(19, 23); }\n"
	progAsm := runCapture(t, gcc, runner, compilerBin, []byte(prog2))
	if len(progAsm) == 0 {
		t.Fatal("stage 2: multi-module compiler produced 0 bytes")
	}
	progAsmPath := filepath.Join(dir, "prog.s")
	progBin := filepath.Join(dir, "prog")
	if err := os.WriteFile(progAsmPath, progAsm, 0o644); err != nil {
		t.Fatalf("write prog asm: %v", err)
	}
	if out, err := exec.Command(gcc, "-static", "-nostdlib", "-no-pie", progAsmPath, "-o", progBin).CombinedOutput(); err != nil {
		t.Fatalf("stage 2: gcc on emitted program: %v\n%s", err, out)
	}
	var pcmd *exec.Cmd
	if len(runner) == 0 {
		pcmd = exec.Command(progBin)
	} else {
		pcmd = exec.Command(runner[0], append(runner[1:], progBin)...)
	}
	_, _ = pcmd.CombinedOutput()
	if code := pcmd.ProcessState.ExitCode(); code != 42 {
		t.Errorf("self-hosted multi-module compile of a.add(19,23) exited %d, want 42", code)
	}
}
