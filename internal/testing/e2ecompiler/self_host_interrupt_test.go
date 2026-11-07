package e2ecompiler

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
)

const interruptSrc = `@interrupt("irq")
function on_irq(): void {
  unsafe {
    __sysreg_write("tpidr_el1", 1i64);
  }
}

@interrupt("sync")
function on_sync(): void {
  unsafe {
    __sysreg_write("elr_el1", __sysreg_read("elr_el1") + 4i64);
  }
}

function main(): i32 {
  return 0;
}
`

func TestSelfHostInterruptVectorTable(t *testing.T) {
	cli := buildSelfHostCLI(t)
	text := emitKernelAsm(t, cli, "arm64-kernel", kernelRAM, interruptSrc)
	wantInOrder(t, "arm64-kernel", text, []string{
		"    adrp x9, __fern_vectors\n    add x9, x9, :lo12:__fern_vectors\n    msr vbar_el1, x9\n    isb\n",
		"    .p2align 11\n__fern_vectors:\n",
	})
	i := strings.Index(text, "__fern_vectors:\n")
	var slots []string
	for _, line := range strings.Split(text[i:], "\n") {
		if strings.HasPrefix(line, "    b ") {
			slots = append(slots, strings.TrimPrefix(line, "    b "))
		}
		if len(slots) == 16 {
			break
		}
	}
	for k, got := range slots {
		want := ".Lvector_park"
		switch k {
		case 4:
			want = "__fern_vector_sync"
		case 5:
			want = "__fern_vector_irq"
		}
		if got != want {
			t.Errorf("vector slot %d branches to %q, want %q", k, got, want)
		}
	}
	for _, slot := range []string{"sync", "irq"} {
		wantInOrder(t, "arm64-kernel", text, []string{
			"__fern_vector_" + slot + ":\n    sub sp, sp, #576\n    stp x0, x1, [sp, #0]\n",
			"    stp q30, q31, [sp, #528]\n    str x30, [sp, #160]\n    mrs x9, fpsr\n",
			"    bl __fn_on_" + slot + "\n",
			"    add sp, sp, #576\n    eret\n",
		})
	}
	if strings.Contains(emitKernelAsm(t, cli, "arm64-kernel", kernelRAM, "function main(): i32 {\n  return 0;\n}\n"), "vbar_el1") {
		t.Error("a kernel without a handler writes vbar_el1")
	}
}

func TestSelfHostInterruptRules(t *testing.T) {
	cli := buildSelfHostCLI(t)
	dir := t.TempDir()
	const main = "\nfunction main(): i32 {\n  return 0;\n}\n"
	for name, c := range map[string]struct {
		src, code, msg string
	}{
		"unknown slot":  {"@interrupt(\"nmi2\")\nfunction h(): void {}" + main, "E103", "slot \"nmi2\""},
		"slot twice":    {"@interrupt(\"irq\")\nfunction a(): void {}\n@interrupt(\"irq\")\nfunction b(): void {}" + main, "E103", "which another handler already takes"},
		"a parameter":   {"@interrupt(\"irq\")\nfunction h(n: i32): void {}" + main, "E103", "must take no parameters and return void"},
		"a result":      {"@interrupt(\"irq\")\nfunction h(): i32 {\n  return 1;\n}" + main, "E103", "must take no parameters and return void"},
		"implies @norc": {"@interrupt(\"irq\")\nfunction h(): void {\n  let s = \"x\";\n}" + main, "E102", "`@interrupt` function \"h\" may not use a string"},
	} {
		src := filepath.Join(dir, strings.ReplaceAll(name, " ", "_")+".fern")
		if err := os.WriteFile(src, []byte(c.src), 0o644); err != nil {
			t.Fatal(err)
		}
		msg, err := cli.compileCmd("arm64-kernel", filepath.Join(dir, "out.elf"), src, "-ram", kernelRAM).CombinedOutput()
		if err == nil || !strings.Contains(string(msg), c.code) || !strings.Contains(string(msg), c.msg) {
			t.Errorf("%s: err %v; want %s %q in:\n%s", name, err, c.code, c.msg, msg)
		}
		if got := goCheckerCodes(t, dir, c.src); !slices.Contains(got, c.code) {
			t.Errorf("%s: Go checker codes = %v, want %s", name, got, c.code)
		}
	}
	src := filepath.Join(dir, "hosted.fern")
	if err := os.WriteFile(src, []byte(interruptSrc), 0o644); err != nil {
		t.Fatal(err)
	}
	msg, err := cli.compileCmd("arm64-linux", filepath.Join(dir, "hosted"), src).CombinedOutput()
	if err == nil || !strings.Contains(string(msg), "E103") || !strings.Contains(string(msg), "only -target arm64-kernel and -target x86-64-kernel have one") {
		t.Errorf("arm64-linux: err %v; want E103 in:\n%s", err, msg)
	}
}

