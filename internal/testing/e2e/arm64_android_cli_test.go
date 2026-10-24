package e2e

import (
	"bytes"
	"debug/elf"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestArm64AndroidCLI(t *testing.T) {
	bin := buildFernCLI(t)
	qemu := arm64QemuOrEmpty(t)
	dir := t.TempDir()
	src := filepath.Join(dir, "prog.fern")
	prog := `function apply(f: (i32) => i32, x: i32): i32 { return f(x); }
function dbl(x: i32): i32 { return x * 2; }
function main(): i32 { print("android pie"); return apply(dbl, 21); }`
	if err := os.WriteFile(src, []byte(prog), 0o644); err != nil {
		t.Fatalf("write src: %v", err)
	}
	out := filepath.Join(dir, "prog.bin")
	if o, err := exec.Command(bin, "-target", "arm64-android", "-o", out, src).CombinedOutput(); err != nil {
		t.Fatalf("arm64-android build failed: %v\n%s", err, o)
	}

	raw, err := os.ReadFile(out)
	if err != nil {
		t.Fatalf("read binary: %v", err)
	}
	f, err := elf.NewFile(bytes.NewReader(raw))
	if err != nil {
		t.Fatalf("output is not a parseable ELF: %v", err)
	}
	if f.Type != elf.ET_DYN {
		t.Errorf("e_type = %v, want ET_DYN (PIE)", f.Type)
	}
	if f.Machine != elf.EM_AARCH64 {
		t.Errorf("e_machine = %v, want EM_AARCH64", f.Machine)
	}
	for _, p := range f.Progs {
		if p.Type == elf.PT_LOAD && p.Flags&elf.PF_W != 0 && p.Flags&elf.PF_X != 0 {
			t.Errorf("PT_LOAD is W+X (%v) — not W^X", p.Flags)
		}
	}

	var cmd *exec.Cmd
	if qemu == "" {
		cmd = exec.Command(out)
	} else {
		cmd = exec.Command(qemu, out)
	}
	stdout, _ := cmd.CombinedOutput()
	code := cmd.ProcessState.ExitCode()
	if code != 42 || string(stdout) != "android pie\n" {
		t.Fatalf("arm64-android run = (%q, %d), want (%q, 42)", stdout, code, "android pie\n")
	}
}
