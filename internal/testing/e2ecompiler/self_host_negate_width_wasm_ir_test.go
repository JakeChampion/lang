package e2ecompiler

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestSelfHostNegateWidthWasm(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping self-host wasm negate width e2e")
	}
	l := newWasmStdlibLoader(t)
	dir := t.TempDir()

	cases := []struct {
		name string
		src  string
		want string
	}{
		{
			name: "i64 receiver",
			src: `import "std/i64";
function main(): i32 { let b: i64 = 5000000000; print((-b).to_string()); return 0; }`,
			want: "-5000000000\n",
		},
		{
			name: "i64 local",
			src: `import "std/i64";
function main(): i32 { let b: i64 = 5000000000; let c: i64 = -b; print(c.to_string()); return 0; }`,
			want: "-5000000000\n",
		},
		{
			name: "i64 min wraps to itself",
			src: `import "std/i64";
function main(): i32 { let b: i64 = 0 - 9223372036854775807 - 1; print((-b).to_string()); return 0; }`,
			want: "-9223372036854775808\n",
		},
		{
			name: "i32 min literal",
			src: `function main(): i32 {
  let n: i32 = 0 - 2147483647 - 1;
  let m: i32 = 7;
  if (m == 0 - 2147483647 - 1) { print("eq-bad"); } else { print("eq-ok"); }
  if (n < 0) { print("neg-ok"); } else { print("neg-bad"); }
  if (n == -2147483648) { print("min-ok"); } else { print("min-bad"); }
  return 0;
}`,
			want: "eq-ok\nneg-ok\nmin-ok\n",
		},
		{
			name: "i32 control",
			src: `import "std/i32";
function main(): i32 { let b: i32 = 5; print((-b).to_string()); return 0; }`,
			want: "-5\n",
		},
		{
			name: "f64 control",
			src: `function main(): i32 {
  let b: f64 = 2.5;
  let c: f64 = -b;
  if (c == 0.0 - 2.5) { print("neg-ok"); } else { print("neg-bad"); }
  if (0.0 - c == b) { print("back-ok"); } else { print("back-bad"); }
  return 0;
}`,
			want: "neg-ok\nback-ok\n",
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			wat := l.emit(t, c.src)
			watFile := filepath.Join(dir, strings.ReplaceAll(c.name, " ", "_")+".wat")
			if werr := os.WriteFile(watFile, wat, 0o644); werr != nil {
				t.Fatalf("write wat: %v", werr)
			}
			run := exec.Command("wasmtime", "run", watFile)
			var out, runErr strings.Builder
			run.Stdout, run.Stderr = &out, &runErr
			_ = run.Run()
			if run.ProcessState == nil || run.ProcessState.ExitCode() != 0 {
				t.Fatalf("wasmtime refused or aborted the module: exit %v\n%s",
					run.ProcessState, runErr.String())
			}
			if out.String() != c.want {
				t.Errorf("stdout = %q, want %q", out.String(), c.want)
			}
		})
	}
}
