package e2ecompiler

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// The expected permutations were checked against GNU shuf 9.12 with the
// counting random source below. Keep arbitrary input bytes out of strings in
// the generated program, including reservoir records and random-source reads.
func TestSelfHostShufBytes(t *testing.T) {
	cli := buildSelfHostCLI(t)
	src, err := filepath.Abs("../../../coreutils/shuf.fern")
	if err != nil {
		t.Fatal(err)
	}
	input := "a\xff\x00\nb\xc0\x80\nc\xed\xa0\x80\nd\xf4\x90\x80\x80"
	zero := "a\xff\nb\x00c\xc0\x80\x00d\xed\xa0\x80"
	long := strings.Repeat("\xff", 262145) + "\nlast\x80"
	cases := []struct {
		name, input, want string
		args              []string
		file              bool
	}{
		{"stdin", input, input + "\n", nil, false},
		{"file", input, input + "\n", nil, true},
		{"reservoir", input, "d\xf4\x90\x80\x80\nb\xc0\x80\n", []string{"-n", "2"}, false},
		{"repeat", input, strings.Repeat("a\xff\x00\n", 4) + "b\xc0\x80\n" + strings.Repeat("a\xff\x00\n", 2), []string{"-r", "-n", "7"}, false},
		{"nul", zero, zero + "\x00", []string{"-z"}, false},
		{"nul reservoir", zero, "d\xed\xa0\x80\x00c\xc0\x80\x00", []string{"-z", "-n", "2"}, false},
		{"nul repeat", zero, strings.Repeat("a\xff\nb\x00", 5) + "c\xc0\x80\x00a\xff\nb\x00", []string{"-z", "-r", "-n", "7"}, false},
		{"long record", long, long + "\n", nil, true},
		{"long reservoir", long, long + "\n", []string{"-n", "2"}, false},
		{"echo", "", "é\n界\n𐐀\n", []string{"-e", "é", "界", "𐐀"}, false},
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
				bin = buildBinArm64(t, gcc, t.TempDir(), "shuf", string(asm))
				if qemu != "" {
					runner = []string{qemu}
				}
			case "wasm32-wasi":
				bin = cli.emit(t, src, target, env...)
				runner = []string{"wasmtime", "run", "--dir=."}
			}
			for _, tc := range cases {
				t.Run(tc.name, func(t *testing.T) {
					dir := t.TempDir()
					seed := make([]byte, 4096)
					for i := range seed {
						seed[i] = byte(i)
					}
					if err := os.WriteFile(filepath.Join(dir, "random-source"), seed, 0o644); err != nil {
						t.Fatal(err)
					}
					args := append([]string{"--random-source=random-source"}, tc.args...)
					if tc.file {
						if err := os.WriteFile(filepath.Join(dir, "input-bytes"), []byte(tc.input), 0o644); err != nil {
							t.Fatal(err)
						}
						args = append(args, "input-bytes")
					}
					argv := append(append(append([]string{}, runner...), bin), args...)
					cmd := exec.Command(argv[0], argv[1:]...)
					cmd.Dir = dir
					cmd.Stdin = strings.NewReader(tc.input)
					var out, diagnostic bytes.Buffer
					cmd.Stdout, cmd.Stderr = &out, &diagnostic
					if err := cmd.Run(); err != nil {
						t.Fatalf("run: %v\n%s", err, diagnostic.String())
					}
					if !bytes.Equal(out.Bytes(), []byte(tc.want)) {
						t.Fatalf("stdout differs: got %d bytes, want %d", out.Len(), len(tc.want))
					}
					if strings.Contains(diagnostic.String(), "fern-sanitizer:") || !strings.Contains(diagnostic.String(), "live_bytes=0") {
						t.Fatalf("missing clean ownership census\n%s", diagnostic.String())
					}
				})
			}
		})
	}
}
