package e2ecompiler

import (
	"bytes"
	"context"
	"debug/elf"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jakechampion/lang/internal/testing/e2eharness"
)

// The board is qemu-virt: its UART is a PL011, and its PSCI firmware takes a
// call through hvc.
const kernelBoardSrc = `const UART: usize = 0x09000000;

@provide("log")
pub function uart_write(data: usize, len: i32): void {
  unsafe {
    let i = 0;
    while (i < len) {
      __mmio_store8(UART, __load_u8(data + i as usize));
      i += 1;
    }
  }
}

// PSCI SYSTEM_OFF.
@norc
pub function power_off(): void {
  unsafe {
    __hvc(0x84000008i64, 0i64, 0i64, 0i64);
  }
}
`

const kernelMainSrc = `import "./board";

function main(): i32 {
  let xs: i32[] = [];
  let i = 0;
  while (i < 10) {
    xs = xs.append(i);
    i += 1;
  }
  print("hello from fern, total " + xs.len().to_string());
  board.power_off();
  return 0;
}
`

// qemu-virt puts the device tree at the start of its RAM, 0x40000000, so the
// image takes the RAM above the first 2 MiB.
const (
	kernelBase = 0x40200000
	kernelSize = 126 << 20
	kernelRAM  = "0x40200000,126M"
)

func writeKernelProgram(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	for name, src := range map[string]string{"board.fern": kernelBoardSrc, "main.fern": kernelMainSrc} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(src), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return filepath.Join(dir, "main.fern")
}

// A kernel image loads at the -ram base. Its text is executable and not
// writable, its data is writable and not executable, every segment is inside
// the RAM below the stack, and the entry is the reset code in the text.
func TestSelfHostKernelImageLayout(t *testing.T) {
	cli := buildSelfHostCLI(t)
	src := writeKernelProgram(t)
	out := filepath.Join(t.TempDir(), "kernel.elf")
	if msg, err := cli.compileCmd("arm64-kernel", out, src, "-ram", kernelRAM).CombinedOutput(); err != nil {
		t.Fatalf("build: %v\n%s", err, msg)
	}
	f, err := elf.Open(out)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if f.Type != elf.ET_EXEC || f.Machine != elf.EM_AARCH64 {
		t.Fatalf("type %v machine %v, want ET_EXEC EM_AARCH64", f.Type, f.Machine)
	}
	var loads []*elf.Prog
	for _, p := range f.Progs {
		if p.Type == elf.PT_LOAD {
			loads = append(loads, p)
		}
	}
	if len(loads) < 2 {
		t.Fatalf("%d PT_LOAD segments, want text and data", len(loads))
	}
	if loads[0].Vaddr != kernelBase {
		t.Errorf("the first segment loads at %#x, want the -ram base %#x", loads[0].Vaddr, kernelBase)
	}
	text, entryInText := false, false
	for _, p := range loads {
		if p.Vaddr < kernelBase || p.Vaddr+p.Memsz > kernelBase+kernelSize-(1<<20) {
			t.Errorf("segment %#x+%#x is outside the RAM below the stack", p.Vaddr, p.Memsz)
		}
		if p.Flags&elf.PF_X != 0 && p.Flags&elf.PF_W != 0 {
			t.Errorf("segment %#x is writable and executable", p.Vaddr)
		}
		if p.Flags&elf.PF_X != 0 {
			text = true
			entryInText = entryInText || f.Entry >= p.Vaddr && f.Entry < p.Vaddr+p.Filesz
		}
	}
	if !text || !entryInText {
		t.Errorf("entry %#x is not in an executable segment", f.Entry)
	}
	asmPath := filepath.Join(t.TempDir(), "kernel.s")
	if msg, err := cli.compileCmd("arm64-kernel", asmPath, src, "-ram", kernelRAM, "-emit", "asm").CombinedOutput(); err != nil {
		t.Fatalf("emit: %v\n%s", err, msg)
	}
	asm, err := os.ReadFile(asmPath)
	if err != nil {
		t.Fatal(err)
	}
	if m := freestandingSyscallRe.FindString(string(asm)); m != "" {
		t.Errorf("the kernel image makes a syscall (%q)", strings.TrimSpace(m))
	}
	if !strings.Contains(string(asm), "\n_start:\n    mrs x9, mpidr_el1\n") {
		t.Error("the image has no reset _start")
	}
}

// The image boots on qemu-virt, prints through the board's UART, and stops the
// VM through PSCI. qemu exits 0 when the guest powers off.
func TestSelfHostKernelBootsOnQemu(t *testing.T) {
	qemu := e2eharness.QemuSystemArm64(t)
	cli := buildSelfHostCLI(t)
	out := filepath.Join(t.TempDir(), "kernel.elf")
	if msg, err := cli.compileCmd("arm64-kernel", out, writeKernelProgram(t), "-ram", kernelRAM).CombinedOutput(); err != nil {
		t.Fatalf("build: %v\n%s", err, msg)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, qemu, "-M", "virt", "-cpu", "cortex-a76", "-m", "128M",
		"-nographic", "-monitor", "none", "-kernel", out)
	console, err := cmd.CombinedOutput()
	if ctx.Err() != nil {
		t.Fatalf("the VM did not power off in 60s; console:\n%s", console)
	}
	if err != nil {
		t.Fatalf("qemu: %v; console:\n%s", err, console)
	}
	if !strings.Contains(string(console), "hello from fern, total 10\n") {
		t.Errorf("console:\n%s\nwant the line %q", console, "hello from fern, total 10")
	}
}

