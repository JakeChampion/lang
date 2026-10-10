package e2ecompiler

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// teeFusionCases pin #6638's tee fusion and the copy propagation that follows it.
//
// The pass itself is twenty lines; the work was that NO consumer lowered
// `tee_local`. The op constructor and its kind tag had existed since the IR was
// written, but the only callers were unit drivers that build op lists and never
// emit them — so fusing without adding the emitter arms would have rewritten real
// code into an op every consumer discards, and the fixpoint could not
// have seen it (a stable miscompile reproduces itself perfectly).
//
// # Why most cases now expect NO local.tee
//
// A fused tee is an intermediate form, not an end state. `fuse_tee` creates it,
// and then `propagate_copies` drops it whenever the slot has no other reader —
// which is most scalar code. A case that emits no `local.tee` is therefore the
// pipeline working correctly, not the fusion failing, and `wantTee` records which
// shapes measurably retain one: the runtime pointer-valued case, where the
// allocated array is read and released. Keep the original static pointer case.
//
// The VALUE assertions carry the weight on every case and every backend. They are
// what would have caught the missing lowering, and `tee-reread-slot` is the sharp
// one: a tee that wrote the operand stack but not the frame passes the others and
// fails that.
//
// # Parameters and runtime input
//
// These programs used to bind literals (`let a: i32 = 7; ...`). Constant
// propagation then folded them whole — correct, faster, and no longer a test of
// codegen, since nothing survived to the backend but a constant. Keep those
// parameterized programs and add an opaque runtime pointer input for tee coverage.
var teeFusionCases = []struct {
	name     string
	src      string
	expected int
	// wantTee: measured, not assumed. Only the runtime pointer shape retains a
	// `local.tee` after copy propagation; asserting it on a shape whose tee is
	// correctly dropped would pin the absence of an optimisation.
	wantTee bool
}{
	// A chain of bindings, each read once: every tee is created and then dropped.
	// 7*3 = 21, +7 = 28.
	{"tee-chain", `function chain(a: i32): i32 {
    let b: i32 = a * 3;
    let c: i32 = b + a;
    return c;
}
function main(): i32 { return chain(7); }`, 28, false},
	// The slot must still hold the value AFTER the tee — `a` is read three more
	// times. c = 5 + 5 = 10, then 10 + 5 - 5 = 10.
	{"tee-reread-slot", `function reread(a: i32): i32 {
    let b: i32 = a;
    let c: i32 = a + b;
    return c + a - 5;
}
function main(): i32 { return reread(5); }`, 10, false},
	// Fusion inside a loop body, where the slot is rewritten every iteration and
	// read across the back edge. sum 0..9 = 45.
	{"tee-loop", `function loopy(k: i32): i32 {
    let acc: i32 = 0;
    let i: i32 = 0;
    while (i < k) {
        let step: i32 = i;
        acc = acc + step;
        i = i + 1;
    }
    return acc;
}
function main(): i32 { return loopy(10); }`, 45, false},
	// A fused pair feeding a call argument, and one inside the callee.
	{"tee-call-arg", `function twice(n: i32): i32 { let d: i32 = n * 2; return d; }
function callarg(x: i32): i32 {
    let y: i32 = twice(x);
    return y + x;
}
function main(): i32 { return callarg(6); }`, 18, false},
	// Preserve the original pointer-width values and result. The array is now
	// static, and the inlined body no longer retains a tee.
	{"tee-ptr-values", `function ptrs(s: string, xs: i32[]): i32 {
    let n: i32 = s.len();
    let m: i32 = xs[2];
    return n + m;
}
function main(): i32 { return ptrs("hello", [3, 4, 5]); }`, 10, false},
	// Program-name-only argv supplies n=1, preserving the original array values.
	// The single-argument reader keeps the pointer store/reload adjacent before
	// the call. The caller must still read that saved pointer to release it.
	{"tee-runtime-ptr-values", `@noinline function ptrs(s: string, xs: i32[]): i32 {
    let n: i32 = s.len();
    let m: i32 = xs[2];
    return n + m;
}
@noinline function read(xs: i32[]): i32 { return ptrs("hello", xs); }
@noinline function probe(n: i32): i32 { return read([n + 2, n + 3, n + 4]); }
function main(): i32 { return probe(args().len()); }`, 10, true},
}