func TestSelfHostInterruptFormatKeepsTheSlot(t *testing.T) {
	h := selfHostCLIForHost(t)
	src := filepath.Join(t.TempDir(), "irq.fern")
	if err := os.WriteFile(src, []byte(interruptSrc), 0o644); err != nil {
		t.Fatal(err)
	}
	out, err := exec.Command(h.cli, "-fmt", src).CombinedOutput()
	if err != nil || !strings.Contains(string(out), "@interrupt(\"irq\")\nfunction on_irq(): void {") || !strings.Contains(string(out), "@interrupt(\"sync\")\nfunction on_sync(): void {") {
		t.Errorf("-fmt: %v\n%s", err, out)
	}
}

func TestSelfHostArm64TextAlignment(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	bin := buildAsmBenchDriver(t, gcc)
	if refused := refusalsFor(t, bin, runner, ".text\n_start:\n    nop\n    .p2align 2\n    nop\n"); len(refused) > 0 {
		t.Errorf(".p2align 2 in .text: refused (%s)", strings.Join(refused, ", "))
	}
	if refused := refusalsFor(t, bin, runner, ".text\n_start:\n    nop\n    .p2align 4\n    nop\n"); len(refused) == 0 {
		t.Error(".p2align 4 in .text without a known origin: the assembler accepts it")
	}
}

const interruptX86Src = `@interrupt("bp")
function on_bp(): void {
  unsafe {
    __sysreg_write("kernel_gs_base", 1i64);
  }
}

@interrupt("gp")
function on_gp(): void {
  unsafe {
    __sysreg_write("kernel_gs_base", 2i64);
  }
}

@interrupt("32")
function on_timer(): void {
}

function main(): i32 {
  unsafe {
    __breakpoint();
  }
  return 0;
}
`

func TestSelfHostInterruptIDT(t *testing.T) {
	cli := buildSelfHostCLI(t)
	text := emitKernelAsm(t, cli, "x86-64-kernel", kernelX86RAM, interruptX86Src)
	wantInOrder(t, "x86-64-kernel", text, []string{
		"    .section .bss\n    .p2align 4\n__fern_idt:\n    .skip 4096\n",
		".Lidt_fill:\n    leaq .Lvector_park(%rip), %rax\n    call __fern_idt_gate\n",
		"    addq $48, %rdi\n    leaq __fern_vector_bp(%rip), %rax\n",
		"    addq $208, %rdi\n    leaq __fern_vector_gp(%rip), %rax\n",
		"    addq $512, %rdi\n    leaq __fern_vector_32(%rip), %rax\n",
		"    lidt (%rsp)\n",
		"    call __fn_main\n",
	})
	wantInOrder(t, "x86-64-kernel", text, []string{
		"__fern_idt_gate:\n    movl %eax, %edx\n    andl $65535, %edx\n    orl $524288, %edx\n",
		"    orl $36352, %edx\n",
		"__fern_vector_bp:\n    pushq %rax\n",
		"    subq $512, %rsp\n    vmovdqu %ymm0, 0(%rsp)\n",
		"    cld\n    call __fn_on_bp\n",
		"    addq $512, %rsp\n",
		"    popq %rax\n    iretq\n",
		"__fern_vector_gp:\n",
		"    subq $520, %rsp\n",
		"    popq %rax\n    addq $8, %rsp\n    iretq\n",
	})
	if !strings.Contains(text, "    int3\n") {
		t.Error("__breakpoint does not emit int3")
	}
	if strings.Contains(emitKernelAsm(t, cli, "x86-64-kernel", kernelX86RAM, "function main(): i32 {\n  return 0;\n}\n"), "lidt") {
		t.Error("a kernel without a handler loads an IDT")
	}
	if !strings.Contains(emitKernelAsm(t, cli, "arm64-kernel", kernelRAM, "function main(): i32 {\n  unsafe {\n    __breakpoint();\n  }\n  return 0;\n}\n"), "    brk #0\n") {
		t.Error("__breakpoint does not emit brk #0 on arm64")
	}
}

