package e2ecompiler

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// Every result type E084 allows `main` maps to the same process status in
// both interpreters and the compiled x86-64 and wasm32-wasi programs: an
// integer of any width is its low byte, a boolean 1 or 0, void 0. WASI's
// proc_exit takes only a status below 126, so wasm exits 1 for the rest.
func TestMainResultStatusAgrees(t *testing.T) {
	cli := buildSelfHostCLI(t)
	oracle := buildLangBinForInterp(t)
	for _, c := range []struct {
		name, src string
		want      int
	}{
		{"void", "function main(): void { print(\"\"); }\n", 0},
		{"boolean-true", "function main(): boolean { return true; }\n", 1},
		{"boolean-false", "function main(): boolean { return false; }\n", 0},
		{"i32", "function main(): i32 { return 42; }\n", 42},
		{"i32-negative", "function main(): i32 { return -1; }\n", 255},
		{"i64-low-byte", "function main(): i64 { return 5000000003 as i64; }\n", 3},
		{"i64-negative", "function main(): i64 { return -3 as i64; }\n", 253},
		{"u8", "function main(): u8 { return 255 as u8; }\n", 255},
		{"u32", "function main(): u32 { return 300 as u32; }\n", 44},
		{"u64", "function main(): u64 { return 4294967298 as u64; }\n", 2},
		{"usize", "function main(): usize { return 7 as usize; }\n", 7},
		// A method named main is not the entry point, whatever it returns.
		{"beside-a-method-main", "struct S { x: i32 }\nfunction (s: S) main(): i64 { return 1 as i64; }\nfunction main(): i32 { return 9; }\n", 9},
	} {
		t.Run(c.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "main.fern")
			if err := os.WriteFile(path, []byte(c.src), 0o644); err != nil {
				t.Fatal(err)
			}
			for leg, cmd := range map[string]*exec.Cmd{
				"self-host interp": runX86_64Bin(cli.runner, cli.bin, "-interp", path, cli.stdlib),
				"oracle interp":    exec.Command(oracle, "-interp", path),
			} {
				out, err := cmd.CombinedOutput()
				if cmd.ProcessState == nil {
					t.Fatalf("%s did not run: %v", leg, err)
				}
				if code := cmd.ProcessState.ExitCode(); code != c.want {
					t.Errorf("%s: exit %d, want %d\n%s", leg, code, c.want, out)
				}
			}
			if stderr, code := cli.exitOfFile(t, path, "x86-64-linux", nil); code != c.want {
				t.Errorf("x86-64: exit %d, want %d\n%s", code, c.want, stderr)
			}
			want := c.want
			if want >= 126 {
				want = 1
			}
			if stderr, code := cli.exitOfFile(t, path, "wasm32-wasi", nil); code != want {
				t.Errorf("wasm32-wasi: exit %d, want %d\n%s", code, want, stderr)
			}
		})
	}
}
