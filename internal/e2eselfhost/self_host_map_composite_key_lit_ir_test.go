package e2eselfhost

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// mapCompositeKeyLitCases pin #7001: a `Map { k: v, … }` literal whose key is a
// struct or enum deriving Eq + Hash.
//
// E045 used to reject every non-i32, non-string key, which native accepts. The
// rejection was load-bearing rather than merely strict: `map_new` and
// `map_new_i32` are the only two constructor spellings, so a composite key has
// neither and the literal's desugared chain based itself on `map_new` — the
// STRING one. `insert` was typed as returning its receiver unchanged and
// `map_new` types its columns unknown, so the chain stayed unknown-keyed and
// everything downstream fell back to the constructor NAME. The map built itself
// string-keyed and `__fern_str_eq` read each key box as a string, taking its
// first field as a length: keys differing only PAST that field compared equal
// and collapsed into one entry.
//
// That is why the discriminating cases below differ in exactly one field. A
// probe whose keys differ in several fields passes even unfixed — the first
// reduction of this bug did exactly that and read as "already fixed".
//
// The `Map {}` + `.insert` form was always correct (its receiver slot carries
// the declared `Map[K, V]`), so it is here as a control: the fix must not
// disturb the path that already worked.
//
// Exit 0 is correct; each nonzero code names the check that failed.
var mapCompositeKeyLitCases = []struct {
	name string
	src  string
}{
	// Keys differing ONLY in the string field, and ONLY in the i32 field. A
	// string-keyed build collapses the first pair; an i32-keyed build the
	// second. Correct is 22 — both maps hold two entries.
	{"struct-key-one-field-differs", `import "core/cmp";
import "core/map";

@derive(cmp.Eq, cmp.Hash)
struct Key { a: i32, b: string }

function main(): i32 {
    var s: Map[Key, i32] = Map { Key { a: 1, b: "x" }: 10, Key { a: 1, b: "y" }: 20 };
    var i: Map[Key, i32] = Map { Key { a: 1, b: "x" }: 10, Key { a: 2, b: "x" }: 20 };
    if (s.len() != 2) { return 90; }
    if (i.len() != 2) { return 91; }
    if (s.get_or(Key { a: 1, b: "y" }, 0) != 20) { return 92; }
    if (i.get_or(Key { a: 2, b: "x" }, 0) != 20) { return 93; }
    return 0;
}
`},
	// Read-back, absent-key miss, and overwrite through a composite key.
	{"struct-key-read-miss-overwrite", `import "core/cmp";
import "core/map";

@derive(cmp.Eq, cmp.Hash)
struct Key { a: i32, b: string }

function main(): i32 {
    var m: Map[Key, i32] = Map { Key { a: 1, b: "x" }: 10, Key { a: 2, b: "y" }: 20 };
    if (m.get_or(Key { a: 1, b: "x" }, 0) != 10) { return 90; }
    if (m.get_or(Key { a: 3, b: "z" }, 77) != 77) { return 91; }
    if (m.has(Key { a: 9, b: "q" })) { return 92; }
    m = m.insert(Key { a: 1, b: "x" }, 11);
    if (m.len() != 2) { return 93; }
    if (m.get_or(Key { a: 1, b: "x" }, 0) != 11) { return 94; }
    return 0;
}
`},
	// A payload-free enum key. This one needed its own fix: type_to_irtag
	// returns "" for a plain union, so the refined `Map[T, V]` tag could not be
	// spelled and the column fell back to the constructor name again.
	{"enum-key-literal", `import "core/cmp";
import "core/map";

@derive(cmp.Eq, cmp.Hash)
enum Tag { Red, Green, Blue }

function main(): i32 {
    var e: Map[Tag, i32] = Map { Red: 1, Green: 2, Blue: 3 };
    if (e.len() != 3) { return 90; }
    if (e.get_or(Green, 0) != 2) { return 91; }
    if (e.get_or(Blue, 0) != 3) { return 92; }
    return 0;
}
`},
	// Control: the `Map {}` + `.insert` form, which was already correct.
	{"insert-form-control", `import "core/cmp";
import "core/map";

@derive(cmp.Eq, cmp.Hash)
struct Key { a: i32, b: string }

function main(): i32 {
    var m: Map[Key, i32] = Map {};
    m = m.insert(Key { a: 1, b: "x" }, 10);
    m = m.insert(Key { a: 1, b: "y" }, 20);
    if (m.len() != 2) { return 90; }
    if (m.get_or(Key { a: 1, b: "y" }, 0) != 20) { return 91; }
    return 0;
}
`},
	// Control: a scalar-keyed literal must be untouched by the composite path.
	{"scalar-key-control", `import "core/map";

function main(): i32 {
    var m: Map[i32, i32] = Map { 1: 10, 2: 20 };
    var s: Map[string, i32] = Map { "a": 40, "b": 2 };
    if (m.len() != 2 || s.len() != 2) { return 90; }
    if (m.get_or(2, 0) != 20) { return 91; }
    if (s.get_or("a", 0) != 40) { return 92; }
    return 0;
}
`},
}

