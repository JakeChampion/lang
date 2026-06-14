package e2e

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestSelfHostIREligibilityProbe exercises the asm_ir_run driver's `-ir-probe`
// flag, which prints asm_ir.eligibility_report(mod) instead of emitting asm: a
// per-function breakdown of what lowers through the stack-IR path vs what bails
// to the AST emitter, plus the module verdict the `-ir` path routes on.
//
// This makes the IR-subset frontier OBSERVABLE — the prerequisite for goal-1
// widening work, which otherwise can't tell whether a construct moved onto the
// IR path (the differential gate only proves "still correct", not "now lowers").
// The cases pin the four signals the report distinguishes: a fully-eligible
// module, a per-function `BAIL call` (a call to an unknown / builtin-only name),
// the no-main "module: AST" verdict, and — as a regression guard on a frontier
// comment that had gone stale — that break/continue inside a `for x in arr`
// body DOES lower (the increment-at-top loop shape, #2788, makes `continue`
// safe, so these statements are on the IR path, not a bail).
func TestSelfHostIREligibilityProbe(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := writeSelfHostAsmProject(t)
	// The probe driver = asm_ir_run.fern (writeSelfHostAsmProject copies its
	// ./-imports; std/io resolves from the real stdlib root).
	src, err := os.ReadFile(filepath.Join("../../examples/self_host", "asm_ir_run.fern"))
	if err != nil {
		t.Fatalf("read asm_ir_run.fern: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "asm_ir_run.fern"), src, 0o644); err != nil {
		t.Fatalf("write asm_ir_run.fern: %v", err)
	}
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_ir_run.fern", "airun")

	probe := func(t *testing.T, prog string) string {
		t.Helper()
		var cmd *exec.Cmd
		if len(runner) == 0 {
			cmd = exec.Command(driverBin, "-ir-probe")
		} else {
			cmd = exec.Command(runner[0], append(append(append([]string{}, runner[1:]...), driverBin), "-ir-probe")...)
		}
		cmd.Stdin = bytes.NewReader([]byte(prog))
		out, err := cmd.Output()
		if err != nil {
			t.Fatalf("probe driver failed for %q: %v", prog, err)
		}
		return string(out)
	}

	cases := []struct {
		name        string
		src         string
		wantVerdict string   // a line the report must contain
		wantLines   []string // per-function lines the report must contain
	}{
		{
			name:        "pure-i32",
			src:         "function add(a: i32, b: i32): i32 { return a + b; }\nfunction main(): i32 { return add(2, 3); }",
			wantVerdict: "module: IR",
			wantLines:   []string{"add: ir", "main: ir"},
		},
		{
			name:        "unknown-call-bails",
			src:         "function main(): i32 { return mystery(3); }",
			wantVerdict: "module: AST",
			wantLines:   []string{"main: BAIL call"},
		},
		{
			name:        "no-main-is-ast",
			src:         "function helper(): i32 { return 1; }",
			wantVerdict: "module: AST",
			wantLines:   []string{"helper: ir"},
		},
		{
			// Regression guard: break/continue in a for-x-in-array body lowers.
			name:        "break-continue-array-for-lowers",
			src:         "function main(): i32 {\n var acc: i32 = 0;\n var xs: i32[] = [1, 2, 3, 4, 5];\n for x in xs { if (x == 2) { continue; } if (x == 4) { break; } acc = acc + x; }\n return acc;\n}",
			wantVerdict: "module: IR",
			wantLines:   []string{"main: ir"},
		},
		{
			// Method receivers lower; the report keys them by the dispatch label.
			name:        "method-receiver-lowers",
			src:         "struct P { x: i32 }\nfunction (p: P) get(): i32 { return p.x; }\nfunction main(): i32 { var p = P { x: 7 }; return p.get(); }",
			wantVerdict: "module: IR",
			wantLines:   []string{"P.get: ir", "main: ir"},
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			rep := probe(t, c.src)
			if !strings.Contains(rep, c.wantVerdict) {
				t.Errorf("verdict: report missing %q\n--- report ---\n%s", c.wantVerdict, rep)
			}
			for _, line := range c.wantLines {
				if !strings.Contains(rep, line) {
					t.Errorf("report missing line %q\n--- report ---\n%s", line, rep)
				}
			}
		})
	}
}
