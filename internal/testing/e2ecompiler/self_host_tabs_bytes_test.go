package e2ecompiler

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"
)

func TestSelfHostTabsBytes(t *testing.T) {
	cli := buildSelfHostCLI(t)
	all := make([]byte, 256)
	for i := range all {
		all[i] = byte(i)
	}
	long := "\t" + strings.Repeat("\xff\xc0\x80        x\b\x00\t", 20000)
	for _, utility := range []string{"expand", "unexpand"} {
		t.Run(utility, func(t *testing.T) {
			gnu := utility
			if runtime.GOOS == "darwin" {
				gnu = "g" + utility
			}
			oracle, err := exec.LookPath(gnu)
			// Use the same configured reference as internal/testing/coreutils. The
			// devbox's system GNU 9.1 differs from the pinned GNU 9.12 on NUL
			// display width, so PATH alone is not the corpus oracle.
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
			mode := "-i"
			if utility == "unexpand" {
				mode = "-a"
			}
			cases := []struct {
				name, input string
				args        []string
				files       bool
				failure     string
			}{
				{"all bytes", string(all), []string{"-t", "3,+5"}, false, ""},
				{"long tail", long, nil, false, ""},
				{"long line", long + "\n\tlast\xff", nil, false, ""},
				{"across operands", long + "\n\tlast\xff", []string{mode}, true, ""},
				{"open error with carry", long + "\n\tlast\xff", []string{mode}, true, "missing"},
				{"read error with carry", long + "\n\tlast\xff", []string{mode}, true, "directory"},
				{"empty", "", nil, false, ""},
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
						bin = buildBinArm64(t, gcc, t.TempDir(), utility, string(asm))
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
							args := append([]string{}, tc.args...)
							if tc.files {
								for name, data := range map[string]string{"a": tc.input[:131073], "b": tc.input[131073:]} {
									if err := os.WriteFile(filepath.Join(dir, name), []byte(data), 0o644); err != nil {
										t.Fatal(err)
									}
								}
								args = append(args, "a")
								if tc.failure != "" {
									if tc.failure == "directory" {
										if err := os.Mkdir(filepath.Join(dir, tc.failure), 0o755); err != nil {
											t.Fatal(err)
										}
									}
									args = append(args, tc.failure)
								}
								args = append(args, "b")
							}
							wantExit := 0
							if tc.failure != "" {
								wantExit = 1
							}
							ref := exec.Command(oracle, args...)
							var wantDiagnostic bytes.Buffer
							ref.Stderr = &wantDiagnostic
							ref.Dir = dir
							ref.Env = append(os.Environ(), "LC_ALL=C")
							ref.Stdin = strings.NewReader(tc.input)
							want, err := ref.Output()
							if ref.ProcessState == nil || ref.ProcessState.ExitCode() != wantExit {
								t.Fatalf("GNU run: %v, want exit %d", err, wantExit)
							}
							argv := append(append(append([]string{}, runner...), bin), args...)
							cmd := exec.Command(argv[0], argv[1:]...)
							cmd.Dir = dir
							cmd.Stdin = strings.NewReader(tc.input)
							cmd.Env = append(os.Environ(), "LC_ALL=C")
							var out, diagnostic bytes.Buffer
							cmd.Stdout, cmd.Stderr = &out, &diagnostic
							err = cmd.Run()
							if cmd.ProcessState == nil || cmd.ProcessState.ExitCode() != wantExit {
								t.Fatalf("run: %v, want exit %d\n%s", err, wantExit, diagnostic.String())
							}
							if !bytes.Equal(out.Bytes(), want) {
								t.Fatalf("stdout differs: got %d bytes, want %d", out.Len(), len(want))
							}
							assertBalancedCensus(t, diagnostic.String())
							census := regexp.MustCompile(`(?m)^leakcheck: allocs=[0-9]+ frees=[0-9]+ live_bytes=[0-9]+\r?\n?`)
							if len(census.FindAllStringIndex(diagnostic.String(), -1)) != 1 {
								t.Fatalf("want one allocation census, got %q", diagnostic.String())
							}
							prefix := regexp.MustCompile(`(?m)^[^:\n]+: `)
							gotError := prefix.ReplaceAllString(census.ReplaceAllString(diagnostic.String(), ""), "utility: ")
							wantError := prefix.ReplaceAllString(wantDiagnostic.String(), "utility: ")
							if gotError != wantError {
								t.Fatalf("diagnostic differs: got %q, want %q", gotError, wantError)
							}
						})
					}
				})
			}
		})
	}
}
