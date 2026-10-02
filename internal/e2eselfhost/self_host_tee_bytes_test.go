package e2eselfhost

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestSelfHostTeeBytes(t *testing.T) {
	cli := buildSelfHostCLI(t)
	src, err := filepath.Abs("../../coreutils/tee.fern")
	if err != nil {
		t.Fatal(err)
	}
	all := make([]byte, 256)
	for i := range all {
		all[i] = byte(i)
	}
	input := append(bytes.Repeat(all, 1025), 0xff, 0xc0, 0x80, 0xed, 0xa0, 0x80)
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
				bin = buildBinArm64(t, gcc, t.TempDir(), "tee", string(asm))
				if qemu != "" {
					runner = []string{qemu}
				}
			case "wasm32-wasi":
				bin = cli.emit(t, src, target, env...)
				runner = []string{"wasmtime", "run", "--dir=."}
			}
			for _, tc := range []struct {
				name          string
				args          []string
				input, prefix []byte
			}{
				{"fanout", []string{"a", "b"}, input, nil},
				{"append", []string{"-a", "a", "b"}, input, []byte{0xff, 0, 0x80}},
				{"truncate", []string{"a", "b"}, nil, nil},
			} {
				t.Run(tc.name, func(t *testing.T) {
					dir := t.TempDir()
					for _, name := range []string{"a", "b"} {
						initial := []byte{0xff, 0, 0x80}
						if err := os.WriteFile(filepath.Join(dir, name), initial, 0o644); err != nil {
							t.Fatal(err)
						}
					}
					argv := append(append(append([]string{}, runner...), bin), tc.args...)
					cmd := exec.Command(argv[0], argv[1:]...)
					cmd.Dir = dir
					cmd.Stdin = bytes.NewReader(tc.input)
					var out, diagnostic bytes.Buffer
					cmd.Stdout, cmd.Stderr = &out, &diagnostic
					if err := cmd.Run(); err != nil {
						t.Fatalf("run: %v\n%s", err, diagnostic.String())
					}
					if !bytes.Equal(out.Bytes(), tc.input) {
						t.Fatalf("stdout differs: got %d bytes, want %d", out.Len(), len(tc.input))
					}
					want := append(append([]byte{}, tc.prefix...), tc.input...)
					for _, name := range []string{"a", "b"} {
						got, err := os.ReadFile(filepath.Join(dir, name))
						if err != nil {
							t.Fatal(err)
						}
						if !bytes.Equal(got, want) {
							t.Fatalf("%s differs: got %d bytes, want %d", name, len(got), len(want))
						}
					}
					assertBalancedCensus(t, diagnostic.String())
				})
			}
		})
	}
}
