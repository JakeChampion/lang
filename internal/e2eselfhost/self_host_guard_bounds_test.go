package e2eselfhost

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// ssabounds.proven_indices marks an index read unchecked when the branches
// into its block prove `0 <= i < len`: a guard on both bounds, the length minus
// a constant, a loop index stepped by one under its guard or stepped down under
// `i >= 0`, and the same field read twice from one value. Each case reads in
// range on every path the program takes, so it must answer what the
// interpreter answers on every backend.
var guardBoundsCases = []struct {
	name string
	src  string
}{
	{"guard_and", `function pick(xs: i32[], k: i32): i32 {
    if (k >= 0 && k < xs.len()) { return xs[k]; }
    return 10;
}
function main(): i32 { let xs: i32[] = [3, 5, 7]; return pick(xs, 2) + pick(xs, 0) + pick(xs, 5) + pick(xs, 0 - 1); }`},
	{"guard_or_early_return", `function pick(xs: i32[], k: i32): i32 {
    if (k < 0 || k >= xs.len()) { return 1; }
    return xs[k];
}
function main(): i32 { let xs: i32[] = [3, 5, 7]; return pick(xs, 1) + pick(xs, 3) + pick(xs, 0 - 4); }`},
	{"last_element", `function last(xs: i32[]): i32 {
    if (xs.len() < 1) { return 50; }
    return xs[xs.len() - 1];
}
function main(): i32 { let e: i32[] = []; return last([4, 9, 21]) + last(e); }`},
	{"string_byte", `function at(s: string, k: i32): i32 {
    if (k < 0 || k >= s.len()) { return 0; }
    return s[k] as i32;
}
function main(): i32 { return at("abc", 1) - at("abc", 9) - 90; }`},
	{"descending_scan", `function find(xs: i32[], n: i32): i32 {
    let i: i32 = xs.len() - 1;
    while (i >= 0) { if (xs[i] == n) { return i; } i = i - 1; }
    return 0 - 1;
}
function main(): i32 { let xs: i32[] = [4, 8, 4, 2]; return find(xs, 4) * 10 + find(xs, 2) + find(xs, 7) + 1; }`},
	{"field_descending_scan", `struct Scope { names: string[] }
function index_of(s: Scope, name: string): i32 {
    let i: i32 = s.names.len() - 1;
    while (i >= 0) { if (s.names[i] == name) { return i; } i = i - 1; }
    return 0 - 1;
}
function main(): i32 { let s: Scope = Scope { names: ["a", "b", "a"] }; return index_of(s, "a") * 10 + index_of(s, "b") + index_of(s, "z"); }`},
	{"field_guard", `struct Op { imm: i32 }
function slot(slots: i32[], op: Op): i32 {
    if (op.imm < 0 || op.imm >= slots.len()) { return 0; }
    return slots[op.imm];
}
function main(): i32 { let s: i32[] = [11, 22, 33]; return slot(s, Op { imm: 2 }) + slot(s, Op { imm: 3 }) + slot(s, Op { imm: 0 - 1 }); }`},
	{"stepped_loop", `function total(xs: i32[]): i32 {
    let t: i32 = 0;
    let i: i32 = 0;
    while (true) {
        if (i >= xs.len()) { break; }
        t = t + xs[i];
        i = i + 1;
    }
    return t;
}
function main(): i32 { return total([1, 2, 3, 4, 5]); }`},
}

// guardBoundsTwins pairs each proven program with one whose guard proves
// nothing about the length: the same reads behind a literal bound.
var guardBoundsTwins = []struct{ name, proven, unproven string }{
	{"guard_and",
		`function pick(xs: i32[], k: i32): i32 { if (k >= 0 && k < xs.len()) { return xs[k]; } return 9; } function main(): i32 { return pick([1, 2, 3], 1); }`,
		`function pick(xs: i32[], k: i32): i32 { if (k >= 0 && k < 3) { return xs[k]; } return 9; } function main(): i32 { return pick([1, 2, 3], 1); }`},
	{"last_element",
		`function last(xs: i32[]): i32 { if (xs.len() < 1) { return 9; } return xs[xs.len() - 1]; } function main(): i32 { return last([1, 2, 3]); }`,
		`function last(xs: i32[]): i32 { if (xs.len() < 0) { return 9; } return xs[xs.len() - 1]; } function main(): i32 { return last([1, 2, 3]); }`},
	{"descending_scan",
		`function find(xs: i32[], n: i32): i32 { let i: i32 = xs.len() - 1; while (i >= 0) { if (xs[i] == n) { return i; } i = i - 1; } return 0; } function main(): i32 { return find([5, 6, 7], 6); }`,
		`function find(xs: i32[], n: i32): i32 { let i: i32 = 2; while (i >= 0) { if (xs[i] == n) { return i; } i = i - 1; } return 0; } function main(): i32 { return find([5, 6, 7], 6); }`},
	{"field_guard",
		`struct Op { imm: i32 } function slot(s: i32[], op: Op): i32 { if (op.imm < 0 || op.imm >= s.len()) { return 0; } return s[op.imm]; } function main(): i32 { return slot([4, 5], Op { imm: 1 }); }`,
		`struct Op { imm: i32 } function slot(s: i32[], op: Op): i32 { if (op.imm < 0 || op.imm >= 2) { return 0; } return s[op.imm]; } function main(): i32 { return slot([4, 5], Op { imm: 1 }); }`},
}

