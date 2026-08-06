package e2e

// core/map's per-process string-hash seed (#6194).
//
// The attack this defends against is hash flooding: the string hash was
// deterministic and public, so a colliding key set could be computed OFFLINE
// and fed to any Fern program that maps attacker-supplied strings — header,
// query, and JSON-object keys in an edge handler are exactly that. Every
// lookup then degrades to a linear scan.
//
// The defence is a seed drawn once per process from the CSPRNG and XORed into
// the FNV offset basis. Placement inside the compression is the point: a
// post-mix of the finished hash is a function OF the hash, so `h(a) == h(b)`
// would still imply `mix(h(a)) == mix(h(b))` and every precomputed collision
// would survive.
//
// THREE PROPERTIES ARE TESTED, and they are the three that can actually
// regress:
//
//   1. The seed VARIES across processes. Without this the defence is nothing —
//      a fixed seed is as precomputable as no seed. Tested by running one
//      binary repeatedly, not by compiling twice, so a compile-time constant
//      cannot pass it.
//   2. The seed is STABLE and NONZERO within a process. Stability is what
//      makes a map's own lookups find their own keys after a grow; the draw is
//      cached in a runtime word and every backend's cache flag IS that word,
//      so a drawn zero would re-draw on every call — which is why the drawn
//      value is forced odd. Zero additionally means "unseeded" to core/map, so
//      a zero escaping the runtime would silently disable seeding.
//   3. ITERATION ORDER DOES NOT MOVE. This is what makes a per-run seed safe
//      to enable by default, and the usual objection to seeding a hash map.
//      core/map iterates the insertion-ordered entry ARRAY, never the bucket
//      table, so key order is seed-independent — and std/json's key-order
//      preservation rides on the same property. The test pins order equality
//      across runs whose seeds differ, so the two facts are checked together
//      rather than assumed to co-occur.
//
// Bucket layout itself is deliberately NOT asserted: it is unobservable from
// Fern by construction, which is property 3 restated. What a wrong seed would
// break is lookups, and the semantics program below is dense enough in grows,
// misses, deletes and re-inserts to catch that.

import (
	"os"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/ast"
)

// Full map semantics with string keys under a live seed: 300 inserts (several
// grows past the 75% load factor), every key read back, misses, overwrite in
// place, delete, and re-insert of the deleted keys. A seed that reached the
// lookup path but not the grow path — or vice versa — leaves entries in
// buckets their own probes cannot find, and every one of these steps would
// then fail.
//
// The delete section is deliberately SMALL (three keys, not a sweep) because
// repeated `without` on a grown map SEGVs on every compiled backend — #6227, a
// pre-existing fault reproduced on stock main and unrelated to seeding. A
// sweep here would be red for that reason instead of this one. Three deletes
// stays under #6227's threshold while still covering what seeding touches: the
// probe that finds the victim, the tombstone, and the rehash that relocates
// the swapped-in last entry, each of which must agree with the seed the map
// was built under.
const mapSeededSemanticsProg = `
import "core/map";
import "std/i32";

function main(): i32 {
    var m: Map[string, i32] = map_new(4);
    var i: i32 = 0;
    while (i < 300) {
        m = m.insert("key" + i.to_string(), i * 7);
        i = i + 1;
    }
    if (m.len() != 300) { return 1; }

    var j: i32 = 0;
    while (j < 300) {
        if (m.get_or("key" + j.to_string(), 0 - 1) != j * 7) { return 2; }
        if (!m.has("key" + j.to_string())) { return 3; }
        j = j + 1;
    }
    if (m.has("key300")) { return 4; }
    if (m.get_or("absent", 0 - 1) != 0 - 1) { return 5; }

    // Overwrite in place: len must not move, value must.
    m = m.insert("key5", 999);
    if (m.len() != 300) { return 6; }
    if (m.get_or("key5", 0) != 999) { return 7; }

    // Delete tombstones the bucket and swaps the last entry down, so both the
    // probe and the last-entry rehash must agree with the map's seed. Kept to
    // three keys by #6227 — see the comment above.
    var (ma, oka) = m.without("key0");
    if (!oka) { return 8; }
    var (mb, okb) = ma.without("key150");
    if (!okb) { return 9; }
    var (mc, okc) = mb.without("key299");
    if (!okc) { return 10; }
    m = mc;
    if (m.len() != 297) { return 11; }
    if (m.has("key0")) { return 12; }
    if (m.has("key150")) { return 13; }
    if (m.has("key299")) { return 14; }
    // The neighbours of the deleted keys must survive the backfill.
    if (m.get_or("key1", 0 - 1) != 7) { return 15; }
    if (m.get_or("key149", 0 - 1) != 1043) { return 16; }
    if (m.get_or("key298", 0 - 1) != 2086) { return 17; }

    // Re-insert: the freed slots must be reachable again under the same seed.
    m = m.insert("key0", 1000);
    m = m.insert("key150", 1001);
    if (m.len() != 299) { return 18; }
    if (m.get_or("key0", 0 - 1) != 1000) { return 19; }
    if (m.get_or("key150", 0 - 1) != 1001) { return 20; }

    return 42;
}
`

