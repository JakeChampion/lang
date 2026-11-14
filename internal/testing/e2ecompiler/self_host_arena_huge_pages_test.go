package e2ecompiler

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
)

// main keeps 1,000,000 boxes, which is about 64 MiB of the arena, and reads
// each of them back.
const arenaHugePagesProg = `struct Item { n: i32, a: i32, b: i32 }

@noinline function fill(count: i32): Item[] {
  let cells: Item[] = [];
  for i in 0..count {
    cells = cells.append(Item { n: i, a: 1, b: 2 });
  }
  return cells;
}

function main(): i32 {
  let cells = fill(1000000);
  let total = 0;
  for c in cells {
    total += c.a + c.b;
  }
  if (total != 3000000 || cells[999999].n != 999999) {
    return 1;
  }
  return 0;
}
`

// The first allocation maps the arena and asks for transparent huge pages
// past the first 4 MiB. A heap of 64 MiB then takes a small part of the page
// faults that 4 KiB pages need.
func TestSelfHostArenaAsksForHugePages(t *testing.T) {
	cli := buildSelfHostCLI(t)
	src := filepath.Join(t.TempDir(), "main.fern")
	if err := os.WriteFile(src, []byte(arenaHugePagesProg), 0o644); err != nil {
		t.Fatal(err)
	}
	advice := map[string]string{
		"x86-64-linux": "leaq 4194304(%rax), %rdi\n    movabs $17175674880, %rsi\n    movl $14, %edx\n    movl $28, %eax\n    syscall\n",
		"arm64-linux":  "add x0, x0, #0x400, lsl #12\n",
	}
	for _, target := range []string{"x86-64-linux", "arm64-linux"} {
		t.Run(target, func(t *testing.T) {
			asm, err := os.ReadFile(cli.emit(t, src, target))
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(string(asm), advice[target]) {
				t.Fatalf("the arena map has no %q", advice[target])
			}
			for _, env := range []string{"", "FERN_SANDBOX=1"} {
				if stderr, code := cli.exitOf(t, arenaHugePagesProg, target, env); code != 0 {
					t.Fatalf("%s: exit %d, want 0\n%s", env, code, stderr)
				}
			}
		})
	}
	t.Run("page faults", func(t *testing.T) {
		mode, err := os.ReadFile("/sys/kernel/mm/transparent_hugepage/enabled")
		if len(cli.runner) != 0 || err != nil || strings.Contains(string(mode), "[never]") {
			t.Skip("this host gives no transparent huge pages to a native x86-64 process")
		}
		cmd := exec.Command(cli.x86Binary(t, src))
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("%v\n%s", err, out)
		}
		faults := cmd.ProcessState.SysUsage().(*syscall.Rusage).Minflt
		if faults > 8000 {
			t.Fatalf("%d page faults for a heap of 64 MiB; 4 KiB pages need about 16,000", faults)
		}
	})
}

// depth recurses 1,500,000 deep, which takes about 48 MiB of stack.
const stackHugePagesProg = `@noinline function depth(n: i64): i64 {
  if (n == 0i64) {
    return 0i64;
  }
  return depth(n - 1i64) + n % 3i64;
}

function main(): i32 {
  if (depth(1500000i64) != 1500000i64) {
    return 1;
  }
  return 0;
}
`

// The entry code maps the stack and asks for transparent huge pages below
// its top 4 MiB. A recursion that takes 48 MiB of stack then takes a small
// part of the page faults that 4 KiB pages need.
func TestSelfHostStackAsksForHugePages(t *testing.T) {
	cli := buildSelfHostCLI(t)
	src := filepath.Join(t.TempDir(), "main.fern")
	if err := os.WriteFile(src, []byte(stackHugePagesProg), 0o644); err != nil {
		t.Fatal(err)
	}
	advice := map[string]string{
		"x86-64-linux": "leaq 1048576(%rbx), %rdi\n    movq $1068498944, %rsi\n    movl $14, %edx\n    movl $28, %eax\n    syscall\n",
		"arm64-linux":  "add x0, x19, #0x100, lsl #12\n",
	}
	for _, target := range []string{"x86-64-linux", "arm64-linux"} {
		t.Run(target, func(t *testing.T) {
			asm, err := os.ReadFile(cli.emit(t, src, target))
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(string(asm), advice[target]) {
				t.Fatalf("the entry code has no %q", advice[target])
			}
			for _, env := range []string{"", "FERN_SANDBOX=1"} {
				if stderr, code := cli.exitOf(t, stackHugePagesProg, target, env); code != 0 {
					t.Fatalf("%s: exit %d, want 0\n%s", env, code, stderr)
				}
			}
		})
	}
	t.Run("page faults", func(t *testing.T) {
		mode, err := os.ReadFile("/sys/kernel/mm/transparent_hugepage/enabled")
		if len(cli.runner) != 0 || err != nil || strings.Contains(string(mode), "[never]") {
			t.Skip("this host gives no transparent huge pages to a native x86-64 process")
		}
		cmd := exec.Command(cli.x86Binary(t, src))
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("%v\n%s", err, out)
		}
		faults := cmd.ProcessState.SysUsage().(*syscall.Rusage).Minflt
		if faults > 6000 {
			t.Fatalf("%d page faults for a stack of 48 MiB; 4 KiB pages need about 12,000", faults)
		}
	})
}
