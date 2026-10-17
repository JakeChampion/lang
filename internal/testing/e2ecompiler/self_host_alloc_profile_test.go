package e2ecompiler

import (
	"os/exec"
	"strconv"
	"strings"
	"testing"
)

const allocationProfileProgram = `import "std/i32";
@noinline function allocate(n: i32, index: i32): i32 {
  let xs: i32[] = [n, n + 1];
  return xs[index];
}
function main(): i32 {
  let index: i32 = args().len() - 1;
  let before: i64 = __heap_alloc_count();
  let i: i32 = 0;
  let sum: i32 = 0;
  while (i < 1024) { sum = sum + allocate(i, index); i = i + 1; }
  if (__heap_alloc_count() - before != 1024i64) { return 12; }
  print(sum.to_string());
  return 7;
}
`

func TestSelfHostAllocationProfile(t *testing.T) {
	cli := buildSelfHostCLI(t)
	for _, target := range []string{"x86-64-linux", "arm64-linux"} {
		t.Run(target, func(t *testing.T) {
			var qemu string
			if target == "arm64-linux" {
				_, qemu = arm64Tooling(t)
				t.Setenv("QEMU_CPU", "max")
			}
			run := func(bin string) *exec.Cmd {
				if target == "arm64-linux" {
					return runArm64Bin(qemu, bin)
				}
				return runX86_64Bin(cli.runner, bin)
			}
			for _, harden := range []bool{false, true} {
				if harden && target != "arm64-linux" {
					continue
				}
				flags := []string{}
				if harden {
					flags = append(flags, "-harden")
				}
				bin := compileCPUProfile(t, cli, allocationProfileProgram, target, "alloc", flags...)
				stdout, stderr := runCPUProfile(t, run(bin), 7)
				if stdout != "523776\n" {
					t.Errorf("stdout %q", stdout)
				}
				lines := strings.Split(strings.TrimSpace(stderr), "\n")
				if lines[0] != "# fern allocation profile samples=16 dropped=0 interval=64 weight=rounded-bytes" {
					t.Fatalf("wrong allocation profile header: %s", lines[0])
				}
				if len(lines) != 17 {
					t.Fatalf("got %d rows, want 16: %s", len(lines)-1, stderr)
				}
				for _, row := range lines[1:] {
					if !strings.Contains(row, "_start;__fn_main;__fn_allocate;") || !strings.HasSuffix(row, " 2560") {
						t.Errorf("allocation is not attributed to its caller with 64 * 40 rounded bytes: %s", row)
					}
				}
			}
		})
	}
}

func TestSelfHostAllocationProfileCapacity(t *testing.T) {
	cli := buildSelfHostCLI(t)
	for _, target := range []string{"x86-64-linux", "arm64-linux"} {
		var qemu string
		if target == "arm64-linux" {
			_, qemu = arm64Tooling(t)
		}
		// More than 8,192 * 64 allocations, mostly served by the same
		// freelist slot. Missing the reuse path cannot pass this test.
		src := strings.ReplaceAll(allocationProfileProgram, "1024", "530000")
		bin := compileCPUProfile(t, cli, src, target, "alloc")
		cmd := runX86_64Bin(cli.runner, bin)
		if target == "arm64-linux" {
			cmd = runArm64Bin(qemu, bin)
		}
		_, stderr := runCPUProfile(t, cmd, 7)
		lines := strings.Split(strings.TrimSpace(stderr), "\n")
		const prefix = "# fern allocation profile samples=8192 dropped="
		if !strings.HasPrefix(lines[0], prefix) {
			t.Fatalf("wrong capacity header: %.200s", stderr)
		}
		dropped, err := strconv.Atoi(strings.Fields(strings.TrimPrefix(lines[0], prefix))[0])
		if err != nil || dropped < 89 {
			t.Errorf("missing drops: %s", lines[0])
		}
		if len(lines) != 8193 {
			t.Errorf("%d rows, want 8192", len(lines)-1)
		}
	}
}
