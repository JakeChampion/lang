package e2e

import (
	"strconv"
	"strings"
	"testing"
)

const mapSeededSemanticsProg = `
import "core/map";
import "std/i32";

function main(): i32 {
    let m: Map[string, i32] = map_new(4);
    let i: i32 = 0;
    while (i < 300) {
        m = m.insert("key" + i.to_string(), i * 7);
        i = i + 1;
    }
    if (m.len() != 300) { return 1; }

    let j: i32 = 0;
    while (j < 300) {
        if (m.get_or("key" + j.to_string(), 0 - 1) != j * 7) { return 2; }
        if (!m.contains("key" + j.to_string())) { return 3; }
        j = j + 1;
    }
    if (m.contains("key300")) { return 4; }
    if (m.get_or("absent", 0 - 1) != 0 - 1) { return 5; }

    // Overwrite in place: len must not move, value must.
    m = m.insert("key5", 999);
    if (m.len() != 300) { return 6; }
    if (m.get_or("key5", 0) != 999) { return 7; }

    // Delete tombstones the bucket and swaps the last entry down, so both the
    // probe and the last-entry rehash must agree with the map's seed.
    let d: i32 = 0;
    while (d < 60) {
        let okd: boolean = m.contains("key" + d.to_string());
        let md = m.remove("key" + d.to_string());
        if (!okd) { return 8; }
        m = md;
        d = d + 1;
    }
    if (m.len() != 240) { return 9; }
    let g: i32 = 0;
    while (g < 60) {
        if (m.contains("key" + g.to_string())) { return 10; }
        g = g + 1;
    }
    // Everything the backfill moved must still be reachable.
    let s: i32 = 60;
    while (s < 300) {
        if (m.get_or("key" + s.to_string(), 0 - 1) != s * 7) { return 11; }
        s = s + 1;
    }
    if (m.get_or("key5", 0 - 1) != 0 - 1) { return 12; }

    // Re-insert: the freed slots must be reachable again under the same seed.
    m = m.insert("key0", 1000);
    m = m.insert("key30", 1001);
    if (m.len() != 242) { return 13; }
    if (m.get_or("key0", 0 - 1) != 1000) { return 14; }
    if (m.get_or("key30", 0 - 1) != 1001) { return 15; }

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

const mapHashSeedStableProg = `
function main(): i32 {
    let a: i32 = __map_hash_seed();
    let b: i32 = __map_hash_seed();
    let c: i32 = __map_hash_seed();
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

const mapHashSeedReportProg = `
import "core/map";
import "std/i32";

function main(): i32 {
    print(__map_hash_seed().to_string());
    let m: Map[string, i32] = map_new(4);
    let i: i32 = 0;
    while (i < 40) {
        m = m.insert("k" + ((i * 17) % 40).to_string(), i);
        i = i + 1;
    }
    let line: string = "";
    for k in m.keys() {
        line = line + k + ",";
    }
    print(line);
    return 42;
}
`

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
