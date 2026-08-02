package e2eselfhost

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// dynEnumIRCases pin #4785: an ENUM value behind a `dyn Trait` slot must
// dispatch to `<Enum>.<method>` on the IR path. An enum value's offset-0
// identity is its VARIANT's interned shape (the same identity op_variant_is
// reads), never the enum name itself, so op_dyn_dispatch needs per-variant
// compare arms — the struct/prim shape arms can never identify it. Before the
// fix the dispatch chain had no enum arm at all and fell through to the 0
// fallback (wrong values, native-valid program). Exit codes are the oracle;
// every case was validated native-first (`fern -interp` + `-target x86-64`).
var dynEnumIRCases = []struct {
	name     string
	src      string
	expected int
}{
	// The issue #4785 repro: an enum LOCAL coerced into a scalar `dyn` local.
	// Add(3).show() = 3 + 1 = 4.
	{"enum-local-coerce",
		`trait Show { function show(self: Self): i32; } enum Op { Add(i32), Neg } impl Show for Op { function show(self: Self): i32 { match (self) { Add(v) => { return v + 1; }, Neg => { return 0; } } } } function go(k: i32): i32 { var e: Op = Add(k); var d: dyn Show = e; return d.show(); } function main(): i32 { return go(3); }`, 4},
	// Direct construction into the dyn slot (the shape that already worked —
	// regression guard). Add(41).show() = 42.
	{"enum-direct-init",
		`trait Show { function show(self: Self): i32; } enum Op { Add(i32), Neg } impl Show for Op { function show(self: Self): i32 { match (self) { Add(v) => { return v + 1; }, Neg => { return 0; } } } } function main(): i32 { var d: dyn Show = Add(41); return d.show(); }`, 42},
	// An enum LOCAL passed to a `dyn` PARAM. Add(9).show() = 10.
	{"enum-local-to-param",
		`trait Show { function show(self: Self): i32; } enum Op { Add(i32), Neg } impl Show for Op { function show(self: Self): i32 { match (self) { Add(v) => { return v + 1; }, Neg => { return 0; } } } } function run(s: dyn Show): i32 { return s.show(); } function main(): i32 { var e: Op = Add(9); return run(e); }`, 10},
	// A UNIT variant behind dyn — the payloadless arm must dispatch too. 7.
	{"enum-unit-variant",
		`trait Show { function show(self: Self): i32; } enum Op { Add(i32), Neg } impl Show for Op { function show(self: Self): i32 { match (self) { Add(v) => { return v + 1; }, Neg => { return 7; } } } } function go(): i32 { var e: Op = Neg; var d: dyn Show = e; return d.show(); } function main(): i32 { return go(); }`, 7},
	// A dispatched method taking an ARGUMENT. Add(5).sc(3) = 5 * 3 = 15.
	{"enum-method-arg",
		`trait Sc { function sc(self: Self, k: i32): i32; } enum Op { Add(i32), Neg } impl Sc for Op { function sc(self: Self, k: i32): i32 { match (self) { Add(v) => { return v * k; }, Neg => { return 0 - k; } } } } function f(s: dyn Sc): i32 { return s.sc(3); } function main(): i32 { var e: Op = Add(5); return f(e); }`, 15},
	// TWO enums implementing the same trait: each value must reach its OWN
	// impl (per-variant keying — a blanket first-enum fallback would send the
	// Col value to Op.show). Add(3).show() + Blue.show() = 4 + 20 = 24.
	{"enum-two-enums",
		`trait Show { function show(self: Self): i32; } enum Op { Add(i32), Neg } enum Col { Red, Blue } impl Show for Op { function show(self: Self): i32 { match (self) { Add(v) => { return v + 1; }, Neg => { return 0; } } } } impl Show for Col { function show(self: Self): i32 { match (self) { Red => { return 10; }, Blue => { return 20; } } } } function run(s: dyn Show): i32 { return s.show(); } function main(): i32 { var a: Op = Add(3); var b: Col = Blue; return run(a) + run(b); }`, 24},
	// A heterogeneous STRUCT + ENUM `dyn Show[]` — the struct element takes
	// the shape arm, the enum-local element the variant arm, in one chain.
	// Circle{3}.show() + Add(4).show() = 9 + 5 = 14. (The native x86-64
	// backend segfaults on this exact shape — tracked separately as #4787;
	// interp, the validity oracle, exits 14.)
	{"enum-struct-mixed-array",
		`trait Show { function show(self: Self): i32; } enum Op { Add(i32), Neg } struct Circle { r: i32 } impl Show for Op { function show(self: Self): i32 { match (self) { Add(v) => { return v + 1; }, Neg => { return 0; } } } } impl Show for Circle { function show(self: Self): i32 { return self.r * self.r; } } function sum(xs: dyn Show[]): i32 { var t: i32 = 0; for x in xs { t = t + x.show(); } return t; } function main(): i32 { var e: Op = Add(4); var xs: dyn Show[] = [Circle { r: 3 }, e]; return sum(xs); }`, 14},
}

