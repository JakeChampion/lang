package e2ecompiler

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

func extraCallerReadSource(n int, kind string) string {
	source := strings.Split(extraCallerSource(n, false), "function main(): i32")[0]
	typeName, value, read := "u64[]", "[33u64, 55u64]", "data[0]"
	switch kind {
	case "record":
		typeName, value, read = "Item", "Item { a: 33u64, b: 55u64 }", "data.a ^ data.b"
	case "array-variable":
		read = "data[(seed & 1u64) as i32]"
	case "string-constant":
		typeName, value, read = "string", `"Az"`, "data[0] as u64"
	case "string-variable":
		typeName, value, read = "string", `"Az"`, "data[(seed & 1u64) as i32] as u64"
	}
	source = strings.Replace(source, "seed: u64)", "seed: u64, data: "+typeName+")", 1)
	source = strings.Replace(source, "return (", "let loaded = "+read+";\nreturn (", 1)
	source = strings.Replace(source, ");\n}", ") ^ loaded;\n}", 1)
	if kind == "record" {
		source = "struct Item { a: u64, b: u64 }\n" + source
	}
	return source + fmt.Sprintf("function main(): i32 { let seed = args().len() as u64; let data = %s; print(combine(seed, data).to_string()); return 0; }\n", value)
}

func TestSelfHostSSAFixedMemoryReads(t *testing.T) {
	cli := buildSelfHostCLI(t)
	for _, n := range []int{9, 20} {
		for _, kind := range []string{"record", "array-constant", "array-variable", "string-constant", "string-variable"} {
			t.Run(fmt.Sprintf("%s/%d", kind, n), func(t *testing.T) {
				source := extraCallerReadSource(n, kind)
				entry := filepath.Join(t.TempDir(), "main.fern")
				if err := os.WriteFile(entry, []byte(source), 0o644); err != nil {
					t.Fatal(err)
				}
				asm := cli.emit(t, entry, "x86-64-linux")
				text, err := os.ReadFile(asm)
				if err != nil {
					t.Fatal(err)
				}
				body := strings.SplitN(string(text), "__fn_combine:\n", 2)
				if len(body) != 2 {
					t.Fatal("missing combine body")
				}
				code := strings.SplitN(body[1], ".cfi_endproc", 2)[0]
				if n == 20 && !strings.Contains(code, "(%rbp)") {
					t.Fatal("fixture no longer exercises spilled operands")
				}
				if n == 9 && kind == "record" {
					for _, reg := range []string{"rcx", "rdx"} {
						if !regexp.MustCompile(`imulq \$[0-9]+, %[a-z0-9]+, %` + reg).MatchString(code) {
							t.Fatalf("fixture does not keep a product in %s across record reads:\n%s", reg, code)
						}
					}
				}
				linked := filepath.Join(t.TempDir(), "linked")
				if out, err := exec.Command(cli.gcc, "-nostdlib", "-static", "-o", linked, asm).CombinedOutput(); err != nil {
					t.Fatalf("link: %v\n%s", err, out)
				}
				for _, mode := range []string{"text", "native", "sanitized"} {
					sanitize := mode == "sanitized"
					binary := linked
					if mode == "native" {
						binary = compileCPUProfile(t, cli, source, "x86-64-linux", "")
					} else if sanitize {
						binary = compileCPUProfile(t, cli, source, "x86-64-linux", "", "-sanitize")
					}
					for _, args := range [][]string{nil, {"second"}} {
						seed := uint64(len(args) + 1)
						first := strings.Split(extraCallerWant(n, false, seed), "\n")[0]
						value, err := strconv.ParseUint(first, 10, 64)
						if err != nil {
							t.Fatal(err)
						}
						loaded := uint64(33)
						switch kind {
						case "record":
							loaded = 33 ^ 55
						case "array-variable":
							if seed&1 != 0 {
								loaded = 55
							}
						case "string-constant":
							loaded = 'A'
						case "string-variable":
							loaded = 'A'
							if seed&1 != 0 {
								loaded = 'z'
							}
						}
						want := strconv.FormatUint(value^loaded, 10) + "\n"
						cmd := runX86_64Bin(cli.runner, binary, args...)
						stdout, stderr := &strings.Builder{}, &strings.Builder{}
						cmd.Stdout, cmd.Stderr = stdout, stderr
						if err := cmd.Run(); err != nil || stdout.String() != want {
							t.Fatalf("sanitize=%v: %v, got %q want %q, stderr %s", sanitize, err, stdout, want, stderr)
						}
						if sanitize {
							m := regexp.MustCompile(`leakcheck: allocs=(\d+) frees=(\d+) live_bytes=0`).FindStringSubmatch(stderr.String())
							if len(m) != 3 || m[1] != m[2] {
								t.Fatalf("unbalanced sanitized execution: %s", stderr)
							}
						}
					}
				}
			})
		}
	}
}
