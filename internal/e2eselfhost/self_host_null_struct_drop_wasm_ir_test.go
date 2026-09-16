package e2eselfhost

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// nullStructDropProg leaves a NULL struct box for $__struct_drop_P to walk, and
// poisons the low scratch the walk would read through it.
//
// `lp` is released eagerly at its last use (the __struct_drop_P + box dec pair
// after inner_size), which zeroes the slot; the exit sweep still releases the
// slot, so the second release reaches the helper with box = 0 — by contract,
// which is why both register backends' bodies open with a low-address guard.
// Reading field 1 off a null box lands at linear address 16, the WASI out-
// parameter slot $__fern_random_i32 and the clock builtins write through.
//
// The while loop is what makes the fault deterministic rather than a coin
// flip: it leaves a word at 16 that is even (an odd word is a tagged pointer
// $__fern_arr_dec returns on) and far past any memory this module grows to, so
// an unguarded walk hands $__fern_arr_dec a wild pointer every run. Answers 2,
// the length of lp.xs.
const nullStructDropProg = `struct P { n: i32, xs: i32[] }
@noinline function inner_size(p: P): i32 { return p.xs.len(); }
function main(): i32 {
    var lp: P = P { n: 1, xs: [1, 2] };
    var s: i32 = inner_size(lp);
    var r: i32 = random_i32();
    while ((r & 1) != 0 || r < 16777216) { r = random_i32(); }
    return s;
}
`

// TestSelfHostNullStructDropWasmIR covers #9481: emit_wasm_struct_drop_body
// walked its fields off an unguarded box, where asm_ir.emit_ir_struct_drop_one
// (`cmpq $0x10000`) and asm_arm64_ir.emit_arm64_struct_drop_one
// (`cmp x10, #16, lsl #12`) both skip the walk on a null / low box. The
// per-field helpers guard their own argument, but that argument is already a
// field READ through the box, so on wasm a null box read the low WASI/clock/
// random scratch and passed whatever a host builtin had left there to
// $__fern_arr_dec — a trap on a program that had answered correctly until an
// unrelated call to a clock or random_i32 was added anywhere in it.
//
// Two assertions, because either alone is weak: the text one pins the guard on
// EVERY emitted body (a struct whose fields happen to read a harmless word
// still has the defect), and the run pins what the guard is for.
func TestSelfHostNullStructDropWasmIR(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping null-struct-drop wasm IR e2e")
	}
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "wasm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "wasm_ir_run.fern", "driver")

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
	for _, name := range unguardedStructDropBodies(string(wat)) {
		t.Errorf("$__struct_drop_%s reads a field before guarding $box (#9481)", name)
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

// unguardedStructDropBodies returns the type names of every $__struct_drop_<T>
// body in `wat` that touches a field before testing $box against the heap base.
// A body with no field to walk is a bare `(local.get $box))` return and needs
// no guard.
func unguardedStructDropBodies(wat string) []string {
	var bad []string
	for _, body := range wasmFuncBodies(wat, "$__struct_drop_") {
		lines := strings.Split(body, "\n")
		if len(lines) < 2 {
			continue
		}
		next := strings.TrimSpace(lines[1])
		if next == "(local.get $box))" || strings.HasPrefix(next, "(if (i32.ge_u (local.get $box) (i32.const ") {
			continue
		}
		name, _, _ := strings.Cut(strings.TrimPrefix(strings.TrimSpace(lines[0]), "(func $__struct_drop_"), " ")
		bad = append(bad, name)
	}
	return bad
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
