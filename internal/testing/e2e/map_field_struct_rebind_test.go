package e2e

import "testing"

// Issue #2763: a value-type struct with a Map field, reconstructed
// through a method (`return IntSet { m: s.m.insert(...) }`), used to
// SIGSEGV on x86-64 (and corrupt on arm64) — the COW mutator returns the
// borrowed receiver's handle in place, so the new struct aliased the
// caller's map and dropping the old struct freed it out from under the
// new one. The StructLit lowering now clones a Map field initialised by a
// COW mutator result, giving the new container its own buffer. main()
// returns the set size (2), which doubles as a no-crash check.
func TestX86_64MapFieldStructRebind(t *testing.T) {
	src := `
import "core/map";
struct IntSet { m: Map[i32, i32] }
function (s: IntSet) insert(x: i32): IntSet { return IntSet { m: s.m.insert(x, 1) }; }
function (s: IntSet) len(): i32 { return s.m.len(); }
function main(): i32 {
    let m0: Map[i32, i32] = map_new(4);
    let s: IntSet = IntSet { m: m0 };
    s = s.insert(10);
    s = s.insert(20);
    s = s.insert(10);   // duplicate — set size stays 2
    return s.len();
}`
	if _, code := compileAndRunX86_64(t, src); code != 2 {
		t.Errorf("IntSet rebind → exit = %d, want 2 (issue #2763)", code)
	}
}

// Issue #4871: the #2763 clone (above) fires only when the Map field value is
// DIRECTLY a COW-mutator call. One `let` removed — `let m = s.m.insert(...);
// return ISet { m: m }` — the field value is a plain ident, the clone was
// missed, and the new struct aliased the borrowed receiver's in-place buffer:
// dropping the old struct on `s = iset_add(s, ...)` freed it, so the SECOND
// wrap-insert hung on the corrupted (open-addressing) map header on x86-64
// (interp was correct). borrowedMapFieldResults now flags a Map local bound to
// a mutator with a field-access receiver so the StructLit clones it too. main()
// returns the set size (2); a hang or a wrong count both fail.
func TestX86_64MapFieldStructRebindIndirect(t *testing.T) {
	src := `
import "core/map";
struct ISet { m: Map[i32, i32] }
function iset_add(s: ISet, x: i32): ISet {
    let m: Map[i32, i32] = s.m.insert(x, 1);
    return ISet { m: m };
}
function main(): i32 {
    let m0: Map[i32, i32] = map_new(4);
    let s: ISet = ISet { m: m0 };
    s = iset_add(s, 10);   // one wrap-insert (was: emptied the map, returned 1→0)
    s = iset_add(s, 20);   // second wrap-insert (was: hung on the freed header)
    s = iset_add(s, 10);   // duplicate — set size stays 2
    return s.m.len();
}`
	if _, code := compileAndRunX86_64(t, src); code != 2 {
		t.Errorf("indirect IntSet rebind → exit = %d, want 2 (issue #4871)", code)
	}
}
