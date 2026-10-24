package e2e

import (
	"testing"
)

const arenaCheckpointSrc = `
struct Node { name: string, kids: string[] }
function mk(k: i32): Node[] {
    let out: Node[] = [];
    let i: i32 = 0;
    while (i < k) {
        let ks: string[] = [];
        let j: i32 = 0;
        while (j < 8) { ks = ks.append("kid"); j = j + 1; }
        out = out.append(Node { name: "n", kids: ks });
        i = i + 1;
    }
    return out;
}
function window(k: i32): i32 {
    let a: Node[] = mk(k);
    let sum: i32 = 0;
    let i: i32 = 0;
    while (i < a.len()) { sum = sum + a[i].kids.len(); i = i + 1; }
    return sum;
}
function main(): i32 {
    // Warm the runtime so the cursor is seeded and b0 is a real address:
    // a mark of 0 means "no checkpoint" and release_to would ignore it.
    if (window(4) != 32) { return 80; }
    let b0: i32 = (__heap_bump_bytes() as i32);
    let m: i64 = __heap_mark();
    if (window(400) != 3200) { return 81; }
    let b1: i32 = (__heap_bump_bytes() as i32);
    __heap_release_to(m);
    let b2: i32 = (__heap_bump_bytes() as i32);
    if (b1 <= b0) { return 82; }
    if (b2 != b0) { return 83; }
    return 0;
}`

const arenaCheckpointCyclesSrc = `
struct Node { name: string, kids: string[] }
function mk(k: i32): Node[] {
    let out: Node[] = [];
    let i: i32 = 0;
    while (i < k) {
        let ks: string[] = [];
        let j: i32 = 0;
        while (j < 8) { ks = ks.append("kid"); j = j + 1; }
        out = out.append(Node { name: "n", kids: ks });
        i = i + 1;
    }
    return out;
}
function window(k: i32): i32 {
    let a: Node[] = mk(k);
    let sum: i32 = 0;
    let i: i32 = 0;
    while (i < a.len()) { sum = sum + a[i].kids.len(); i = i + 1; }
    return sum;
}
function main(): i32 {
    if (window(4) != 32) { return 80; }
    let b0: i32 = (__heap_bump_bytes() as i32);
    let c: i32 = 0;
    while (c < 300) {
        let m: i64 = __heap_mark();
        if (window(200) != 1600) { return 81; }
        __heap_release_to(m);
        c = c + 1;
    }
    // 300 windows of ~200 nodes each, and the cursor has not moved at all.
    if ((__heap_bump_bytes() as i32) != b0) { return 82; }
    // The arena is still usable after the releases.
    if (window(50) != 400) { return 83; }
    return 0;
}`

func runArenaCheckpointChecks(t *testing.T, run func(*testing.T, string) int) {
	t.Helper()
	for _, c := range []struct {
		name string
		src  string
	}{
		{"rewind", arenaCheckpointSrc},
		{"cycles", arenaCheckpointCyclesSrc},
	} {
		t.Run(c.name, func(t *testing.T) {
			switch got := run(t, c.src); got {
			case 0:
			case 80, 81:
				t.Fatalf("value-incorrect run (exit %d): the window computed the wrong result, so the checkpoint corrupted live data", got)
			case 82:
				t.Fatalf("exit 82: the cursor did not behave as expected (vacuous window, or a release that did not rewind)")
			case 83:
				t.Fatalf("exit 83: the release did not rewind the cursor exactly, or the arena was unusable afterwards")
			default:
				t.Fatalf("unexpected exit %d", got)
			}
		})
	}
}

func TestX86_64ArenaCheckpoint(t *testing.T) {
	runArenaCheckpointChecks(t, mustRunX86_64FreeOn)
}

func TestArm64ArenaCheckpoint(t *testing.T) {
	runArenaCheckpointChecks(t, mustRunArm64FreeOn)
}
