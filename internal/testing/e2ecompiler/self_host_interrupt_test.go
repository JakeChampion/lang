package e2ecompiler

import (
	"os"
	"os/exec"
	"path/filepath"
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
		"unknown slot":  {"@interrupt(\"nmi\")\nfunction h(): void {}" + main, "E103", "names the slot \"nmi\""},
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
	if err == nil || !strings.Contains(string(msg), "E103") || !strings.Contains(string(msg), "only -target arm64-kernel has one") {
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
