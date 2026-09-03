package ir

import (
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/checker"
	"github.com/jakechampion/lang/internal/constfold"
	"github.com/jakechampion/lang/internal/modload"
	"github.com/jakechampion/lang/internal/monomorph"
)

// lowerImportsWith is lowerSourceWith through the module loader, for a
// program whose imports must resolve (`core/map` for the map helpers,
// `core/cmp` for a derived key).
func lowerImportsWith(t *testing.T, src string, ptrW int) *Program {
	t.Helper()
	prog, _, err := modload.LoadSource(src)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if err := constfold.Fold(prog, nil); err != nil {
		t.Fatalf("fold: %v", err)
	}
	info, err := checker.Check(prog)
	if err != nil {
		t.Fatalf("check: %v", err)
	}
	if err := monomorph.Run(prog, info); err != nil {
		t.Fatalf("monomorph: %v", err)
	}
	p, err := LowerWith(prog, info, ptrW)
	if err != nil {
		t.Fatalf("lower: %v", err)
	}
	return p
}

// countCallPrefix counts the direct calls in fnName whose callee name
// starts with prefix — the drop-family form of countCalls (`__drop_arr_*`).
func countCallPrefix(p *Program, fnName, prefix string) int {
	f := funcNamed(p, fnName)
	if f == nil {
		return -1
	}
	n := 0
	for _, op := range f.Ops {
		if op.Kind == OpCallDirect && strings.HasPrefix(op.Str, prefix) {
			n++
		}
	}
	return n
}

// #4399 sink 5 — `m.insert(k, v)` is a COUNTED store for a string key and
// for a value the map both retains and drops: emitMapSetRetains incs the
// aliased key / value (mapSetKeyCounted / mapSetValueCounted) and
// appendMapDropChain walks those columns at the map's drop, so
// computeFreeEligible's Map_set arm no longer escape-taints their sources.
// Pinned at the op level on the native single-word ABI (ptrW 8): the
// source's exit release routes through the freeing drop rather than the
// never-freeing plain dec.
func TestMapSetSourceFreeEligible(t *testing.T) {
	p := lowerImportsWith(t, `import "core/map";
function work(k: i32): i32 {
    var stem: string = "alpha";
    var key: string = stem + "-key-long";
    var src: i32[][] = [[k, k + 1], [k + 2]];
    var m: Map[string, i32[]] = map_new(4);
    m = m.insert(key, src[0]);
    return m.len();
}
function main(): i32 { return 0; }`, 8)
	// `src` (the projection's source container) deep-drops at its decl-site
	// reinit and its exit sweep — the tainted baseline had neither.
	if got := countCallPrefix(p, "work", "__drop_arr_"); got != 2 {
		t.Errorf("want 2 deep array drops (src reinit + sweep; tainted baseline 0), got %d:\n%s", got, p)
	}
	// `stem` (literal-seeded) and `key` (a fresh concat) both free through
	// __fern_str_dec at their reinit and sweep sites; the tainted baseline
	// skipped `key` entirely, leaving only stem's two.
	if got := countCallPrefix(p, "work", "__fern_str_dec"); got != 4 {
		t.Errorf("want 4 string releases (stem + key, reinit + sweep each; tainted baseline 2), got %d:\n%s", got, p)
	}
	// The store itself retains the aliased key and the aliased element, and
	// the map's drop walks both columns it now co-owns.
	incs := 0
	for _, op := range funcNamed(p, "work").Ops {
		if op.Kind == OpRcInc {
			incs++
		}
	}
	if incs < 2 {
		t.Errorf("want >= 2 store retains (key + element), got %d:\n%s", incs, p)
	}
	if got := countCallPrefix(p, "work", "__drop_map_str_keys"); got != 2 {
		t.Errorf("want the key column walked at both map drops, got %d:\n%s", got, p)
	}
	if got := countCallPrefix(p, "work", "__map_drop_values"); got != 2 {
		t.Errorf("want the value column walked at both map drops, got %d:\n%s", got, p)
	}
}

// The `Map { k: v }` literal stores through the same retain, so it is the
// same counted store.
func TestMapLitSourceFreeEligible(t *testing.T) {
	p := lowerImportsWith(t, `import "core/map";
function work(k: i32): i32 {
    var stem: string = "alpha";
    var key: string = stem + "-key-long";
    var src: i32[][] = [[k, k + 1], [k + 2]];
    var m: Map[string, i32[]] = Map { key: src[0] };
    return m.len();
}
function main(): i32 { return 0; }`, 8)
	// Three sites for `src`: its decl-site reinit, the last-use release
	// right after the literal (its only read is the entry projection), and
	// the null-guarded exit sweep. The tainted baseline had none.
	if got := countCallPrefix(p, "work", "__drop_arr_"); got != 3 {
		t.Errorf("want 3 deep array drops (src reinit + last-use + sweep), got %d:\n%s", got, p)
	}
	if got := countCallPrefix(p, "work", "__fern_str_dec"); got != 4 {
		t.Errorf("want 4 string releases (stem + key, reinit + sweep each), got %d:\n%s", got, p)
	}
}

// The uncounted remainder keeps the taint. A struct / enum KEY (kind 3) is
// stored as a raw pointer the key column never retains nor drops, and a
// nested-Map VALUE (kind 1) is neither retained on set nor walked at drop:
// freeing either source at scope exit would reclaim what the map still
// holds, so both stay ineligible.
func TestMapSetUncountedEntriesStayTainted(t *testing.T) {
	p := lowerImportsWith(t, `import "core/map";
import "core/cmp";
@derive(cmp.Eq, cmp.Hash)
struct Pt { x: i32, y: i32 }
function work(k: i32): i32 {
    var p: Pt = Pt { x: k, y: k + 1 };
    var m: Map[Pt, i32] = map_new(4);
    m = m.insert(p, k);
    return m.len();
}
function main(): i32 { return 0; }`, 8)
	if got := countCallPrefix(p, "work", "__drop_struct_Pt"); got != 0 {
		t.Errorf("a struct key's source must stay tainted (the key column holds it uncounted): want 0 deep struct drops, got %d:\n%s", got, p)
	}
	p2 := lowerImportsWith(t, `import "core/map";
function work(k: i32): i32 {
    var inner: Map[i32, i32] = map_new(2);
    inner = inner.insert(k, k);
    var outer: Map[i32, Map[i32, i32]] = map_new(2);
    outer = outer.insert(k, inner);
    return outer.len();
}
function main(): i32 { return 0; }`, 8)
	// Only `outer` drops (reinit + sweep); `inner` is held uncounted by
	// outer's value column and must not be reclaimed by its own name.
	if got := countCallPrefix(p2, "work", "__fern_map_drop"); got != 2 {
		t.Errorf("a nested-Map value's source must stay tainted: want 2 map drops (outer only), got %d:\n%s", got, p2)
	}
}
