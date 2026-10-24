package e2ecompiler

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

type operatorOverloadIRCase struct {
	name     string
	src      string
	expected int
}

var operatorOverloadIRCases = []operatorOverloadIRCase{
	{"binary_add", `struct V { x: i32 }
function (self: V) add(o: V): V { return V { x: self.x + o.x }; }
function main(): i32 { let a = V { x: 3 }; let b = V { x: 4 }; let c = a + b; return c.x; }`, 7},
	{"all_ops", `struct V { x: i32 }
function (self: V) add(o: V): V { return V { x: self.x + o.x }; }
function (self: V) sub(o: V): V { return V { x: self.x - o.x }; }
function (self: V) mul(o: V): V { return V { x: self.x * o.x }; }
function (self: V) div(o: V): V { return V { x: self.x / o.x }; }
function main(): i32 { let a = V { x: 20 }; let b = V { x: 4 }; let r = a + b; r = r - b; r = r * b; r = r / b; return r.x; }`, 20},
	{"unary_neg", `struct V { x: i32 }
function (self: V) neg(): V { return V { x: 0 - self.x }; }
function main(): i32 { let a = V { x: 5 }; let b = -a; return b.x + 100; }`, 95},
	{"inline_field", `struct V { x: i32 }
function (self: V) add(o: V): V { return V { x: self.x + o.x }; }
function main(): i32 { let a = V { x: 3 }; let b = V { x: 4 }; return (a + b).x; }`, 7},
	{"chained", `struct V { x: i32 }
function (self: V) add(o: V): V { return V { x: self.x + o.x }; }
function main(): i32 { let a = V { x: 3 }; let b = V { x: 4 }; let c = a + b; let d = c + a; return d.x; }`, 10},
	{"named_struct", `struct Money { cents: i32, tag: i32 }
function (self: Money) add(o: Money): Money { return Money { cents: self.cents + o.cents, tag: self.tag }; }
function main(): i32 { let a = Money { cents: 50, tag: 1 }; let b = Money { cents: 30, tag: 2 }; let c = a + b; return c.cents; }`, 80},
}

func TestSelfHostOperatorOverloadIRX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := writeSelfHostAsmProject(t)
	src, err := os.ReadFile("../../../compiler/drivers/asm_run.fern")
	if err != nil {
		t.Fatalf("read asm_run.fern: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "drivers/asm_run.fern"), src, 0o644); err != nil {
		t.Fatalf("write asm_run.fern: %v", err)
	}
	driverBin := buildSelfHostBin(t, gcc, dir, "drivers/asm_run.fern", "driver")

	for _, tc := range operatorOverloadIRCases {
		t.Run(tc.name, func(t *testing.T) {
			asm := runCapture(t, gcc, runner, driverBin, []byte(tc.src))
			if len(asm) == 0 {
				t.Fatal("driver produced no asm")
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

func TestSelfHostOperatorOverloadWasmIR(t *testing.T) {
	cli := buildSelfHostCLI(t)
	for _, tc := range operatorOverloadIRCases {
		t.Run(tc.name, func(t *testing.T) {
			src := tc.src
			for _, target := range []string{"wasm32-wasi"} {
				if stderr, code := cli.exitOf(t, src, target); code != tc.expected {
					t.Errorf("%s on %s exited %d, want %d\n%s", tc.name, target, code, tc.expected, stderr)
				}
			}
		})
	}
}
