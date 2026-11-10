package e2ecompiler

import (
	"fmt"
	"math/bits"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

func TestSelfHostSSASlotScratchPreservesLiveRegisters(t *testing.T) {
	cli := buildSelfHostCLI(t)
	cli.flags = []string{"-O"}
	for _, width := range []int{16, 32} {
		quarter := width / 4
		var groups [][4]int
		for i := 0; i < quarter; i++ {
			groups = append(groups, [4]int{i, quarter + i, 2*quarter + i, 3*quarter + i})
		}
		for i := 0; i < quarter; i++ {
			groups = append(groups, [4]int{i, quarter + (i+1)%quarter, 2*quarter + (i+2)%quarter, 3*quarter + (i+3)%quarter})
		}
		for _, seed := range []uint64{1, 0xffffffffffffffff, 0x123456789abcdef0} {
			t.Run(fmt.Sprintf("%d/%d", width, seed), func(t *testing.T) {
				var program strings.Builder
				program.WriteString("@noinline function carry(n: u64, count: i32): u64 {\n")
				values := make([]uint64, width)
				for i := range values {
					fmt.Fprintf(&program, "let v%d: u64 = n + %du64;\n", i, i)
					values[i] = seed + uint64(i)
				}
				if width == 32 {
					program.WriteString("let cold: u64 = n * 17u64;\nlet rhs: u64 = n * 19u64;\n")
				}
				program.WriteString("let left: i32 = count;\nwhile (left > 0) {\n")
				if width == 32 {
					// These cold values force arithmetic between two spill slots.
					program.WriteString("cold = cold + rhs;\n")
				}
				// Each group has an independent chain. Interleave the chains so
				// register values remain live across operations on shared spill slots.
				for half := 0; half < 2; half++ {
					for phase := 0; phase < 8; phase++ {
						for _, g := range groups[half*quarter : half*quarter+quarter] {
							a, b, c, d := g[0], g[1], g[2], g[3]
							switch phase {
							case 0, 4:
								fmt.Fprintf(&program, "v%d = v%d + v%d + n;\n", a, a, b)
							case 1, 5:
								rotate := 32
								if phase == 5 {
									rotate = 16
								}
								fmt.Fprintf(&program, "v%d = v%d ^ v%d; v%d = (v%d >> %d) | (v%d << %d);\n", d, d, a, d, d, rotate, d, 64-rotate)
							case 2, 6:
								fmt.Fprintf(&program, "v%d = v%d + v%d;\n", c, c, d)
							case 3, 7:
								rotate := 24
								if phase == 7 {
									rotate = 63
								}
								fmt.Fprintf(&program, "v%d = v%d ^ v%d; v%d = (v%d >> %d) | (v%d << %d);\n", b, b, c, b, b, rotate, b, 64-rotate)
							}
						}
					}
				}
				program.WriteString("left = left - 1;\n}\nreturn v0")
				for i := 1; i < width; i++ {
					fmt.Fprintf(&program, " ^ v%d", i)
				}
				if width == 32 {
					program.WriteString(" ^ cold")
				}
				fmt.Fprintf(&program, ";\n}\nfunction main(): i32 { return (carry(%du64, 3) %% 251u64) as i32; }\n", seed)
				for round := 0; round < 3; round++ {
					for _, g := range groups {
						a, b, c, d := g[0], g[1], g[2], g[3]
						for j := 0; j < 2; j++ {
							values[a] += values[b] + seed
							rotateD, rotateB := -32, -24
							if j == 1 {
								rotateD, rotateB = -16, -63
							}
							values[d] = bits.RotateLeft64(values[d]^values[a], rotateD)
							values[c] += values[d]
							values[b] = bits.RotateLeft64(values[b]^values[c], rotateB)
						}
					}
				}
				var want uint64
				if width == 32 {
					want = seed*17 + 3*(seed*19)
				}
				for _, v := range values {
					want ^= v
				}
				src := filepath.Join(t.TempDir(), "carry.fern")
				if err := os.WriteFile(src, []byte(program.String()), 0o644); err != nil {
					t.Fatal(err)
				}
				for _, target := range []string{"x86-64-linux", "arm64-linux"} {
					bin := filepath.Join(t.TempDir(), "carry")
					if out, err := cli.compileCmd(target, bin, src).CombinedOutput(); err != nil {
						t.Fatalf("%s compile: %v\n%s", target, err, out)
					}
					cmd := runX86_64Bin(cli.runner, bin)
					if target == "arm64-linux" {
						cmd = runArm64Bin(arm64Runner(t), bin)
					}
					out, err := cmd.CombinedOutput()
					if cmd.ProcessState == nil || !cmd.ProcessState.Exited() || cmd.ProcessState.ExitCode() != int(want%251) {
						t.Fatalf("%s: want exit %d: %v\n%s", target, want%251, err, out)
					}
				}
				if width == 32 && seed == 1 {
					assembly, err := os.ReadFile(cli.emit(t, src, "x86-64-linux"))
					if err != nil {
						t.Fatal(err)
					}
					if !regexp.MustCompile(`movq -\d+\(%rbp\), %r11\n\s+(addq|xorq) %r11, -\d+\(%rbp\)`).Match(assembly) {
						t.Fatal("no spill-to-spill arithmetic uses the reserved scratch register")
					}
				}
			})
		}
	}
}
