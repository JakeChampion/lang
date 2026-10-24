package e2e

import "testing"

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
