package e2e

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/checker"
	"github.com/jakechampion/lang/internal/codegen/x86_64"
	"github.com/jakechampion/lang/internal/constfold"
	"github.com/jakechampion/lang/internal/modload"
)

// TestSelfHostFixpointArm64 is the ARM64 counterpart of
// TestSelfHostFixpoint: a compiler whose codegen layer is the
// pure-Fern asm_arm64.fern reaches a CONVERGENT self-hosting
// fixpoint, emitting aarch64 assembly.
//
//	stage 0: the Go compiler builds bundle_run_arm64 (the
//	         multi-module driver) as an x86-64 host binary. The
//	         driver runs on the test host; only its OUTPUT is
//	         arm64 asm.
//	stage 1: that driver bundles the compiler's OWN source —
//	         lexer + parser + asm_arm64 + flatten + a
//	         stdin-reading driver — into mmc, a self-hosted
//	         aarch64 compiler binary (assembled with the
//	         aarch64 cross-gcc, run under qemu-aarch64).
//	stage 2: mmc compiles its own source bundle → gen2.
//	stage 3: gen2 compiles its own source bundle → gen3.
//
// mmc, gen2, and gen3 are all asserted BYTE-IDENTICAL — the
// self-hosted aarch64 compiler reproduces itself. As a sanity
// check the produced compiler also compiles a separate 2-module
// program to an aarch64 binary that exits 42 under qemu.
//
// SKIPs cleanly when the aarch64 cross-toolchain / qemu-aarch64
// aren't installed (see arm64Tooling); CI provides them.
func TestSelfHostFixpointArm64(t *testing.T) {
	arm64gcc, qemu := arm64Tooling(t)
	x86gcc, x86runner := x86_64Tooling(t)
	dir := t.TempDir()
	for _, name := range []string{"lexer.fern", "parser.fern", "flatten.fern", "asm_arm64.fern", "bundle_run_arm64.fern"} {
		src, err := os.ReadFile(filepath.Join("../../examples/self_host", name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		if err := os.WriteFile(filepath.Join(dir, name), src, 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}

	// stage 0: build bundle_run_arm64 with the Go compiler as an
	// x86-64 host binary (the driver runs on the test host).
	prog, _, err := modload.Load(filepath.Join(dir, "bundle_run_arm64.fern"))
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
	driverBin := buildBin(t, x86gcc, dir, "driver", asm)

	// The compiler's own source as a marked multi-module bundle.
	// The driver module is bundle_run_arm64 with std/io swapped
	// for the read_all_stdin builtin (the emitter can't lower
	// std/io's Reader).
	lexerSrc, _ := os.ReadFile("../../examples/self_host/lexer.fern")
	parserSrc, _ := os.ReadFile("../../examples/self_host/parser.fern")
	asmSrc, _ := os.ReadFile("../../examples/self_host/asm_arm64.fern")
	flattenSrc, _ := os.ReadFile("../../examples/self_host/flatten.fern")
	bundleRun, _ := os.ReadFile("../../examples/self_host/bundle_run_arm64.fern")
	driverMod := strings.ReplaceAll(string(bundleRun), "import \"std/io\";", "")
	driverMod = strings.ReplaceAll(driverMod, "io.read_all_stdin()", "read_all_stdin()")

	var srcBundle bytes.Buffer
	srcBundle.WriteString("///MODULE lexer\n")
	srcBundle.Write(lexerSrc)
	srcBundle.WriteString("\n///MODULE parser\n")
	srcBundle.Write(parserSrc)
	srcBundle.WriteString("\n///MODULE asm_arm64\n")
	srcBundle.Write(asmSrc)
	srcBundle.WriteString("\n///MODULE flatten\n")
	srcBundle.Write(flattenSrc)
	srcBundle.WriteString("\n///MODULE main\n")
	srcBundle.WriteString(driverMod)
	bundleBytes := srcBundle.Bytes()

	// runner for the produced aarch64 binaries: qemu prefix on
	// x86 hosts, empty on native arm64.
	var arm64runner []string
	if qemu != "" {
		arm64runner = []string{qemu}
	}

	// stage 1: driver (x86 host binary) -> mmc (aarch64 binary).
	mmcAsm := runCapture(t, x86gcc, x86runner, driverBin, bundleBytes)
	if len(mmcAsm) == 0 {
		t.Fatal("stage 1: driver emitted 0 bytes")
	}
	mmcBin := buildBin(t, arm64gcc, dir, "mmc", string(mmcAsm))

	// stage 2: mmc compiles its own source -> gen2.
	gen2Asm := runCapture(t, arm64gcc, arm64runner, mmcBin, bundleBytes)
	if len(gen2Asm) == 0 {
		t.Fatal("stage 2: mmc emitted 0 bytes compiling its own source")
	}
	gen2Bin := buildBin(t, arm64gcc, dir, "gen2", string(gen2Asm))

	// stage 3: gen2 compiles its own source -> gen3.
	gen3Asm := runCapture(t, arm64gcc, arm64runner, gen2Bin, bundleBytes)
	if len(gen3Asm) == 0 {
		t.Fatal("stage 3: gen2 emitted 0 bytes compiling its own source")
	}

	// Fixpoint: the Go-bootstrapped compiler (mmc) and both
	// self-hosted generations must be byte-identical.
	if !bytes.Equal(mmcAsm, gen2Asm) {
		t.Fatalf("stage-1 fixpoint broken: mmc=%d bytes, gen2=%d bytes", len(mmcAsm), len(gen2Asm))
	}
	if !bytes.Equal(gen2Asm, gen3Asm) {
		t.Fatalf("fixpoint not convergent: gen2=%d bytes, gen3=%d bytes", len(gen2Asm), len(gen3Asm))
	}
	t.Logf("byte-identical arm64 self-hosting fixpoint: mmc == gen2 == gen3 (%d bytes)", len(gen2Asm))

	// Sanity: gen2 is a working compiler — compile a 2-module program.
	prog2 := "///MODULE a\n" +
		"pub function add(x: i32, y: i32): i32 { return x + y; }\n" +
		"function main(): i32 { return 0; }\n" +
		"///MODULE main\n" +
		"import \"./a\";\n" +
		"function main(): i32 { return a.add(19, 23); }\n"
	progAsm := runCapture(t, arm64gcc, arm64runner, gen2Bin, []byte(prog2))
	progBin := buildBin(t, arm64gcc, dir, "prog", string(progAsm))
	pcmd := runArm64Bin(qemu, progBin)
	_, _ = pcmd.CombinedOutput()
	if code := pcmd.ProcessState.ExitCode(); code != 42 {
		t.Errorf("gen2-compiled a.add(19,23) exited %d, want 42", code)
	}
}
