package e2eselfhost

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// operatorOverloadIRCase is a self-host composite-operator-overload program
// whose exit code is pinned against the native interpreter's oracle. Each
// exercises irlower's #2706 lowering: a binary `a <op> b` (or unary `-a`) on a
// struct rewrites to the conventionally-named method (`+`→add, `-`→sub, `*`→mul,
// `/`→div, unary `-`→neg) and lowers through the existing struct-returning
// method-call path. Before this, the self-host *admitted* `a + b` on a struct
// and emitted scalar arithmetic on the struct pointers — a silent miscompile
// (the field read returned garbage). The native compiler desugars these in its
// checker; this is the matching self-host IR-path support. Exit codes <= 120.
type operatorOverloadIRCase struct {
	name     string
	src      string
	expected int
}

var operatorOverloadIRCases = []operatorOverloadIRCase{
	// `a + b` → `a.add(b)`, then a field read off the struct result. The init
	// `a + b` must type `c` as the struct so `c.x` resolves.
	{"binary_add", `struct V { x: i32 }
function (self: V) add(o: V): V { return V { x: self.x + o.x }; }
function main(): i32 { var a = V { x: 3 }; var b = V { x: 4 }; var c = a + b; return c.x; }`, 7},
	// All four arithmetic operators chained: (20+4)=24, -4=20, *4=80, /4=20.
	{"all_ops", `struct V { x: i32 }
function (self: V) add(o: V): V { return V { x: self.x + o.x }; }
function (self: V) sub(o: V): V { return V { x: self.x - o.x }; }
function (self: V) mul(o: V): V { return V { x: self.x * o.x }; }
function (self: V) div(o: V): V { return V { x: self.x / o.x }; }
function main(): i32 { var a = V { x: 20 }; var b = V { x: 4 }; var r = a + b; r = r - b; r = r * b; r = r / b; return r.x; }`, 20},
	// Unary `-a` → `a.neg()`. -5 + 100 = 95.
	{"unary_neg", `struct V { x: i32 }
function (self: V) neg(): V { return V { x: 0 - self.x }; }
function main(): i32 { var a = V { x: 5 }; var b = -a; return b.x + 100; }`, 95},
	// The result of `a + b` is a struct value read inline (no intermediate
	// local): exercises expr_struct_type on the ExprBinary for the field read.
	{"inline_field", `struct V { x: i32 }
function (self: V) add(o: V): V { return V { x: self.x + o.x }; }
function main(): i32 { var a = V { x: 3 }; var b = V { x: 4 }; return (a + b).x; }`, 7},
	// A composite result fed back in as an operand of another overload
	// (`(a + b) + a`): the intermediate is typed as the struct, so it dispatches
	// `.add` again. 7 + 3 = 10.
	{"chained", `struct V { x: i32 }
function (self: V) add(o: V): V { return V { x: self.x + o.x }; }
function main(): i32 { var a = V { x: 3 }; var b = V { x: 4 }; var c = a + b; var d = c + a; return d.x; }`, 10},
	// A second struct type with a multi-field payload, to show it's not
	// V-specific: Money{50} + Money{30} → 80.
	{"named_struct", `struct Money { cents: i32, tag: i32 }
function (self: Money) add(o: Money): Money { return Money { cents: self.cents + o.cents, tag: self.tag }; }
function main(): i32 { var a = Money { cents: 50, tag: 1 }; var b = Money { cents: 30, tag: 2 }; var c = a + b; return c.cents; }`, 80},
}

// TestSelfHostOperatorOverloadIRX86_64 builds the self-host asm_run driver and
// runs each program (Fern → x86-64 asm → native binary → exit code), asserting
// the oracle value. A size bound proves the small IR path was taken (a bail to
// the AST runtime would be far larger — and would silently miscompile the
// struct arithmetic).
func TestSelfHostOperatorOverloadIRX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := writeSelfHostAsmProject(t)
	src, err := os.ReadFile("../../examples/self_host/asm_run.fern")
	if err != nil {
		t.Fatalf("read asm_run.fern: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "asm_run.fern"), src, 0o644); err != nil {
		t.Fatalf("write asm_run.fern: %v", err)
	}
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_run.fern", "driver")

	for _, tc := range operatorOverloadIRCases {
		t.Run(tc.name, func(t *testing.T) {
			asm := runCapture(t, gcc, runner, driverBin, []byte(tc.src))
			if len(asm) == 0 || len(asm) > 18000 {
				t.Fatalf("asm is %d bytes — expected small IR output; the module likely bailed to the AST runtime", len(asm))
			}
			progBin := buildBin(t, gcc, dir, "operator_overload_"+tc.name, string(asm))
			var cmd *exec.Cmd
			if len(runner) == 0 {
				cmd = exec.Command(progBin)
			} else {
				cmd = exec.Command(runner[0], append(runner[1:], progBin)...)
			}
			_ = cmd.Run()
			if code := cmd.ProcessState.ExitCode(); code != tc.expected {
				t.Errorf("operator-overload %q exit %d, want %d", tc.name, code, tc.expected)
			}
		})
	}
}

// TestSelfHostOperatorOverloadWasmIR is the wasm sibling: the overload lowering
// lives in irlower (target-independent), so the wasm IR backend gets it for
// free. Same oracle exit codes via the wasm_ir_run driver.
func TestSelfHostOperatorOverloadWasmIR(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping self-host operator-overload wasm IR e2e")
	}
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "wasm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "wasm_ir_run.fern", "driver")

	for _, tc := range operatorOverloadIRCases {
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
				t.Fatalf("driver failed for %q: %v", tc.name, err)
			}
			watFile := filepath.Join(dir, "oo_prog.wat")
			if err := os.WriteFile(watFile, wat, 0o644); err != nil {
				t.Fatalf("write wat: %v", err)
			}
			rcmd := exec.Command("wasmtime", "run", watFile)
			_ = rcmd.Run()
			if rcmd.ProcessState == nil || !rcmd.ProcessState.Exited() {
				t.Fatalf("wasmtime did not exit normally for %q:\n%s", tc.name, wat)
			}
			if got := rcmd.ProcessState.ExitCode(); got != tc.expected {
				t.Errorf("operator-overload wasm IR %q = %d, want %d", tc.name, got, tc.expected)
			}
		})
	}
}
