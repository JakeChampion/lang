package e2eselfhost

import (
	"os/exec"
	"strings"
	"testing"
)

// The map runtime's key search is ONE Fern helper now (#2649):
// `__fern_map_find(keys, key, keykind, eqfn) -> i32`, the index of the key or
// -1. It replaces eighteen hand-written copies of itself — map_set, map_get and
// map_has each carried a string, an i32 and a struct/enum variant of the same
// loop, per register backend — plus the fourth copy `__fern_map_delete`'s Fern
// body had inlined.
//
// Three programs, one per key kind, each driving the search through all four
// callers so a variant that only map_set (or only map_delete) reaches cannot
// pass on a sibling's coverage:
//
//	152 = present*100 + absent-defaults-to-5 *10 + overwritten-then-read 2
//
// Each digit fails differently. 90 means the shared helper found nothing where
// the key is present; 91 means it claimed a hit on a key that was never
// inserted — the two directions a botched three-way dispatch takes.
const (
	mapFindStrSrc = `function main(): i32 {
    var m: Map[string, i32] = Map {};
    m = m.insert("a", 1);
    m = m.insert("b", 9);
    if (!m.has("a")) { return 90; }
    if (m.has("zz")) { return 91; }
    m = m.insert("b", 2);
    var d: (Map[string, i32], boolean) = m.without("q");
    if (d.1) { return 91; }
    return m.get_or("a", 0) * 100 + m.get_or("zz", 5) * 10 + m.get_or("b", 0);
}
`
	mapFindI32Src = `function main(): i32 {
    var m: Map[i32, i32] = Map {};
    m = m.insert(10, 1);
    m = m.insert(20, 9);
    if (!m.has(10)) { return 90; }
    if (m.has(99)) { return 91; }
    m = m.insert(20, 2);
    var d: (Map[i32, i32], boolean) = m.without(77);
    if (d.1) { return 91; }
    return m.get_or(10, 0) * 100 + m.get_or(99, 5) * 10 + m.get_or(20, 0);
}
`
	// The struct key is the case that matters most: it is the only one that
	// reaches the derived `__fn_K__eq` through the runtime code address the
	// `eqfn: i32` parameter carries, and the only one where comparing the key
	// BOX pointers instead would still find the key it was just handed. Each
	// lookup below builds a FRESH key, so a pointer compare answers 90.
	mapFindStructSrc = `import "core/map";
import "core/cmp";
@derive(cmp.Eq, cmp.Hash)
struct K { a: i32, b: i32 }
function main(): i32 {
    var m: Map[K, i32] = Map {};
    m = m.insert(K { a: 1, b: 1 }, 1);
    m = m.insert(K { a: 2, b: 2 }, 9);
    if (!m.has(K { a: 1, b: 1 })) { return 90; }
    if (m.has(K { a: 9, b: 9 })) { return 91; }
    m = m.insert(K { a: 2, b: 2 }, 2);
    var d: (Map[K, i32], boolean) = m.without(K { a: 7, b: 7 });
    if (d.1) { return 91; }
    return m.get_or(K { a: 1, b: 1 }, 0) * 100 + m.get_or(K { a: 9, b: 9 }, 5) * 10 + m.get_or(K { a: 2, b: 2 }, 0);
}
`
)

func mapFindCases() []struct{ name, src string } {
	return []struct{ name, src string }{
		{"string_key", mapFindStrSrc},
		{"i32_key", mapFindI32Src},
		{"struct_key", mapFindStructSrc},
	}
}

