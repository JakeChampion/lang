package e2ecompiler

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/testing/e2eharness"
)

// The atomics of #14258: an acquire load, a release store, and a sequentially
// consistent fetch-and-add and compare-and-swap of the 64-bit word at an
// address.
const atomicsSrc = `function main(): i32 {
  unsafe {
    let p = __alloc(16) as usize;
    __atomic_store_release(p, 5i64);
    let old = __atomic_fetch_add(p, 2i64);
    let seen = __atomic_cas(p, 7i64, 9i64);
    return (__atomic_load_acquire(p) + old + seen) as i32;
  }
}
`

func TestSelfHostAtomicsEmit(t *testing.T) {
	cli := buildSelfHostCLI(t)
	wantInOrder(t, "arm64-kernel", emitKernelAsm(t, cli, "arm64-kernel", kernelRAM, atomicsSrc), []string{
		"    stlr x10, [x9]\n",
		"    ldaddal x10, x0, [x9]\n",
		"    casal x0, x10, [x9]\n",
		"    ldar x0, [x9]\n",
	})
	wantInOrder(t, "x86-64-kernel", emitKernelAsm(t, cli, "x86-64-kernel", kernelX86RAM, atomicsSrc), []string{
		"    movq %rax, (%rcx)\n",
		"    lock xaddq %rax, (%rcx)\n",
		"    lock cmpxchgq %rdx, (%rcx)\n",
		"    movq (%rcx), %rax\n",
	})
}

// A message handoff: the data store comes before the release store of the
// flag, and the data load after the acquire load that sees the flag. No pass
// may move a plain access across an atomic one, or the receiver can read the
// data before the sender wrote it.
const atomicsOrderSrc = `@noinline
function send(data: usize, flag: usize): void {
  unsafe {
    __store_i64(data, 42i64);
    __atomic_store_release(flag, 1i64);
  }
}

@noinline
function receive(data: usize, flag: usize): i64 {
  unsafe {
    while (__atomic_load_acquire(flag) == 0i64) {}
    return __load_i64(data);
  }
}

function main(): i32 {
  unsafe {
    let p = __alloc(16) as usize;
    send(p, p + 8 as usize);
    return receive(p, p + 8 as usize) as i32;
  }
}
`

// The body of the function at label in the assembly text.
func asmBody(t *testing.T, text, label string) string {
	t.Helper()
	i := strings.Index(text, label+":\n")
	if i < 0 {
		t.Fatalf("no %s in the assembly", label)
	}
	body := text[i+len(label)+2:]
	if j := strings.Index(body, ".cfi_endproc"); j >= 0 {
		body = body[:j]
	}
	return body
}

func TestSelfHostAtomicsKeepOrder(t *testing.T) {
	cli := buildSelfHostCLI(t)
	for _, c := range []struct {
		target, ram, release, acquire string
		store, load                   *regexp.Regexp
	}{
		{"arm64-kernel", kernelRAM, "    stlr ", "    ldar ", regexp.MustCompile(`\n    str x\d+, \[x\d+`), regexp.MustCompile(`\n    ldr x\d+, \[x\d+`)},
		{"x86-64-kernel", kernelX86RAM, "    movq %rax, (%rcx)\n", "    movq (%rcx), %rax\n", regexp.MustCompile(`\n    movq %\w+, \d*\(%\w+\)\n`), regexp.MustCompile(`\n    movq \d*\(%\w+\), %\w+\n`)},
	} {
		text := emitKernelAsm(t, cli, c.target, c.ram, atomicsOrderSrc)
		send := asmBody(t, text, "__fn_send")
		r := strings.Index(send, c.release)
		if s := c.store.FindStringIndex(send); r < 0 || s == nil || s[0] > r {
			t.Errorf("%s: send has no data store before the release:\n%s", c.target, send)
		}
		receive := asmBody(t, text, "__fn_receive")
		a := strings.Index(receive, c.acquire)
		if a < 0 || !c.load.MatchString(receive[a+len(c.acquire):]) {
			t.Errorf("%s: receive has no data load after the acquire:\n%s", c.target, receive)
		}
	}
}

