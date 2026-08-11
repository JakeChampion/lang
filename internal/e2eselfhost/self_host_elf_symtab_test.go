package e2eselfhost

import (
	"encoding/hex"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestSelfHostELFSymtab exercises the self-host ELF section-header and
// .symtab writer (examples/self_host/elf.fern, #6637) — the layer that
// makes a self-host-built binary readable by nm, a debugger and a
// profiler, which today it is not.
//
// The driver's own assertions cover the emitted field values. What this
// adds is the half that cannot be checked from inside Fern:
//
//   - REAL TOOLS read it. `nm` must resolve the symbols to the addresses
//     the driver placed them at. A hand-rolled section table that only
//     satisfies its author's reading of the spec is worth very little.
//   - IT STILL RUNS. The risk in appending a section-header table is not
//     that nm fails, it is that the loader stops accepting the image, so
//     the binary is executed and its exit code checked.
//
// The driver builds a genuine twelve-byte x86-64 `exit(7)` program, so a
// wrong p_filesz or a disturbed program header shows up as a failure to
// execute rather than as a passing byte comparison.
func TestSelfHostELFSymtab(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	if len(runner) != 0 {
		t.Skip("emitted binary is executed directly; skipping under an exec runner")
	}
	if _, err := exec.LookPath("nm"); err != nil {
		t.Skip("nm not on PATH; skipping symbol-table read-back")
	}

	dir := t.TempDir()
	copySelfHostDriver(t, dir, "elf_syms_run.fern")
	bin := buildSelfHostBin(t, gcc, dir, "elf_syms_run.fern", "elf_syms_run")

	cmd := exec.Command(bin)
	out, _ := cmd.Output()
	if cmd.ProcessState == nil || !cmd.ProcessState.Exited() {
		t.Fatalf("elf_syms_run did not exit normally")
	}
	if code := cmd.ProcessState.ExitCode(); code != 0 {
		t.Fatalf("elf_syms_run exit code = %d, want 0 — that code is the failing assertion's id in elf_syms_run.fern", code)
	}

	raw, err := hex.DecodeString(strings.TrimSpace(string(out)))
	if err != nil {
		t.Fatalf("decoding the emitted image: %v", err)
	}
	if len(raw) == 0 {
		t.Fatal("driver emitted no image")
	}

	img := filepath.Join(dir, "g.bin")
	if err := os.WriteFile(img, raw, 0o755); err != nil {
		t.Fatalf("writing image: %v", err)
	}

	// nm resolves both symbols, at the addresses the driver placed them.
	// 0x4000b0 is elf_base_vaddr() + the W^X header block (64 + 2*56).
	nmOut, err := exec.Command("nm", img).Output()
	if err != nil {
		t.Fatalf("nm on the emitted image: %v", err)
	}
	for _, want := range []string{
		"00000000004000b0 T _start",
		"00000000004000b5 T helper",
	} {
		if !strings.Contains(string(nmOut), want) {
			t.Errorf("nm output missing %q\n--- got ---\n%s", want, nmOut)
		}
	}

	// And the image still executes: the section table is appended past the
	// last segment, so it must not change what the loader maps.
	run := exec.Command(img)
	_ = run.Run()
	if run.ProcessState == nil || !run.ProcessState.Exited() {
		t.Fatalf("the symbol-bearing binary did not exit normally — the section table disturbed the loadable image")
	}
	if code := run.ProcessState.ExitCode(); code != 7 {
		t.Errorf("symbol-bearing binary exit = %d, want 7", code)
	}
}
