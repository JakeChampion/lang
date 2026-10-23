package e2ecompiler

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/pkg/platforms"
	"github.com/jakechampion/lang/internal/testing/e2eharness"
)

// The playground driver emits the bytes the CLI writes for the same program.
//
// Every output form the page asks for — a native target's assembly for the
// assembly pane, the wasm core module as text or as the binary a preview-1
// host runs, the wasi:cli/run component the download offers — comes out of
// the CLI's own pipeline (semlower.target_substitution, the same gates and
// the same emitter entry points), so what the page shows is what `fern`
// would build. The driver takes the CLI's `-emit` vocabulary, and its default
// is what the CLI writes for the target with `-o`.
// This pins that byte for byte, per form, with the CLI resolving the stdlib
// from its root argument and the driver from its embedded overlay.

// playgroundFormsProgram reaches the stdlib (to_string is std/i32's) and
// prints, so the component takes the stdout framing and the assembly carries
// a string table.
const playgroundFormsProgram = `import "std/i32";
function fact(n: i32): i32 {
    if (n == 0) { return 1; }
    return n * fact(n - 1);
}
function main(): i32 {
    let f: i32 = fact(5);
    print("5! = " + f.to_string());
    return f;
}
`

func TestSelfHostPlaygroundEmitsWhatTheCLIEmits(t *testing.T) {
	_, runner := x86_64Tooling(t)
	if len(runner) != 0 {
		t.Skip("the CLI and the playground driver run natively; skipping under an exec runner")
	}
	stdlib := e2eharness.SelfHostStdlibRoot(t)
	cli := e2eharness.SelfHostCLI(t)
	drv := buildPlaygroundDriver(t)
	srcPath := filepath.Join(t.TempDir(), "prog.fern")
	if err := os.WriteFile(srcPath, []byte(playgroundFormsProgram), 0o644); err != nil {
		t.Fatal(err)
	}

	handlerPath := filepath.Join(t.TempDir(), "handler.fern")
	if err := os.WriteFile(handlerPath, []byte(wasiHttpRouterSrc), 0o644); err != nil {
		t.Fatal(err)
	}

	// The wasi:http forms take a handler program: the CLI appends the
	// incoming-handler export to it (emitforms.HTTP_ENTRY_STUB), and the
	// driver has to append the same one.
	cases := []struct {
		name string
		src  string
		path string
		cli  []string
		drv  []string
	}{
		{"x86-64-linux", playgroundFormsProgram, srcPath, []string{"-target", "x86-64-linux", "-emit", "asm"}, []string{"-target", "x86-64-linux"}},
		{"arm64-linux", playgroundFormsProgram, srcPath, []string{"-target", "arm64-linux", "-emit", "asm"}, []string{"-target", "arm64-linux"}},
		{"arm64-android", playgroundFormsProgram, srcPath, []string{"-target", "arm64-android", "-emit", "asm"}, []string{"-target", "arm64-android"}},
		{"arm64-darwin", playgroundFormsProgram, srcPath, []string{"-target", "arm64-darwin", "-emit", "asm"}, []string{"-target", "arm64-darwin", "-emit", "asm"}},
		{"wasm-asm", playgroundFormsProgram, srcPath, []string{"-target", "wasm32-wasi", "-emit", "asm"}, []string{"-emit", "asm"}},
		{"wasm-core-module", playgroundFormsProgram, srcPath, []string{"-target", "wasm32-wasi", "-emit", "core-module"}, []string{"-emit", "core-module"}},
		{"wasm-command-module", playgroundFormsProgram, srcPath, []string{"-target", "wasm32-wasi", "-emit", "command-module"}, []string{"-emit", "command-module"}},
		{"wasm-component", playgroundFormsProgram, srcPath, []string{"-target", "wasm32-wasi"}, nil},
		{"http-asm", wasiHttpRouterSrc, handlerPath, []string{"-target", "wasm32-wasi-http", "-emit", "asm"}, []string{"-target", "wasm32-wasi-http", "-emit", "asm"}},
		{"http-core-module", wasiHttpRouterSrc, handlerPath, []string{"-target", "wasm32-wasi-http", "-emit", "core-module"}, []string{"-target", "wasm32-wasi-http", "-emit", "core-module"}},
		{"http-component", wasiHttpRouterSrc, handlerPath, []string{"-target", "wasm32-wasi-http"}, []string{"-target", "wasm32-wasi-http"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// Through -o: the CLI writes a wasm binary to a file and nowhere
			// else, and every form, assembly included, lands in it.
			outPath := filepath.Join(t.TempDir(), "out")
			cmd := exec.Command(cli, append(tc.cli, "-o", outPath, tc.path, stdlib)...)
			var cliErr bytes.Buffer
			cmd.Stderr = &cliErr
			if err := cmd.Run(); err != nil {
				t.Fatalf("fern %s: %v\n%s", strings.Join(tc.cli, " "), err, cliErr.String())
			}
			want, err := os.ReadFile(outPath)
			if err != nil {
				t.Fatal(err)
			}
			got, stderr, code := runPlayground(t, drv, t.TempDir(), tc.src, tc.drv...)
			if code != 0 {
				t.Fatalf("playground_run %s exited %d\n%s", strings.Join(tc.drv, " "), code, stderr)
			}
			if !bytes.Equal([]byte(got), want) {
				t.Fatalf("playground_run %s emits %d bytes, fern %s %d bytes, first difference at byte %d",
					strings.Join(tc.drv, " "), len(got), strings.Join(tc.cli, " "), len(want), firstDiff([]byte(got), want))
			}
			if len(want) == 0 {
				t.Fatal("both emitted nothing")
			}
		})
	}
}