// The CPU pushes an interrupt frame onto the current stack, so kernel code must
// keep no data below %rsp: x86-64 kernel code has no red zone.
func TestSelfHostKernelX86UsesNoRedZone(t *testing.T) {
	cli := buildSelfHostCLI(t)
	asmPath := filepath.Join(t.TempDir(), "kernel.s")
	if msg, err := cli.compileCmd("x86-64-kernel", asmPath, writeKernelX86Program(t), "-ram", kernelX86RAM, "-emit", "asm").CombinedOutput(); err != nil {
		t.Fatalf("emit: %v\n%s", err, msg)
	}
	asm, err := os.ReadFile(asmPath)
	if err != nil {
		t.Fatal(err)
	}
	text := string(asm)
	below := regexp.MustCompile(`[^0-9a-z_]-[0-9]+\(%rsp\)`)
	if m := below.FindString(text); m != "" {
		t.Errorf("x86-64-kernel code addresses memory below %%rsp: %q", m)
	}
}

func TestSelfHostInterruptSlotOfTheTarget(t *testing.T) {
	cli := buildSelfHostCLI(t)
	dir := t.TempDir()
	for _, c := range []struct{ target, ram, slot string }{
		{"x86-64-kernel", kernelX86RAM, "irq"},
		{"arm64-kernel", kernelRAM, "bp"},
		{"x86-64-kernel", kernelX86RAM, "31"},
	} {
		src := filepath.Join(dir, c.slot+".fern")
		prog := "@interrupt(\"" + c.slot + "\")\nfunction h(): void {}\n\nfunction main(): i32 {\n  return 0;\n}\n"
		if err := os.WriteFile(src, []byte(prog), 0o644); err != nil {
			t.Fatal(err)
		}
		msg, err := cli.compileCmd(c.target, filepath.Join(dir, "out.elf"), src, "-ram", c.ram).CombinedOutput()
		if err == nil || !strings.Contains(string(msg), "E103") {
			t.Errorf("%s with slot %q: err %v; want E103 in:\n%s", c.target, c.slot, err, msg)
		}
	}
	for _, target := range []string{"arm64-linux", "x86-64-linux"} {
		src := filepath.Join(dir, "bp.fern")
		if err := os.WriteFile(src, []byte("function main(): i32 {\n  unsafe {\n    __breakpoint();\n  }\n  return 0;\n}\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		msg, err := cli.compileCmd(target, filepath.Join(dir, "bp-"+target), src).CombinedOutput()
		if err == nil || !strings.Contains(string(msg), "E066") || !strings.Contains(string(msg), "__breakpoint") {
			t.Errorf("%s: err %v; want E066 naming __breakpoint in:\n%s", target, err, msg)
		}
	}
}

func TestSelfHostInterruptX86FormsMatchGas(t *testing.T) {
	compareFormCases(t, []string{
		"vmovdqu %ymm0, 0(%rsp)", "vmovdqu %ymm15, 480(%rsp)", "vmovdqu 480(%rsp), %ymm15", "vmovdqu 256(%rsp), %ymm8",
		"pushq %r11", "popq %r8", "decl %ecx", "andl $-65536, %edx", "orl $36352, %edx", "orl $524288, %edx",
		"movl %edx, 8(%rdi)", "movl $0, 12(%rdi)", "movl $4095, (%rsp)", "movq %rax, 2(%rsp)", "shrq $32, %rdx",
	})
}