// TestSelfHostDynEnumIRX86_64 routes each case through the self-hosted x86-64
// driver (asm_run) and asserts the exit code, AND probes the routing
// (asm_pathprobe_run) to pin each case to the "ir" path.
func TestSelfHostDynEnumIRX86_64(t *testing.T) {
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

	for _, tc := range dynEnumIRCases {
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

// TestSelfHostDynEnumIRWasm runs the same cases through the wasm IR backend
// (wasm_ir_run -ir): the identity is the variant's numeric type id
// (i32.load @0), and the enum arm is an OR-chain over the enum's variants.
func TestSelfHostDynEnumIRWasm(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping self-host dyn-enum wasm IR e2e")
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

	for _, tc := range dynEnumIRCases {
		t.Run(tc.name, func(t *testing.T) {
			var cmd *exec.Cmd
			if len(runner) == 0 {
				cmd = exec.Command(driverBin, "-ir")
			} else {
				cmd = exec.Command(runner[0], append(append(append([]string{}, runner[1:]...), driverBin), "-ir")...)
			}
			cmd.Stdin = bytes.NewReader([]byte(tc.src))
			wat, err := cmd.Output()
			if err != nil || len(wat) == 0 {
				t.Fatalf("driver failed for %q: %v", tc.src, err)
			}
			watFile := filepath.Join(dir, "dynenum_prog.wat")
			if err := os.WriteFile(watFile, wat, 0o644); err != nil {
				t.Fatalf("write wat: %v", err)
			}
			run := exec.Command("wasmtime", "run", watFile)
			_ = run.Run()
			if run.ProcessState == nil || !run.ProcessState.Exited() {
				t.Fatalf("wasmtime did not exit normally for %q:\n%s", tc.src, wat)
			}
			if code := run.ProcessState.ExitCode(); code != tc.expected {
				t.Errorf("dyn-enum wasm IR %q = %d, want %d", tc.name, code, tc.expected)
			}
		})
	}
}

// TestSelfHostDynEnumIRArm64: the arm64 sibling under qemu — the same cases
// through asm_ir_run -target arm64 (the per-variant adrp/cmp arm chain).
func TestSelfHostDynEnumIRArm64(t *testing.T) {
	arm64gcc, qemu := arm64Tooling(t)
	x86gcc, x86runner := x86_64Tooling(t)
	dir := t.TempDir()
	for _, name := range []string{"util.fern", "astwalk.fern", "asmcore.fern", "lexer.fern", "parser.fern", "ir.fern", "irlower.fern", "asm_ir.fern", "asm_arm64_ir.fern", "asm_ir_run.fern"} {
		src, err := os.ReadFile(filepath.Join("../../examples/self_host", name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		if err := os.WriteFile(filepath.Join(dir, name), src, 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	driverBin := buildSelfHostBin(t, x86gcc, dir, "asm_ir_run.fern", "driver")

	for _, tc := range dynEnumIRCases {
		t.Run(tc.name, func(t *testing.T) {
			asm := runCapture(t, x86gcc, x86runner, driverBin, []byte(tc.src), "-target", "arm64")
			if len(asm) == 0 {
				t.Fatalf("self-host arm64 compiler emitted 0 bytes")
			}
			bin := buildBinArm64(t, arm64gcc, dir, "dynenum-"+tc.name, string(asm))
			cmd := runArm64Bin(qemu, bin)
			_ = cmd.Run()
			if code := cmd.ProcessState.ExitCode(); code != tc.expected {
				t.Errorf("dyn-enum arm64 IR %q = %d, want %d", tc.name, code, tc.expected)
			}
		})
	}
}
