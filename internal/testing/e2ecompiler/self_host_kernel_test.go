package e2ecompiler

import (
	"debug/elf"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The board's UART is the PL011 of qemu-virt.
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
  return 0;
}
`

const (
	kernelBase = 0x40000000
	kernelSize = 128 << 20
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
	if msg, err := cli.compileCmd("arm64-kernel", out, src, "-ram", "0x40000000,128M").CombinedOutput(); err != nil {
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
	if msg, err := cli.compileCmd("arm64-kernel", asmPath, src, "-ram", "0x40000000,128M", "-emit", "asm").CombinedOutput(); err != nil {
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