func TestMapSeededSemanticsInterp(t *testing.T) {
	if got := runInterpExit(t, mapSeededSemanticsProg); got != 42 {
		t.Fatalf("interp got %d, want 42", got)
	}
}

func TestMapSeededSemanticsX86_64(t *testing.T) {
	if _, got := compileAndRunX86_64(t, mapSeededSemanticsProg); got != 42 {
		t.Fatalf("x86-64 got %d, want 42", got)
	}
}

func TestMapSeededSemanticsWasm(t *testing.T) {
	if got := compileAndRunWasmbinMain(t, mapSeededSemanticsProg); got != 42 {
		t.Fatalf("wasm got %d, want 42", got)
	}
}

func TestMapSeededSemanticsArm64(t *testing.T) {
	if _, got := compileAndRunArm64(t, mapSeededSemanticsProg); got != 42 {
		t.Fatalf("arm64 got %d, want 42", got)
	}
}

// The seed is drawn once and reused: three calls in one process must agree,
// and none may be zero. Exit 42 on success so the interpreter can run it too.
const mapHashSeedStableProg = `
function main(): i32 {
    var a: i32 = __map_hash_seed();
    var b: i32 = __map_hash_seed();
    var c: i32 = __map_hash_seed();
    if (a == 0) { return 1; }
    if (b != a) { return 2; }
    if (c != a) { return 3; }
    return 42;
}
`

func TestMapHashSeedStableInterp(t *testing.T) {
	if got := runInterpExit(t, mapHashSeedStableProg); got != 42 {
		t.Fatalf("interp got %d, want 42", got)
	}
}

func TestMapHashSeedStableX86_64(t *testing.T) {
	if _, got := compileAndRunX86_64(t, mapHashSeedStableProg); got != 42 {
		t.Fatalf("x86-64 got %d, want 42", got)
	}
}

func TestMapHashSeedStableWasm(t *testing.T) {
	if got := compileAndRunWasmbinMain(t, mapHashSeedStableProg); got != 42 {
		t.Fatalf("wasm got %d, want 42", got)
	}
}

func TestMapHashSeedStableArm64(t *testing.T) {
	if _, got := compileAndRunArm64(t, mapHashSeedStableProg); got != 42 {
		t.Fatalf("arm64 got %d, want 42", got)
	}
}

// Prints the process's seed, then the map's keys in iteration order. The two
// halves are what properties 1 and 3 are checked against: across runs the seed
// line must MOVE and the keys line must NOT.
//
// Keys are inserted in an order that is not their sorted order, so "iteration
// order is insertion order" is distinguishable from "iteration order happens
// to be sorted".
const mapHashSeedReportProg = `
import "core/map";
import "std/i32";

function main(): i32 {
    print(__map_hash_seed().to_string());
    var m: Map[string, i32] = map_new(4);
    var i: i32 = 0;
    while (i < 40) {
        m = m.insert("k" + ((i * 17) % 40).to_string(), i);
        i = i + 1;
    }
    var line: string = "";
    for k in m.keys() {
        line = line + k + ",";
    }
    print(line);
    return 42;
}
`

// seedAndOrder splits mapHashSeedReportProg's two output lines.
func seedAndOrder(t *testing.T, out string) (seed int64, order string) {
	t.Helper()
	lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
	if len(lines) != 2 {
		t.Fatalf("expected 2 output lines, got %d: %q", len(lines), out)
	}
	v, err := strconv.ParseInt(strings.TrimSpace(lines[0]), 10, 64)
	if err != nil {
		t.Fatalf("first line is not a seed: %q (%v)", lines[0], err)
	}
	return v, lines[1]
}

