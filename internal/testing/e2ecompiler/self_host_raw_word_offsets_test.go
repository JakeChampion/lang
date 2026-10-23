package e2ecompiler

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

func TestSelfHostRawWordConstantOffsets(t *testing.T) {
	cli := buildRuntimeCLI(t)
	var source strings.Builder
	cases := []struct {
		name  string
		index int
	}{{"zero", 0}, {"one", 1}, {"two", 2}, {"fifteen", 15}, {"last", 4095}, {"outside", 4096}, {"negative", -2}}
	for _, tc := range cases {
		fmt.Fprintf(&source, "@noinline function peek_%s(p: usize): usize { return __raw_load_ptr(p, %d); }\n", tc.name, tc.index)
		if tc.index > 0 {
			fmt.Fprintf(&source, "@noinline function poke_%s(p: usize, n: usize): void { __raw_store_ptr(p, %d, n); }\n", tc.name, tc.index)
		}
	}
	source.WriteString("function main(): i32 { let p: usize = __raw_arr_box(4097); let status: i32 = 0;\n")
	for _, tc := range cases {
		if tc.index > 0 {
			fmt.Fprintf(&source, "__store_i64(p + %d, %di64);\n", tc.index*8, 777+tc.index)
		}
	}
	for i, tc := range cases {
		want := 4097
		if tc.index > 0 {
			want = 777 + tc.index
		}
		fmt.Fprintf(&source, "if (peek_%s(p) != %d as usize) { status = %d; }\n", tc.name, want, i+1)
		if tc.index > 0 {
			fmt.Fprintf(&source, "poke_%s(p, %d as usize); if (__load_i64(p + %d) != %di64) { status = %d; }\n", tc.name, 9000+tc.index, tc.index*8, 9000+tc.index, i+11)
		}
	}
	source.WriteString("__fern_rc_dec(p); return status; }\n")
	src := filepath.Join(t.TempDir(), "raw_words.fern")
	if err := os.WriteFile(src, []byte(source.String()), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, target := range []string{"x86-64-linux", "arm64-linux"} {
		t.Run(target, func(t *testing.T) {
			gcc, runner := x86_64Tooling(t)
			if target == "arm64-linux" {
				var qemu string
				gcc, qemu = arm64Tooling(t)
				runner = nil
				if qemu != "" {
					runner = []string{qemu}
				}
			}
			for _, sanitize := range []bool{false, true} {
				t.Run(fmt.Sprintf("sanitized-%t", sanitize), func(t *testing.T) {
					var env []string
					if sanitize {
						env = []string{"FERN_SANITIZE=1"}
					}
					raw, err := os.ReadFile(cli.emit(t, src, target, env...))
					if err != nil {
						t.Fatal(err)
					}
					asm := string(raw)
					for _, name := range []string{"one", "last"} {
						offset := 8
						if name == "last" {
							offset = 32760
						}
						for _, op := range []string{"peek_", "poke_"} {
							body := asmFuncBody(t, asm, "__fn_"+op+name+".r")
							pattern := fmt.Sprintf(`\bmovq [^\n]*\b%d\(%%r\w+\)`, offset)
							if target == "arm64-linux" {
								pattern = fmt.Sprintf(`\b(?:ldr|str) x\d+, \[x\d+, #%d\]`, offset)
							}
							if !regexp.MustCompile(pattern).MatchString(body) {
								t.Errorf("%s has no word-scaled displacement %d:\n%s", op+name, offset, body)
							}
						}
					}
					for _, name := range []string{"outside", "negative"} {
						body := asmFuncBody(t, asm, "__fn_peek_"+name+".r")
						pattern := `\([^\n]*,8\)`
						if target == "arm64-linux" {
							pattern = `lsl #3\]`
						}
						if !regexp.MustCompile(pattern).MatchString(body) {
							t.Errorf("%s must retain the register-index fallback:\n%s", name, body)
						}
					}
					bin := buildBin(t, gcc, t.TempDir(), "raw_words", asm)
					check := func(bin string) {
						t.Helper()
						stderr, code := runCapturingStderr(t, ssaBackendTarget{runner: runner}, bin)
						if code != 0 || strings.Contains(stderr, "fern-sanitizer:") {
							t.Fatalf("exit=%d\n%s", code, stderr)
						}
						if sanitize && !strings.Contains(stderr, "allocs=1 frees=1 live_bytes=0") {
							t.Fatalf("unbalanced raw allocation:\n%s", stderr)
						}
					}
					check(bin)
					native := filepath.Join(t.TempDir(), "native")
					cmd := runX86_64Bin(cli.runner, cli.bin, "-target", target, "-o", native, src, cli.stdlib)
					cmd.Env = append(os.Environ(), env...)
					if out, err := cmd.CombinedOutput(); err != nil {
						t.Fatalf("native link: %v\n%s", err, out)
					}
					if err := os.Chmod(native, 0o755); err != nil {
						t.Fatal(err)
					}
					check(native)
				})
			}
		})
	}
}
