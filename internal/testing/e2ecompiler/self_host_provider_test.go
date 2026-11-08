package e2ecompiler

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
)

// The board's UART is a buffer that the embedder links at a fixed address. Its
// first word counts the bytes that the provider wrote after the first 8 bytes.
const providerBoardSrc = `const UART: usize = 0x10000000;

@provide("log")
pub function uart_write(data: usize, len: i32): void {
  unsafe {
    let at = __mmio_load32(UART);
    let i = 0;
    while (i < len) {
      __mmio_store8(UART + 8 + (at + i) as usize, __load_u8(data + i as usize));
      i += 1;
    }
    __mmio_store32(UART, at + len);
  }
}
`

const providerLibSrc = `import "./board";

function hello(n: i32): i32 {
  print("hello");
  eprint("n=" + n.to_string() + "\n");
  return n + 1;
}
`

// The embedder seeds the heap, calls hello, writes the UART's bytes to stdout
// and exits with the result.
const providerEmbedder = `extern void __fern_heap_init(void *base, unsigned long len);
extern int hello(int n);
static _Alignas(16) unsigned char heap[1 << 20];
__attribute__((section(".uart"), used)) volatile unsigned char uart[256];
SYS3
void _start(void) {
  __fern_heap_init(heap, sizeof heap);
  int r = hello(41);
  sys3(WRITE, 1, (long)(uart + 8), *(volatile int *)uart);
  sys3(EXIT, r, 0, 0);
  for (;;) {}
}
`

var providerSys3 = map[string][3]string{
	"x86-64-freestanding": {`static long sys3(long n, long a, long b, long c) {
  register long rax __asm__("rax") = n;
  register long rdi __asm__("rdi") = a;
  register long rsi __asm__("rsi") = b;
  register long rdx __asm__("rdx") = c;
  __asm__ volatile("syscall" : "+r"(rax) : "r"(rdi), "r"(rsi), "r"(rdx) : "rcx", "r11", "memory");
  return rax;
}`, "1", "60"},
	"arm64-freestanding": {`static long sys3(long n, long a, long b, long c) {
  register long x8 __asm__("x8") = n;
  register long x0 __asm__("x0") = a;
  register long x1 __asm__("x1") = b;
  register long x2 __asm__("x2") = c;
  __asm__ volatile("svc #0" : "+r"(x0) : "r"(x8), "r"(x1), "r"(x2) : "memory");
  return x0;
}`, "64", "93"},
}

