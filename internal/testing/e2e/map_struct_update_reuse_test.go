package e2e

import "testing"

const mapStructUpdateReuseProg = `
import "core/map";
import "std/i32";

struct Seen { m: Map[string, i32], hits: i32 }

function step(s0: Seen, key: string): Seen {
    let s: Seen = s0;
    match (s.m.get(key)) {
        Some(_) => {
            return Seen { ...s, hits: s.hits + 1 };
        },
        None => {
            s = Seen { ...s, m: s.m.insert(key, 1) };
        }
    }
    return s;
}

function main(): i32 {
    let m: Map[string, i32] = map_new(16);
    let k: Seen = Seen { m: m, hits: 0 };
    let i: i32 = 0;
    while (i < 24) {
        k = step(k, "key-that-heap-allocates-" + i.to_string());
        if (k.m.len() != i + 1) { return 1; }
        i = i + 1;
    }
    // Every key is still there after the last rebind, and every one of them
    // is a hit the second time round.
    if (k.m.len() != 24) { return 2; }
    let j: i32 = 0;
    while (j < 24) {
        k = step(k, "key-that-heap-allocates-" + j.to_string());
        j = j + 1;
    }
    if (k.hits != 24) { return 3; }
    if (k.m.len() != 24) { return 4; }
    return 42;
}
`

func TestMapStructUpdateReuseInterp(t *testing.T) {
	if got := runInterpExit(t, mapStructUpdateReuseProg); got != 42 {
		t.Fatalf("interp got %d, want 42", got)
	}
}

func TestMapStructUpdateReuseX86_64(t *testing.T) {
	if _, got := compileAndRunX86_64(t, mapStructUpdateReuseProg); got != 42 {
		t.Fatalf("x86-64 got %d, want 42", got)
	}
}

func TestMapStructUpdateReuseWasm(t *testing.T) {
	if got := compileAndRunWasmbinMain(t, mapStructUpdateReuseProg); got != 42 {
		t.Fatalf("wasm got %d, want 42", got)
	}
}

func TestMapStructUpdateReuseArm64(t *testing.T) {
	if _, got := compileAndRunArm64(t, mapStructUpdateReuseProg); got != 42 {
		t.Fatalf("arm64 got %d, want 42", got)
	}
}

const mapStructUpdateReuseRcProg = `
import "core/map";
import "std/i32";

struct Seen { m: Map[string, i32], hits: i32 }

function put(s0: Seen, key: string): Seen {
    let s: Seen = s0;
    return Seen { ...s, m: s.m.insert(key, 1) };
}

function main(): i32 {
    let m: Map[string, i32] = map_new(8);
    let k: Seen = Seen { m: m, hits: 0 };
    let i: i32 = 0;
    while (i < 32) {
        k = put(k, "key-that-heap-allocates-" + i.to_string());
        i = i + 1;
    }
    if (k.m.len() != 32) { return 1; }
    return 42 + __rc_underflow_count();
}
`

func TestMapStructUpdateReuseNoUnderflowX86_64(t *testing.T) {
	if _, got := compileAndRunX86_64(t, mapStructUpdateReuseRcProg); got != 42 {
		t.Fatalf("x86-64 got %d, want 42 (%d rc underflows)", got, got-42)
	}
}

func TestMapStructUpdateReuseNoUnderflowWasm(t *testing.T) {
	if got := compileAndRunWasmbinMain(t, mapStructUpdateReuseRcProg); got != 42 {
		t.Fatalf("wasm got %d, want 42 (%d rc underflows)", got, got-42)
	}
}

func TestMapStructUpdateReuseNoUnderflowArm64(t *testing.T) {
	if _, got := compileAndRunArm64(t, mapStructUpdateReuseRcProg); got != 42 {
		t.Fatalf("arm64 got %d, want 42 (%d rc underflows)", got, got-42)
	}
}
