package e2eselfhost

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// TestSelfHostModloadFixpointX86_64 is the file-based, import-driven
// counterpart of TestSelfHostFixpoint — and the keystone for retiring the
// `///MODULE`-marker bundle harness (bundle_run.fern). Where the marker
// fixpoint glues the compiler's modules into one stdin blob, this one
// compiles the compiler straight from its source FILES on disk:
// asm_modload_run.fern is both the driver AND the entry being compiled, and
// it pulls in lexer / parser / flatten / asm (transitively the whole
// compiler) plus the real builtins.fern by following its own imports.
//
//	stage 0: native fern builds the driver (asm_modload_run) → host binary.
//	stage 1: that driver compiles the compiler's own source (the on-disk
//	         asm_modload_run.fern + its import graph) → mmc.
//	stage 2: mmc compiles the same source → gen2.
//	stage 3: gen2 compiles the same source → gen3.
//
// mmc == gen2 == gen3, byte-identical — the same self-hosting fixpoint the
// marker harness guarantees, with zero `///MODULE` markers and zero stdin
// bundle. Proves the file-based loader fully replaces bundle_run for the
// hardest case (the whole compiler), so the per-feature marker tests can
// migrate onto it and bundle_run can eventually be deleted.
//
// RETIRED FROM ROUTINE CI (#3457 slice 2). This is the last routine test
// that compiles the whole compiler through the MERGED bundle — i.e. the
// legacy AST emitter (`asm.emit_module`) that #3457 is retiring. Routine
// coverage of the file-based whole-compiler self-compile now comes from
// TestSelfHostModloadPerModuleWholeCompilerX86_64 (the per-module IR path,
// ~6 min), with the per-module BYTE-IDENTITY proof in the env-gated
// TestSelfHostPerModuleEmitAllFixpointX86_64. This test stays runnable
// (RUN_MERGED_FIXPOINT=1) as an on-demand byte-identity backstop for the
// merged path until slice 5 deletes `asm.fern`, at which point it goes with
// it. Keeping it env-gated (not deleted) preserves the merged byte-identity
// signal on demand and keeps the many docs that cite it valid.
func TestSelfHostModloadFixpointX86_64(t *testing.T) {
	if os.Getenv("RUN_MERGED_FIXPOINT") == "" {
		t.Skip("set RUN_MERGED_FIXPOINT=1 to run the merged-bundle self-compile byte-identity fixpoint (retired from routine CI, #3457 slice 2 — routine coverage is TestSelfHostModloadPerModuleWholeCompilerX86_64)")
	}
	gcc, runner := x86_64Tooling(t)
	dir := writeSelfHostModloadProject(t)

	// stage 0: build the driver (asm_modload_run) as an x86 host binary.
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_modload_run.fern", "driver")

	// The compiler's own source, addressed by its on-disk entry. The driver
	// follows asm_modload_run.fern's import graph to the sibling modules.
	entry := filepath.Join(dir, "asm_modload_run.fern")

	// stage 1: driver compiles the compiler's own source -> mmc.
	mmcAsm := runDriverFile(t, runner, driverBin, entry, "-merged")
	if len(mmcAsm) == 0 {
		t.Fatal("stage 1: driver emitted 0 bytes compiling the compiler source")
	}
	mmcBin := buildBin(t, gcc, dir, "mmc", string(mmcAsm))

	// stage 2: mmc compiles the same source -> gen2.
	gen2Asm := runDriverFile(t, runner, mmcBin, entry, "-merged")
	if len(gen2Asm) == 0 {
		t.Fatal("stage 2: mmc emitted 0 bytes compiling the compiler source")
	}
	gen2Bin := buildBin(t, gcc, dir, "gen2", string(gen2Asm))

	// stage 3: gen2 compiles the same source -> gen3.
	gen3Asm := runDriverFile(t, runner, gen2Bin, entry, "-merged")
	if len(gen3Asm) == 0 {
		t.Fatal("stage 3: gen2 emitted 0 bytes compiling the compiler source")
	}

	// Fixpoint: the Go-bootstrapped driver and both self-hosted generations
	// must be byte-identical.
	if string(mmcAsm) != string(gen2Asm) {
		t.Fatalf("stage-1 fixpoint broken: mmc=%d bytes, gen2=%d bytes", len(mmcAsm), len(gen2Asm))
	}
	if string(gen2Asm) != string(gen3Asm) {
		t.Fatalf("fixpoint not convergent: gen2=%d bytes, gen3=%d bytes", len(gen2Asm), len(gen3Asm))
	}
	t.Logf("file-based self-hosting fixpoint: mmc == gen2 == gen3 (%d bytes)", len(gen2Asm))

	// Sanity: gen2 is a working compiler — compile a 2-module program from
	// real files (no markers) and run it.
	progDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(progDir, "a.fern"),
		[]byte("pub function add(x: i32, y: i32): i32 { return x + y; }\n"), 0o644); err != nil {
		t.Fatalf("write a.fern: %v", err)
	}
	if err := os.WriteFile(filepath.Join(progDir, "main.fern"),
		[]byte("import \"./a\";\nfunction main(): i32 { return a.add(19, 23); }\n"), 0o644); err != nil {
		t.Fatalf("write main.fern: %v", err)
	}
	progAsm := runDriverFile(t, runner, gen2Bin, filepath.Join(progDir, "main.fern"))
	progBin := buildBin(t, gcc, progDir, "prog", string(progAsm))
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

	// Sanity 2 (folded from the retired multimodule test): a CROSS-MODULE
	// qualified-type struct-update spread (`b.P { ...p, y: 40 }`) — guards
	// the parser's qualified-postfix `...` look-ahead. 1 + 40 = 41.
	spreadDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(spreadDir, "b.fern"),
		[]byte("pub struct P { x: i32, y: i32 }\npub function mk(): P { return P { x: 1, y: 2 }; }\n"), 0o644); err != nil {
		t.Fatalf("write b.fern: %v", err)
	}
	if err := os.WriteFile(filepath.Join(spreadDir, "main.fern"),
		[]byte("import \"./b\";\nfunction main(): i32 { var p: b.P = b.mk(); var q: b.P = b.P { ...p, y: 40 }; return q.x + q.y; }\n"), 0o644); err != nil {
		t.Fatalf("write main.fern: %v", err)
	}
	spreadAsm := runDriverFile(t, runner, gen2Bin, filepath.Join(spreadDir, "main.fern"))
	spreadBin := buildBin(t, gcc, spreadDir, "spread", string(spreadAsm))
	var scmd *exec.Cmd
	if len(runner) == 0 {
		scmd = exec.Command(spreadBin)
	} else {
		scmd = exec.Command(runner[0], append(runner[1:], spreadBin)...)
	}
	_, _ = scmd.CombinedOutput()
	if code := scmd.ProcessState.ExitCode(); code != 41 {
		t.Errorf("gen2-compiled qualified-type struct-update spread exited %d, want 41", code)
	}
}
