package e2ecompiler

import (
	"context"
	"debug/elf"
	"encoding/binary"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jakechampion/lang/internal/testing/e2eharness"
)

// The board is qemu microvm: COM1 at port 0x3f8, and the isa-debug-exit device
// that the test adds at port 0xf4.
const kernelX86BoardSrc = `const COM1: i32 = 0x3f8;

@provide("log")
pub function uart_write(data: usize, len: i32): void {
  unsafe {
    let i = 0;
    while (i < len) {
      __port_out8(COM1, __load_u8(data + i as usize));
      i += 1;
    }
  }
}

// qemu exits with the status value * 2 + 1.
@norc
pub function exit_qemu(value: i32): void {
  unsafe {
    __port_out8(0xf4, value);
  }
}
`

const kernelX86MainSrc = `import "./board";

function main(): i32 {
  let xs: i32[] = [];
  let i = 0;
  while (i < 10) {
    xs = xs.append(i);
    i += 1;
  }
  print("hello from fern on x86-64, total " + xs.len().to_string());
  board.exit_qemu(16);
  return 0;
}
`

const (
	kernelX86Base = 0x200000
	kernelX86Size = 126 << 20
	kernelX86RAM  = "0x200000,126M"
)

func writeKernelX86Program(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	for name, src := range map[string]string{"board.fern": kernelX86BoardSrc, "main.fern": kernelX86MainSrc} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(src), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return filepath.Join(dir, "main.fern")
}

// An x86-64 kernel image loads at the -ram base. Its segments are inside the
// RAM below the stack and the page tables, and its PVH note names the entry,
// which is the reset code in the text.
func TestSelfHostKernelX86ImageLayout(t *testing.T) {
	cli := buildSelfHostCLI(t)
	src := writeKernelX86Program(t)
	out := filepath.Join(t.TempDir(), "kernel.elf")
	if msg, err := cli.compileCmd("x86-64-kernel", out, src, "-ram", kernelX86RAM).CombinedOutput(); err != nil {
		t.Fatalf("build: %v\n%s", err, msg)
	}
	f, err := elf.Open(out)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if f.Type != elf.ET_EXEC || f.Machine != elf.EM_X86_64 {
		t.Fatalf("type %v machine %v, want ET_EXEC EM_X86_64", f.Type, f.Machine)
	}
	// The stack takes the top 1 MiB, and the tables take the 7 pages below it.
	limit := uint64(kernelX86Base+kernelX86Size-(1<<20)-7*4096) &^ 4095
	var loads []*elf.Prog
	var note *elf.Prog
	for _, p := range f.Progs {
		switch p.Type {
		case elf.PT_LOAD:
			loads = append(loads, p)
		case elf.PT_NOTE:
			note = p
		}
	}
	if len(loads) < 2 || loads[0].Vaddr != kernelX86Base {
		t.Fatalf("PT_LOAD segments %v, want text at the -ram base %#x and data", loads, kernelX86Base)
	}
	entryInText := false
	for _, p := range loads {
		if p.Vaddr < kernelX86Base || p.Vaddr+p.Memsz > limit {
			t.Errorf("segment %#x+%#x is outside the RAM below the tables at %#x", p.Vaddr, p.Memsz, limit)
		}
		if p.Flags&elf.PF_X != 0 && p.Flags&elf.PF_W != 0 {
			t.Errorf("segment %#x is writable and executable", p.Vaddr)
		}
		if p.Flags&elf.PF_X != 0 && f.Entry >= p.Vaddr && f.Entry < p.Vaddr+p.Filesz {
			entryInText = true
		}
	}
	if !entryInText {
		t.Errorf("entry %#x is not in an executable segment", f.Entry)
	}
	if note == nil {
		t.Fatal("no PT_NOTE segment")
	}
	b := make([]byte, note.Filesz)
	if _, err := note.ReadAt(b, 0); err != nil {
		t.Fatal(err)
	}
	le := binary.LittleEndian
	if len(b) != 20 || le.Uint32(b[0:]) != 4 || le.Uint32(b[4:]) != 4 || le.Uint32(b[8:]) != 18 || string(b[12:16]) != "Xen\x00" {
		t.Fatalf("note % x, want the Xen PVH entry note (type 18)", b)
	}
	if got := uint64(le.Uint32(b[16:])); got != f.Entry {
		t.Errorf("the PVH entry is %#x, want the ELF entry %#x", got, f.Entry)
	}
}