// TestSelfHostMapFindSharedIRX86_64 runs each key kind on x86-64. 152 is the
// interpreter's answer for all three, taken as the oracle.
func TestSelfHostMapFindSharedIRX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_ir_run.fern", "driver")

	for _, tc := range mapFindCases() {
		t.Run(tc.name, func(t *testing.T) {
			asm := string(runCapture(t, gcc, runner, driverBin, []byte(tc.src), "-ir"))
			if len(asm) == 0 {
				t.Fatal("self-host emitted 0 bytes")
			}
			bin := buildBin(t, gcc, dir, "mf_"+tc.name, asm)
			var cmd *exec.Cmd
			if len(runner) == 0 {
				cmd = exec.Command(bin)
			} else {
				cmd = exec.Command(runner[0], append(append([]string{}, runner[1:]...), bin)...)
			}
			_ = cmd.Run()
			if got := cmd.ProcessState.ExitCode(); got != 152 {
				t.Errorf("exited %d, want %d (90=the shared search missed a present key, "+
					"91=it hit a key that was never inserted)", got, 152)
			}
		})
	}
}

// TestSelfHostMapFindSharedIRArm64 is the same three programs under qemu. The
// helper source is shared, but each of the three hand-asm callers marshals its
// four arguments per backend — and arm64's stack ABI uses 16-byte slots where
// x86-64 uses 8 — so a slot-size or argument-order slip is arm64-only.
func TestSelfHostMapFindSharedIRArm64(t *testing.T) {
	arm64gcc, qemu := arm64Tooling(t)
	x86gcc, x86runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, x86gcc, dir, "asm_ir_run.fern", "driver")

	for _, tc := range mapFindCases() {
		t.Run(tc.name, func(t *testing.T) {
			asm := string(runCapture(t, x86gcc, x86runner, driverBin, []byte(tc.src), "-target", "arm64-linux"))
			if len(asm) == 0 {
				t.Fatal("self-host arm64 compiler emitted 0 bytes")
			}
			bin := buildBinArm64(t, arm64gcc, dir, "mf_arm64_"+tc.name, asm)
			cmd := runArm64Bin(qemu, bin)
			_ = cmd.Run()
			if got := cmd.ProcessState.ExitCode(); got != 152 {
				t.Errorf("arm64 exited %d, want %d", got, 152)
			}
		})
	}
}

// TestSelfHostMapFindHandAsmLoopsGone pins the deletion, in both directions.
//
// The loops are keyed by their own local labels: `.Lmg_loop` / `.Lmh_loop` /
// `.Lms_loop` and their `_i32` / `_struct` variants were emitted by nothing but
// the three hand-asm searches, on either backend. The surviving `.Lmg_none` /
// `.Lms_append` / `.Lmh_no` labels are the callers' answer arms and stay, so
// the assertion has to key on `_loop` rather than on the `.Lm` stem.
//
// The present-side half matters as much: the helper must be emitted ONCE. Three
// callers reach it by symbol and a fourth (`__fern_map_delete`) by an ordinary
// Fern call, so emitting it per caller would be a duplicate-symbol link failure
// — but only for a program that uses more than one map verb, which is exactly
// the shape a narrower test would miss.
func TestSelfHostMapFindHandAsmLoopsGone(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_ir_run.fern", "driver")

	for _, tc := range []struct{ name, target string }{
		{"x86_64", ""},
		{"arm64", "arm64-linux"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			args := []string{"-ir"}
			if tc.target != "" {
				args = []string{"-target", tc.target}
			}
			asm := string(runCapture(t, gcc, runner, driverBin, []byte(mapFindStrSrc), args...))
			if n := strings.Count(asm, "__fn___fern_map_find:"); n != 1 {
				t.Fatalf("%s: %d definitions of __fn___fern_map_find, want exactly 1", tc.name, n)
			}
			// map_set, map_get and map_has each call it, and map_delete is not
			// in this program — so three, and a smaller count means a caller
			// kept a loop of its own.
			if n := strings.Count(asm, "__fn___fern_map_find\n"); n < 3 {
				t.Errorf("%s: only %d call sites of __fn___fern_map_find, want 3 (set, get, has)", tc.name, n)
			}
			for _, lbl := range []string{".Lmg_loop", ".Lmh_loop", ".Lms_loop"} {
				if strings.Contains(asm, lbl) {
					t.Errorf("%s: a hand-asm key-search loop is back (%s is in the output)", tc.name, lbl)
				}
			}
		})
	}
}
