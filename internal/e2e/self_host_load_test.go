package e2e

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// TestSelfHostLoadFixpointX86_64 is the import-driven, file-loading
// capstone: asm_load_run.fern follows `import "./x"` declarations to
// sibling files on disk (read_file), loads them transitively, and
// compiles the whole set — no stdin marker-bundle.
//
//	stage 0  the Go compiler builds asm_load_run -> mmc.
//	stage 1  mmc compiles asm_load_run.fern (loading lexer + parser +
//	         flatten + asm from disk) -> gen1.
//	stage 2  gen1 compiles asm_load_run.fern -> gen2.
//
// mmc / gen1 / gen2 emit BYTE-IDENTICAL asm — a file-driven self-host
// fixpoint. As a sanity check gen1 also compiles a separate 2-file
// `import`ed program to a binary that exits 42.
func TestSelfHostLoadFixpointX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	if len(runner) != 0 {
		// The driver reads module files by host path from argv; a
		// qemu runner wouldn't resolve the same paths. Native only.
		t.Skip("file-loading driver test runs only natively (argv paths)")
	}
	dir := writeSelfHostAsmProject(t) // lexer, parser, asm
	for _, name := range []string{"flatten.fern", "asm.fern", "asm_load_run.fern"} {
		src, err := os.ReadFile(filepath.Join("../../examples/self_host", name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		if err := os.WriteFile(filepath.Join(dir, name), src, 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}

	// stage 0: build the loader with the Go backend.
	mmc := buildSelfHostBin(t, gcc, dir, "asm_load_run.fern", "mmc")

	entryPath := filepath.Join(dir, "asm_load_run.fern")

	// stage 1: mmc compiles its own source (resolving imports from disk).
	gen1Asm, err := exec.Command(mmc, entryPath).Output()
	if err != nil {
		t.Fatalf("stage 1 (mmc): %v", err)
	}
	if len(gen1Asm) == 0 {
		t.Fatal("stage 1: mmc emitted 0 bytes")
	}
	gen1Bin := buildBin(t, gcc, dir, "gen1", string(gen1Asm))

	// stage 2: gen1 compiles the same source.
	gen2Asm, err := exec.Command(gen1Bin, entryPath).Output()
	if err != nil {
		t.Fatalf("stage 2 (gen1): %v", err)
	}

	if !bytes.Equal(gen1Asm, gen2Asm) {
		t.Fatalf("file-driven fixpoint broken: gen1=%d bytes, gen2=%d bytes", len(gen1Asm), len(gen2Asm))
	}
	t.Logf("byte-identical file-driven self-hosting fixpoint: mmc == gen1 == gen2 (%d bytes)", len(gen1Asm))

	// Sanity: gen1 is a working compiler — compile a 2-file program
	// where main imports a sibling module from disk.
	proj := t.TempDir()
	if err := os.WriteFile(filepath.Join(proj, "a.fern"),
		[]byte("pub function add(x: i32, y: i32): i32 { return x + y; }\n"), 0o644); err != nil {
		t.Fatalf("write a.fern: %v", err)
	}
	if err := os.WriteFile(filepath.Join(proj, "main.fern"),
		[]byte("import \"./a\";\nfunction main(): i32 { return a.add(19, 23); }\n"), 0o644); err != nil {
		t.Fatalf("write main.fern: %v", err)
	}
	progAsm, err := exec.Command(gen1Bin, filepath.Join(proj, "main.fern")).Output()
	if err != nil {
		t.Fatalf("gen1 on 2-file program: %v", err)
	}
	progBin := buildBin(t, gcc, dir, "prog", string(progAsm))
	cmd := exec.Command(progBin)
	_ = cmd.Run()
	if code := cmd.ProcessState.ExitCode(); code != 42 {
		t.Errorf("gen1-compiled a.add(19,23) exited %d, want 42", code)
	}
}
