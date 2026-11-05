package e2ecompiler

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

var constCases = []struct {
	name string
	src  string
	exit int
}{
	{"simple", "const X: i32 = 42; function main(): i32 { return X; }", 42},
	{"two-consts", "const A: i32 = 10; const B: i32 = 32; function main(): i32 { return A + B; }", 42},
	{"pub-const-loop", "pub const N: i32 = 7; function main(): i32 { let s: i32 = 0; let i: i32 = 0; while (i < N) { s = s + i; i = i + 1; } return s; }", 21},
	{"const-expr", "const BASE: i32 = 1000; function main(): i32 { return (BASE / 100) + 32; }", 42},
}

func TestSelfHostConstX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := writeSelfHostAsmProject(t)
	src, err := os.ReadFile("../../../compiler/drivers/asm_run.fern")
	if err != nil {
		t.Fatalf("read asm_run.fern: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "drivers/asm_run.fern"), src, 0o644); err != nil {
		t.Fatalf("write asm_run.fern: %v", err)
	}
	driverBin := buildSelfHostBin(t, gcc, dir, "drivers/asm_run.fern", "driver")

	for _, tc := range constCases {
		t.Run(tc.name, func(t *testing.T) {
			asm := runCapture(t, gcc, runner, driverBin, []byte(tc.src))
			if len(asm) == 0 {
				t.Fatal("self-host compiler emitted 0 bytes")
			}
			progBin := buildBin(t, gcc, dir, tc.name, string(asm))
			var cmd *exec.Cmd
			if len(runner) == 0 {
				cmd = exec.Command(progBin)
			} else {
				cmd = exec.Command(runner[0], append(runner[1:], progBin)...)
			}
			_ = cmd.Run()
			if code := cmd.ProcessState.ExitCode(); code != tc.exit {
				t.Errorf("%s exited %d, want %d", tc.name, code, tc.exit)
			}
		})
	}
}

func TestSelfHostConstArm64(t *testing.T) {
	cli := buildSelfHostCLI(t)
	for _, tc := range constCases {
		t.Run(tc.name, func(t *testing.T) {
			src := tc.src
			for _, target := range []string{"arm64-linux"} {
				if stderr, code := cli.exitOf(t, src, target); code != tc.exit {
					t.Errorf("%s on %s exited %d, want %d\n%s", tc.name, target, code, tc.exit, stderr)
				}
			}
		})
	}
}

// A module uses a const of a module it imports, though the importer sorts
// first in the merge. A forward reference inside a module stays E093.
func TestSelfHostConstFromImportedModule(t *testing.T) {
	cli := buildSelfHostCLI(t)
	dir := t.TempDir()
	for name, source := range map[string]string{
		"a.fern":       "import \"./zz\";\npub const N: i32 = 48 + zz.A;\n",
		"zz.fern":      "pub const A: i32 = 64;\n",
		"main.fern":    "import \"./a\";\nfunction main(): i32 { return a.N - 70; }\n",
		"forward.fern": "import \"./zz\";\nconst X: i32 = Y + zz.A;\nconst Y: i32 = 1;\nfunction main(): i32 { return X; }\n",
	} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(source), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if stderr, code := cli.exitOfFile(t, filepath.Join(dir, "main.fern"), "x86-64-linux", nil); code != 42 {
		t.Fatalf("main.fern exited %d, want 42\n%s", code, stderr)
	}
	out, err := runX86_64Bin(cli.runner, cli.bin, "-check", filepath.Join(dir, "forward.fern"), cli.stdlib).CombinedOutput()
	if err == nil || !strings.Contains(string(out), `error[E093]: const X: "Y" is not a constant`) {
		t.Fatalf("a forward reference in a module: %v\n%s", err, out)
	}
}