// The atomics need an unsafe block, as the other raw accesses do, and a kernel
// target: a hosted target refuses them at the capability gate.
func TestSelfHostAtomicsRefusals(t *testing.T) {
	cli := buildSelfHostCLI(t)
	dir := t.TempDir()
	const outside = "function main(): i32 {\n  return __atomic_load_acquire(0x1000 as usize) as i32;\n}\n"
	bare := filepath.Join(dir, "outside.fern")
	if err := os.WriteFile(bare, []byte(outside), 0o644); err != nil {
		t.Fatal(err)
	}
	msg, err := cli.compileCmd("arm64-kernel", filepath.Join(dir, "outside.elf"), bare, "-ram", kernelRAM).CombinedOutput()
	if err == nil || !strings.Contains(string(msg), "E095") {
		t.Errorf("outside unsafe: err %v; want E095 in:\n%s", err, msg)
	}
	if got := goCheckerCodes(t, t.TempDir(), outside); !slices.Contains(got, "E095") {
		t.Errorf("outside unsafe: Go checker codes = %v, want E095", got)
	}
	src := filepath.Join(dir, "main.fern")
	if err := os.WriteFile(src, []byte(atomicsSrc), 0o644); err != nil {
		t.Fatal(err)
	}
	msg, err = cli.compileCmd("arm64-linux", filepath.Join(dir, "hosted"), src).CombinedOutput()
	if err == nil || !strings.Contains(string(msg), "E066") || !strings.Contains(string(msg), "__atomic_fetch_add") {
		t.Errorf("arm64-linux: err %v; want E066 for the atomics in:\n%s", err, msg)
	}
}

// The values on one core: fetch_add and cas give the old word, a cas with a
// stale expected word changes nothing, and the acquire load sees the last
// write.
const atomicsBootSrc = `import "./board";

function show(): void {
  unsafe {
    let p = __alloc(16) as usize;
    __atomic_store_release(p, 5i64);
    let a = __atomic_fetch_add(p, 2i64);
    let b = __atomic_cas(p, 7i64, 9i64);
    let c = __atomic_cas(p, 7i64, 11i64);
    let d = __atomic_load_acquire(p);
    print("atomics " + a.to_string() + " " + b.to_string() + " " + c.to_string() + " " + d.to_string());
  }
}
`

func TestSelfHostKernelAtomicsOnQemu(t *testing.T) {
	qemu := e2eharness.QemuSystem(t, "qemu-system-aarch64")
	cli := buildSelfHostCLI(t)
	src := writeTickProgram(t, kernelBoardSrc, atomicsBootSrc+"\nfunction main(): i32 {\n  show();\n  board.power_off_hvc();\n  return 0;\n}\n")
	out := filepath.Join(t.TempDir(), "kernel.elf")
	if msg, err := cli.compileCmd("arm64-kernel", out, src, "-ram", kernelRAM).CombinedOutput(); err != nil {
		t.Fatalf("build: %v\n%s", err, msg)
	}
	console, err := bootQemu(t, qemu, "-M", "virt", "-cpu", "cortex-a76", "-m", "128M",
		"-nographic", "-monitor", "none", "-nic", "none", "-kernel", out)
	if err != nil {
		t.Fatalf("qemu: %v; console:\n%s", err, console)
	}
	if !strings.Contains(string(console), "atomics 5 7 9 9\n") {
		t.Errorf("console:\n%s\nwant \"atomics 5 7 9 9\"", console)
	}
}

// main exits through isa-debug-exit with 16, so qemu exits with 33.
func TestSelfHostKernelX86AtomicsOnQemu(t *testing.T) {
	qemu := e2eharness.QemuSystem(t, "qemu-system-x86_64")
	cli := buildSelfHostCLI(t)
	src := writeTickProgram(t, kernelX86BoardSrc, atomicsBootSrc+"\nfunction main(): i32 {\n  show();\n  board.exit_qemu(16);\n  return 0;\n}\n")
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
	if !strings.Contains(string(console), "atomics 5 7 9 9\n") {
		t.Errorf("console:\n%s\nwant \"atomics 5 7 9 9\"", console)
	}
}
