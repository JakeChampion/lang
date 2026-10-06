package e2eselfhost

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestSelfHostStructArrElemDropWasm is the wasm mirror of the ARRAY-ELEMENT deep-drop
// (the x86 sibling is TestSelfHostStructArrElemDropIRX86_64). A struct-array field
// `S { elems: Inner[] }` releases each element's OWN rc fields: $__sem_drop_S walks
// a uniquely-owned buffer and calls $__sem_release_Inner per element.
//
// Reclaim is proven by a memory-cap differential: a long alloc->drop churn over a fresh
// `S{ elems: [Inner{items:[..]}, ..] }` stays bounded under a tight max-memory-size cap
// with trap-on-grow-failure (each element's items buffer + boxes recycle onto the
// freelist); a regression to the shallow element walk leaks the items past the cap and
// traps. The WAT assertion pins the per-element $__sem_release_Inner. items goes
// through id so it is built on the heap rather than placed as a constant.
func TestSelfHostStructArrElemDropWasm(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping wasm array-element deep-drop e2e")
	}
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/wasm_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "drivers/wasm_run.fern", "wasm_run")

	const cap = "16777216" // 16 MiB
	prog := `struct Inner { items: i32[] }
function id(xs: i32[]): i32[] { return xs; }
struct S { elems: Inner[], tag: i32 }
function mk(): i32 {
    let s: S = S { elems: [Inner { items: id([1,2,3,4,5,6,7,8]) }, Inner { items: id([9,10,11,12,13,14,15,16]) }], tag: 3 };
    return s.elems[0].items[0] + s.elems[1].items[7] + s.tag;
}
function main(): i32 {
    let acc: i32 = 0; let k: i32 = 0;
    while (k < 400000) { acc = mk(); k = k + 1; }
    return acc - 20;
}`
	wat := runCapture(t, gcc, runner, driverBin, []byte(prog))
	if len(wat) == 0 {
		t.Fatal("wasm emitter produced 0 bytes")
	}
	if !strings.Contains(string(wat), "$__sem_release_Inner") {
		t.Fatalf("emitted WAT missing $__sem_release_Inner — the struct-array element did not deep-drop\n--- WAT ---\n%s", wat)
	}
	watPath := filepath.Join(dir, "struct_arr_elem_drop.wat")
	if err := os.WriteFile(watPath, wat, 0o644); err != nil {
		t.Fatalf("write wat: %v", err)
	}
	cmd := exec.Command("wasmtime", "run",
		"-W", "max-memory-size="+cap,
		"-W", "trap-on-grow-failure=y",
		"--dir", dir, watPath)
	_, _ = cmd.Output()
	if code := cmd.ProcessState.ExitCode(); code != 0 {
		t.Errorf("wasm exited %d, want 0 (a trap means each element's items leaked past the %s-byte cap — the array element did not reclaim)\n--- WAT ---\n%s", code, cap, wat)
	}
}