// TestSelfHostTeeFusionIRX86_64 runs the cases through the self-hosted x86-64
// compiler.
func TestSelfHostTeeFusionIRX86_64(t *testing.T) {
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

	for _, tc := range teeFusionCases {
		t.Run(tc.name, func(t *testing.T) {
			asm := runCapture(t, gcc, runner, driverBin, []byte(tc.src+"\n"))
			if len(asm) == 0 {
				t.Fatal("self-host compiler emitted 0 bytes")
			}
			bin := buildBin(t, gcc, dir, tc.name, string(asm))
			var cmd *exec.Cmd
			if len(runner) == 0 {
				cmd = exec.Command(bin)
			} else {
				cmd = exec.Command(runner[0], append(runner[1:], bin)...)
			}
			_ = cmd.Run()
			if got := cmd.ProcessState.ExitCode(); got != tc.expected {
				t.Errorf("tee fusion x86-64 %q = %d, want %d", tc.name, got, tc.expected)
			}
		})
	}
}

// TestSelfHostTeeFusionIRArm64 is the arm64 leg: `ldr x0, [sp]` peeks the
// operand-stack top where store_local pops it.
func TestSelfHostTeeFusionIRArm64(t *testing.T) {
	arm64gcc, qemu := arm64Tooling(t)
	x86gcc, x86runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, x86gcc, dir, "drivers/asm_ir_run.fern", "driver")

	for _, tc := range teeFusionCases {
		t.Run(tc.name, func(t *testing.T) {
			asm := runCapture(t, x86gcc, x86runner, driverBin, []byte(tc.src+"\n"), "-target", "arm64-linux")
			if len(asm) == 0 {
				t.Fatal("self-host arm64 compiler emitted 0 bytes")
			}
			bin := buildBinArm64(t, arm64gcc, dir, tc.name, string(asm))
			cmd := runArm64Bin(qemu, bin)
			_ = cmd.Run()
			if got := cmd.ProcessState.ExitCode(); got != tc.expected {
				t.Errorf("tee fusion arm64 %q = %d, want %d", tc.name, got, tc.expected)
			}
		})
	}
}

// TestSelfHostTeeFusionWasmIR is the leg the pass exists for: it asserts the WAT
// actually carries `local.tee` — the emitted instruction is the whole point —
// and that the program still computes the right answer through it.
func TestSelfHostTeeFusionWasmIR(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping self-host tee-fusion wasm IR e2e")
	}
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/wasm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "drivers/wasm_ir_run.fern", "driver")

	for _, tc := range teeFusionCases {
		t.Run(tc.name, func(t *testing.T) {
			var cmd *exec.Cmd
			if len(runner) == 0 {
				cmd = exec.Command(driverBin)
			} else {
				cmd = exec.Command(runner[0], append(append([]string{}, runner[1:]...), driverBin)...)
			}
			cmd.Stdin = bytes.NewReader([]byte(tc.src))
			wat, err := cmd.Output()
			if err != nil || len(wat) == 0 {
				t.Fatalf("driver failed for %q: %v", tc.name, err)
			}
			if tc.wantTee {
				bodies := wasmFuncBodies(string(wat), "$probe ")
				if len(bodies) != 1 {
					t.Fatalf("%q: want one probe body, got %d", tc.name, len(bodies))
				}
				body := bodies[0]
				tee := regexp.MustCompile(`local\.tee ([0-9]+)\s+call \$read`).FindStringSubmatch(body)
				if len(tee) != 2 {
					t.Fatalf("%q emitted no pointer tee feeding read\n%s", tc.name, body)
				}
				if !strings.Contains(body, "call $__fern_arr_box") || !regexp.MustCompile(`local\.get `+tee[1]+`\s+call \$__fern_arr_dec`).MatchString(body) {
					t.Errorf("%q must allocate and release the pointer saved by tee\n%s", tc.name, body)
				}
			}
			if tc.name == "tee-ptr-values" {
				bodies := wasmFuncBodies(string(wat), "$main ")
				if len(bodies) != 1 || !strings.Contains(bodies[0], "global.get $__cagg_base") || strings.Contains(bodies[0], "local.tee") {
					t.Errorf("%q lost its static no-tee control\n%s", tc.name, wat)
				}
			}
			watFile := filepath.Join(dir, tc.name+".wat")
			if err := os.WriteFile(watFile, wat, 0o644); err != nil {
				t.Fatalf("write wat: %v", err)
			}
			rcmd := exec.Command("wasmtime", "run", watFile)
			_ = rcmd.Run()
			if rcmd.ProcessState == nil || !rcmd.ProcessState.Exited() {
				t.Fatalf("wasmtime did not exit normally for %q:\n%s", tc.name, wat)
			}
			if got := rcmd.ProcessState.ExitCode(); got != tc.expected {
				t.Errorf("tee fusion wasm IR %q = %d, want %d", tc.name, got, tc.expected)
			}
		})
	}
}