func firstDiff(a, b []byte) int {
	n := min(len(a), len(b))
	for i := 0; i < n; i++ {
		if a[i] != b[i] {
			return i
		}
	}
	return n
}

// A target or form the driver does not take is refused with the CLI's own
// line, not an empty module: an unknown target, the old `wat` and `component`
// spellings, a wasm form on a native target, and a freestanding target, which
// type-checks and has no backend.
func TestSelfHostPlaygroundRefusesUnknownForms(t *testing.T) {
	_, runner := x86_64Tooling(t)
	if len(runner) != 0 {
		t.Skip("playground driver runs natively; skipping under an exec runner")
	}
	cli := e2eharness.SelfHostCLI(t)
	drv := buildPlaygroundDriver(t)
	quiet := "function main(): i32 { return 0; }\n"
	srcPath := filepath.Join(t.TempDir(), "quiet.fern")
	if err := os.WriteFile(srcPath, []byte(quiet), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{
		{"-target", "riscv64-linux"},
		{"-emit", "elf"},
		{"-emit", "wat"},
		{"-emit", "component"},
		{"-target", "x86-64-linux", "-emit", "core-module"},
		{"-target", "wasm32-wasi-http", "-emit", "command-module"},
		{"-target", "arm64-freestanding"},
		{"-target", "x86-64-freestanding", "-emit", "asm"},
	} {
		cmd := exec.Command(cli, append(append([]string{}, args...), "-o", filepath.Join(t.TempDir(), "out"), srcPath, e2eharness.SelfHostStdlibRoot(t))...)
		var cliErr strings.Builder
		cmd.Stderr = &cliErr
		_ = cmd.Run()
		out, stderr, code := runPlayground(t, drv, t.TempDir(), quiet, args...)
		if code != 2 || cmd.ProcessState.ExitCode() != 2 {
			t.Errorf("%v: playground exited %d and fern %d, want 2 from both\n%s", args, code, cmd.ProcessState.ExitCode(), stderr)
		}
		if stderr != cliErr.String() || stderr == "" {
			t.Errorf("%v: playground refused with %q, fern with %q", args, stderr, cliErr.String())
		}
		if out != "" {
			t.Errorf("%v: emitted %d bytes on a refusal", args, len(out))
		}
	}
}

// The playground's targets are the platforms table's: an unknown one is
// refused naming exactly that list, and every one in it, the freestanding
// pair and arm64-android included, is taken for -check.
func TestSelfHostPlaygroundTargetsAreThePlatformsTable(t *testing.T) {
	_, runner := x86_64Tooling(t)
	if len(runner) != 0 {
		t.Skip("playground driver runs natively; skipping under an exec runner")
	}
	drv := buildPlaygroundDriver(t)
	quiet := "function main(): i32 { return 0; }\n"
	_, stderr, _ := runPlayground(t, drv, t.TempDir(), quiet, "-check", "-target", "riscv64-linux")
	if want := "fern: unknown -target: riscv64-linux (want one of: " + strings.Join(platforms.Targets(), " ") + ")\n"; stderr != want {
		t.Errorf("unknown target refused with %q, want %q", stderr, want)
	}
	for _, target := range platforms.Targets() {
		src := quiet
		if target == "wasm32-wasi-http" {
			src = wasiHttpRouterSrc
		}
		if _, stderr, code := runPlayground(t, drv, t.TempDir(), src, "-check", "-target", target); code != 0 {
			t.Errorf("-check -target %s exited %d, want 0\n%s", target, code, stderr)
		}
	}
}

// The playground runs the CLI's target gate (E066), and its handler gate
// (E080), where the CLI does, on both paths: a program `fern -target T`
// refuses, the browser refuses with the same diagnostics. The CLI renders the
// entry by its path where the playground names no file, so each CLI line
// ends with the playground's.
func TestSelfHostPlaygroundRefusesWhatTheTargetLacks(t *testing.T) {
	_, runner := x86_64Tooling(t)
	if len(runner) != 0 {
		t.Skip("the CLI and the playground driver run natively; skipping under an exec runner")
	}
	cli := e2eharness.SelfHostCLI(t)
	drv := buildPlaygroundDriver(t)
	httpWrites := "import \"std/http\";\nimport \"std/platform\";\n" +
		"function handle(req: HttpRequest, plat: platform.Platform): HttpResponse {\n    write(\"x\");\n    return http.ok(\"hi\");\n}\n"
	for _, tc := range []struct {
		name, target, src, builtin string
	}{
		{"native-only", "wasm32-wasi", "function main(): i32 { return timer_fd(10); }\n", "`timer_fd`"},
		{"private-runtime-floor", "wasm32-wasi", "function main(): i32 { let p: usize = __raw_alloc(64); return 0; }\n", "__raw_alloc"},
		{"no-stdout-on-http", "wasm32-wasi-http", httpWrites, "`write`"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srcPath := filepath.Join(t.TempDir(), "prog.fern")
			if err := os.WriteFile(srcPath, []byte(tc.src), 0o644); err != nil {
				t.Fatal(err)
			}
			for _, mode := range [][]string{{"-check"}, {"-emit", "asm"}} {
				cmd := exec.Command(cli, append(append([]string{"-target", tc.target}, mode...), srcPath, e2eharness.SelfHostStdlibRoot(t))...)
				var cliErr strings.Builder
				cmd.Stderr = &cliErr
				_ = cmd.Run()
				out, stderr, code := runPlayground(t, drv, t.TempDir(), tc.src, append([]string{"-target", tc.target}, mode...)...)
				if code != 1 || cmd.ProcessState.ExitCode() != 1 || out != "" {
					t.Fatalf("%v: playground exited %d with %d bytes, fern %d; want 1 and nothing from both\n%s", mode, code, len(out), cmd.ProcessState.ExitCode(), stderr)
				}
				got, want := gateLines(stderr), gateLines(cliErr.String())
				if len(got) == 0 || len(got) != len(want) {
					t.Fatalf("%v: playground reported %q, fern %q", mode, got, want)
				}
				for i := range got {
					if !strings.HasSuffix(want[i], got[i]) || !strings.Contains(got[i], tc.builtin) {
						t.Errorf("%v: playground line %q, fern line %q", mode, got[i], want[i])
					}
				}
			}
		})
	}
}

func gateLines(stderr string) []string {
	var out []string
	for _, line := range strings.Split(stderr, "\n") {
		if strings.Contains(line, "error[E001]") || strings.Contains(line, "error[E066]") || strings.Contains(line, "error[E080]") {
			out = append(out, line)
		}
	}
	return out
}
