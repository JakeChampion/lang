package e2ecompiler

import (
	"encoding/hex"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestSelfHostELFSymtab(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	if len(runner) != 0 {
		t.Skip("emitted binary is executed directly; skipping under an exec runner")
	}
	if _, err := exec.LookPath("nm"); err != nil {
		t.Skip("nm not on PATH; skipping symbol-table read-back")
	}

	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/elf_syms_run.fern")
	bin := buildSelfHostBin(t, gcc, dir, "drivers/elf_syms_run.fern", "elf_syms_run")

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

	le16 := func(off int) uint64 { return uint64(raw[off]) | uint64(raw[off+1])<<8 }
	textVA := 0x400000 + le16(52) + le16(56)*le16(54)
	nmOut, err := exec.Command("nm", img).Output()
	if err != nil {
		t.Fatalf("nm on the emitted image: %v", err)
	}
	for _, want := range []string{
		fmt.Sprintf("%016x T _start", textVA),
		fmt.Sprintf("%016x T helper", textVA+5),
	} {
		if !strings.Contains(string(nmOut), want) {
			t.Errorf("nm output missing %q\n--- got ---\n%s", want, nmOut)
		}
	}

	run := exec.Command(img)
	_ = run.Run()
	if run.ProcessState == nil || !run.ProcessState.Exited() {
		t.Fatalf("the symbol-bearing binary did not exit normally — the section table disturbed the loadable image")
	}
	if code := run.ProcessState.ExitCode(); code != 7 {
		t.Errorf("symbol-bearing binary exit = %d, want 7", code)
	}
}
