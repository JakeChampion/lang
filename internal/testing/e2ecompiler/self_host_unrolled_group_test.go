package e2ecompiler

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// An unrolled loop makes the base of each index again, so the reads of one
// row have bases that are equal values and not one value. Those reads get one
// check for the group, also when they read floats.
const unrolledGroupLoops = `@noinline function row_f(t: f64[], y: i32): f64 {
  let s = 0.0;
  for v in 0..8 { s += t[y * 8 + v]; }
  return s;
}
@noinline function row_i(t: i32[], y: i32): i32 {
  let s = 0;
  for v in 0..8 { s += t[y * 8 + v]; }
  return s;
}
@noinline function fseq(n: i32): f64[] {
  let a: f64[] = [];
  for i in 0..n { a = a.append(i as f64); }
  return a;
}
@noinline function iseq(n: i32): i32[] {
  let a: i32[] = [];
  for i in 0..n { a = a.append(i); }
  return a;
}
`

func TestSelfHostUnrolledGroupCheck(t *testing.T) {
	cli := buildSelfHostCLI(t)
	program := unrolledGroupLoops + `function main(): i32 {
  if (row_f(fseq(24), 1) != 92.0 || row_f(fseq(16), 0) != 28.0) { return 1; }
  if (row_i(iseq(24), 2) != 156 || row_i(iseq(16), 1) != 92) { return 2; }
  return 0;
}`
	src := filepath.Join(t.TempDir(), "unrolled_group.fern")
	if err := os.WriteFile(src, []byte(program), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, target := range []string{"x86-64-linux", "arm64-linux", "wasm32-wasi"} {
		t.Run(target, func(t *testing.T) {
			stderr, code := cli.exitOf(t, program, target)
			if code != 0 {
				t.Fatalf("exit %d, want 0\n%s", code, stderr)
			}
			// The first index of each row is in range, and the last is not.
			for name, call := range map[string]string{
				"row_f": "row_f(fseq(20), 2) as i32",
				"row_i": "row_i(iseq(20), 2)",
			} {
				out := unrolledGroupLoops + "function main(): i32 { return " + call + "; }"
				stderr, code := cli.exitOf(t, out, target)
				if target == "wasm32-wasi" && code == 0 || target != "wasm32-wasi" && (code != 134 || !strings.Contains(stderr, "fern: array index out of range")) {
					t.Errorf("%s past the end exited %d, want the array abort\n%s", name, code, stderr)
				}
			}
			if target == "wasm32-wasi" {
				return
			}
			asm, err := os.ReadFile(cli.emit(t, src, target))
			if err != nil {
				t.Fatal(err)
			}
			for _, name := range []string{"row_f", "row_i"} {
				body := asmFunction(string(asm), "__fn_"+name+":")
				if got := strings.Count(body, "oob_abort"); got != 1 {
					t.Errorf("%s has %d checks, want one for the 8 reads of the row\n%s", name, got, body)
				}
			}
		})
	}
}
