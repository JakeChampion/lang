package e2ecompiler

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestSelfHostTypedArrayInvariantIR(t *testing.T) {
	wasmtime, err := exec.LookPath("wasmtime")
	if err != nil {
		t.Skip("wasmtime not on PATH")
	}
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostFiles(t, dir, "lexer.fern", "parser.fern", "util.fern", "astwalk.fern", "asmcore.fern", "ir.fern", "irtables.fern", "lift.fern", "irverify.fern", "irverifystack.fern", "irverifygate.fern", "asm_ir.fern", "wasm_ir.fern", "drivers/wasm_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "drivers/wasm_run.fern", "wasm_run")

	for _, tc := range []struct {
		name string
		src  string
		exit int
		argv []string
	}{
		{"args-index", `function main(): i32 { let a = args(); return a.len(); }`, 3, []string{"A", "B"}},
		{"args-for", `function main(): i32 { let a = args(); let n = 0; for s in a { n = n + 1; } return n; }`, 3, []string{"A", "B"}},

		{"split-index", `function main(): i32 { let xs = "a-b-c".split("-"); return xs.len(); }`, 3, nil},
		{"split-for", `function main(): i32 { let xs = "a-b-c".split("-"); let n = 0; for s in xs { n = n + s.len(); } return n; }`, 3, nil},
		{"strret-index", "function mk(): string[] { return [\"ab\", \"cde\"]; }\nfunction main(): i32 { let xs = mk(); return xs[0].len() + xs[1].len(); }", 5, nil},
		{"strret-for", "function mk(): string[] { return [\"ab\", \"cde\"]; }\nfunction main(): i32 { let xs = mk(); let n = 0; for s in xs { n = n + s.len(); } return n; }", 5, nil},

		{"bytes-for", `function main(): i32 { let b = "abc".bytes(); let n = 0; for c in b { n = n + 1; } return n; }`, 3, nil},
		{"lines-for", "function main(): i32 { let l = \"a\\nb\\nc\".lines(); let n = 0; for x in l { n = n + 1; } return n; }", 3, nil},

		{"i64ret-for", "function mk(): i64[] { return [5000000000, 1]; }\nfunction main(): i32 { let xs = mk(); let n = 0; for v in xs { n = n + 1; } return n; }", 2, nil},
		{"f64ret-for", "function mk(): f64[] { return [1.5, 2.5]; }\nfunction main(): i32 { let xs = mk(); let n = 0; for v in xs { n = n + 1; } return n; }", 2, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			src := []byte(tc.src + "\n")
			route := strings.TrimSpace(string(runCapture(t, gcc, runner, driverBin, src, "-decide")))
			if route != "ir" {
				t.Fatalf("%s routed %q, want \"ir\" — an element-kind predicate and the array-ness decision disagree about this expression again", tc.name, route)
			}
			wat := runCapture(t, gcc, runner, driverBin, src)
			if len(wat) == 0 {
				t.Fatal("wasm emitter produced 0 bytes")
			}
			watPath := filepath.Join(dir, tc.name+".wat")
			if werr := os.WriteFile(watPath, wat, 0o644); werr != nil {
				t.Fatalf("write wat: %v", werr)
			}
			cmd := exec.Command(wasmtime, append([]string{"run", watPath}, tc.argv...)...)
			out, _ := cmd.CombinedOutput()
			if code := cmd.ProcessState.ExitCode(); code != tc.exit {
				t.Errorf("%s: wasm exited %d, want %d\n%s", tc.name, code, tc.exit, out)
			}
		})
	}
}
