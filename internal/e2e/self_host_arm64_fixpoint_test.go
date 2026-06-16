package e2e

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/jakechampion/lang/internal/checker"
	"github.com/jakechampion/lang/internal/codegen/x86_64"
	"github.com/jakechampion/lang/internal/constfold"
	"github.com/jakechampion/lang/internal/modload"
)

// TestSelfHostFixpointArm64 — the file-based arm64 self-hosting fixpoint,
// arm64 counterpart of TestSelfHostModloadFixpointX86_64 and successor to
// the retired bundle_run_arm64 marker fixpoint. The arm64-emitting driver
// (asm_arm64_modload_run) compiles its OWN source graph off disk to a
// byte-identical 3-generation fixpoint, emitting aarch64 asm.
//
//	stage 0: native fern builds the arm64 driver as an x86 HOST binary
//	         (runs on x86, emits arm64 asm).
//	stage 1: that driver compiles the compiler's own on-disk source
//	         (asm_arm64_modload_run.fern + its import graph) → mmc, an
//	         aarch64 binary (cross-gcc linked, run under qemu-aarch64).
//	stage 2: mmc compiles the same source → gen2.
//	stage 3: gen2 compiles the same source → gen3.
//
// mmc == gen2 == gen3, byte-identical — the self-hosted aarch64 compiler
// reproduces itself from files, no ///MODULE bundle. A 2-module program
// compiled by gen2 exits 42 under qemu.
//
// SKIPs cleanly without the aarch64 cross-toolchain / qemu-aarch64
// (arm64Tooling); CI provides them.
func TestSelfHostFixpointArm64(t *testing.T) {
	arm64gcc, qemu := arm64Tooling(t)
	x86gcc, x86runner := x86_64Tooling(t)
	dir := writeSelfHostModloadProject(t)

	// stage 0: build the arm64 driver (asm_arm64_modload_run) as an x86
	// host binary — it runs on the test host and emits arm64 asm.
	prog, _, err := modload.Load(filepath.Join(dir, "asm_arm64_modload_run.fern"))
	if err != nil {
		t.Fatalf("modload arm64 driver: %v", err)
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

	entry := filepath.Join(dir, "asm_arm64_modload_run.fern")
	var arm64runner []string
	if qemu != "" {
		arm64runner = []string{qemu}
	}

	// stage 1: driver (x86) compiles the compiler's own source -> mmc (arm64).
	mmcAsm := runDriverFile(t, x86runner, driverBin, entry)
	if len(mmcAsm) == 0 {
		t.Fatal("stage 1: driver emitted 0 bytes compiling the arm64 compiler source")
	}
	mmcBin := buildBin(t, arm64gcc, dir, "mmc", string(mmcAsm))

	// stage 2: mmc (arm64, under qemu) compiles the same source -> gen2.
	gen2Asm := runDriverFile(t, arm64runner, mmcBin, entry)
	if len(gen2Asm) == 0 {
		t.Fatal("stage 2: mmc emitted 0 bytes compiling the arm64 compiler source")
	}
	gen2Bin := buildBin(t, arm64gcc, dir, "gen2", string(gen2Asm))

	// stage 3: gen2 compiles the same source -> gen3.
	gen3Asm := runDriverFile(t, arm64runner, gen2Bin, entry)
	if len(gen3Asm) == 0 {
		t.Fatal("stage 3: gen2 emitted 0 bytes compiling the arm64 compiler source")
	}

	if string(mmcAsm) != string(gen2Asm) {
		t.Fatalf("stage-1 fixpoint broken: mmc=%d bytes, gen2=%d bytes", len(mmcAsm), len(gen2Asm))
	}
	if string(gen2Asm) != string(gen3Asm) {
		t.Fatalf("fixpoint not convergent: gen2=%d bytes, gen3=%d bytes", len(gen2Asm), len(gen3Asm))
	}
	t.Logf("byte-identical arm64 file-based fixpoint: mmc == gen2 == gen3 (%d bytes)", len(gen2Asm))

	// Sanity: gen2 is a working compiler — compile a 2-module program
	// from real files and run it under qemu.
	progDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(progDir, "a.fern"),
		[]byte("pub function add(x: i32, y: i32): i32 { return x + y; }\n"), 0o644); err != nil {
		t.Fatalf("write a.fern: %v", err)
	}
	if err := os.WriteFile(filepath.Join(progDir, "main.fern"),
		[]byte("import \"./a\";\nfunction main(): i32 { return a.add(19, 23); }\n"), 0o644); err != nil {
		t.Fatalf("write main.fern: %v", err)
	}
	progAsm := runDriverFile(t, arm64runner, gen2Bin, filepath.Join(progDir, "main.fern"))
	progBin := buildBin(t, arm64gcc, progDir, "prog", string(progAsm))
	pcmd := runArm64Bin(qemu, progBin)
	_, _ = pcmd.CombinedOutput()
	if code := pcmd.ProcessState.ExitCode(); code != 42 {
		t.Errorf("gen2-compiled a.add(19,23) exited %d, want 42", code)
	}
}
