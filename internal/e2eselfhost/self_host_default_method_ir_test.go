package e2eselfhost

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// defaultMethodIRCases exercise trait DEFAULT methods through the
// self-hosted compiler (parser.fern's parse_trait_decl retains a method's
// `{ … }` body and parse_module synthesises a copy onto each impl that
// omits it — Self-host parity with the Go checker's synthesizeTraitDefaults,
// see docs/TRAITS.md). Each `trait` declaration is real source the native
// compiler also honours; the self-host now inherits the default instead of
// discarding the trait.
var defaultMethodIRCases = []struct {
	name     string
	src      string
	expected int
}{
	// Inherited default: Dog omits `score`, so it inherits `function
	// score(self) { return self.base() + 10; }`. 5 + 10 = 15.
	{"inherited",
		`trait Counter { function base(self: Self): i32; function score(self: Self): i32 { return self.base() + 10; } } struct Dog { v: i32 } impl Counter for Dog { function base(self: Self): i32 { return self.v; } } function main(): i32 { var d: Dog = Dog { v: 5 }; return d.score(); }`, 15},
	// Override: Cat provides its own `score`, which must win over the default.
	{"override",
		`trait Counter { function base(self: Self): i32; function score(self: Self): i32 { return self.base() + 10; } } struct Cat { v: i32 } impl Counter for Cat { function base(self: Self): i32 { return self.v; } function score(self: Self): i32 { return 99; } } function main(): i32 { var c: Cat = Cat { v: 5 }; return c.score(); }`, 99},
	// Default body calls another (abstract) method on `self`, plus a string
	// length — "rex".len() (3) + 4 = 7.
	{"calls-abstract",
		`trait Greet { function name(self: Self): string; function tag(self: Self): i32 { return self.name().len() + 4; } } struct Pet { age: i32 } impl Greet for Pet { function name(self: Self): string { return "rex"; } } function main(): i32 { var p: Pet = Pet { age: 1 }; return p.tag(); }`, 7},
	// Two impls of the same trait each inherit the default independently.
	// a.score() = 2 + 1 = 3; b.score() = 3*10 + 1 = 31; total 34.
	{"two-impls",
		`trait Counter { function base(self: Self): i32; function score(self: Self): i32 { return self.base() + 1; } } struct A { v: i32 } impl Counter for A { function base(self: Self): i32 { return self.v; } } struct B { v: i32 } impl Counter for B { function base(self: Self): i32 { return self.v * 10; } } function main(): i32 { var a: A = A { v: 2 }; var b: B = B { v: 3 }; return a.score() + b.score(); }`, 34},
}

// TestSelfHostDefaultMethodIRX86_64 routes each case through the self-hosted
// x86-64 driver (asm_run → emit_module, IR default-on) and asserts the exit
// code, while pinning the route to the "ir" path via asm_pathprobe_run.
func TestSelfHostDefaultMethodIRX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := writeSelfHostAsmProject(t)
	copySelfHostDriver(t, dir, "asm_run.fern", "asm_pathprobe_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_run.fern", "driver")
	probeBin := buildSelfHostBin(t, gcc, dir, "asm_pathprobe_run.fern", "pathprobe")

	for _, tc := range defaultMethodIRCases {
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

// TestSelfHostDefaultMethodIRWasm runs the same cases through the wasm IR
// backend (wasm_ir_run -ir) so default methods are verified on the
// stack-machine backend too, not just the register ABI.
func TestSelfHostDefaultMethodIRWasm(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping self-host default-method wasm IR e2e")
	}
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "wasm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "wasm_ir_run.fern", "driver")

	for _, tc := range defaultMethodIRCases {
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
			watFile := filepath.Join(dir, "defaultmethod_prog.wat")
			if err := os.WriteFile(watFile, wat, 0o644); err != nil {
				t.Fatalf("write wat: %v", err)
			}
			run := exec.Command("wasmtime", "run", watFile)
			_ = run.Run()
			if run.ProcessState == nil || !run.ProcessState.Exited() {
				t.Fatalf("wasmtime did not exit normally for %q:\n%s", tc.src, wat)
			}
			if code := run.ProcessState.ExitCode(); code != tc.expected {
				t.Errorf("default-method wasm IR %q = %d, want %d", tc.name, code, tc.expected)
			}
		})
	}
}
