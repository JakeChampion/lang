package e2ecompiler

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

var extraCallerCoefficients = []uint64{3, 5, 7, 11, 13, 17, 19, 23, 29, 31, 37, 41, 43, 47, 53, 59, 61, 67, 71, 73}

func extraCallerSource(n int, colored bool) string {
	var out strings.Builder
	out.WriteString("import \"std/u64\";\n@noinline\nfunction combine(seed: u64")
	if colored {
		out.WriteString(", a:u64, b:u64, c:u64, d:u64, e:u64")
	}
	out.WriteString("): u64 {\n")
	for i, c := range extraCallerCoefficients[:n] {
		fmt.Fprintf(&out, "let v%d = seed * %du64", i, c)
		if colored {
			fmt.Fprintf(&out, " ^ %c", 'a'+i%5)
		}
		out.WriteString(";\n")
	}
	out.WriteString("return (")
	for i := range n {
		if i != 0 {
			out.WriteString(" ^ ")
		}
		fmt.Fprintf(&out, "v%d", i)
	}
	out.WriteString(")")
	if colored {
		out.WriteString(" + a + b + c + d + e")
	}
	out.WriteString(";\n}\nfunction main(): i32 { let seed = args().len() as u64;\n")
	if colored {
		out.WriteString("print(combine(seed, seed+1u64, seed+2u64, seed+3u64, seed+4u64, seed+5u64).to_string());\n")
	} else {
		out.WriteString("print(combine(seed).to_string()); print(combine(seed*17u64).to_string()); print(combine(18446744073709551615u64).to_string());\n")
	}
	out.WriteString("return 0; }\n")
	return out.String()
}

func extraCallerWant(n int, colored bool, argc uint64) string {
	combine := func(seed uint64) uint64 {
		var result uint64
		for i, c := range extraCallerCoefficients[:n] {
			v := seed * c
			if colored {
				v ^= seed + 1 + uint64(i%5)
			}
			result ^= v
		}
		if colored {
			for i := uint64(1); i <= 5; i++ {
				result += seed + i
			}
		}
		return result
	}
	seeds := []uint64{argc, argc * 17, ^uint64(0)}
	if colored {
		seeds = seeds[:1]
	}
	var out strings.Builder
	for _, seed := range seeds {
		out.WriteString(strconv.FormatUint(combine(seed), 10) + "\n")
	}
	return out.String()
}

func TestSelfHostSSAExtraCallerRegisters(t *testing.T) {
	cli := buildSelfHostCLI(t)
	for _, c := range []struct {
		name    string
		n       int
		colored bool
	}{
		{"nine-values", 9, false},
		{"spilled-values", 20, false},
		{"spilled-rhs-into-rcx", 20, true},
	} {
		t.Run(c.name, func(t *testing.T) {
			src := extraCallerSource(c.n, c.colored)
			entry := filepath.Join(t.TempDir(), "main.fern")
			if err := os.WriteFile(entry, []byte(src), 0o644); err != nil {
				t.Fatal(err)
			}
			asm := cli.emit(t, entry, "x86-64-linux")
			text, err := os.ReadFile(asm)
			if err != nil {
				t.Fatal(err)
			}
			a := strings.Index(string(text), "__fn_combine:")
			if a < 0 {
				t.Fatal("no combine body")
			}
			b := strings.Index(string(text[a:]), ".cfi_endproc")
			if b < 0 {
				t.Fatal("no combine end")
			}
			body := string(text[a : a+b])
			if c.n == 9 {
				for _, reg := range []string{"%rcx", "%rdx", "%r11"} {
					if !strings.Contains(body, reg) {
						t.Errorf("missing caller home %s:\n%s", reg, body)
					}
				}
				if strings.Contains(body, "pushq") || strings.Contains(body, "(%rbp)") {
					t.Fatalf("register-only leaf saves or spills:\n%s", body)
				}
			} else if !strings.Contains(body, "(%rbp)") {
				t.Fatalf("fixture no longer exercises spills:\n%s", body)
			}
			if c.colored && !regexp.MustCompile(`xorq -[0-9]+\(%rbp\), %rcx`).MatchString(body) {
				t.Fatalf("fixture no longer exercises a spilled RHS with rcx destination:\n%s", body)
			}
			linked := filepath.Join(t.TempDir(), "linked")
			if out, err := exec.Command(cli.gcc, "-nostdlib", "-static", "-o", linked, asm).CombinedOutput(); err != nil {
				t.Fatalf("link: %v\n%s", err, out)
			}
			for _, mode := range []string{"text", "native", "sanitized"} {
				t.Run(mode, func(t *testing.T) {
					bin := linked
					if mode == "native" {
						bin = compileCPUProfile(t, cli, src, "x86-64-linux", "")
					} else if mode == "sanitized" {
						bin = compileCPUProfile(t, cli, src, "x86-64-linux", "", "-sanitize")
					}
					for _, args := range [][]string{nil, {"sample"}} {
						cmd := runX86_64Bin(cli.runner, bin, args...)
						var stdout, stderr bytes.Buffer
						cmd.Stdout, cmd.Stderr = &stdout, &stderr
						err := cmd.Run()
						want := extraCallerWant(c.n, c.colored, uint64(len(args)+1))
						if err != nil || stdout.String() != want {
							t.Fatalf("%v: got %q, want %q; stderr %s", err, &stdout, want, &stderr)
						}
						if mode == "sanitized" {
							m := regexp.MustCompile(`leakcheck: allocs=(\d+) frees=(\d+) live_bytes=0`).FindStringSubmatch(stderr.String())
							if len(m) != 3 || m[1] != m[2] {
								t.Fatalf("unbalanced sanitized execution: %s", &stderr)
							}
						}
					}
				})
			}
		})
	}
}
