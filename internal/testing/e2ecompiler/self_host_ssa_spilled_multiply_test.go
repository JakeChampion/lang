package e2ecompiler

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

func TestSelfHostSSASpilledImmediateMultiply(t *testing.T) {
	cli := buildSelfHostCLI(t)
	cli.flags = []string{"-O"}
	for _, factor := range []uint32{2, 3, 257, 4294967293} {
		t.Run(fmt.Sprint(factor), func(t *testing.T) {
			var program strings.Builder
			program.WriteString("@noinline function product(n: u32, count: i32): u32 {\n")
			for i := 0; i < 18; i++ {
				fmt.Fprintf(&program, "  let a%d: u32 = n + %du32;\n", i, i)
			}
			program.WriteString("  let left: i32 = count;\n  while (left > 0) {\n")
			var want uint32
			for i := 0; i < 18; i++ {
				fmt.Fprintf(&program, "    a%d = a%d * %du32;\n", i, i, factor)
				value := uint32(i + 1)
				for round := 0; round < 3; round++ {
					value *= factor
				}
				want ^= value
			}
			program.WriteString("    left = left - 1;\n  }\n  return a0")
			for i := 1; i < 18; i++ {
				fmt.Fprintf(&program, " ^ a%d", i)
			}
			program.WriteString(";\n}\nfunction main(): i32 { return (product(1u32, 3) % 251u32) as i32; }\n")
			src := filepath.Join(t.TempDir(), "product.fern")
			if err := os.WriteFile(src, []byte(program.String()), 0o644); err != nil {
				t.Fatal(err)
			}
			for _, target := range []string{"x86-64-linux", "arm64-linux"} {
				bin := filepath.Join(t.TempDir(), "product")
				if out, err := cli.compileCmd(target, bin, src).CombinedOutput(); err != nil {
					t.Fatalf("%s native assembly: %v\n%s", target, err, out)
				}
				cmd := exec.Command(bin)
				if target == "arm64-linux" {
					cmd = exec.Command("qemu-aarch64", bin)
				}
				out, err := cmd.CombinedOutput()
				if cmd.ProcessState == nil || !cmd.ProcessState.Exited() || cmd.ProcessState.ExitCode() != int(want%251) {
					t.Fatalf("%s: want exit %d: %v\n%s", target, want%251, err, out)
				}
			}
			if factor == 3 {
				assembly, err := os.ReadFile(cli.emit(t, src, "x86-64-linux"))
				if err != nil {
					t.Fatal(err)
				}
				spill := regexp.MustCompile(`movq -\d+\(%rbp\), %r11\n\s+imull \$3, %r11d, %r11d`)
				if !spill.Match(assembly) {
					t.Fatal("no immediate multiply reads a spilled operand")
				}
			}
		})
	}
}
