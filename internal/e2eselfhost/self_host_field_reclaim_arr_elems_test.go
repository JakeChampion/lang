package e2eselfhost

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// fieldReclaimArrElemsCases pin #7067: the two per-type reclaim helpers disagreed
// about a struct's ARRAY field whose ELEMENTS are pointer-shaped.
// __struct_drop_<T> (scope exit) is_unique-gates the buffer, walks it dec'ing each
// element box, then decs the buffer; __field_reclaim_<T> (the consume-rebind path)
// dec'd the buffer only. So `var b: Bag = Bag { es: [P { .. }, P { .. }], .. }`
// inside a loop stranded one element box per element on every iteration but the
// last — the value that goes out of scope is reclaimed, every value that is
// REBOUND leaks. The reclaim body now runs the same walk (and, for a deep-drop-ok
// element struct, the same __struct_arr_elems_drop_<E> pre-pass).
//
// The walk is admitted per type by the "sarr:" half of
// irlower.strfld_reclaim_ok_types_of, and the append case below is why it needs
// one at all: `d = Doc { ...d, vals: d.vals.append(v) }` hands the NEW buffer the
// same element pointers the superseded one holds, uncounted, so walking the old
// buffer would free boxes the live one still references (it segfaulted the
// conformance case alloc_flat_array_push_bound_elem). __struct_drop needs no such
// admission: at scope exit every buffer that ever shared those elements is dying
// too. Reads are therefore restricted to a bare `.len()` borrow, and stores to an
// array literal of fresh elements — the same contract the string[] sibling uses.
var fieldReclaimArrElemsCases = []struct {
	name string
	src  string
	want int
	wasm bool // exercised on the wasm leg too
}{
	// The reported shape: a struct-array field rebound per iteration. 98 before.
	{"structarr-field-rebind", `struct P { x: i32, y: i32 }
struct Bag { es: P[], n: i32 }
function main(): i32 {
    var acc: i32 = 0;
    var i: i32 = 0;
    while (i < 200) {
        var b: Bag = Bag { es: [P { x: i, y: i + 1 }, P { x: i + 2, y: i + 3 }], n: i };
        acc = (acc + b.n + b.es.len()) % 251;
        i = i + 1;
    }
    var b1: i32 = (__heap_bump_bytes() as i32);
    var j: i32 = 0;
    while (j < 2000) {
        var b: Bag = Bag { es: [P { x: j, y: j + 1 }, P { x: j + 2, y: j + 3 }], n: j };
        acc = (acc + b.n + b.es.len()) % 251;
        j = j + 1;
    }
    var b2: i32 = (__heap_bump_bytes() as i32);
    if (__rc_underflow() != 0) { return 99; }
    if (b2 - b1 >= 4096) { return 98; }
    if (acc < 0) { return 97; }
    return 0;
}`, 0, true},

	// MULTI-LEVEL: the element struct carries its own rc-array field, so the
	// element's buffer leaks too unless the reclaim runs
	// __struct_arr_elems_drop_P before freeing the element boxes — the level
	// __struct_drop's array arm already had.
	{"structarr-field-rebind-elem-fields", `struct P { xs: i32[], a: i32 }
struct Bag { es: P[], n: i32 }
function main(): i32 {
    var acc: i32 = 0;
    var i: i32 = 0;
    while (i < 200) {
        var b: Bag = Bag { es: [P { xs: [i, i + 1], a: i }, P { xs: [i + 2], a: i + 3 }], n: i };
        acc = (acc + b.n + b.es.len()) % 251;
        i = i + 1;
    }
    var b1: i32 = (__heap_bump_bytes() as i32);
    var j: i32 = 0;
    while (j < 2000) {
        var b: Bag = Bag { es: [P { xs: [j, j + 1], a: j }, P { xs: [j + 2], a: j + 3 }], n: j };
        acc = (acc + b.n + b.es.len()) % 251;
        j = j + 1;
    }
    var b2: i32 = (__heap_bump_bytes() as i32);
    if (__rc_underflow() != 0) { return 99; }
    if (b2 - b1 >= 4096) { return 98; }
    if (acc < 0) { return 97; }
    return 0;
}`, 0, true},

	// ADMISSION (negative): the field is grown by `d.vals.append(v)`, so the
	// superseded buffer shares every element with the live one. The type must
	// NOT be admitted — walking it double-frees (segfault / 99), and the sound
	// buffer-only dec has to stand instead.
	{"structarr-field-append-carry-safe", `struct Val { kind: i32, kids: i32[] }
struct Doc { vals: Val[], root: i32 }
function main(): i32 {
    var bad: i32 = 0;
    var i: i32 = 0;
    while (i < 200) {
        var d: Doc = Doc { vals: [], root: i };
        var j: i32 = 0;
        while (j < 4) {
            var v: Val = Val { kind: j, kids: [j, j + 1] };
            d = Doc { ...d, vals: d.vals.append(v) };
            j = j + 1;
        }
        if (d.vals.len() != 4) { bad = 1; }
        i = i + 1;
    }
    if (__rc_underflow() != 0) { return 99; }
    if (bad != 0) { return 97; }
    return 0;
}`, 0, true},

	// The enum-array sibling: the element boxes are variant constructions, freed
	// by the same walk (their PAYLOAD is one level deeper — the shared gap
	// __struct_drop has as well — so the variants here stay scalar). Register
	// backends only: wasm classifies a method-less enum element as scalar and
	// keeps BOTH helpers on the flat dec.
	{"enumarr-field-rebind", `enum Shape { Circle(i32), Square(i32) }
struct Bag { es: Shape[], n: i32 }
function main(): i32 {
    var acc: i32 = 0;
    var i: i32 = 0;
    while (i < 200) {
        var b: Bag = Bag { es: [Shape.Circle(i), Shape.Square(i + 1)], n: i };
        acc = (acc + b.n + b.es.len()) % 251;
        i = i + 1;
    }
    var b1: i32 = (__heap_bump_bytes() as i32);
    var j: i32 = 0;
    while (j < 2000) {
        var b: Bag = Bag { es: [Shape.Circle(j), Shape.Square(j + 1)], n: j };
        acc = (acc + b.n + b.es.len()) % 251;
        j = j + 1;
    }
    var b2: i32 = (__heap_bump_bytes() as i32);
    if (__rc_underflow() != 0) { return 99; }
    if (b2 - b1 >= 4096) { return 98; }
    if (acc < 0) { return 97; }
    return 0;
}`, 0, false},
}

