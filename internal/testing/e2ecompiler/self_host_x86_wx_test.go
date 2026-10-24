package e2ecompiler

import (
	"debug/elf"
	"encoding/binary"
	"fmt"
	"os"
	"path/filepath"
	"syscall"
	"testing"
)

func TestSelfHostX86WXExecutable(t *testing.T) {
	cli := buildSelfHostCLI(t)
	if len(cli.runner) != 0 {
		t.Skip("requires native x86-64 Linux memory protection")
	}
	dir := t.TempDir()
	src := filepath.Join(dir, "main.fern")
	program := `struct Named { name: string, values: i32[] }
function table(): Named { return Named { name: "relocated", values: [7, 9] }; }
function inc(n: i32): i32 { return n + 1; }
function main(): i32 {
  let f: (i32) => i32 = inc;
  let t: Named = table();
  let xs: i32[] = [];
  let i: i32 = 0;
  while (i < 100) { xs = xs.append(f(i)); i = i + 1; }
  if (xs[99] != 100 || t.name != "relocated" || t.values[1] != 9) { return 1; }
  let changed: i32[] = t.values.with(0, 11);
  if (changed[0] != 11 || table().values[0] != 7) { return 2; }
  return 0;
}`
	if err := os.WriteFile(src, []byte(program), 0o644); err != nil {
		t.Fatal(err)
	}
	compile := func(src, out string, flags ...string) {
		t.Helper()
		args := append([]string{"-target", "x86-64-linux", "-o", out}, flags...)
		args = append(args, src, cli.stdlib)
		cmd := runX86_64Bin(cli.runner, cli.bin, args...)
		if msg, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("compile: %v\n%s", err, msg)
		}
		if err := os.Chmod(out, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	bin := filepath.Join(dir, "main")
	compile(src, bin, "-g")
	if out, err := runX86_64Bin(cli.runner, bin).CombinedOutput(); err != nil {
		t.Fatalf("relocated program: %v\n%s", err, out)
	}
	f, err := elf.Open(bin)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	var code, data *elf.Prog
	nxStack := false
	for _, p := range f.Progs {
		if p.Type == elf.PT_GNU_STACK {
			nxStack = p.Flags&elf.PF_X == 0
		}
		if p.Type != elf.PT_LOAD {
			continue
		}
		if p.Flags&elf.PF_X != 0 {
			if p.Flags&elf.PF_W != 0 {
				t.Fatal("executable segment is writable")
			}
			code = p
		}
		if p.Flags&elf.PF_W != 0 {
			data = p
		}
	}
	if code == nil || data == nil || data.Vaddr < code.Vaddr+code.Memsz || data.Memsz <= data.Filesz {
		t.Fatal("missing separate code, data or BSS mapping")
	}
	if data.Align != 4096 || data.Off-code.Filesz >= 4096 {
		t.Fatal("x86 data mapping has more than one 4 KiB page of padding")
	}
	text := f.Section(".text")
	if !nxStack || text == nil || text.Addr%16 != 0 {
		t.Fatal("missing non-executable stack or lost 16-byte code alignment")
	}
	syms, err := f.Symbols()
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, s := range syms {
		if s.Name == "__fn_main" {
			found = s.Value >= code.Vaddr && s.Value+s.Size <= code.Vaddr+code.Filesz
		}
	}
	if !found || f.Section(".debug_info") == nil {
		t.Fatal("debug symbols do not describe the executable mapping")
	}
	assertSegv := func(path string) {
		t.Helper()
		cmd := runX86_64Bin(cli.runner, path)
		out, err := cmd.CombinedOutput()
		if err == nil || cmd.ProcessState == nil {
			t.Fatalf("memory protection did not fault: %v\n%s", err, out)
		}
		status, ok := cmd.ProcessState.Sys().(syscall.WaitStatus)
		if !ok || !status.Signaled() || status.Signal() != syscall.SIGSEGV {
			t.Fatalf("want SIGSEGV, got %v\n%s", err, out)
		}
	}
	writeSrc := filepath.Join(dir, "write_code.fern")
	if err := os.WriteFile(writeSrc, []byte(fmt.Sprintf("function main(): i32 { __store_u8(%du64 as usize, 0); return 0; }", code.Vaddr)), 0o644); err != nil {
		t.Fatal(err)
	}
	writeBin := filepath.Join(dir, "write_code")
	compile(writeSrc, writeBin)
	assertSegv(writeBin)
	image, err := os.ReadFile(bin)
	if err != nil {
		t.Fatal(err)
	}
	copy(image[data.Off:], []byte{0xb8, 60, 0, 0, 0, 0x31, 0xff, 0x0f, 0x05})
	binary.LittleEndian.PutUint64(image[24:32], data.Vaddr)
	nxBin := filepath.Join(dir, "execute_data")
	if err := os.WriteFile(nxBin, image, 0o755); err != nil {
		t.Fatal(err)
	}
	assertSegv(nxBin)
}