// runsVaryTheSeed is the shared body of the per-backend cases. `run` compiles
// and executes the program once; calling it repeatedly gives independent
// processes.
//
// Three runs, requiring at least two DISTINCT seeds. Two runs would do for a
// live CSPRNG (a collision is 2^-32); the third is there so a single unlucky
// or replayed value cannot fail the build.
func runsVaryTheSeed(t *testing.T, run func(t *testing.T) string) {
	t.Helper()
	seeds := make([]int64, 0, 3)
	var firstOrder string
	for i := 0; i < 3; i++ {
		seed, order := seedAndOrder(t, run(t))
		if seed == 0 {
			t.Fatalf("run %d: seed is 0 — that is core/map's \"unseeded\" sentinel, "+
				"so every string map in this process hashes unseeded", i)
		}
		if i == 0 {
			firstOrder = order
		} else if order != firstOrder {
			t.Fatalf("run %d: iteration order changed with the seed\n first: %s\n  this: %s\n"+
				"core/map must iterate the insertion-ordered entry array, never the "+
				"bucket table — std/json's key-order preservation depends on it", i, firstOrder, order)
		}
		seeds = append(seeds, seed)
	}
	distinct := map[int64]bool{}
	for _, s := range seeds {
		distinct[s] = true
	}
	if len(distinct) < 2 {
		t.Fatalf("all %d runs drew the same seed %d — the seed is not per-process, "+
			"so it is as precomputable as no seed at all", len(seeds), seeds[0])
	}
	// Insertion order, not sorted order: k0 is inserted first (i=0 → (0*17)%40).
	if !strings.HasPrefix(firstOrder, "k0,k17,k34,") {
		t.Fatalf("iteration order is not insertion order: %s", firstOrder)
	}
}

func TestMapHashSeedVariesPerProcessX86_64(t *testing.T) {
	runsVaryTheSeed(t, func(t *testing.T) string {
		out, code := compileAndRunX86_64(t, mapHashSeedReportProg)
		if code != 42 {
			t.Fatalf("x86-64 exited %d, want 42", code)
		}
		return out
	})
}

func TestMapHashSeedVariesPerProcessArm64(t *testing.T) {
	runsVaryTheSeed(t, func(t *testing.T) string {
		out, code := compileAndRunArm64(t, mapHashSeedReportProg)
		if code != 42 {
			t.Fatalf("arm64 exited %d, want 42", code)
		}
		return out
	})
}

// The kv-buffer header size is spelled twice — once in Go as
// ast.MapHeaderBytes (which the generated __drop_map_* column walks and every
// backend's __fern_map_drop use) and once in Fern as __map_hdr_bytes (which
// allocates and indexes the buffer). They must agree exactly.
//
// This is not hypothetical: while the header was being widened for the seed,
// the Fern side moved to 24 while three Go sites still said 16. The symptom
// was a SEGV in the drop path on arm64 — whose 16-byte entry stride puts the
// misread furthest out — with nothing in the failure resembling a layout bug.
// A mismatch is worth catching as a one-line diff instead.
func TestMapHeaderBytesMatchesStdlib(t *testing.T) {
	src, err := os.ReadFile("../stdlib/core/map.fern")
	if err != nil {
		t.Fatalf("read core/map.fern: %v", err)
	}
	re := regexp.MustCompile(`(?s)function __map_hdr_bytes\(keyKind: i32\): i32 \{\s*return (\d+);`)
	m := re.FindSubmatch(src)
	if m == nil {
		t.Fatal("could not find __map_hdr_bytes's return value in core/map.fern — " +
			"if its shape changed, update this test rather than dropping it")
	}
	fern, err := strconv.Atoi(string(m[1]))
	if err != nil {
		t.Fatalf("__map_hdr_bytes returns a non-integer %q", m[1])
	}
	if fern != ast.MapHeaderBytes {
		t.Fatalf("map kv header disagrees: core/map.fern says %d, ast.MapHeaderBytes says %d. "+
			"Both spellings must change together — the Fern side allocates and indexes "+
			"the buffer, the Go side frees it and walks its entry column.",
			fern, ast.MapHeaderBytes)
	}
}
