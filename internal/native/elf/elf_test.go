package elf_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/jakechampion/lang/internal/native/arm64"
	"github.com/jakechampion/lang/internal/native/elf"
)

// TestStaticExecutableHeader checks the fixed-layout fields of the
// produced ELF-64 header + program header without needing any tools:
// magic, class/data, machine = EM_AARCH64, one PT_LOAD, and an entry
// that points just past the two headers.
func TestStaticExecutableHeader(t *testing.T) {
	text := []byte{0x00, 0x00, 0x80, 0xd2} // one instruction (movz x0,#0)
	bin := elf.StaticExecutable(text)

	if len(bin) != 64+56+len(text) {
		t.Fatalf("len = %d, want %d", len(bin), 64+56+len(text))
	}
	if string(bin[:4]) != "\x7fELF" {
		t.Errorf("bad magic: % x", bin[:4])
	}
	if bin[4] != 2 || bin[5] != 1 { // ELFCLASS64, ELFDATA2LSB
		t.Errorf("class/data = %d/%d, want 2/1", bin[4], bin[5])
	}
	if e_type := u16(bin, 16); e_type != 2 { // ET_EXEC
		t.Errorf("e_type = %d, want 2 (ET_EXEC)", e_type)
	}
	if e_machine := u16(bin, 18); e_machine != 183 { // EM_AARCH64
		t.Errorf("e_machine = %d, want 183 (EM_AARCH64)", e_machine)
	}
	if e_phnum := u16(bin, 56); e_phnum != 1 {
		t.Errorf("e_phnum = %d, want 1", e_phnum)
	}
	if e_entry := u64(bin, 24); e_entry != 0x400000+64+56 {
		t.Errorf("e_entry = %#x, want %#x", e_entry, 0x400000+64+56)
	}
	// Program header begins at e_phoff = 64; p_type must be PT_LOAD(1).
	if p_type := u32(bin, 64); p_type != 1 {
		t.Errorf("p_type = %d, want 1 (PT_LOAD)", p_type)
	}
	if p_flags := u32(bin, 68); p_flags != 5 { // PF_R|PF_X
		t.Errorf("p_flags = %d, want 5 (R|X)", p_flags)
	}
}

// TestExitCodeRunsUnderQemu is the end-to-end gate: encode a tiny
// exit(42) program, wrap it in a static ELF via StaticExecutable,
// and run it under qemu-aarch64. The whole pipeline — instruction
// encoding, ELF layout, kernel/qemu load, syscall — has to be right
// for the process to exit 42.
func TestExitCodeRunsUnderQemu(t *testing.T) {
	qemu, err := exec.LookPath("qemu-aarch64")
	if err != nil {
		if qemu, err = exec.LookPath("qemu-aarch64-static"); err != nil {
			t.Skip("qemu-aarch64 not on PATH")
		}
	}

	// exit(42): movz x0,#42 ; movz x8,#93 (__NR_exit) ; svc #0.
	var text []byte
	text = arm64.Put(text, arm64.MOVZ(0, 42, 0))
	text = arm64.Put(text, arm64.MOVZ(8, 93, 0))
	text = arm64.Put(text, arm64.SVC(0))
	bin := elf.StaticExecutable(text)

	path := filepath.Join(t.TempDir(), "exit42")
	if err := os.WriteFile(path, bin, 0o755); err != nil {
		t.Fatalf("write binary: %v", err)
	}

	cmd := exec.Command(qemu, path)
	err = cmd.Run()
	if err == nil {
		t.Fatalf("process exited 0, want 42")
	}
	ee, ok := err.(*exec.ExitError)
	if !ok {
		t.Fatalf("run failed (not an exit code): %v", err)
	}
	if ee.ExitCode() != 42 {
		t.Fatalf("exit code = %d, want 42", ee.ExitCode())
	}
}

func u16(b []byte, off int) uint16 {
	return uint16(b[off]) | uint16(b[off+1])<<8
}

func u32(b []byte, off int) uint32 {
	return uint32(b[off]) | uint32(b[off+1])<<8 | uint32(b[off+2])<<16 | uint32(b[off+3])<<24
}

func u64(b []byte, off int) uint64 {
	var v uint64
	for i := 7; i >= 0; i-- {
		v = v<<8 | uint64(b[off+i])
	}
	return v
}
