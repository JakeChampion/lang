package e2eselfhost

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestSelfHostMapKeyedArm64IR pins #6081: a `Map[K, V]` whose key K is a struct
// or enum compares its keys through K's derived `Eq` on the self-host ARM64
// path, not by reinterpreting the key box.
//
// The arm64 map runtime had no keyed-compare path at all. irlower threads the
// derived equality symbol through every keyed map op (`map_key_eqfn` →
// `Op.str`), and the x86-64 emitter loads it into %r8 for __fern_map_set /
// _get / _has / _delete — but the arm64 emitter dropped it on the floor, so a
// struct key fell through to the STRING loop and `__fern_str_eq` read the key
// box as a `{data, len}` string box.
//
// A struct/enum box is {shape-ptr @0, field0 @8, …}, so read as a string box
// its "data" is the SHAPE pointer and its "length" is field 0. __fern_str_eq
// compares the lengths first, so on the broken runtime two keys of the same
// type compared equal iff their FIRST FIELD was bit-identical — and when it
// was, the byte loop then walked the shared shape pointer and trivially agreed.
// That single fact explains every observed symptom:
//   - `Name { first: string, … }` — field 0 is a string-box POINTER, so a
//     freshly built value-equal key had a different "length" and missed. This
//     is the one the fixture caught.
//   - `P { a: i32, b: i32 }` — the positive probes hit for the right-looking
//     reason, but `P { a: 1, b: 4 }` ALSO matched `P { a: 1, b: 2 }`: equal
//     first field, and `b` was never read at all. Hence the scalar case below,
//     with a negative probe that differs only in the second field.
//
// The issue read the miss as "does not hash structurally"; it was narrower and
// worse than that — the runtime was comparing the wrong bytes entirely.
//
// The programs import the real std/core modules (the checker requires
// `@derive(Eq, Hash)` and `import "core/map"` for a struct key — there is no
// import-free spelling of this shape), so they route through asm_load_run's
// file-loading driver with a stdlib root, like the std/regex IR table does.
var mapKeyedArm64Cases = []struct {
	name string
	src  string
	want int
}{
	// Struct key carrying a STRING field: the #6081 shape. Every probe key is
	// built fresh — one from a concatenation, so its bytes live in a different
	// buffer than the inserted key's — and must still hit.
	{"struct-string-field", `import "core/map";
import "core/cmp";

@derive(cmp.Eq, cmp.Hash)
struct Name { first: string, rank: i32 }

function main(): i32 {
    var m: Map[Name, i32] = map_new(8);
    m = m.insert(Name { first: "ada", rank: 1 }, 10);
    m = m.insert(Name { first: "bob", rank: 2 }, 20);
    if (m.get_or(Name { first: "a" + "da", rank: 1 }, -1) != 10) { return 1; }   // map_get
    if (m.get_or(Name { first: "ada", rank: 9 }, -1) != -1) { return 2; }        // absent
    if (!m.has(Name { first: "b" + "ob", rank: 2 })) { return 3; }               // map_has
    if (m.has(Name { first: "zzz", rank: 1 })) { return 4; }
    m = m.insert(Name { first: "a" + "da", rank: 1 }, 99);                       // map_set overwrite
    if (m.len() != 2) { return 5; }
    if (m.get_or(Name { first: "ada", rank: 1 }, -1) != 99) { return 6; }
    var (m2, gone) = m.without(Name { first: "a" + "da", rank: 1 });             // map_delete
    if (!gone) { return 7; }
    if (m2.has(Name { first: "ada", rank: 1 })) { return 8; }
    if (m2.len() != 1) { return 9; }
    return 42;
}`, 42},

	// Struct key of SCALARS only. The fixture never caught this shape because
	// its positive probes hit; check 3 is the one that exposes it — on the
	// broken runtime only field 0 was ever compared, so a key differing solely
	// in `b` matched.
	{"struct-scalars", `import "core/map";
import "core/cmp";

@derive(cmp.Eq, cmp.Hash)
struct P { a: i32, b: i32 }

function main(): i32 {
    var m: Map[P, i32] = map_new(8);
    m = m.insert(P { a: 1, b: 2 }, 10);
    m = m.insert(P { a: 3, b: 4 }, 20);
    if (m.get_or(P { a: 1, b: 2 }, -1) != 10) { return 1; }
    if (m.get_or(P { a: 3, b: 4 }, -1) != 20) { return 2; }
    if (m.get_or(P { a: 1, b: 4 }, -1) != -1) { return 3; }   // both fields must match
    if (m.get_or(P { a: 9, b: 9 }, -1) != -1) { return 4; }
    return 42;
}`, 42},

	// Enum key: unit, scalar-payload and string-payload variants through the
	// same derived eq. A fresh string payload is the enum twin of case 1.
	{"enum-key", `import "core/map";
import "core/cmp";

@derive(cmp.Eq, cmp.Hash)
enum Tag { A(i32), B, C(string) }

function main(): i32 {
    var m: Map[Tag, i32] = map_new(8);
    m = m.insert(A(1), 100);
    m = m.insert(B, 200);
    m = m.insert(C("x" + "y"), 300);
    if (m.get_or(A(1), 0) != 100) { return 1; }
    if (m.get_or(B, 0) != 200) { return 2; }
    if (m.get_or(C("xy"), 0) != 300) { return 3; }    // fresh string payload
    if (m.get_or(A(2), -1) != -1) { return 4; }       // same variant, different payload
    if (m.get_or(C("zz"), -1) != -1) { return 5; }
    return 42;
}`, 42},
}

func TestSelfHostMapKeyedArm64IR(t *testing.T) {
	arm64gcc, qemu := arm64Tooling(t)
	x86gcc, x86runner := x86_64Tooling(t)
	dir := copySelfHostTree(t)
	driver := buildSelfHostBin(t, x86gcc, dir, "asm_load_run.fern", "alr")
	root, err := filepath.Abs("../../internal/stdlib")
	if err != nil {
		t.Fatalf("abs stdlib root: %v", err)
	}

	for _, tc := range mapKeyedArm64Cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			entry := filepath.Join(dir, "mapkeyed_"+tc.name+".fern")
			if err := os.WriteFile(entry, []byte(tc.src+"\n"), 0o644); err != nil {
				t.Fatalf("write entry: %v", err)
			}
			out, err := runX86_64Bin(x86runner, driver, entry, root, "-target", "arm64-linux").Output()
			if err != nil || len(out) == 0 {
				t.Fatalf("%s: arm64 driver failed (%d bytes, err %v)", tc.name, len(out), err)
			}
			// `.Lira_` is the arm64 IR emitter's per-function label prefix; its
			// presence proves the module routed through asm_arm64_ir rather than
			// erroring out to an empty emit.
			if !strings.Contains(string(out), ".Lira_") {
				t.Fatalf("%s: arm64 asm has no .Lira_ marker", tc.name)
			}
			bin := buildBinArm64(t, arm64gcc, dir, "mapkeyed_"+tc.name, string(out))
			run := runArm64Bin(qemu, bin)
			_ = run.Run()
			if run.ProcessState == nil || !run.ProcessState.Exited() {
				t.Fatalf("%s: inner did not exit normally", tc.name)
			}
			if code := run.ProcessState.ExitCode(); code != tc.want {
				t.Errorf("%s: exit %d, want %d (a non-42 value is the failing check's index)", tc.name, code, tc.want)
			}
		})
	}
}
