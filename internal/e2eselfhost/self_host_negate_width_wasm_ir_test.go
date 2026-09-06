package e2eselfhost

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// --- Unary minus keeps its operand's width ----------------------------------
//
// lower_expr_unary lowered `-x` as `const_i32(0); x; sub(width 0)` for every
// integer type. The register backends are right by ACCIDENT — the operation
// lands in a 64-bit register and the wrong width is invisible — so an i64
// negation only shows up on the one typed backend:
//
//	self-host, -target wasm32-wasi:
//	  Error: failed to compile: wasm[0]::function[27]
//	  type mismatch: expected i32, found i64
//
// lower_i64's own unary arm has had the right shape all along (const_i64 "0"
// and a 64-bit sub), so this is the case where a 64-bit value reaches the
// 32-bit lowering instead — through a method receiver, here `(-b).to_string()`.
//
// The rows assert the printed VALUE, not that the module loads: a width bug
// that truncates rather than failing validation is the one this would miss
// otherwise. i32 and f64 are the controls — they were already correct, and
// they pin that selecting a width did not disturb them.
func TestSelfHostNegateWidthWasm(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping self-host wasm negate width e2e")
	}
	gcc, _ := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "wasm_ir_run.fern")
	driverBin := cachedDriverBin(t, gcc, dir, "wasm_ir_run.fern")

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
function main(): i32 { var b: i64 = 5000000000; print((-b).to_string()); return 0; }`,
			want: "-5000000000\n",
		},
		{
			// Bound to a local rather than used as a receiver, so the two
			// routes into the lowering are both covered.
			name: "i64 local",
			src: `import "std/i64";
function main(): i32 { var b: i64 = 5000000000; var c: i64 = -b; print(c.to_string()); return 0; }`,
			want: "-5000000000\n",
		},
		{
			// Negating i64 MIN is the value that has no positive counterpart;
			// it wraps to itself, which docs/INTEGER-SEMANTICS.md defines.
			name: "i64 min wraps to itself",
			src: `import "std/i64";
function main(): i32 { var b: i64 = 0 - 9223372036854775807 - 1; print((-b).to_string()); return 0; }`,
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
  var n: i32 = 0 - 2147483647 - 1;
  var m: i32 = 7;
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
function main(): i32 { var b: i32 = 5; print((-b).to_string()); return 0; }`,
			want: "-5\n",
		},
		{
			// f64 negation is its own arm (fneg) and is untouched by the width
			// selection; this pins that. It avoids to_string because the wasm
			// driver resolves no imports, so `std/f64` is not available to it —
			// the comparisons say the same thing about the value.
			name: "f64 control",
			src: `function main(): i32 {
  var b: f64 = 2.5;
  var c: f64 = -b;
  if (c == 0.0 - 2.5) { print("neg-ok"); } else { print("neg-bad"); }
  if (0.0 - c == b) { print("back-ok"); } else { print("back-bad"); }
  return 0;
}`,
			want: "neg-ok\nback-ok\n",
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			cmd := exec.Command(driverBin, "-ir")
			cmd.Stdin = bytes.NewReader([]byte(c.src))
			cmd.Env = []string{"PATH=/usr/bin:/bin"}
			var emitErr strings.Builder
			cmd.Stderr = &emitErr
			wat, err := cmd.Output()
			if err != nil || len(wat) == 0 {
				t.Fatalf("self-host wasm emit failed: %v\n%s", err, emitErr.String())
			}
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