const privilegedSrc = `function main(): i32 {
  __wfi();
  unsafe {
    let a = __hvc(0x84000000i64, 1i64, 2i64, 3i64);
    let b = __smc(0x84000000i64, a, 0i64, 0i64);
    return b as i32;
  }
}
`

// Each privileged op is one instruction, with the SMC calling convention's
// arguments in x0 to x3.
func TestSelfHostKernelPrivilegedOps(t *testing.T) {
	cli := buildSelfHostCLI(t)
	src := filepath.Join(t.TempDir(), "main.fern")
	if err := os.WriteFile(src, []byte(privilegedSrc), 0o644); err != nil {
		t.Fatal(err)
	}
	asmPath := filepath.Join(t.TempDir(), "kernel.s")
	if msg, err := cli.compileCmd("arm64-kernel", asmPath, src, "-ram", kernelRAM, "-emit", "asm").CombinedOutput(); err != nil {
		t.Fatalf("emit: %v\n%s", err, msg)
	}
	asm, err := os.ReadFile(asmPath)
	if err != nil {
		t.Fatal(err)
	}
	text := string(asm)
	last := 0
	for _, want := range []string{"    wfi\n", "    ldr x3, [sp], #16\n", "    ldr x0, [sp], #16\n    hvc #0\n", "    ldr x0, [sp], #16\n    smc #0\n"} {
		i := strings.Index(text[last:], want)
		if i < 0 {
			t.Fatalf("no %q after offset %d in:\n%s", want, last, text)
		}
		last += i + len(want)
	}
	out := filepath.Join(t.TempDir(), "kernel.elf")
	if msg, err := cli.compileCmd("arm64-kernel", out, src, "-ram", kernelRAM).CombinedOutput(); err != nil {
		t.Fatalf("build: %v\n%s", err, msg)
	}
	image, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	for name, word := range map[string][]byte{
		"wfi":    {0x7f, 0x20, 0x03, 0xd5},
		"hvc #0": {0x02, 0x00, 0x00, 0xd4},
		"smc #0": {0x03, 0x00, 0x00, 0xd4},
	} {
		if !bytes.Contains(image, word) {
			t.Errorf("the image has no %s (% x)", name, word)
		}
	}
}

// Only a kernel environment grants the privileged ops: a hosted target refuses
// each one with E066, and __hvc and __smc need an unsafe block.
func TestSelfHostKernelPrivilegedOpsNeedAKernel(t *testing.T) {
	cli := buildSelfHostCLI(t)
	dir := t.TempDir()
	src := filepath.Join(dir, "main.fern")
	if err := os.WriteFile(src, []byte(privilegedSrc), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		target string
		extra  []string
	}{
		{"arm64-linux", nil},
		{"x86-64-linux", nil},
		{"arm64-freestanding", []string{"-emit", "asm"}},
	} {
		target := c.target
		msg, err := cli.compileCmd(target, filepath.Join(dir, "out-"+target), src, c.extra...).CombinedOutput()
		for _, op := range []string{"__wfi", "__hvc", "__smc"} {
			if err == nil || !strings.Contains(string(msg), "E066") || !strings.Contains(string(msg), op) {
				t.Errorf("%s: err %v; want E066 naming %s in:\n%s", target, err, op, msg)
			}
		}
	}
	const outside = "function main(): i32 {\n  return __hvc(0i64, 0i64, 0i64, 0i64) as i32;\n}\n"
	if got := goCheckerCodes(t, dir, outside); len(got) != 1 || got[0] != "E095" {
		t.Errorf("Go checker codes = %v, want [E095]", got)
	}
	bad := filepath.Join(dir, "outside.fern")
	if err := os.WriteFile(bad, []byte(outside), 0o644); err != nil {
		t.Fatal(err)
	}
	msg, err := runX86_64Bin(cli.runner, cli.bin, "-check", bad, cli.stdlib).CombinedOutput()
	if err == nil || !strings.Contains(string(msg), `error[E095]: raw builtin "__hvc" is called outside an unsafe block`) {
		t.Errorf("fern -check: err %v; want E095 for __hvc in:\n%s", err, msg)
	}
}

func TestSelfHostKernelRamFlag(t *testing.T) {
	cli := buildSelfHostCLI(t)
	src := writeKernelProgram(t)
	for _, c := range []struct {
		name, target, ram, want string
	}{
		{"missing", "arm64-kernel", "", "needs -ram BASE,SIZE"},
		{"unaligned base", "arm64-kernel", "0x40001000,128M", "-ram takes BASE,SIZE"},
		{"small size", "arm64-kernel", "0x40000000,1M", "-ram takes BASE,SIZE"},
		{"above 2G", "arm64-kernel", "0x80000000,128M", "-ram takes BASE,SIZE"},
		{"hosted target", "arm64-linux", "0x40000000,128M", "-ram is only for a kernel target"},
	} {
		t.Run(c.name, func(t *testing.T) {
			var extra []string
			if c.ram != "" {
				extra = []string{"-ram", c.ram}
			}
			msg, err := cli.compileCmd(c.target, filepath.Join(t.TempDir(), "out"), src, extra...).CombinedOutput()
			if err == nil || !strings.Contains(string(msg), c.want) {
				t.Errorf("err %v, output %q; want a refusal with %q", err, msg, c.want)
			}
		})
	}
}
