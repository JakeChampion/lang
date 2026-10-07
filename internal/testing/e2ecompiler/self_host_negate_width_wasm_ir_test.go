package e2ecompiler

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// --- Unary minus keeps its operand's width ----------------------------------
//
// `-x` must negate at the operand's width; a 32-bit `0 - x` for every integer
// type is wrong for i64. The register backends hide that — the operation lands
// in a 64-bit register and the wrong width is invisible — so an i64 negation
// only shows up on the one typed backend:
//
//	self-host, -target wasm32-wasi:
//	  Error: failed to compile: wasm[0]::function[27]
//	  type mismatch: expected i32, found i64
//
// The rows reach the negation through a method receiver, `(-b).to_string()`,
// and through a local.
//
// The rows assert the printed VALUE, not that the module loads: a width bug
// that truncates rather than failing validation is the one this would miss
// otherwise. i32 and f64 are the controls: they pin that selecting a width
// did not disturb them.
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
			// The issue's repro. 5000000000 does not fit in i32, so a 32-bit
			// negation cannot even represent the answer.
			name: "i64 receiver",
			src: `import "std/i64";
function main(): i32 { let b: i64 = 5000000000; print((-b).to_string()); return 0; }`,
			want: "-5000000000\n",
		},
		{
			// Bound to a local rather than used as a receiver, so the two
			// routes into the lowering are both covered.
			name: "i64 local",
			src: `import "std/i64";
function main(): i32 { let b: i64 = 5000000000; let c: i64 = -b; print(c.to_string()); return 0; }`,
			want: "-5000000000\n",
		},
		{
			// Negating i64 MIN is the value that has no positive counterpart;
			// it wraps to itself, which docs/INTEGER-SEMANTICS.md defines.
			name: "i64 min wraps to itself",
			src: `import "std/i64";
function main(): i32 { let b: i64 = 0 - 9223372036854775807 - 1; print((-b).to_string()); return 0; }`,
			want: "-9223372036854775808\n",
		},
		{
			// `0 - 2147483647 - 1` is how std/i32 spells i32::MIN, and constfold
			// folds it to unary minus over the magnitude 2147483648 — a literal
			// too big for i32 until the sign is applied. Deciding the width from
			// the operand alone made the negation 64-bit, and the i32 compare
			// against it failed validation in every module that kept
			// i32.to_string (#8656). The rows avoid to_string on purpose: the
			// value is pinned by comparisons the driver can lower without it.
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
			// f64 negation is its own arm (fneg) and is untouched by the width
			// selection; this pins that.
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
