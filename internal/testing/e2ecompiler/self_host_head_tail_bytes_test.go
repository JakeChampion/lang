package e2ecompiler

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/testing/e2eharness"
)

func TestSelfHostHeadTailBytes(t *testing.T) {
	cli := buildSelfHostCLI(t)
	for _, utility := range []string{"head", "tail"} {
		t.Run(utility, func(t *testing.T) {
			gnu := utility
			if runtime.GOOS == "darwin" {
				gnu = "g" + utility
			}
			oracle, err := exec.LookPath(gnu)
			for _, dir := range filepath.SplitList(os.Getenv("FERN_GNU_COREUTILS")) {
				candidate := filepath.Join(dir, utility)
				if info, statErr := os.Stat(candidate); statErr == nil && !info.IsDir() {
					oracle, err = candidate, nil
					break
				}
			}
			if err != nil {
				t.Skip("requires GNU " + utility)
			}
			version, err := exec.Command(oracle, "--version").Output()
			if err != nil || !bytes.Contains(version, []byte("GNU coreutils")) {
				t.Skip("requires GNU " + utility)
			}
			src, err := filepath.Abs("../../../coreutils/" + utility + ".fern")
			if err != nil {
				t.Fatal(err)
			}
			for _, target := range []string{"x86-64-linux", "arm64-linux", "wasm32-wasi"} {
				t.Run(target, func(t *testing.T) {
					var bin string
					var runner []string
					env := []string{"FERN_SEM_IR=1", "FERN_SEM_IR_STRICT=1", "FERN_SEM_IR_ONLY=", "FERN_SEM_IR_SKIP=", "FERN_STRICT_IR=1", "FERN_SANITIZE=1", "FERN_LEAKCHECK=1"}
					switch target {
					case "x86-64-linux":
						bin = cli.x86Binary(t, src, env...)
						runner = cli.runner
					case "arm64-linux":
						gcc, qemu := arm64Tooling(t)
						asm, err := os.ReadFile(cli.emit(t, src, target, env...))
						if err != nil {
							t.Fatal(err)
						}
						bin = buildBinArm64(t, gcc, t.TempDir(), utility, string(asm))
						if qemu != "" {
							runner = []string{qemu}
						}
					case "wasm32-wasi":
						bin = cli.emit(t, src, target, env...)
						runner = []string{"wasmtime", "run", "--dir=."}
					}
					for _, tc := range e2eharness.HeadTailByteCases(utility) {
						t.Run(tc.Name, func(t *testing.T) {
							dir := t.TempDir()
							args := append([]string{}, tc.Args...)
							input := tc.Input
							if tc.File {
								if err := os.WriteFile(filepath.Join(dir, "input"), []byte(input), 0o644); err != nil {
									t.Fatal(err)
								}
								args = append(args, "input")
								input = ""
							}
							ref := exec.Command(oracle, args...)
							ref.Dir, ref.Env, ref.Stdin = dir, append(os.Environ(), "LC_ALL=C"), strings.NewReader(input)
							want, err := ref.Output()
							if err != nil {
								t.Fatalf("GNU run: %v", err)
							}
							argv := append(append(append([]string{}, runner...), bin), args...)
							cmd := exec.Command(argv[0], argv[1:]...)
							cmd.Dir, cmd.Env, cmd.Stdin = dir, append(os.Environ(), "LC_ALL=C"), strings.NewReader(input)
							var out, diagnostic bytes.Buffer
							cmd.Stdout, cmd.Stderr = &out, &diagnostic
							if err := cmd.Run(); err != nil {
								t.Fatalf("run: %v\n%s", err, diagnostic.String())
							}
							if !bytes.Equal(out.Bytes(), want) {
								t.Fatalf("stdout differs: got %d bytes, want %d", out.Len(), len(want))
							}
							assertBalancedCensus(t, diagnostic.String())
						})
					}
				})
			}
		})
	}
}
