package e2eselfhost

import (
	"os"
	"path/filepath"
	"testing"
)

// TestSelfHostFixpointArm64 — the file-based arm64 self-hosting fixpoint,
// arm64 counterpart of TestSelfHostModloadFixpointX86_64 and successor to
// the retired bundle_run_arm64 marker fixpoint. The arm64-emitting driver
// (asm_modload_run -target arm64, #4398 fold) compiles its OWN source graph off disk to a
// byte-identical 3-generation fixpoint, emitting aarch64 asm.
//
//	stage 0: native fern builds the arm64 driver as an x86 HOST binary
//	         (runs on x86, emits arm64 asm).
//	stage 1: that driver compiles the compiler's own on-disk source
//	         (asm_modload_run.fern + its import graph) → mmc, an
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
//
// RETIRED FROM ROUTINE CI (#3457 slice 2, arm64 leg — mirrors the x86
// TestSelfHostModloadFixpointX86_64 retirement). This is the last routine
// arm64 test compiling the whole compiler through the MERGED bundle — the
// legacy AST emitter (`asm_arm64.emit_module`) that #3457 is retiring.
// Routine arm64 coverage of the file-based whole-compiler self-compile now
// comes from TestSelfHostModloadPerModuleWholeCompilerArm64 (per-module IR).
// This stays runnable on demand (RUN_MERGED_FIXPOINT=1) as a merged-path
// byte-identity backstop until slice 5 deletes `asm_arm64.fern`.
func TestSelfHostFixpointArm64(t *testing.T) {
	if os.Getenv("RUN_MERGED_FIXPOINT") == "" {
		t.Skip("set RUN_MERGED_FIXPOINT=1 to run the arm64 merged-bundle self-compile byte-identity fixpoint (retired from routine CI, #3457 slice 2 — routine coverage is TestSelfHostModloadPerModuleWholeCompilerArm64)")
	}
	arm64gcc, qemu := arm64Tooling(t)
	x86gcc, x86runner := x86_64Tooling(t)
	dir := writeSelfHostModloadProject(t)

	// stage 0: build the merged modload driver (asm_modload_run, whose
	// -target arm64 mode replaced the asm_arm64_modload_run mirror, #4398) as
	// an x86 host binary — it runs on the test host and emits arm64 asm.
	driverBin := buildSelfHostBin(t, x86gcc, dir, "asm_modload_run.fern", "driver")

	entry := filepath.Join(dir, "asm_modload_run.fern")
	var arm64runner []string
	if qemu != "" {
		arm64runner = []string{qemu}
	}

	// stage 1: driver (x86) compiles the compiler's own source -> mmc (arm64).
	mmcAsm := runDriverFile(t, x86runner, driverBin, entry, "-target", "arm64")
	if len(mmcAsm) == 0 {
		t.Fatal("stage 1: driver emitted 0 bytes compiling the arm64 compiler source")
	}
	mmcBin := buildBin(t, arm64gcc, dir, "mmc", string(mmcAsm))

	// stage 2: mmc (arm64, under qemu) compiles the same source -> gen2.
	gen2Asm := runDriverFile(t, arm64runner, mmcBin, entry, "-target", "arm64")
	if len(gen2Asm) == 0 {
		t.Fatal("stage 2: mmc emitted 0 bytes compiling the arm64 compiler source")
	}
	gen2Bin := buildBin(t, arm64gcc, dir, "gen2", string(gen2Asm))

	// stage 3: gen2 compiles the same source -> gen3.
	gen3Asm := runDriverFile(t, arm64runner, gen2Bin, entry, "-target", "arm64")
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
	progAsm := runDriverFile(t, arm64runner, gen2Bin, filepath.Join(progDir, "main.fern"), "-target", "arm64")
	progBin := buildBin(t, arm64gcc, progDir, "prog", string(progAsm))
	pcmd := runArm64Bin(qemu, progBin)
	_, _ = pcmd.CombinedOutput()
	if code := pcmd.ProcessState.ExitCode(); code != 42 {
		t.Errorf("gen2-compiled a.add(19,23) exited %d, want 42", code)
	}
}
