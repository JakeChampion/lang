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

// Supply a descriptor at the copy entry and stop at the allocator entry.
// This checks large lengths without a large allocation or a memory copy.
func TestSelfHostStringBytesAllocationCapacity(t *testing.T) {
	cli := e2eharness.SelfHostCLI(t)
	stdlib, err := filepath.Abs("../../stdlib")
	if err != nil {
		t.Fatal(err)
	}
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
			const program = `import "std/string";
function main(): i32 {
  let bytes: u8[] = args()[0].bytes();
  return bytes.len();
}
`
			if err := os.WriteFile(src, []byte(program), 0o644); err != nil {
				t.Fatal(err)
			}
			if out, err := exec.Command(cli, "-target", target, "-emit", "asm", "-o", asm, src, stdlib).CombinedOutput(); err != nil {
				t.Fatalf("compile: %v\n%s", err, out)
			}
			text, err := os.ReadFile(asm)
			if err != nil {
				t.Fatal(err)
			}
			for _, n := range []int64{0, 1, 7, 8, 9, 2147483640, 2147483641, 2147483647} {
				t.Run(fmt.Sprint(n), func(t *testing.T) {
					body := stringBytesCapacityProbe(t, target, string(text), n)
					path, bin := filepath.Join(dir, fmt.Sprint(n)+".s"), filepath.Join(dir, fmt.Sprint(n))
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
					if cmd.ProcessState.ExitCode() != 42 || len(out) != 0 {
						t.Errorf("length %d must request %d words: exit=%d output=%q", n, (n+7)/8, cmd.ProcessState.ExitCode(), out)
					}
				})
			}
		})
	}
}

func stringBytesCapacityProbe(t *testing.T, target, text string, n int64) string {
	t.Helper()
	const entry = "__fn___fern_str_bytes_u8.r:\n"
	const alloc = "__fern_arr_box:\n"
	if strings.Count(text, entry) != 1 || strings.Count(text, alloc) != 1 {
		t.Fatal("expected one string byte copy entry and one array allocator entry")
	}
	words := (n + 7) / 8
	var inject, hook string
	if target == "x86-64-linux" {
		inject = "    movq $1, .Lbytes_capacity_armed(%rip)\n    leaq .Lbytes_capacity_header(%rip), %rax\n"
		hook = fmt.Sprintf("    cmpq $0, .Lbytes_capacity_armed(%%rip)\n    je .Lbytes_capacity_continue\n    cmpq $%d, %%rdi\n    jne .Lbytes_capacity_bad\n    movl $42, %%edi\n    jmp .Lbytes_capacity_exit\n.Lbytes_capacity_bad:\n    movl $13, %%edi\n.Lbytes_capacity_exit:\n    movl $60, %%eax\n    syscall\n.Lbytes_capacity_continue:\n", words)
	} else {
		inject = "    adrp x9, .Lbytes_capacity_armed\n    add x9, x9, :lo12:.Lbytes_capacity_armed\n    mov x10, #1\n    str x10, [x9]\n    adrp x0, .Lbytes_capacity_header\n    add x0, x0, :lo12:.Lbytes_capacity_header\n"
		hook = fmt.Sprintf("    adrp x9, .Lbytes_capacity_armed\n    ldr x10, [x9, :lo12:.Lbytes_capacity_armed]\n    cbz x10, .Lbytes_capacity_continue\n    mov x9, #%d\n    cmp x0, x9\n    b.ne .Lbytes_capacity_bad\n    mov x0, #42\n    b .Lbytes_capacity_exit\n.Lbytes_capacity_bad:\n    mov x0, #13\n.Lbytes_capacity_exit:\n    mov x8, #93\n    svc #0\n.Lbytes_capacity_continue:\n", words)
	}
	data := fmt.Sprintf("\n.data\n.balign 8\n.Lbytes_capacity_armed:\n.quad 0\n.Lbytes_capacity_header:\n.quad .Lbytes_capacity_data\n.quad %d\n.Lbytes_capacity_data:\n.zero 8\n", n)
	return strings.Replace(strings.Replace(text, entry, entry+inject, 1), alloc, alloc+hook, 1) + data
}
