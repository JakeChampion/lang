package e2eselfhost

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestSelfHostTsortBytes(t *testing.T) {
	cli := buildSelfHostCLI(t)
	src, err := filepath.Abs("../../coreutils/tsort.fern")
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name, input, output, diagnostic string
		code                            int
	}{
		{"binary names", "a\xff b\xfe\n", "a\xff\nb\xfe\n", "", 0},
		// Both names hash to FNV-1a 0x5e4daa9d; they must remain distinct.
		{"hash collision", "costarring liquid liquid zz", "costarring\nliquid\nzz\n", "", 0},
		{"binary cycle", "a\xff b\xfe b\xfe a\xff\n", "a\xff\nb\xfe\n", "-: input contains a loop:\na\xff\nb\xfe\n", 1},
		{"unsigned ordering", "\xff \xff \x80 \x80 a a", "a\n\x80\n\xff\n", "", 0},
		{"NUL identity", "a\x00x b a\x00y c", "a\nc\nb\n", "", 0},
		{"empty name", "\x00x \x00y", "\n", "", 0},
		{"odd binary token", "a\xa0b c d\n", "", "-: input contains an odd number of tokens\n", 1},
	}
	for _, target := range []string{"x86-64-linux", "arm64-linux", "wasm32-wasi"} {
		t.Run(target, func(t *testing.T) {
			var bin string
			var runner []string
			env := []string{"FERN_SANITIZE=1", "FERN_LEAKCHECK=1"}
			switch target {
			case "x86-64-linux":
				bin, runner = cli.x86Binary(t, src, env...), cli.runner
			case "arm64-linux":
				gcc, qemu := arm64Tooling(t)
				asm, err := os.ReadFile(cli.emit(t, src, target, env...))
				if err != nil {
					t.Fatal(err)
				}
				bin = buildBinArm64(t, gcc, t.TempDir(), "tsort", string(asm))
				if qemu != "" {
					runner = []string{qemu}
				}
			case "wasm32-wasi":
				bin, runner = cli.emit(t, src, target, env...), []string{"wasmtime", "run"}
			}
			for _, tc := range cases {
				t.Run(tc.name, func(t *testing.T) {
					cmd := runX86_64Bin(runner, bin)
					cmd.Stdin = strings.NewReader(tc.input)
					var out, diagnostic bytes.Buffer
					cmd.Stdout, cmd.Stderr = &out, &diagnostic
					err := cmd.Run()
					code := 0
					if err != nil {
						if exit, ok := err.(*exec.ExitError); ok {
							code = exit.ExitCode()
						} else {
							t.Fatal(err)
						}
					}
					if code != tc.code || out.String() != tc.output {
						t.Fatalf("exit=%d stdout=%q; want %d %q\n%s", code, out.String(), tc.code, tc.output, diagnostic.String())
					}
					assertBalancedCensus(t, diagnostic.String())
					var messages string
					for _, line := range strings.Split(diagnostic.String(), "\n") {
						if line == "" || strings.HasPrefix(line, "leakcheck:") {
							continue
						}
						_, message, ok := strings.Cut(line, ": ")
						if !ok {
							t.Fatalf("unexpected diagnostic: %q", line)
						}
						messages += message + "\n"
					}
					if messages != tc.diagnostic {
						t.Fatalf("diagnostic=%q; want %q", messages, tc.diagnostic)
					}
				})
			}
		})
	}
}
