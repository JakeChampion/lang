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

// TestSelfHostFixpoint is the capstone: a compiler written entirely in
// Fern reaches a CONVERGENT self-hosting fixpoint.
//
//	stage 0: the Go compiler builds bundle_run (the multi-module
//	         driver).
//	stage 1: bundle_run bundles the compiler's OWN source — lexer +
//	         parser + asm + flatten + a stdin-reading driver — into
//	         mmc, a self-hosted multi-module compiler binary.
//	stage 2: mmc compiles its own source bundle → gen2.
//	stage 3: gen2 compiles its own source bundle → gen3.
//
// gen2 and gen3 are asserted BYTE-IDENTICAL — the self-hosted compiler
// reproduces itself exactly, the gold-standard bootstrap fixpoint. As
// a sanity check the produced compiler also compiles a separate
// 2-module program to a binary that exits 42.
//
// (mmc itself differs from gen2 by one trailing byte: mmc is emitted
// by the Go-built bundle_run, whose stdlib io.read_all_stdin / print
// differ by a trailing newline from the self-hosted driver's
// read_all_stdin builtin. From gen2 onward everything is self-hosted,
// so gen2 == gen3.)
func TestSelfHostFixpoint(t *testing.T) {
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

	// stage 0: build bundle_run with the Go compiler.
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
	driverBin := buildBin(t, gcc, dir, "driver", asm)

	// The compiler's own source as a marked multi-module bundle. The
	// driver module is bundle_run with std/io swapped for the
	// read_all_stdin builtin (the emitter can't lower std/io's Reader).
	lexerSrc, _ := os.ReadFile("../../examples/self_host/lexer.fern")
	parserSrc, _ := os.ReadFile("../../examples/self_host/parser.fern")
	asmSrc, _ := os.ReadFile("../../examples/self_host/asm.fern")
	flattenSrc, _ := os.ReadFile("../../examples/self_host/flatten.fern")
	bundleRun, _ := os.ReadFile("../../examples/self_host/bundle_run.fern")
	driverMod := strings.ReplaceAll(string(bundleRun), "import \"std/io\";", "")
	driverMod = strings.ReplaceAll(driverMod, "io.read_all_stdin()", "read_all_stdin()")

	var srcBundle bytes.Buffer
	srcBundle.WriteString("///MODULE lexer\n")
	srcBundle.Write(lexerSrc)
	srcBundle.WriteString("\n///MODULE parser\n")
	srcBundle.Write(parserSrc)
	srcBundle.WriteString("\n///MODULE asm\n")
	srcBundle.Write(asmSrc)
	srcBundle.WriteString("\n///MODULE flatten\n")
	srcBundle.Write(flattenSrc)
	srcBundle.WriteString("\n///MODULE main\n")
	srcBundle.WriteString(driverMod)
	bundleBytes := srcBundle.Bytes()

	// stage 1: bundle_run -> mmc.
	mmcAsm := runCapture(t, gcc, runner, driverBin, bundleBytes)
	mmcBin := buildBin(t, gcc, dir, "mmc", string(mmcAsm))

	// stage 2: mmc compiles its own source -> gen2.
	gen2Asm := runCapture(t, gcc, runner, mmcBin, bundleBytes)
	if len(gen2Asm) == 0 {
		t.Fatal("stage 2: mmc emitted 0 bytes compiling its own source")
	}
	gen2Bin := buildBin(t, gcc, dir, "gen2", string(gen2Asm))

	// stage 3: gen2 compiles its own source -> gen3.
	gen3Asm := runCapture(t, gcc, runner, gen2Bin, bundleBytes)
	if len(gen3Asm) == 0 {
		t.Fatal("stage 3: gen2 emitted 0 bytes compiling its own source")
	}

	// Convergence: gen2 and gen3 must be byte-identical.
	if !bytes.Equal(gen2Asm, gen3Asm) {
		t.Fatalf("fixpoint not convergent: gen2=%d bytes, gen3=%d bytes", len(gen2Asm), len(gen3Asm))
	}
	t.Logf("convergent self-hosting fixpoint: gen2 == gen3 (%d bytes)", len(gen2Asm))

	// Sanity: gen2 is a working compiler — compile a 2-module program.
	prog2 := "///MODULE a\n" +
		"pub function add(x: i32, y: i32): i32 { return x + y; }\n" +
		"function main(): i32 { return 0; }\n" +
		"///MODULE main\n" +
		"import \"./a\";\n" +
		"function main(): i32 { return a.add(19, 23); }\n"
	progAsm := runCapture(t, gcc, runner, gen2Bin, []byte(prog2))
	progBin := buildBin(t, gcc, dir, "prog", string(progAsm))
	var pcmd *exec.Cmd
	if len(runner) == 0 {
		pcmd = exec.Command(progBin)
	} else {
		pcmd = exec.Command(runner[0], append(runner[1:], progBin)...)
	}
	_, _ = pcmd.CombinedOutput()
	if code := pcmd.ProcessState.ExitCode(); code != 42 {
		t.Errorf("gen2-compiled a.add(19,23) exited %d, want 42", code)
	}
}

// buildBin assembles+links asm into dir/name and returns its path.
func buildBin(t *testing.T, gcc, dir, name, asm string) string {
	t.Helper()
	asmPath := filepath.Join(dir, name+".s")
	binPath := filepath.Join(dir, name)
	if err := os.WriteFile(asmPath, []byte(asm), 0o644); err != nil {
		t.Fatalf("write %s asm: %v", name, err)
	}
	if out, err := exec.Command(gcc, "-static", "-nostdlib", "-no-pie", asmPath, "-o", binPath).CombinedOutput(); err != nil {
		t.Fatalf("gcc %s: %v\n%s", name, err, out)
	}
	return binPath
}
