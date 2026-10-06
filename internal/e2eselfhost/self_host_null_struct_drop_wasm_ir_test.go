package e2eselfhost

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// nullStructDropProg releases a struct whose slot may be null at the release,
// and poisons the low scratch a field walk through a null box would read:
// field 1 off a null box lands at linear address 16, the WASI out-parameter
// slot $__fern_random_i32 and the clock builtins write through.
//
// The while loop is what makes the fault deterministic rather than a coin
// flip: it leaves a word at 16 that is even (an odd word is a tagged pointer
// $__fern_arr_dec returns on) and far past any memory this module grows to, so
// an unguarded walk hands $__fern_arr_dec a wild pointer every run. Answers 2,
// the length of lp.xs.
const nullStructDropProg = `struct P { n: i32, xs: i32[] }
@noinline function inner_size(p: P): i32 { return p.xs.len(); }
function main(): i32 {
    let lp: P = P { n: 1, xs: [1, 2] };
    let s: i32 = inner_size(lp);
    let r: i32 = random_i32();
    while ((r & 1) != 0 || r < 16777216) { r = random_i32(); }
    return s;
}
`

// TestSelfHostNullStructDropWasmIR covers #9481: a struct release on wasm that
// walked its fields off a null box read the low WASI/clock/random scratch and
// passed whatever a host builtin had left there to $__fern_arr_dec — a trap on
// a program that had answered correctly until an unrelated call to a clock or
// random_i32 was added anywhere in it.
func TestSelfHostNullStructDropWasmIR(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping null-struct-drop wasm IR e2e")
	}
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/wasm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "drivers/wasm_ir_run.fern", "driver")

	var cmd *exec.Cmd
	if len(runner) == 0 {
		cmd = exec.Command(driverBin, "-ir")
	} else {
		cmd = exec.Command(runner[0], append(append(append([]string{}, runner[1:]...), driverBin), "-ir")...)
	}
	cmd.Stdin = bytes.NewReader([]byte(nullStructDropProg))
	wat, err := cmd.Output()
	if err != nil || len(wat) == 0 {
		t.Fatalf("driver failed: %v", err)
	}
	watFile := filepath.Join(dir, "null_struct_drop.wat")
	if err := os.WriteFile(watFile, wat, 0o644); err != nil {
		t.Fatalf("write wat: %v", err)
	}
	rcmd := exec.Command("wasmtime", "run", watFile)
	rcmd.Stdin = bytes.NewReader(nil)
	_ = rcmd.Run()
	if rcmd.ProcessState == nil || !rcmd.ProcessState.Exited() {
		t.Fatalf("wasmtime did not exit normally")
	}
	if got := rcmd.ProcessState.ExitCode(); got != 2 {
		t.Errorf("program = %d, want 2 (134 = the null box walked the low scratch)", got)
	}
}

// wasmFuncBodies returns each emitted `(func <prefix>…)` body in `wat`, header
// line included, from the header through the line before the next function.
//
// Matching a function by name and reading its body is what lets a test assert
// what is in ONE body: a bare substring search over the whole module spans
// whatever follows, so it breaks whenever a prologue is added (the #9481 guard
// broke six such assertions) and it can match text belonging to a different
// type's body.
func wasmFuncBodies(wat, prefix string) []string {
	var out []string
	lines := strings.Split(wat, "\n")
	for i, line := range lines {
		if !strings.HasPrefix(strings.TrimSpace(line), "(func "+prefix) {
			continue
		}
		body := []string{line}
		for j := i + 1; j < len(lines) && !strings.HasPrefix(strings.TrimSpace(lines[j]), "(func "); j++ {
			body = append(body, lines[j])
		}
		out = append(out, strings.Join(body, "\n"))
	}
	return out
}
