//go:build linux

package e2e

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/jakechampion/lang/internal/testing/e2eharness"
)

const entropyReadSource = `import "std/rand";
function main(): i32 {
  let bs: u8[] = rand.bytes(257);
  if (bs.len() != 257) { return 2; }
  let i: i32 = 0;
  while (i < bs.len()) {
    if (bs[i] != 90 as u8) { return 3; }
    i = i + 1;
  }
  if (rand.next_i32() != 1515870810) { return 4; }
  print("entropy complete");
  return 0;
}`

func TestSelfHostEntropyReadCompletion(t *testing.T) {
	cli := e2eharness.SelfHostCLI(t)
	targets := []string{e2eharness.HostSelfHostTarget()}
	if runtime.GOARCH == "amd64" {
		targets = append(targets, "arm64-linux")
	}
	for _, target := range targets {
		t.Run(target, func(t *testing.T) {
			var gcc string
			var runner []string
			if target == "arm64-linux" {
				var qemu string
				gcc, qemu = e2eharness.Arm64Tooling(t)
				if qemu != "" {
					runner = []string{qemu}
				}
			} else {
				gcc, _ = e2eharness.X86_64Tooling(t)
			}
			dir := t.TempDir()
			src, asm := filepath.Join(dir, "main.fern"), filepath.Join(dir, "main.s")
			if err := os.WriteFile(src, []byte(entropyReadSource), 0o644); err != nil {
				t.Fatal(err)
			}
			if out, err := exec.Command(cli, "-target", target, "-emit", "asm", "-o", asm, src, entropyStdlibRoot(t)).CombinedOutput(); err != nil {
				t.Fatalf("compile: %v\n%s", err, out)
			}
			text, err := os.ReadFile(asm)
			if err != nil {
				t.Fatal(err)
			}
			for _, mode := range []string{"full", "short", "interrupted", "zero"} {
				t.Run(mode, func(t *testing.T) {
					body := entropySyscallStub(t, target, mode, string(text))
					path, bin := filepath.Join(dir, mode+".s"), filepath.Join(dir, mode)
					if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
						t.Fatal(err)
					}
					if out, err := exec.Command(gcc, "-nostdlib", "-static", "-no-pie", "-o", bin, path).CombinedOutput(); err != nil {
						t.Fatalf("link: %v\n%s", err, out)
					}
					ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
					defer cancel()
					args := append(append([]string{}, runner...), bin)
					cmd := exec.CommandContext(ctx, args[0], args[1:]...)
					out, err := cmd.CombinedOutput()
					if ctx.Err() != nil || cmd.ProcessState == nil {
						t.Fatalf("run: %v\n%s", err, out)
					}
					code, want, output := cmd.ProcessState.ExitCode(), 0, "entropy complete\n"
					if mode == "zero" {
						want, output = 134, "fern: entropy unavailable\n"
					}
					if code != want || string(out) != output {
						t.Errorf("exit=%d output=%q, want exit=%d output=%q", code, out, want, output)
					}
				})
			}
		})
	}
}

// Replace only entropy results. Other syscalls retain their kernel behavior.
func entropySyscallStub(t *testing.T, target, mode, text string) string {
	t.Helper()
	lines, count := strings.Split(text, "\n"), 0
	for i, line := range lines {
		op := strings.TrimSpace(line)
		if target == "x86-64-linux" && op == "syscall" {
			label := fmt.Sprintf(".Lentropy_probe_%d", count)
			lines[i] = "    cmpq $318, %rax\n    jne " + label + "_pass\n    leaq " + label + "_resume(%rip), %rcx\n    jmp .Lentropy_test_result\n" + label + "_pass:\n    syscall\n" + label + "_resume:"
			count++
		} else if target == "arm64-linux" && op == "svc #0" {
			label := fmt.Sprintf(".Lentropy_probe_%d", count)
			lines[i] = armEntropySyscallStub(mode, label)
			count++
		}
	}
	if count == 0 {
		t.Fatal("the emitted program contains no syscall instructions")
	}
	if target == "x86-64-linux" {
		body := ""
		switch mode {
		case "short":
			body = "    movb $90, (%rdi)\n    movl $1, %eax\n    jmp *%rcx\n"
		case "zero":
			body = "    xorl %eax, %eax\n    jmp *%rcx\n"
		case "interrupted":
			body = "    cmpq $256, %rsi\n    jne .Lentropy_full\n    cmpq $0, .Lentropy_interrupted(%rip)\n    jne .Lentropy_full\n    movq $1, .Lentropy_interrupted(%rip)\n    movq $-4, %rax\n    jmp *%rcx\n"
		}
		body += ".Lentropy_full:\n    xorl %eax, %eax\n.Lentropy_fill:\n    cmpq %rsi, %rax\n    jae .Lentropy_done\n    movb $90, (%rdi,%rax)\n    incq %rax\n    jmp .Lentropy_fill\n.Lentropy_done:\n    jmp *%rcx\n"
		lines = append(lines, "    .text\n.Lentropy_test_result:\n"+body)
	}
	return strings.Join(lines, "\n") + "\n    .bss\n    .balign 8\n.Lentropy_interrupted: .skip 8\n"
}

