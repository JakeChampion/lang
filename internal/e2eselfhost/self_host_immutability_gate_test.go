package e2eselfhost

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestSelfHostImmutabilityGateX86_64 verifies the self-host compile driver
// (asm_load_run) ENFORCES the immutable-data cycle rules: a program that
// violates E048 (field assign) / E049 (reference-capture write-back) / E055
// (discarded pure result) / E056 (subscript assign) / E057 (Cell over a
// composite) is rejected with a formatted diagnostic on stderr and a non-zero
// exit, instead of silently compiling — the full cycle-rule set #2678 requires
// the self-host drivers to gate. The valid (functional-update / scalar-Cell)
// forms compile cleanly. This is the self-host enforcement that the Go
// reference compiler has via its checker (docs/IMMUTABILITY-MIGRATION-PLAN.md
// §4); the gate filters check_module to the cycle rules so the partial
// checker's other rules can't false-positive-reject a valid program.
func TestSelfHostImmutabilityGateX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	if len(runner) != 0 {
		t.Skip("file-loading driver test runs only natively (argv paths)")
	}
	dir := writeSelfHostAsmProject(t) // lexer, parser, asm
	for _, name := range []string{"flatten.fern", "checker.fern", "asm_arm64_ir.fern", "asm_load_run.fern"} {
		src, err := os.ReadFile(filepath.Join("../../examples/self_host", name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		if err := os.WriteFile(filepath.Join(dir, name), src, 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	mmc := buildSelfHostBin(t, gcc, dir, "asm_load_run.fern", "mmc")

	cases := []struct {
		name     string
		src      string
		wantDiag string // non-empty ⇒ expect rejection (exit≠0) with this on stderr
	}{
		{
			name:     "field-assign-E048",
			src:      "struct P { x: i32 }\nfunction main(): i32 { var p: P = P { x: 1 }; p.x = 5; return p.x; }\n",
			wantDiag: "error[E048]",
		},
		{
			name:     "subscript-assign-E056",
			src:      "function main(): i32 { var a: i32[] = [1, 2, 3]; a[0] = 9; return a[0]; }\n",
			wantDiag: "error[E056]",
		},
		{
			// E049: a reference-typed value (here an i32[]) captured by a closure
			// is read-only — reassigning it inside the closure could close a
			// reference cycle, so the native compiler rejects it before codegen.
			// The self-host build gate must match (it filters check_module to the
			// cycle rules, of which E049 is the reference-capture write-back one).
			name:     "captured-ref-assign-E049",
			src:      "function main(): i32 { var xs: i32[] = [1]; var f = function (): i32 { xs = xs.append(2); return xs.len(); }; return f(); }\n",
			wantDiag: "error[E049]",
		},
		{
			name:     "discarded-append-E055",
			src:      "function main(): i32 { var a: i32[] = [1]; a.append(2); return a.len(); }\n",
			wantDiag: "error[E055]",
		},
		{
			// E057: a Cell[T] element must be cycle-free — a composite element
			// (here a struct) could reconstruct a reference cycle, so the
			// native compiler rejects it before codegen. The self-host build
			// gate must match (it previously filtered E057 out and silently
			// compiled this — more permissive than native).
			name:     "cell-composite-E057",
			src:      "struct P { x: i32 }\nfunction main(): i32 { var c = cell_new(P { x: 1 }); return 0; }\n",
			wantDiag: "error[E057]",
		},
		{
			// A Cell over a scalar is cycle-free and still compiles cleanly —
			// guards against the gate over-rejecting valid Cell uses.
			name:     "cell-scalar-ok",
			src:      "function main(): i32 { var c = cell_new(0); c.set(c.get() + 1); return c.get(); }\n",
			wantDiag: "",
		},
		{
			// The functional-update forms compile cleanly (no diagnostic).
			name:     "valid-functional-update",
			src:      "struct P { x: i32 }\nfunction main(): i32 { var p: P = P { x: 1 }; p = P { ...p, x: 5 }; var a: i32[] = [1, 2, 3]; a = a.with(0, 9); return p.x + a[0]; }\n",
			wantDiag: "",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			prog := filepath.Join(t.TempDir(), "prog.fern")
			if err := os.WriteFile(prog, []byte(tc.src), 0o644); err != nil {
				t.Fatalf("write prog: %v", err)
			}
			out, err := exec.Command(mmc, prog).Output()
			if tc.wantDiag == "" {
				if err != nil || len(out) == 0 {
					stderr := ""
					if ee, ok := err.(*exec.ExitError); ok {
						stderr = string(ee.Stderr)
					}
					t.Fatalf("valid program rejected: %v\nstderr: %s", err, stderr)
				}
				return
			}
			// Expect rejection: non-zero exit + the diagnostic on stderr.
			ee, ok := err.(*exec.ExitError)
			if !ok {
				t.Fatalf("expected rejection (exit≠0), got success with %d bytes of asm", len(out))
			}
			if !strings.Contains(string(ee.Stderr), tc.wantDiag) {
				t.Errorf("stderr missing %q\ngot stderr: %s", tc.wantDiag, ee.Stderr)
			}
		})
	}
}
