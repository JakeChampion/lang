package e2ecompiler

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestSelfHostX86SymbolAddends(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	driver := buildX86AsmBenchDriver(t, gcc)
	as, objcopy, _ := findGNUToolsX86(t)
	instructions := []string{
		"cmpq $0, %s(%%rip)",
		"cmpq $1000, %s(%%rip)",
		"movq $0, %s(%%rip)",
		"movq %s(%%rip), %%r10",
		"movl %s(%%rip), %%r10d",
		"movq %%r10, %s(%%rip)",
		"leaq %s(%%rip), %%r10",
		"lock cmpxchgq %%r10, %s(%%rip)",
		"lock addq $1, %s(%%rip)",
		"call *%s(%%rip)",
	}
	for i, instruction := range instructions {
		for _, expression := range []string{".Lslot+24", ".Lslot-8", ".Lslot+0x18", ".Lslot - 0X8", ".Lslot+030", ".Lslot+0b11000", ".Lslot-010", `".Lplus+minus-"+8`} {
			for _, forward := range []bool{false, true} {
				t.Run(fmt.Sprintf("%d/%s/forward=%v", i, expression, forward), func(t *testing.T) {
					block := strings.Repeat("nop\n", 16) + ".Lslot:\n\".Lplus+minus-\":\n" + strings.Repeat("nop\n", 64)
					op := fmt.Sprintf(instruction, expression) + "\n"
					src := ".text\n" + block + "_start:\n" + op
					if forward {
						src = ".text\n_start:\n" + op + block
					}
					got := assembleSelfHostX86(t, driver, runner, src)
					want := gnuAsTextX86(t, as, objcopy, t.TempDir(), src)
					if !bytes.Equal(got, want) {
						t.Fatalf("%s: self-host % x; GNU as % x", op, got, want)
					}
				})
			}
		}
	}
	for _, padding := range []string{"", ".ascii \"padding-value-12345678\"\n"} {
		t.Run("linked-bss/"+padding, func(t *testing.T) {
			var src strings.Builder
			src.WriteString(".text\n.globl _start\n_start:\n")
			for _, instruction := range instructions {
				for _, expression := range []string{"slot+24", "slot-8", "slot+0x18", "slot - 0X8"} {
					src.WriteString(fmt.Sprintf(instruction, expression) + "\n")
				}
			}
			src.WriteString(".section .rodata\n" + padding + ".bss\n.zero 16\nslot: .zero 64\n")
			got := assembleSelfHostX86(t, driver, runner, src.String())
			want := gnuLinkedAddendText(t, as, objcopy, src.String())
			if !bytes.Equal(got, want) {
				t.Fatalf("self-host % x; linked GNU % x", got, want)
			}
		})
	}

	for _, expression := range []string{"missing+24", ".Lslot+", ".Lslot+0x", ".Lslot+2oops", ".Lslot+09", ".Lslot+0b102", ".Lslot+4+8", ".Lslot+2147483648", ".Lslot-2147483648"} {
		t.Run("refuse/"+expression, func(t *testing.T) {
			src := ".text\n_start:\nmovq " + expression + "(%rip), %r10\n.Lslot:\n.zero 64\n"
			if refused := refusalsForX86(t, driver, runner, src); len(refused) == 0 {
				t.Fatal("malformed or unresolved expression was accepted")
			}
		})
	}
}

func gnuLinkedAddendText(t *testing.T, as, objcopy, src string) []byte {
	t.Helper()
	ld, err := exec.LookPath("ld")
	if err != nil {
		t.Skip("ld not on PATH")
	}
	dir := t.TempDir()
	source, object, image, text, script := filepath.Join(dir, "test.s"), filepath.Join(dir, "test.o"), filepath.Join(dir, "test.elf"), filepath.Join(dir, "test.text"), filepath.Join(dir, "layout.ld")
	if err := os.WriteFile(source, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	layout := "SECTIONS { .text 0 : { *(.text) } . = ALIGN(16); .rodata : { *(.rodata) } . = ALIGN(16); .bss : { *(.bss) } }\n"
	if err := os.WriteFile(script, []byte(layout), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, argv := range [][]string{{as, "--64", source, "-o", object}, {ld, "-m", "elf_x86_64", "-T", script, object, "-o", image}, {objcopy, "-O", "binary", "--only-section=.text", image, text}} {
		if out, err := exec.Command(argv[0], argv[1:]...).CombinedOutput(); err != nil {
			t.Fatalf("%v: %v\n%s", argv, err, out)
		}
	}
	out, err := os.ReadFile(text)
	if err != nil {
		t.Fatal(err)
	}
	return out
}
