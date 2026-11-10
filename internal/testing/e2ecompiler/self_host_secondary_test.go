package e2ecompiler

import (
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/testing/e2eharness"
)

// A second core of an arm64 kernel (#14258). The board starts it with PSCI
// CPU_ON at the address that __secondary_entry gives, and the secondary entry
// calls the @secondary function with the context id of the start.
const secondaryEmitSrc = `@secondary
function idle(context: usize): void {}

function main(): i32 {
  unsafe {
    return __secondary_entry() as i32;
  }
}
`

func TestSelfHostSecondaryEntry(t *testing.T) {
	cli := buildSelfHostCLI(t)
	text := emitKernelAsm(t, cli, "arm64-kernel", kernelRAM, secondaryEmitSrc)
	wantInOrder(t, "arm64-kernel", text, []string{
		"    mrs x9, ttbr0_el1\n    adrp x10, __fern_secondary_ttbr0\n",
		"__fern_secondary_start:\n    msr daifset, #15\n    mov x19, x0\n",
		"    adrp x9, __fern_secondary_ttbr0\n    ldr x9, [x9, :lo12:__fern_secondary_ttbr0]\n    msr ttbr0_el1, x9\n",
		"    msr sctlr_el1, x9\n",
		"    str x19, [sp]\n    bl __fn_idle\n",
		".Lsecondary_park:\n",
	})
	if strings.Contains(emitKernelAsm(t, cli, "arm64-kernel", kernelRAM, "function main(): i32 {\n  return 0;\n}\n"), "__fern_secondary_start") {
		t.Error("a kernel with no second core has the secondary entry")
	}
}

func TestSelfHostSecondaryRules(t *testing.T) {
	cli := buildSelfHostCLI(t)
	dir := t.TempDir()
	const main = "\nfunction main(): i32 {\n  return 0;\n}\n"
	for name, c := range map[string]struct {
		src, code, msg string
	}{
		"no parameter":  {"@secondary\nfunction s(): void {}" + main, "E105", "must take one usize, the context id, and return void"},
		"an i32":        {"@secondary\nfunction s(n: i32): void {}" + main, "E105", "must take one usize, the context id, and return void"},
		"a result":      {"@secondary\nfunction s(c: usize): i32 {\n  return 1;\n}" + main, "E105", "must take one usize, the context id, and return void"},
		"two entries":   {"@secondary\nfunction a(c: usize): void {}\n@secondary\nfunction b(c: usize): void {}" + main, "E105", "is a second entry: \"a\" is the first"},
		"implies @norc": {"@secondary\nfunction s(c: usize): void {\n  let t = \"x\";\n}" + main, "E102", "`@secondary` function \"s\" may not use a string"},
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
	src := filepath.Join(dir, "x86.fern")
	if err := os.WriteFile(src, []byte("@secondary\nfunction s(c: usize): void {}"+main), 0o644); err != nil {
		t.Fatal(err)
	}
	msg, err := cli.compileCmd("x86-64-kernel", filepath.Join(dir, "x86.elf"), src, "-ram", kernelX86RAM).CombinedOutput()
	if err == nil || !strings.Contains(string(msg), "E105") || !strings.Contains(string(msg), "needs the secondary entry of -target arm64-kernel") {
		t.Errorf("x86-64-kernel: err %v; want E105 in:\n%s", err, msg)
	}
}

func TestSelfHostSecondaryFormatKeepsTheAttribute(t *testing.T) {
	h := selfHostCLIForHost(t)
	src := filepath.Join(t.TempDir(), "secondary.fern")
	if err := os.WriteFile(src, []byte(secondaryEmitSrc), 0o644); err != nil {
		t.Fatal(err)
	}
	out, err := exec.Command(h.cli, "-fmt", src).CombinedOutput()
	if err != nil || !strings.Contains(string(out), "@secondary\nfunction idle(context: usize): void {") {
		t.Errorf("-fmt: %v\n%s", err, out)
	}
}

// The first "done when" item of #14258: two cores increment one counter with
// __atomic_fetch_add, and the result is exact. Core 1 releases a flag when it
// is done, and core 0 waits for the flag with an acquire load.
const secondaryCountSrc = `import "./board";

const ROUNDS: i64 = 200000;

@secondary
function count(shared: usize): void {
  unsafe {
    let i = 0i64;
    while (i < ROUNDS) {
      let old = __atomic_fetch_add(shared, 1i64);
      i += 1i64;
    }
    __atomic_store_release(shared + 8 as usize, 1i64);
  }
}

function main(): i32 {
  unsafe {
    let shared = __alloc(16) as usize;
    __store_i64(shared, 0i64);
    __store_i64(shared + 8 as usize, 0i64);
    // PSCI CPU_ON for the core of MPIDR 1.
    let started = __hvc(0xc4000003i64, 1i64, __secondary_entry() as i64, shared as i64);
    let i = 0i64;
    while (i < ROUNDS) {
      let old = __atomic_fetch_add(shared, 1i64);
      i += 1i64;
    }
    while (__atomic_load_acquire(shared + 8 as usize) == 0i64) {}
    print("cpu_on " + started.to_string() + ", counter " + __atomic_load_acquire(shared).to_string());
  }
  board.power_off_hvc();
  return 0;
}
`

func TestSelfHostKernelSecondaryCountsOnQemu(t *testing.T) {
	qemu := e2eharness.QemuSystem(t, "qemu-system-aarch64")
	cli := buildSelfHostCLI(t)
	src := writeTickProgram(t, kernelBoardSrc, secondaryCountSrc)
	out := filepath.Join(t.TempDir(), "kernel.elf")
	if msg, err := cli.compileCmd("arm64-kernel", out, src, "-ram", kernelRAM).CombinedOutput(); err != nil {
		t.Fatalf("build: %v\n%s", err, msg)
	}
	console, err := bootQemu(t, qemu, "-M", "virt", "-cpu", "cortex-a76", "-smp", "2", "-m", "128M",
		"-nographic", "-monitor", "none", "-nic", "none", "-kernel", out)
	if err != nil {
		t.Fatalf("qemu: %v; console:\n%s", err, console)
	}
	if !strings.Contains(string(console), "cpu_on 0, counter 400000\n") {
		t.Errorf("console:\n%s\nwant \"cpu_on 0, counter 400000\"", console)
	}
}