// TestSelfHostFieldReclaimArrElemsIRX86_64 drives the cases through the
// self-hosted x86-64 compiler.
func TestSelfHostFieldReclaimArrElemsIRX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_ir_run.fern", "driver")

	for _, tc := range fieldReclaimArrElemsCases {
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
			if code := cmd.ProcessState.ExitCode(); code != tc.want {
				t.Errorf("%s = %d, want %d (98 = element boxes leaked; 99 = over-release; 97 = value corrupted)", tc.name, code, tc.want)
			}
		})
	}
}

// TestSelfHostFieldReclaimArrElemsIRArm64 is the arm64 leg: the walk is a
// transcription, not shared code (x10/x11/x12 hold new/old/snap across the field
// loop, so the buffer/index registers are borrowed and reloaded), which is
// exactly the kind of divergence a register backend hides until it runs.
func TestSelfHostFieldReclaimArrElemsIRArm64(t *testing.T) {
	arm64gcc, qemu := arm64Tooling(t)
	x86gcc, x86runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, x86gcc, dir, "asm_ir_run.fern", "driver")

	for _, tc := range fieldReclaimArrElemsCases {
		t.Run(tc.name, func(t *testing.T) {
			asm := runCapture(t, x86gcc, x86runner, driverBin, []byte(tc.src+"\n"), "-target", "arm64-linux")
			if len(asm) == 0 {
				t.Fatal("self-host arm64 compiler emitted 0 bytes")
			}
			bin := buildBinArm64(t, arm64gcc, dir, tc.name, string(asm))
			cmd := runArm64Bin(qemu, bin)
			_ = cmd.Run()
			if code := cmd.ProcessState.ExitCode(); code != tc.want {
				t.Errorf("%s = %d, want %d (98 = element boxes leaked; 99 = over-release; 97 = value corrupted)", tc.name, code, tc.want)
			}
		})
	}
}

// TestSelfHostFieldReclaimArrElemsWasmIR is the wasm leg: there the deep release
// is $__fern_arr_dec_ptr (which rc-gates the element walk internally), routed on
// the same array_field_elem_is_ptr predicate $__struct_drop_<T> uses.
func TestSelfHostFieldReclaimArrElemsWasmIR(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping field-reclaim array-element wasm IR e2e")
	}
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "wasm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "wasm_ir_run.fern", "driver")

	for _, tc := range fieldReclaimArrElemsCases {
		if !tc.wasm {
			continue
		}
		t.Run(tc.name, func(t *testing.T) {
			var cmd *exec.Cmd
			if len(runner) == 0 {
				cmd = exec.Command(driverBin, "-ir")
			} else {
				cmd = exec.Command(runner[0], append(append(append([]string{}, runner[1:]...), driverBin), "-ir")...)
			}
			cmd.Stdin = bytes.NewReader([]byte(tc.src + "\n"))
			wat, err := cmd.Output()
			if err != nil || len(wat) == 0 {
				t.Fatalf("driver failed for %s: %v", tc.name, err)
			}
			watFile := filepath.Join(dir, strings.ReplaceAll(tc.name, "/", "_")+".wat")
			if err := os.WriteFile(watFile, wat, 0o644); err != nil {
				t.Fatalf("write wat: %v", err)
			}
			rcmd := exec.Command("wasmtime", "run", watFile)
			_ = rcmd.Run()
			if rcmd.ProcessState == nil || !rcmd.ProcessState.Exited() {
				t.Fatalf("wasmtime did not exit normally for %s", tc.name)
			}
			if got := rcmd.ProcessState.ExitCode(); got != tc.want {
				t.Errorf("%s = %d, want %d (98 = element boxes leaked; 99 = over-release; 97 = value corrupted)", tc.name, got, tc.want)
			}
		})
	}
}