func writeProviderProgram(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	for name, src := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(src), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

// A freestanding program that imports a board with a log provider can call
// print and eprint. Their bytes reach the provider, and the artifact makes no
// syscall.
func TestSelfHostProviderSuppliesLog(t *testing.T) {
	cli := buildSelfHostCLI(t)
	dir := writeProviderProgram(t, map[string]string{"board.fern": providerBoardSrc, "lib.fern": providerLibSrc})
	for _, target := range []string{"x86-64-freestanding", "arm64-freestanding"} {
		t.Run(target, func(t *testing.T) {
			gcc, runner := freestandingTooling(t, cli, target)
			lib := filepath.Join(t.TempDir(), "lib.s")
			if msg, err := cli.compileCmd(target, lib, filepath.Join(dir, "lib.fern"), "-export", "hello", "-emit", "asm").CombinedOutput(); err != nil {
				t.Fatalf("emit: %v\n%s", err, msg)
			}
			asm, err := os.ReadFile(lib)
			if err != nil {
				t.Fatal(err)
			}
			if m := freestandingSyscallRe.FindString(string(asm)); m != "" {
				t.Errorf("the artifact makes a syscall (%q); print must reach the provider", strings.TrimSpace(m))
			}
			sys := providerSys3[target]
			src := strings.NewReplacer("SYS3", sys[0], "WRITE", sys[1], "EXIT", sys[2]).Replace(providerEmbedder)
			cfile := filepath.Join(filepath.Dir(lib), "embed.c")
			if err := os.WriteFile(cfile, []byte(src), 0o644); err != nil {
				t.Fatal(err)
			}
			bin := filepath.Join(filepath.Dir(lib), "embed")
			if out, err := exec.Command(gcc, "-nostdlib", "-static", "-ffreestanding", "-O1", "-Wl,--section-start=.uart=0x10000000", "-o", bin, cfile, lib).CombinedOutput(); err != nil {
				t.Fatalf("link the embedder: %v\n%s", err, out)
			}
			cmd := runner(bin)
			out, _ := cmd.Output()
			status := cmd.ProcessState.Sys().(syscall.WaitStatus)
			if status.Signaled() || status.ExitStatus() != 42 || string(out) != "hello\nn=41\n" {
				t.Errorf("status %v, UART %q; want exit 42 and %q", status, out, "hello\nn=41\n")
			}
		})
	}
}

// Without the board, print needs a capability that the target does not grant.
func TestSelfHostProviderAbsentIsE066(t *testing.T) {
	cli := buildSelfHostCLI(t)
	dir := writeProviderProgram(t, map[string]string{"lib.fern": strings.Replace(providerLibSrc, "import \"./board\";\n", "", 1)})
	out, err := cli.compileCmd("arm64-freestanding", filepath.Join(dir, "lib.s"), filepath.Join(dir, "lib.fern"), "-export", "hello", "-emit", "asm").CombinedOutput()
	if err == nil {
		t.Fatal("the build without a log provider succeeded")
	}
	if !strings.Contains(string(out), "error[E066]") || !strings.Contains(string(out), "or import a module with a `@provide(\"log\")` function") {
		t.Errorf("want an E066 that names @provide:\n%s", out)
	}
}

func TestSelfHostProviderProblemsAreE101(t *testing.T) {
	cli := buildSelfHostCLI(t)
	dir := writeProviderProgram(t, map[string]string{"bad.fern": `@provide("now")
function a(data: usize, len: i32): void {}
@provide("log")
function b(data: string): void {}
@provide("log")
function c(data: usize, len: i32): void {}
@provide("log")
function d(data: usize, len: i32): void {}
@provide("exit")
function e(code: i64): void {}
function main(): i32 {
  return 0;
}
`})
	out, err := cli.compileCmd("x86-64-linux", filepath.Join(dir, "bad"), filepath.Join(dir, "bad.fern")).CombinedOutput()
	if err == nil {
		t.Fatal("the build with four provider problems succeeded")
	}
	for _, want := range []string{
		"bad.fern:2:1: error[E101]: `@provide(\"now\")`: no capability `now` takes a provider; a provider can implement `log` or `exit`",
		"bad.fern:4:1: error[E101]: the `log` provider \"b\" must be a function with the signature (data: usize, len: i32): void",
		"bad.fern:8:1: error[E101]: capability `log` has two providers, \"c\" and \"d\"",
		"bad.fern:10:1: error[E101]: the `exit` provider \"e\" must be a function with the signature (code: i32): void",
	} {
		if !strings.Contains(string(out), want) {
			t.Errorf("no %q in:\n%s", want, out)
		}
	}
}

// A target that grants log keeps its own implementation, and the provider is
// an ordinary function.
func TestSelfHostProviderIgnoredWhereTargetGrantsLog(t *testing.T) {
	cli := buildSelfHostCLI(t)
	dir := writeProviderProgram(t, map[string]string{"board.fern": providerBoardSrc, "main.fern": `import "./board";

function main(): i32 {
  print("host");
  return 0;
}
`})
	cmd := runX86_64Bin(cli.runner, cli.x86Binary(t, filepath.Join(dir, "main.fern")))
	out, err := cmd.CombinedOutput()
	if err != nil || string(out) != "host\n" {
		t.Errorf("run: %v, output %q, want %q", err, out, "host\n")
	}
}
