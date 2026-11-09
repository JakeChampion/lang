package e2ecompiler

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/testing/e2eharness"
)

// The early-setup hook of a kernel image (#14253): the reset entry calls the
// one `@early` function after the stack, .bss, the translation and the vector
// table or IDT exist, and before the heap starts.
const earlySrc = `@early
function setup(): void {
  unsafe {
    __sysreg_write("tpidr_el1", 77i64);
  }
}

function main(): i32 {
  return 0;
}
`

func TestSelfHostEarlyHookOrder(t *testing.T) {
	cli := buildSelfHostCLI(t)
	arm := emitKernelAsm(t, cli, "arm64-kernel", kernelRAM, earlySrc)
	wantInOrder(t, "arm64-kernel", arm, []string{
		"    msr sctlr_el1, x9\n    isb\n",
		"    bl __fn_setup\n    adrp x1, __fern_bss_end\n    add x1, x1, :lo12:__fern_bss_end\n",
		"    bl __fern_heap_init\n",
		"    bl __fn_main\n",
	})
	x86 := emitKernelAsm(t, cli, "x86-64-kernel", kernelX86RAM, strings.ReplaceAll(earlySrc, "tpidr_el1", "kernel_gs_base"))
	wantInOrder(t, "x86-64-kernel", x86, []string{
		"    rep stosb\n",
		"    call __fn_setup\n",
		"    call __fern_heap_init\n",
		"    call __fn_main\n",
	})
	none := "function main(): i32 {\n  return 0;\n}\n"
	if strings.Contains(emitKernelAsm(t, cli, "arm64-kernel", kernelRAM, none), "__fn_setup") {
		t.Error("a kernel without a hook calls one")
	}
}

func TestSelfHostEarlyHookRules(t *testing.T) {
	cli := buildSelfHostCLI(t)
	dir := t.TempDir()
	const main = "\nfunction main(): i32 {\n  return 0;\n}\n"
	for name, c := range map[string]struct {
		src, code, msg string
	}{
		"a parameter":   {"@early\nfunction h(n: i32): void {}" + main, "E104", "must take no parameters and return void"},
		"a result":      {"@early\nfunction h(): i32 {\n  return 1;\n}" + main, "E104", "must take no parameters and return void"},
		"two hooks":     {"@early\nfunction a(): void {}\n@early\nfunction b(): void {}" + main, "E104", "is a second hook: \"a\" is the first"},
		"implies @norc": {"@early\nfunction h(): void {\n  let s = \"x\";\n}" + main, "E102", "`@early` function \"h\" may not use a string"},
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
	if err := os.WriteFile(src, []byte(earlySrc), 0o644); err != nil {
		t.Fatal(err)
	}
	msg, err := cli.compileCmd("arm64-linux", filepath.Join(dir, "hosted"), src).CombinedOutput()
	if err == nil || !strings.Contains(string(msg), "E104") || !strings.Contains(string(msg), "needs a reset entry, and only -target arm64-kernel and -target x86-64-kernel have one") {
		t.Errorf("arm64-linux: err %v; want E104 in:\n%s", err, msg)
	}
}

func TestSelfHostEarlyFormatKeepsTheAttribute(t *testing.T) {
	h := selfHostCLIForHost(t)
	src := filepath.Join(t.TempDir(), "early.fern")
	if err := os.WriteFile(src, []byte(earlySrc), 0o644); err != nil {
		t.Fatal(err)
	}
	out, err := exec.Command(h.cli, "-fmt", src).CombinedOutput()
	if err != nil || !strings.Contains(string(out), "@early\nfunction setup(): void {") {
		t.Errorf("-fmt: %v\n%s", err, out)
	}
}

// The hook records a value in a system register and takes a breakpoint,
// which the handler counts and steps over. main prints both, so the hook ran
// first, and the vector table or the IDT was in place when it ran.
const earlyBootMainSrc = `import "./board";

@interrupt("sync")
function on_sync(): void {
  unsafe {
    __sysreg_write("tpidrro_el0", __sysreg_read("tpidrro_el0") + 1i64);
    __sysreg_write("elr_el1", __sysreg_read("elr_el1") + 4i64);
  }
}

@early
function setup(): void {
  unsafe {
    __sysreg_write("tpidr_el1", 77i64);
    __breakpoint();
  }
}

function main(): i32 {
  unsafe {
    print("early wrote " + __sysreg_read("tpidr_el1").to_string() + ", traps " + __sysreg_read("tpidrro_el0").to_string());
  }
  board.power_off_hvc();
  return 0;
}
`

// int3 is a trap, so the handler returns to the next instruction.
const earlyBootX86MainSrc = `import "./board";

@interrupt("bp")
function on_bp(): void {
  unsafe {
    __sysreg_write("kernel_gs_base", __sysreg_read("kernel_gs_base") + 100i64);
  }
}

@early
function setup(): void {
  unsafe {
    __sysreg_write("kernel_gs_base", 77i64);
    __breakpoint();
  }
}

function main(): i32 {
  unsafe {
    print("early wrote and trapped " + __sysreg_read("kernel_gs_base").to_string());
  }
  board.exit_qemu(16);
  return 0;
}
`

func TestSelfHostKernelEarlyHookOnQemu(t *testing.T) {
	qemu := e2eharness.QemuSystem(t, "qemu-system-aarch64")
	cli := buildSelfHostCLI(t)
	src := writeTickProgram(t, kernelBoardSrc, earlyBootMainSrc)
	out := filepath.Join(t.TempDir(), "kernel.elf")
	if msg, err := cli.compileCmd("arm64-kernel", out, src, "-ram", kernelRAM).CombinedOutput(); err != nil {
		t.Fatalf("build: %v\n%s", err, msg)
	}
	console, err := bootQemu(t, qemu, "-M", "virt", "-cpu", "cortex-a76", "-m", "128M",
		"-nographic", "-monitor", "none", "-nic", "none", "-kernel", out)
	if err != nil {
		t.Fatalf("qemu: %v; console:\n%s", err, console)
	}
	if !strings.Contains(string(console), "early wrote 77, traps 1\n") {
		t.Errorf("console:\n%s\nwant \"early wrote 77, traps 1\"", console)
	}
}

// main exits through isa-debug-exit with 16, so qemu exits with 33.
func TestSelfHostKernelX86EarlyHookOnQemu(t *testing.T) {
	qemu := e2eharness.QemuSystem(t, "qemu-system-x86_64")
	cli := buildSelfHostCLI(t)
	src := writeTickProgram(t, kernelX86BoardSrc, earlyBootX86MainSrc)
	out := filepath.Join(t.TempDir(), "kernel.elf")
	if msg, err := cli.compileCmd("x86-64-kernel", out, src, "-ram", kernelX86RAM).CombinedOutput(); err != nil {
		t.Fatalf("build: %v\n%s", err, msg)
	}
	console, err := bootQemu(t, qemu, "-M", "microvm", "-cpu", "max", "-m", "128M",
		"-nodefaults", "-no-user-config", "-nographic", "-serial", "stdio",
		"-device", "isa-debug-exit,iobase=0xf4,iosize=4", "-kernel", out, "-append", "fern=pvh")
	var exit *exec.ExitError
	if !errors.As(err, &exit) || exit.ExitCode() != 33 {
		t.Fatalf("qemu: %v, want exit status 33 from isa-debug-exit; console:\n%s", err, console)
	}
	if !strings.Contains(string(console), "early wrote and trapped 177\n") {
		t.Errorf("console:\n%s\nwant \"early wrote and trapped 177\"", console)
	}
}
