package e2ecompiler

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"syscall"
	"testing"

	"github.com/jakechampion/lang/internal/testing/e2eharness"
)

const freestandingLibSrc = `function add(a: i32, b: i32): i32 {
  return a + b;
}

function total(n: i32): i32 {
  let xs: i32[] = [];
  let i = 0;
  while (i < n) {
    xs = xs.append(i);
    i += 1;
  }
  let t = 0;
  for x in xs {
    t += x;
  }
  return t;
}
`

var (
	freestandingSyscallRe = regexp.MustCompile(`(?m)^\s+(svc|syscall)\b`)
	freestandingStartRe   = regexp.MustCompile(`(?m)^(_start:|\s+\.globl _start$)`)
)

// The trap instruction each ISA's runtime stops at when it has no host.
var freestandingTrap = map[string]string{"x86-64-freestanding": "ud2", "arm64-freestanding": "brk #1"}

func emitFreestanding(t *testing.T, cli *selfHostCLI, target, src string, env ...string) (string, string, error) {
	t.Helper()
	dir := t.TempDir()
	in := filepath.Join(dir, "lib.fern")
	if err := os.WriteFile(in, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(dir, "lib.s")
	cmd := cli.compileCmd(target, out, in, "-export", "add,total", "-emit", "asm")
	cmd.Env = append(os.Environ(), env...)
	msg, err := cmd.CombinedOutput()
	if err != nil {
		return "", string(msg), err
	}
	asm, rerr := os.ReadFile(out)
	if rerr != nil {
		t.Fatal(rerr)
	}
	return string(asm), string(msg), nil
}

// A freestanding artifact has no process entry and makes no syscall. Its
// embedder seeds the heap and calls the exports, and every host-shaped stop
// is a trap.
func TestSelfHostFreestandingEmitsNoHost(t *testing.T) {
	cli := buildSelfHostCLI(t)
	for _, target := range []string{"x86-64-freestanding", "arm64-freestanding"} {
		t.Run(target, func(t *testing.T) {
			asm, msg, err := emitFreestanding(t, cli, target, freestandingLibSrc)
			if err != nil {
				t.Fatalf("emit: %v\n%s", err, msg)
			}
			if m := freestandingSyscallRe.FindString(asm); m != "" {
				t.Errorf("the artifact makes a syscall (%q); a freestanding target has no kernel", strings.TrimSpace(m))
			}
			if freestandingStartRe.MatchString(asm) {
				t.Error("the artifact defines _start; nothing enters a freestanding artifact as a process")
			}
			if strings.Contains(asm, "__fern_busy") {
				t.Error("the export entries take the busy lock; only one context calls a freestanding artifact")
			}
			for _, want := range []string{".globl __fern_heap_init", ".globl add", "\nadd:\n", ".globl total", "\ntotal:\n"} {
				if !strings.Contains(asm, want) {
					t.Errorf("the artifact has no %q", strings.TrimSpace(want))
				}
			}
			if !strings.Contains(asm, "__fern_report:\n    "+freestandingTrap[target]+"\n") {
				t.Errorf("__fern_report is not a %s trap", freestandingTrap[target])
			}
		})
	}
}

// A runtime switch that writes a report needs a host, so the build stops and
// names the syscall instead of emitting it.
func TestSelfHostFreestandingRefusesHostedRuntime(t *testing.T) {
	cli := buildSelfHostCLI(t)
	for _, target := range []string{"x86-64-freestanding", "arm64-freestanding"} {
		t.Run(target, func(t *testing.T) {
			_, msg, err := emitFreestanding(t, cli, target, freestandingLibSrc, "FERN_LEAKCHECK=1")
			if err == nil {
				t.Fatal("the build with FERN_LEAKCHECK=1 succeeded; its leak report needs a host")
			}
			if !strings.Contains(msg, "has no host, but this build needs syscall") {
				t.Errorf("the refusal does not name the syscall:\n%s", msg)
			}
			if strings.Contains(msg, "IR-eligible") {
				t.Errorf("the refusal is followed by an unrelated IR diagnostic:\n%s", msg)
			}
		})
	}
}

// The embedder is a C program with no libc. Its _start seeds the heap, calls
// the exports, and exits with their result.
const freestandingEmbedder = `extern void __fern_heap_init(void *base, unsigned long len);
extern int add(int a, int b);
extern int total(int n);
static _Alignas(16) unsigned char heap[1 << 20];
static void sys_exit(long code) {
  EXIT_SYSCALL
  for (;;) {}
}
void _start(void) {
  SEED
  sys_exit(RESULT);
}
`

var freestandingExit = map[string]string{
	"x86-64-freestanding": `register long rdi __asm__("rdi") = code;
  register long rax __asm__("rax") = 60;
  __asm__ volatile("syscall" : : "r"(rdi), "r"(rax));`,
	"arm64-freestanding": `register long x0 __asm__("x0") = code;
  register long x8 __asm__("x8") = 93;
  __asm__ volatile("svc #0" : : "r"(x0), "r"(x8));`,
}

var freestandingTrapSignal = map[string]syscall.Signal{"x86-64-freestanding": syscall.SIGILL, "arm64-freestanding": syscall.SIGTRAP}

func TestSelfHostFreestandingRunsInEmbedder(t *testing.T) {
	cli := buildSelfHostCLI(t)
	cases := []struct {
		name, seed, result string
		exit               int
		trap               bool
	}{
		{"exports", "__fern_heap_init(heap, sizeof heap);", "add(total(9), 6)", 42, false},
		{"alloc_before_heap_init_traps", "", "total(1000)", 0, true},
		{"heap_exhaustion_traps", "__fern_heap_init(heap, 64);", "total(1000)", 0, true},
	}
	for _, target := range []string{"x86-64-freestanding", "arm64-freestanding"} {
		t.Run(target, func(t *testing.T) {
			gcc, runner := freestandingTooling(t, cli, target)
			asm, msg, err := emitFreestanding(t, cli, target, freestandingLibSrc)
			if err != nil {
				t.Fatalf("emit: %v\n%s", err, msg)
			}
			dir := t.TempDir()
			lib := filepath.Join(dir, "lib.s")
			if err := os.WriteFile(lib, []byte(asm), 0o644); err != nil {
				t.Fatal(err)
			}
			for _, c := range cases {
				t.Run(c.name, func(t *testing.T) {
					src := strings.NewReplacer("EXIT_SYSCALL", freestandingExit[target], "SEED", c.seed, "RESULT", c.result).Replace(freestandingEmbedder)
					cfile := filepath.Join(dir, c.name+".c")
					if err := os.WriteFile(cfile, []byte(src), 0o644); err != nil {
						t.Fatal(err)
					}
					bin := filepath.Join(dir, c.name)
					if out, err := exec.Command(gcc, "-nostdlib", "-static", "-ffreestanding", "-O1", "-o", bin, cfile, lib).CombinedOutput(); err != nil {
						t.Fatalf("link the embedder: %v\n%s", err, out)
					}
					cmd := runner(bin)
					out, err := cmd.CombinedOutput()
					var exitErr *exec.ExitError
					if err != nil && !errors.As(err, &exitErr) {
						t.Fatalf("run: %v\n%s", err, out)
					}
					status := cmd.ProcessState.Sys().(syscall.WaitStatus)
					if c.trap {
						if !status.Signaled() || status.Signal() != freestandingTrapSignal[target] {
							t.Errorf("status %v, want death by %v from the runtime's trap\n%s", status, freestandingTrapSignal[target], out)
						}
						return
					}
					if status.Signaled() || status.ExitStatus() != c.exit {
						t.Errorf("status %v, want exit %d\n%s", status, c.exit, out)
					}
				})
			}
		})
	}
}

// The C compiler for the target's ISA, and the way to run its output here.
func freestandingTooling(t *testing.T, cli *selfHostCLI, target string) (string, func(string) *exec.Cmd) {
	t.Helper()
	if target == "arm64-freestanding" {
		gcc, qemu := e2eharness.Arm64Tooling(t)
		return gcc, func(bin string) *exec.Cmd { return e2eharness.RunArm64Bin(qemu, bin) }
	}
	return cli.gcc, func(bin string) *exec.Cmd { return runX86_64Bin(cli.runner, bin) }
}