// guardBoundsUnsafe are reads a guard does not cover, each run out of range:
// the check must remain, so each traps with exit 134.
var guardBoundsUnsafe = []struct{ name, src string }{
	{"guard_on_another_array", `function pick(xs: i32[], ys: i32[], k: i32): i32 { if (k >= 0 && k < ys.len()) { return xs[k]; } return 0; }
function main(): i32 { return pick([1], [1, 2, 3], 2); }`},
	{"no_lower_bound", `function pick(xs: i32[], k: i32): i32 { if (k < xs.len()) { return xs[k]; } return 0; }
function main(): i32 { return pick([1, 2, 3], 0 - 1); }`},
	{"weak_upper_bound", `function pick(xs: i32[], k: i32): i32 { if (k >= 0 && k <= xs.len()) { return xs[k]; } return 0; }
function main(): i32 { return pick([1, 2, 3], 3); }`},
	{"index_moved_after_guard", `function pick(xs: i32[], k: i32): i32 { if (k >= 0 && k < xs.len()) { k = k + 1; return xs[k]; } return 0; }
function main(): i32 { return pick([1, 2, 3], 2); }`},
	{"last_of_empty", `function last(xs: i32[]): i32 { return xs[xs.len() - 1]; }
function main(): i32 { let e: i32[] = []; return last(e); }`},
	{"descending_without_lower_bound", `function walk(xs: i32[]): i32 { let t: i32 = 0; let i: i32 = xs.len() - 1; while (i > 0 - 3) { t = t + xs[i]; i = i - 1; } return t; }
function main(): i32 { return walk([1, 2, 3]); }`},
	{"step_of_two", `function total(xs: i32[]): i32 { let t: i32 = 0; let i: i32 = 0; while (true) { if (i > xs.len()) { break; } t = t + xs[i]; i = i + 2; } return t; }
function main(): i32 { return total([1, 2, 3, 4]); }`},
	{"read_after_loop", `function past(xs: i32[]): i32 { let i: i32 = 0; while (i < xs.len()) { i = i + 1; } return xs[i]; }
function main(): i32 { return past([1, 2, 3]); }`},
}

func TestSelfHostGuardBoundsX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	interpBin := buildLangBinForInterp(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "drivers/asm_ir_run.fern", "driver")
	emit := func(t *testing.T, src string) string {
		asm := string(runCapture(t, gcc, runner, driverBin, []byte(src+"\n"), "-ir"))
		if !strings.Contains(asm, ".Lssa_") {
			t.Fatalf("did not lower through the IR (no .Lssa_ labels)")
		}
		return asm
	}
	runExit := func(bin string) int {
		var run *exec.Cmd
		if len(runner) == 0 {
			run = exec.Command(bin)
		} else {
			run = exec.Command(runner[0], append(runner[1:], bin)...)
		}
		_ = run.Run()
		return run.ProcessState.ExitCode()
	}
	for _, tc := range guardBoundsCases {
		t.Run(tc.name, func(t *testing.T) {
			want := interpExit(t, interpBin, tc.src+"\n")
			if code := runExit(buildBin(t, gcc, dir, "gbounds_"+tc.name, emit(t, tc.src))); code != want {
				t.Errorf("exited %d, want %d (interp oracle)", code, want)
			}
		})
	}
	for _, tw := range guardBoundsTwins {
		t.Run("fewer_checks/"+tw.name, func(t *testing.T) {
			proven := emit(t, tw.proven)
			unproven := emit(t, tw.unproven)
			got, base := strings.Count(proven, "__fern_oob_abort"), strings.Count(unproven, "__fern_oob_abort")
			if got >= base {
				t.Fatalf("the guard proved nothing: %d __fern_oob_abort against the unproven twin's %d", got, base)
			}
			want := interpExit(t, interpBin, tw.proven+"\n")
			for name, asm := range map[string]string{"proven": proven, "unproven": unproven} {
				if code := runExit(buildBin(t, gcc, dir, "gbtwin_"+tw.name+"_"+name, asm)); code != want {
					t.Errorf("%s exited %d, want %d", name, code, want)
				}
			}
		})
	}
	for _, c := range guardBoundsUnsafe {
		t.Run("stays_checked/"+c.name, func(t *testing.T) {
			if code := runExit(buildBin(t, gcc, dir, "gbunsafe_"+c.name, emit(t, c.src))); code != 134 {
				t.Errorf("exited %d, want 134 (the bounds check must remain)", code)
			}
		})
	}
}

