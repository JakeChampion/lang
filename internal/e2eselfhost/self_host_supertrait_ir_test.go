package e2eselfhost

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// supertraitIRCases exercise supertraits (`trait Ord: Eq`) through the
// self-hosted compiler. The self-host dispatches by receiver type and
// doesn't validate conformance, so it carries no supertrait semantics —
// but it must PARSE the `: Eq` clause (parse_trait_decl skips it) and a
// bounded generic over `Ord` whose body calls the supertrait's `eq`
// method still runs: monomorphisation clones `rank` per concrete type and
// dispatches `a.eq(b)` / `a.lt(b)` to the type's methods. See docs/TRAITS.md.
var supertraitIRCases = []struct {
	name     string
	src      string
	expected int
}{
	// rank(p,q)=1 (lt), rank(q,p)=9 (gt), rank(p,p)=5 (eq); sum 15.
	{"bounded-generic",
		`trait Eq { function eq(self: Self, other: Self): boolean; } trait Ord: Eq { function lt(self: Self, other: Self): boolean; } struct P { x: i32 } impl Eq for P { function eq(self: Self, other: Self): boolean { return self.x == other.x; } } impl Ord for P { function lt(self: Self, other: Self): boolean { return self.x < other.x; } } function rank[T: Ord](a: T, b: T): i32 { if (a.eq(b)) { return 5; } if (a.lt(b)) { return 1; } return 9; } function main(): i32 { var p: P = P { x: 3 }; var q: P = P { x: 5 }; return rank(p, q) + rank(q, p) + rank(p, p); }`, 15},
	// Direct dispatch of both the trait's and the supertrait's method.
	// p.eq(q) false → +0; p.lt(q) true → +4; 0 + 4 + 8 = 12.
	{"direct",
		`trait Eq { function eq(self: Self, other: Self): boolean; } trait Ord: Eq { function lt(self: Self, other: Self): boolean; } struct P { x: i32 } impl Eq for P { function eq(self: Self, other: Self): boolean { return self.x == other.x; } } impl Ord for P { function lt(self: Self, other: Self): boolean { return self.x < other.x; } } function main(): i32 { var p: P = P { x: 3 }; var q: P = P { x: 5 }; var r: i32 = 8; if (p.eq(q)) { r = r + 100; } if (p.lt(q)) { r = r + 4; } return r; }`, 12},
}

// TestSelfHostSupertraitIRX86_64 routes each case through the self-hosted
// x86-64 driver (asm_run, IR default-on), pinning the route to "ir".
func TestSelfHostSupertraitIRX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := writeSelfHostAsmProject(t)
	for _, name := range []string{"asm_run.fern", "asm_pathprobe_run.fern"} {
		src, err := os.ReadFile(filepath.Join("../../examples/self_host", name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		if err := os.WriteFile(filepath.Join(dir, name), src, 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_run.fern", "driver")
	probeBin := buildSelfHostBin(t, gcc, dir, "asm_pathprobe_run.fern", "pathprobe")

	for _, tc := range supertraitIRCases {
		t.Run(tc.name, func(t *testing.T) {
			path := strings.TrimSpace(string(runCapture(t, gcc, runner, probeBin, []byte(tc.src))))
			if path != "ir" {
				t.Fatalf("%s routed through %q path, want \"ir\"", tc.name, path)
			}
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
			if code := cmd.ProcessState.ExitCode(); code != tc.expected {
				t.Errorf("%s exited %d, want %d", tc.name, code, tc.expected)
			}
		})
	}
}

// TestSelfHostSupertraitIRWasm runs the same cases through the wasm IR
// backend (wasm_ir_run -ir).
func TestSelfHostSupertraitIRWasm(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping self-host supertrait wasm IR e2e")
	}
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	for _, name := range []string{
		"util.fern", "astwalk.fern", "asmcore.fern", "lexer.fern", "parser.fern",
		"ir.fern", "irlower.fern", "asm_ir.fern", "wasm_ir.fern", "wasm_ir_run.fern",
	} {
		src, err := os.ReadFile(filepath.Join("../../examples/self_host", name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		if err := os.WriteFile(filepath.Join(dir, name), src, 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	driverBin := buildSelfHostBin(t, gcc, dir, "wasm_ir_run.fern", "driver")

	for _, tc := range supertraitIRCases {
		t.Run(tc.name, func(t *testing.T) {
			var cmd *exec.Cmd
			if len(runner) == 0 {
				cmd = exec.Command(driverBin, "-ir")
			} else {
				cmd = exec.Command(runner[0], append(append(append([]string{}, runner[1:]...), driverBin), "-ir")...)
			}
			cmd.Stdin = strings.NewReader(tc.src)
			wat, err := cmd.Output()
			if err != nil || len(wat) == 0 {
				t.Fatalf("self-host wasm driver failed for %q: %v (%d bytes)", tc.src, err, len(wat))
			}
			watFile := filepath.Join(dir, "supertrait_prog.wat")
			if err := os.WriteFile(watFile, wat, 0o644); err != nil {
				t.Fatalf("write wat: %v", err)
			}
			run := exec.Command("wasmtime", "run", watFile)
			_ = run.Run()
			if run.ProcessState == nil || !run.ProcessState.Exited() {
				t.Fatalf("wasmtime did not exit normally for %q:\n%s", tc.src, wat)
			}
			if code := run.ProcessState.ExitCode(); code != tc.expected {
				t.Errorf("supertrait wasm IR %q = %d, want %d", tc.name, code, tc.expected)
			}
		})
	}
}