// The image boots on qemu microvm through its PVH note, prints through COM1,
// and exits through isa-debug-exit. The reset code enables AVX, so the CPU
// model needs it.
func TestSelfHostKernelX86BootsOnQemu(t *testing.T) {
	qemu := e2eharness.QemuSystem(t, "qemu-system-x86_64")
	cli := buildSelfHostCLI(t)
	out := filepath.Join(t.TempDir(), "kernel.elf")
	if msg, err := cli.compileCmd("x86-64-kernel", out, writeKernelX86Program(t), "-ram", kernelX86RAM).CombinedOutput(); err != nil {
		t.Fatalf("build: %v\n%s", err, msg)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, qemu, "-M", "microvm", "-cpu", "max", "-m", "128M",
		"-nodefaults", "-no-user-config", "-nographic", "-serial", "stdio",
		"-device", "isa-debug-exit,iobase=0xf4,iosize=4", "-kernel", out)
	console, err := cmd.CombinedOutput()
	if ctx.Err() != nil {
		t.Fatalf("the VM did not exit in 60s; console:\n%s", console)
	}
	var exit *exec.ExitError
	if !errors.As(err, &exit) || exit.ExitCode() != 33 {
		t.Fatalf("qemu: %v, want exit status 33 from isa-debug-exit; console:\n%s", err, console)
	}
	if !strings.Contains(string(console), "hello from fern on x86-64, total 10\n") {
		t.Errorf("console:\n%s\nwant the line %q", console, "hello from fern on x86-64, total 10")
	}
}

const portSrc = `function main(): i32 {
  unsafe {
    let a = __port_in8(0x3fd) + __port_in16(0x60) + __port_in32(0xcfc);
    __port_out8(0x3f8, a);
    __port_out16(0x64, a);
    __port_out32(0xcf8, a);
  }
  return 0;
}
`

// Each port access is one in or out through %dx. arm64 has no port space, so
// it refuses the ops, and a hosted target refuses them with E066.
func TestSelfHostKernelPortOps(t *testing.T) {
	cli := buildSelfHostCLI(t)
	dir := t.TempDir()
	src := filepath.Join(dir, "main.fern")
	if err := os.WriteFile(src, []byte(portSrc), 0o644); err != nil {
		t.Fatal(err)
	}
	asmPath := filepath.Join(dir, "kernel.s")
	if msg, err := cli.compileCmd("x86-64-kernel", asmPath, src, "-ram", kernelX86RAM, "-emit", "asm").CombinedOutput(); err != nil {
		t.Fatalf("emit: %v\n%s", err, msg)
	}
	asm, err := os.ReadFile(asmPath)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"    inb %dx, %al\n", "    inw %dx, %ax\n", "    inl %dx, %eax\n", "    outb %al, %dx\n", "    outw %ax, %dx\n", "    outl %eax, %dx\n"} {
		if !strings.Contains(string(asm), want) {
			t.Errorf("no %q in the x86-64-kernel assembly", strings.TrimSpace(want))
		}
	}
	if msg, err := cli.compileCmd("x86-64-kernel", filepath.Join(dir, "kernel.elf"), src, "-ram", kernelX86RAM).CombinedOutput(); err != nil {
		t.Fatalf("build: %v\n%s", err, msg)
	}
	msg, err := cli.compileCmd("arm64-kernel", filepath.Join(dir, "arm64.elf"), src, "-ram", kernelRAM).CombinedOutput()
	if err == nil || !strings.Contains(string(msg), "port_in") {
		t.Errorf("arm64-kernel: err %v; want a refusal that names port_in in:\n%s", err, msg)
	}
	msg, err = cli.compileCmd("x86-64-linux", filepath.Join(dir, "hosted"), src).CombinedOutput()
	if err == nil || !strings.Contains(string(msg), "E066") || !strings.Contains(string(msg), "__port_out8") {
		t.Errorf("x86-64-linux: err %v; want E066 naming __port_out8 in:\n%s", err, msg)
	}
}

// An x86-64 image starts in 32-bit protected mode, so its RAM ends at or below
// 2G.
func TestSelfHostKernelX86RamFlag(t *testing.T) {
	cli := buildSelfHostCLI(t)
	msg, err := cli.compileCmd("x86-64-kernel", filepath.Join(t.TempDir(), "out"), writeKernelX86Program(t), "-ram", "0x80000000,128M").CombinedOutput()
	if err == nil || !strings.Contains(string(msg), "an end at or below 2G") {
		t.Errorf("err %v, output %q; want the 2G refusal", err, msg)
	}
}
