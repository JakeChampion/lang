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

// The board is qemu-virt. Its UART is a PL011, and the firmware puts the
// device tree at the start of the RAM. PSCI takes a call through hvc or smc,
// as the device tree says.
const kernelBoardSrc = `const UART: usize = 0x09000000;

const DEVICE_TREE: usize = 0x40000000;

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

// A copy of the device tree, with the length that its header gives.
pub function device_tree(): u8[] {
  let out: u8[] = [];
  unsafe {
    let size = 0;
    for k in 4..8 {
      size = size << 8 | __load_u8(DEVICE_TREE + k as usize);
    }
    for k in 0..size {
      out = out.append(__load_u8(DEVICE_TREE + k as usize) as u8);
    }
  }
  return out;
}

// PSCI SYSTEM_OFF.
@norc
pub function power_off_hvc(): void {
  unsafe {
    __hvc(0x84000008i64, 0i64, 0i64, 0i64);
  }
}

@norc
pub function power_off_smc(): void {
  unsafe {
    __smc(0x84000008i64, 0i64, 0i64, 0i64);
  }
}
`

const kernelMainSrc = `import "./board";
import "std/fdt";
import "std/option";

function describe(t: fdt.Tree): string {
  let Some(uart) = t.find_compatible("arm,pl011") else {
    return "no uart";
  };
  let Some(r) = t.reg(uart, 0) else {
    return "no uart reg";
  };
  let Some(psci) = t.find_name("psci") else {
    return "no psci";
  };
  return "uart " + r.address.to_string() + " psci " + t.property_string(psci, "method").unwrap_or("none");
}

function main(): i32 {
  let xs: i32[] = [];
  let i = 0;
  while (i < 10) {
    xs = xs.append(i);
    i += 1;
  }
  print("hello from fern, total " + xs.len().to_string());
  match (fdt.parse(board.device_tree())) {
    Ok(t) => {
      let line = describe(t);
      print(line);
      if (line.ends_with(" smc")) {
        board.power_off_smc();
      }
    },
    Err(e) => {
      print(e.message());
    }
  }
  board.power_off_hvc();
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
	// Firecracker puts the RAM at 0x80000000, above the 32-bit signed range.
	for _, c := range []struct {
		ram        string
		base, size uint64
	}{
		{kernelRAM, kernelBase, kernelSize},
		{"0x80000000,128M", 0x80000000, 128 << 20},
	} {
		t.Run(c.ram, func(t *testing.T) {
			checkKernelImage(t, cli, src, c.ram, c.base, c.size)
		})
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

// The image boots on qemu-virt and prints through the board's UART. It finds
// the UART and the PSCI conduit in the device tree, and stops the VM through
// PSCI. qemu exits 0 when the guest powers off.
func TestSelfHostKernelBootsOnQemu(t *testing.T) {
	qemu := e2eharness.QemuSystemArm64(t)
	cli := buildSelfHostCLI(t)
	src := writeKernelProgram(t)
	// The second layout puts the image above 4 GiB, so each address in the
	// image needs 64 bits.
	for _, c := range []struct{ name, ram, mem string }{
		{"low", kernelRAM, "128M"},
		{"above 4G", "0x200000000,64M", "8G"},
	} {
		t.Run(c.name, func(t *testing.T) {
			out := filepath.Join(t.TempDir(), "kernel.elf")
			if msg, err := cli.compileCmd("arm64-kernel", out, src, "-ram", c.ram).CombinedOutput(); err != nil {
				t.Fatalf("build: %v\n%s", err, msg)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
			defer cancel()
			cmd := exec.CommandContext(ctx, qemu, "-M", "virt", "-cpu", "cortex-a76", "-m", c.mem,
				"-nographic", "-monitor", "none", "-kernel", out)
			console, err := cmd.CombinedOutput()
			if ctx.Err() != nil {
				t.Fatalf("the VM did not power off in 60s; console:\n%s", console)
			}
			if err != nil {
				t.Fatalf("qemu: %v; console:\n%s", err, console)
			}
			for _, want := range []string{"hello from fern, total 10\n", "uart 150994944 psci hvc\n"} {
				if !strings.Contains(string(console), want) {
					t.Errorf("console:\n%s\nwant the line %q", console, strings.TrimSpace(want))
				}
			}
		})
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
		{"above 1T", "arm64-kernel", "0xff00000000,8G", "-ram takes BASE,SIZE"},
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

func checkKernelImage(t *testing.T, cli *selfHostCLI, src, ram string, base, size uint64) {
	t.Helper()
	out := filepath.Join(t.TempDir(), "kernel.elf")
	if msg, err := cli.compileCmd("arm64-kernel", out, src, "-ram", ram).CombinedOutput(); err != nil {
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
	if loads[0].Vaddr != base {
		t.Errorf("the first segment loads at %#x, want the -ram base %#x", loads[0].Vaddr, base)
	}
	text, entryInText := false, false
	for _, p := range loads {
		if p.Vaddr < base || p.Vaddr+p.Memsz > base+size-(1<<20) {
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
}