func armEntropySyscallStub(mode, label string) string {
	body := "    cmp x8, #278\n    b.ne " + label + "_pass\n    stp x9, x10, [sp, #-16]!\n"
	switch mode {
	case "short":
		body += "    mov x9, #90\n    strb w9, [x0]\n    mov x0, #1\n    b " + label + "_done\n"
	case "zero":
		body += "    mov x0, #0\n    b " + label + "_done\n"
	case "interrupted":
		body += "    cmp x1, #256\n    b.ne " + label + "_full\n    adrp x9, .Lentropy_interrupted\n    add x9, x9, :lo12:.Lentropy_interrupted\n    ldr x10, [x9]\n    cbnz x10, " + label + "_full\n    mov x10, #1\n    str x10, [x9]\n    mov x0, #-4\n    b " + label + "_done\n"
	}
	body += label + "_full:\n    mov x9, #90\n    mov x10, #0\n" + label + "_fill:\n    cmp x10, x1\n    b.hs " + label + "_filled\n    strb w9, [x0, x10]\n    add x10, x10, #1\n    b " + label + "_fill\n" + label + "_filled:\n    mov x0, x1\n" + label + "_done:\n    ldp x9, x10, [sp], #16\n    b " + label + "_resume\n" + label + "_pass:\n    svc #0\n" + label + "_resume:"
	return body
}

// Stop at the allocator entry to check the requested capacity without a
// multi-gigabyte allocation or entropy read. Small counts are positive controls.
func TestSelfHostEntropyAllocationCapacity(t *testing.T) {
	cli := e2eharness.SelfHostCLI(t)
	targets := []string{e2eharness.HostSelfHostTarget()}
	if runtime.GOARCH == "amd64" {
		targets = append(targets, "arm64-linux")
	}
	for _, target := range targets {
		t.Run(target, func(t *testing.T) {
			var gcc string
			var runner []string
			if target == "arm64-linux" {
				var qemu string
				gcc, qemu = e2eharness.Arm64Tooling(t)
				if qemu != "" {
					runner = []string{qemu}
				}
			} else {
				gcc, _ = e2eharness.X86_64Tooling(t)
			}
			for _, n := range []int64{-1, 0, 1, 7, 8, 9, 2147483640, 2147483641, 2147483647} {
				t.Run(fmt.Sprint(n), func(t *testing.T) {
					dir := t.TempDir()
					src, asm, bin := filepath.Join(dir, "main.fern"), filepath.Join(dir, "main.s"), filepath.Join(dir, "main")
					program := fmt.Sprintf("import \"std/rand\";\nfunction main(): i32 { let bs: u8[] = rand.bytes(%d); return bs.len(); }\n", n)
					if err := os.WriteFile(src, []byte(program), 0o644); err != nil {
						t.Fatal(err)
					}
					if out, err := exec.Command(cli, "-target", target, "-emit", "asm", "-o", asm, src, entropyStdlibRoot(t)).CombinedOutput(); err != nil {
						t.Fatalf("compile: %v\n%s", err, out)
					}
					text, err := os.ReadFile(asm)
					if err != nil {
						t.Fatal(err)
					}
					const label = "__fern_arr_box:\n"
					if strings.Count(string(text), label) != 1 {
						t.Fatal("expected exactly one array allocator entry")
					}
					positive := n
					if positive < 0 {
						positive = 0
					}
					words := (positive + 7) / 8
					var hook string
					if target == "x86-64-linux" {
						hook = fmt.Sprintf("    cmpq $%d, %%rdi\n    jne .Lentropy_capacity_bad\n    movl $42, %%edi\n    jmp .Lentropy_capacity_exit\n.Lentropy_capacity_bad:\n    movl $13, %%edi\n.Lentropy_capacity_exit:\n    movl $60, %%eax\n    syscall\n", words)
					} else {
						hook = fmt.Sprintf("    mov x9, #%d\n    cmp x0, x9\n    b.ne .Lentropy_capacity_bad\n    mov x0, #42\n    b .Lentropy_capacity_exit\n.Lentropy_capacity_bad:\n    mov x0, #13\n.Lentropy_capacity_exit:\n    mov x8, #93\n    svc #0\n", words)
					}
					body := strings.Replace(string(text), label, label+hook, 1)
					if err := os.WriteFile(asm, []byte(body), 0o644); err != nil {
						t.Fatal(err)
					}
					if out, err := exec.Command(gcc, "-nostdlib", "-static", "-no-pie", "-o", bin, asm).CombinedOutput(); err != nil {
						t.Fatalf("link: %v\n%s", err, out)
					}
					ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
					defer cancel()
					args := append(append([]string{}, runner...), bin)
					cmd := exec.CommandContext(ctx, args[0], args[1:]...)
					out, err := cmd.CombinedOutput()
					if ctx.Err() != nil || cmd.ProcessState == nil {
						t.Fatalf("run: %v\n%s", err, out)
					}
					if cmd.ProcessState.ExitCode() != 42 || len(out) != 0 {
						t.Errorf("length %d must request %d words: exit=%d output=%q", n, words, cmd.ProcessState.ExitCode(), out)
					}
				})
			}
		})
	}
}

func entropyStdlibRoot(t *testing.T) string {
	t.Helper()
	root, err := filepath.Abs("../../stdlib")
	if err != nil {
		t.Fatal(err)
	}
	return root
}
