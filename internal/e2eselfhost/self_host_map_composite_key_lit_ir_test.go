package e2eselfhost

import "testing"

// mapCompositeKeyLitCases pin #7001: a `Map { k: v, … }` literal whose key is a
// struct or enum deriving Eq + Hash.
//
// E045 used to reject every non-i32, non-string key, which native accepts. The
// rejection was essential rather than merely strict: `map_new` and
// `__map_new_i32` are the only two constructor spellings, so a composite key has
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
// CLI for x86-64.
func TestSelfHostMapCompositeKeyLitIRX86_64(t *testing.T) {
	cli := newStrictCLI(t)
	for _, tc := range mapCompositeKeyLitCases {
		t.Run(tc.name, func(t *testing.T) {
			if code, _ := cli.runX86(t, cli.emit(t, "x86-64-linux", tc.src)); code != 0 {
				t.Errorf("%s exited %d, want %d — the composite key column read back wrong", tc.name, code, 0)
			}
		})
	}
}

// TestSelfHostMapCompositeKeyLitIRArm64 is the arm64 leg, run under qemu.
func TestSelfHostMapCompositeKeyLitIRArm64(t *testing.T) {
	gcc, qemu := arm64Tooling(t)
	cli := newStrictCLI(t)
	for _, tc := range mapCompositeKeyLitCases {
		t.Run(tc.name, func(t *testing.T) {
			if code, _ := runArm64(t, gcc, qemu, cli.emit(t, "arm64-linux", tc.src)); code != 0 {
				t.Errorf("%s exited %d, want %d — the composite key column read back wrong", tc.name, code, 0)
			}
		})
	}
}

// TestSelfHostMapCompositeKeyLitIRWasm is the wasm leg.
func TestSelfHostMapCompositeKeyLitIRWasm(t *testing.T) {
	cli := newStrictCLI(t)
	for _, tc := range mapCompositeKeyLitCases {
		t.Run(tc.name, func(t *testing.T) {
			if code, _ := runWasm(t, cli.emit(t, "wasm32-wasi", tc.src)); code != 0 {
				t.Errorf("%s exited %d, want %d — the composite key column read back wrong", tc.name, code, 0)
			}
		})
	}
}