// TestSelfHostMapCompositeKeyLitIRX86_64 runs each case through the self-hosted
// x86-64 IR driver, pinned to the "ir" path.
func TestSelfHostMapCompositeKeyLitIRX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := writeSelfHostAsmProject(t)
	copySelfHostDriver(t, dir, "asm_run.fern", "asm_pathprobe_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_run.fern", "driver")
	probeBin := buildSelfHostBin(t, gcc, dir, "asm_pathprobe_run.fern", "pathprobe")

	for _, tc := range mapCompositeKeyLitCases {
		t.Run(tc.name, func(t *testing.T) {
			src := []byte(tc.src)
			path := strings.TrimSpace(string(runCapture(t, gcc, runner, probeBin, src)))
			if path != "ir" {
				t.Fatalf("%s routed through %q path, want \"ir\"", tc.name, path)
			}
			asm := runCapture(t, gcc, runner, driverBin, src)
			if len(asm) == 0 {
				t.Fatal("self-host compiler emitted 0 bytes")
			}
			progBin := buildBin(t, gcc, dir, "mck_"+tc.name, string(asm))
			var cmd *exec.Cmd
			if len(runner) == 0 {
				cmd = exec.Command(progBin)
			} else {
				cmd = exec.Command(runner[0], append(runner[1:], progBin)...)
			}
			_ = cmd.Run()
			if code := cmd.ProcessState.ExitCode(); code != 0 {
				t.Errorf("%s exited %d, want 0 — the composite key column read back wrong", tc.name, code)
			}
		})
	}
}

// TestSelfHostMapCompositeKeyLitIRArm64 is the arm64 leg, run under qemu.
func TestSelfHostMapCompositeKeyLitIRArm64(t *testing.T) {
	arm64gcc, qemu := arm64Tooling(t)
	x86gcc, x86runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, x86gcc, dir, "asm_ir_run.fern", "driver")

	for _, tc := range mapCompositeKeyLitCases {
		t.Run(tc.name, func(t *testing.T) {
			asm := runCapture(t, x86gcc, x86runner, driverBin, []byte(tc.src), "-target", "arm64-linux")
			if len(asm) == 0 {
				t.Fatalf("%s: self-host arm64 compiler emitted 0 bytes", tc.name)
			}
			bin := buildBinArm64(t, arm64gcc, dir, "mck_"+tc.name, string(asm))
			cmd := runArm64Bin(qemu, bin)
			_ = cmd.Run()
			if code := cmd.ProcessState.ExitCode(); code != 0 {
				t.Errorf("%s exited %d, want 0 — the composite key column read back wrong", tc.name, code)
			}
		})
	}
}

// TestSelfHostMapCompositeKeyLitIRWasm is the wasm leg.
func TestSelfHostMapCompositeKeyLitIRWasm(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping self-host map-composite-key wasm IR e2e")
	}
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "wasm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "wasm_ir_run.fern", "driver")

	for _, tc := range mapCompositeKeyLitCases {
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
			watFile := filepath.Join(dir, "mapcompositekey_prog.wat")
			if err := os.WriteFile(watFile, wat, 0o644); err != nil {
				t.Fatalf("write wat: %v", err)
			}
			run := exec.Command("wasmtime", "run", watFile)
			out, runErr := run.CombinedOutput()
			if run.ProcessState == nil || !run.ProcessState.Exited() {
				t.Fatalf("wasmtime did not exit normally for %q: %v\n%s", tc.name, runErr, out)
			}
			if code := run.ProcessState.ExitCode(); code != 0 {
				t.Errorf("map-composite-key wasm IR %q = %d, want 0\n%s", tc.name, code, out)
			}
		})
	}
}
