package e2ecompiler

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSelfHostSemReachScalarArguments(t *testing.T) {
	cli := buildSelfHostCLI(t)
	const functions = `@noinline function slow(n: i32): i32 { return n * 7 + 1; }
@noinline function branch(flag: boolean): i32 {
  if (flag) { return slow(7); }
  return 3;
}
`
	for _, tc := range []struct {
		name, main string
		want       int
		slow       bool
	}{
		{"constant", "return branch(false);", 3, false},
		{"mixed", "return branch(false) + branch(true);", 53, true},
		{"unknown", "return branch(args().len() > 0);", 50, true},
		{"function-value", "let f: (boolean) => i32 = branch; return branch(false) + f(args().len() > 0);", 53, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			src := functions + "function main(): i32 { " + tc.main + " }\n"
			path := filepath.Join(t.TempDir(), "arguments.fern")
			if err := os.WriteFile(path, []byte(src), 0o644); err != nil {
				t.Fatal(err)
			}
			for _, target := range []string{"x86-64-linux", "arm64-linux"} {
				asm := readFile(t, cli.emit(t, path, target))
				if got := strings.Contains(asm, "\n__fn_slow:"); got != tc.slow {
					t.Errorf("%s: slow emitted %v, want %v", target, got, tc.slow)
				}
			}
			for _, target := range []string{"x86-64-linux", "arm64-linux", "wasm32-wasi"} {
				if _, got := cli.exitOf(t, src, target); got != tc.want {
					t.Errorf("%s: exit %d, want %d", target, got, tc.want)
				}
			}
		})
	}
}