func TestSelfHostGuardBoundsArm64(t *testing.T) {
	arm64gcc, qemu := arm64Tooling(t)
	x86gcc, x86runner := x86_64Tooling(t)
	interpBin := buildLangBinForInterp(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, x86gcc, dir, "drivers/asm_ir_run.fern", "driver")
	runExit := func(t *testing.T, name, src string) int {
		asm := runCapture(t, x86gcc, x86runner, driverBin, []byte(src+"\n"), "-target", "arm64-linux")
		if len(asm) == 0 {
			t.Fatalf("the self-host arm64 compiler emitted nothing")
		}
		cmd := runArm64Bin(qemu, buildBin(t, arm64gcc, dir, name, string(asm)))
		_ = cmd.Run()
		return cmd.ProcessState.ExitCode()
	}
	for _, tc := range guardBoundsCases {
		t.Run(tc.name, func(t *testing.T) {
			want := interpExit(t, interpBin, tc.src+"\n")
			if code := runExit(t, "gbounds_"+tc.name, tc.src); code != want {
				t.Errorf("arm64 exited %d, want %d (interp oracle)", code, want)
			}
		})
	}
	for _, c := range guardBoundsUnsafe {
		t.Run("stays_checked/"+c.name, func(t *testing.T) {
			if code := runExit(t, "gbunsafe_"+c.name, c.src); code != 134 {
				t.Errorf("arm64 exited %d, want 134 (the bounds check must remain)", code)
			}
		})
	}
}

func TestSelfHostGuardBoundsWasm(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH")
	}
	gcc, runner := x86_64Tooling(t)
	interpBin := buildLangBinForInterp(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/wasm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "drivers/wasm_ir_run.fern", "driver")
	run := func(t *testing.T, name, src string) int {
		var cmd *exec.Cmd
		if len(runner) == 0 {
			cmd = exec.Command(driverBin, "-ir")
		} else {
			cmd = exec.Command(runner[0], append(append(append([]string{}, runner[1:]...), driverBin), "-ir")...)
		}
		cmd.Stdin = bytes.NewReader([]byte(src + "\n"))
		wat, err := cmd.Output()
		if err != nil || len(wat) == 0 {
			t.Fatalf("driver failed: %v", err)
		}
		watFile := filepath.Join(dir, name+".wat")
		if err := os.WriteFile(watFile, wat, 0o644); err != nil {
			t.Fatal(err)
		}
		rcmd := exec.Command("wasmtime", "run", watFile)
		_ = rcmd.Run()
		if rcmd.ProcessState == nil || !rcmd.ProcessState.Exited() {
			t.Fatalf("wasmtime did not exit normally")
		}
		return rcmd.ProcessState.ExitCode()
	}
	for _, tc := range guardBoundsCases {
		t.Run(tc.name, func(t *testing.T) {
			want := interpExit(t, interpBin, tc.src+"\n")
			if code := run(t, "gbounds_"+tc.name, tc.src); code != want {
				t.Errorf("wasm exited %d, want %d (interp oracle)", code, want)
			}
		})
	}
	for _, c := range guardBoundsUnsafe {
		t.Run("stays_checked/"+c.name, func(t *testing.T) {
			if code := run(t, "gbunsafe_"+c.name, c.src); code != 134 {
				t.Errorf("wasm exited %d, want 134 (the bounds check must remain)", code)
			}
		})
	}
}
