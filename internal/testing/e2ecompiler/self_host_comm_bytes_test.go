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

func TestSelfHostCommBytes(t *testing.T) {
	gnu := "comm"
	if runtime.GOOS == "darwin" {
		gnu = "gcomm"
	}
	oracle, err := exec.LookPath(gnu)
	for _, dir := range filepath.SplitList(os.Getenv("FERN_GNU_COREUTILS")) {
		candidate := filepath.Join(dir, "comm")
		if info, statErr := os.Stat(candidate); statErr == nil && !info.IsDir() {
			oracle, err = candidate, nil
			break
		}
	}
	if err != nil {
		t.Skip("requires GNU comm")
	}
	version, err := exec.Command(oracle, "--version").Output()
	if err != nil || !bytes.Contains(version, []byte("GNU coreutils")) {
		t.Skip("requires GNU comm")
	}
	cli := buildSelfHostCLI(t)
	src, err := filepath.Abs("../../../coreutils/comm.fern")
	if err != nil {
		t.Fatal(err)
	}
	for _, target := range []string{"x86-64-linux", "arm64-linux", "wasm32-wasi"} {
		t.Run(target, func(t *testing.T) {
			var bin string
			var runner []string
			env := []string{"FERN_STRICT_IR=1", "FERN_SANITIZE=1", "FERN_LEAKCHECK=1"}
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
				bin = buildBinArm64(t, gcc, t.TempDir(), "comm", string(asm))
				if qemu != "" {
					runner = []string{qemu}
				}
			case "wasm32-wasi":
				bin = cli.emit(t, src, target, env...)
				runner = []string{"wasmtime", "run", "--dir=."}
			}
			for _, tc := range e2eharness.CommByteCases() {
				t.Run(tc.Name, func(t *testing.T) {
					dir := t.TempDir()
					args := append([]string{}, tc.Args...)
					if tc.Shared {
						args = append(args, "-", "-")
					} else {
						for name, data := range map[string]string{"a": tc.A, "b": tc.B} {
							if err := os.WriteFile(filepath.Join(dir, name), []byte(data), 0o644); err != nil {
								t.Fatal(err)
							}
						}
						args = append(args, "a", "b")
					}
					ref := exec.Command(oracle, args...)
					ref.Dir, ref.Env, ref.Stdin = dir, append(os.Environ(), "LC_ALL=C"), strings.NewReader(tc.Stdin)
					want, err := ref.Output()
					if ref.ProcessState == nil || ref.ProcessState.ExitCode() != tc.Exit {
						t.Fatalf("GNU run: %v, want exit %d", err, tc.Exit)
					}
					argv := append(append(append([]string{}, runner...), bin), args...)
					cmd := exec.Command(argv[0], argv[1:]...)
					cmd.Dir, cmd.Env, cmd.Stdin = dir, append(os.Environ(), "LC_ALL=C"), strings.NewReader(tc.Stdin)
					var out, diagnostic bytes.Buffer
					cmd.Stdout, cmd.Stderr = &out, &diagnostic
					err = cmd.Run()
					if cmd.ProcessState == nil || cmd.ProcessState.ExitCode() != tc.Exit {
						t.Fatalf("run: %v, want exit %d\n%s", err, tc.Exit, diagnostic.String())
					}
					if !bytes.Equal(out.Bytes(), want) {
						t.Fatalf("stdout differs: got %d bytes, want %d", out.Len(), len(want))
					}
					assertBalancedCensus(t, diagnostic.String())
				})
			}
		})
	}
}
