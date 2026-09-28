package e2eselfhost

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// The playground driver emits the bytes the CLI writes for the same program.
//
// Every output form the page asks for — a native target's assembly for the
// assembly pane, the wasm core module as text or as the binary a preview-1
// host runs, the wasi:cli/run component the download offers — comes out of
// the CLI's own pipeline (emitforms.substitution, the same gates and the same
// emitter entry points), so what the page shows is what `fern` would build.
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
    var f: i32 = fact(5);
    print("5! = " + f.to_string());
    return f;
}
`

func TestSelfHostPlaygroundEmitsWhatTheCLIEmits(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	if len(runner) != 0 {
		t.Skip("the CLI and the playground driver run natively; skipping under an exec runner")
	}
	stdlib, err := filepath.Abs(filepath.Join("..", "stdlib"))
	if err != nil {
		t.Fatal(err)
	}
	dir := writeSelfHostAsmProject(t)
	copySelfHostDriver(t, dir, "fern.fern")
	cli := buildSelfHostBin(t, gcc, dir, "fern.fern", "fern")
	drv := buildPlaygroundDriver(t)
	srcPath := filepath.Join(t.TempDir(), "prog.fern")
	if err := os.WriteFile(srcPath, []byte(playgroundFormsProgram), 0o644); err != nil {
		t.Fatal(err)
	}

	cases := []struct {
		name string
		cli  []string
		drv  []string
	}{
		{"x86-64-linux", []string{"-target", "x86-64-linux", "-emit", "asm"}, []string{"-target", "x86-64-linux"}},
		{"arm64-linux", []string{"-target", "arm64-linux", "-emit", "asm"}, []string{"-target", "arm64-linux"}},
		{"arm64-darwin", []string{"-target", "arm64-darwin", "-emit", "asm"}, []string{"-target", "arm64-darwin", "-emit", "asm"}},
		{"wasm-wat", []string{"-target", "wasm32-wasi", "-emit", "asm"}, nil},
		{"wasm-core-module", []string{"-target", "wasm32-wasi", "-emit", "core-module"}, []string{"-emit", "core-module"}},
		{"wasm-component", []string{"-target", "wasm32-wasi"}, []string{"-target", "wasm32-wasi", "-emit", "component"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cmd := exec.Command(cli, append(tc.cli, srcPath, stdlib)...)
			var want, cliErr bytes.Buffer
			cmd.Stdout = &want
			cmd.Stderr = &cliErr
			if err := cmd.Run(); err != nil {
				t.Fatalf("fern %s: %v\n%s", strings.Join(tc.cli, " "), err, cliErr.String())
			}
			got, stderr, code := runPlayground(t, drv, t.TempDir(), playgroundFormsProgram, tc.drv...)
			if code != 0 {
				t.Fatalf("playground_run %s exited %d\n%s", strings.Join(tc.drv, " "), code, stderr)
			}
			if !bytes.Equal([]byte(got), want.Bytes()) {
				t.Fatalf("playground_run %s emits %d bytes, fern %s %d bytes, first difference at byte %d",
					strings.Join(tc.drv, " "), len(got), strings.Join(tc.cli, " "), want.Len(), firstDiff([]byte(got), want.Bytes()))
			}
			if want.Len() == 0 {
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

// A target or form the driver does not know is a refusal naming what it
// wanted, not an empty module, and a wasm form on a native target is refused
// the same way.
func TestSelfHostPlaygroundRefusesUnknownForms(t *testing.T) {
	_, runner := x86_64Tooling(t)
	if len(runner) != 0 {
		t.Skip("playground driver runs natively; skipping under an exec runner")
	}
	drv := buildPlaygroundDriver(t)
	for _, tc := range []struct {
		args []string
		want string
	}{
		{[]string{"-target", "riscv64-linux"}, "unknown -target: riscv64-linux"},
		{[]string{"-emit", "elf"}, "unknown -emit: elf"},
		{[]string{"-target", "x86-64-linux", "-emit", "core-module"}, "is a wasm output form"},
	} {
		out, stderr, code := runPlayground(t, drv, t.TempDir(), playgroundFormsProgram, tc.args...)
		if code != 2 {
			t.Errorf("%v: exited %d, want 2\n%s", tc.args, code, stderr)
		}
		if !strings.Contains(stderr, tc.want) {
			t.Errorf("%v: stderr %q does not name the refusal %q", tc.args, stderr, tc.want)
		}
		if out != "" {
			t.Errorf("%v: emitted %d bytes on a refusal", tc.args, len(out))
		}
	}
}
