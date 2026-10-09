package e2ecompiler

import (
	"testing"
)

// A map's key search, per key kind, through all four operations that probe
// it: insert, get_or, has and without.
//
// Three programs, one per key kind, each driving the search through all four
// operations so one that only insert (or only without) reaches cannot pass on a
// sibling's coverage:
//
//	152 = present*100 + absent-defaults-to-5 *10 + overwritten-then-read 2
//
// Each digit fails differently. 90 means the shared helper found nothing where
// the key is present; 91 means it claimed a hit on a key that was never
// inserted — the two directions a botched three-way dispatch takes.
const (
	mapFindStrSrc = `import "core/map";
function main(): i32 {
    let m: Map[string, i32] = Map {};
    m = m.insert("a", 1);
    m = m.insert("b", 9);
    if (!m.has("a")) { return 90; }
    if (m.has("zz")) { return 91; }
    m = m.insert("b", 2);
    let d: (Map[string, i32], boolean) = m.without("q");
    if (d.1) { return 91; }
    return m.get_or("a", 0) * 100 + m.get_or("zz", 5) * 10 + m.get_or("b", 0);
}
`
	mapFindI32Src = `import "core/map";
function main(): i32 {
    let m: Map[i32, i32] = Map {};
    m = m.insert(10, 1);
    m = m.insert(20, 9);
    if (!m.has(10)) { return 90; }
    if (m.has(99)) { return 91; }
    m = m.insert(20, 2);
    let d: (Map[i32, i32], boolean) = m.without(77);
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
    let m: Map[K, i32] = Map {};
    m = m.insert(K { a: 1, b: 1 }, 1);
    m = m.insert(K { a: 2, b: 2 }, 9);
    if (!m.has(K { a: 1, b: 1 })) { return 90; }
    if (m.has(K { a: 9, b: 9 })) { return 91; }
    m = m.insert(K { a: 2, b: 2 }, 2);
    let d: (Map[K, i32], boolean) = m.without(K { a: 7, b: 7 });
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

const mapFindFailFmt = "%s exited %d, want 152 (90=the search missed a present key, 91=it hit a key that was never inserted)"

// TestSelfHostMapFindSharedIRX86_64 runs each key kind on x86-64. 152 is the
// interpreter's answer for all three, taken as the oracle.
func TestSelfHostMapFindSharedIRX86_64(t *testing.T) {
	cli := newStrictCLI(t)
	for _, tc := range mapFindCases() {
		t.Run(tc.name, func(t *testing.T) {
			if code, _ := cli.runX86(t, cli.emit(t, "x86-64-linux", tc.src)); code != 152 {
				t.Errorf(mapFindFailFmt, tc.name, code)
			}
		})
	}
}

// TestSelfHostMapFindSharedIRArm64 is the same three programs under qemu.
func TestSelfHostMapFindSharedIRArm64(t *testing.T) {
	gcc, qemu := arm64Tooling(t)
	cli := newStrictCLI(t)
	for _, tc := range mapFindCases() {
		t.Run(tc.name, func(t *testing.T) {
			if code, _ := runArm64(t, gcc, qemu, cli.emit(t, "arm64-linux", tc.src)); code != 152 {
				t.Errorf(mapFindFailFmt, tc.name, code)
			}
		})
	}
}
