package e2ecompiler

import "testing"

// mapValueAliasRetainIRCases pin the retain a map insert owes an ALIASED array
// VALUE on the register backends (#6880).
//
// The register __fern_map_set stores the value pointer with no rc-inc of its
// own, while the exit dec-sweep releases EVERY array slot unconditionally. So
// `let a = [...]; m.insert(k, a);` left the map's value column naming a buffer
// the sweep freed, and the next allocation of that size class handed the block
// out again underneath the map — a read of the value then sees the recycled
// block's contents, and nothing reports an over-release because the map's alias
// was never counted at all. The lowering now records the verdict (op_map_set's
// vretain bit) and both register backends retain, mirroring the native oracle's
// emitMapSetValueRetain: alias shapes only, arrays only — a fresh value
// transfers its sole rc=1 to the map and retaining one would leak.
//
// Each program forces the recycle itself (a same-size-class array literal
// allocated between the insert and the read) rather than relying on a size
// class that happens to collide: as found, the `Map[string, string[]]` column
// in url_codec only misread once __fern_map_get's Option box moved to a 32-byte
// class, and reads clean at 40. Exit 0 is correct; each nonzero code names the
// check that failed.
//
// The x86-64 and arm64 legs are the ones that fail without the retain (three of
// the four cases each, the fourth being the fresh-value control). The wasm leg
// is a PARITY gate, not a failing-before one: $__fern_map_set already retained
// every `vis` value it was not told to consume, which is the divergence this
// closes.
var mapValueAliasRetainIRCases = []struct {
	name string
	src  string
}{
	// The reduced #6880 shape: a helper builds the value array and returns the
	// map, so the helper's exit sweep is what freed the stored buffer.
	{"helper-insert", `import "core/map";
function put(m: Map[string, i32[]], k: string, a: i32, b: i32): Map[string, i32[]] {
    let arr: i32[] = [a, b];
    return m.insert(k, arr);
}

function main(): i32 {
    let m: Map[string, i32[]] = Map {};
    m = put(m, "k", 3, 4);
    let junk: i32[] = [7, 9];
    match (m.get("k")) {
        Some(v) => {
            if (v.len() != 2) { return 2; }
            if (v[0] != 3) { return 3; }
            if (v[1] != 4) { return 4; }
        },
        None => { return 1; }
    }
    if (junk[0] != 7) { return 5; }
    return 0;
}
`},
	// Loop-scoped value locals — std/url's query_parse shape, where each round's
	// array is freed at the iteration's sweep and the next round recycles it.
	{"loop-scoped-value", `import "core/map";
function main(): i32 {
    let m: Map[i32, i32[]] = Map {};
    let i: i32 = 0;
    while (i < 6) {
        let arr: i32[] = [i, i + 100];
        m = m.insert(i, arr);
        i = i + 1;
    }
    let j: i32 = 0;
    while (j < 6) {
        match (m.get(j)) {
            Some(v) => {
                if (v.len() != 2) { return 2; }
                if (v[0] != j) { return 3; }
                if (v[1] != j + 100) { return 4; }
            },
            None => { return 1; }
        }
        j = j + 1;
    }
    return 0;
}
`},
	// The reported column type: Map[string, string[]].
	{"string-array-value", `import "core/map";
function put(m: Map[string, string[]], k: string, v: string): Map[string, string[]] {
    let a: string[] = [v];
    return m.insert(k, a);
}

function main(): i32 {
    let m: Map[string, string[]] = Map {};
    m = put(m, "k", "hello");
    let junk: string[] = ["zz"];
    match (m.get("k")) {
        Some(v) => {
            if (v.len() != 1) { return 2; }
            if (v[0] != "hello") { return 3; }
        },
        None => { return 1; }
    }
    if (junk[0] != "zz") { return 4; }
    return 0;
}
`},
	// Control: a FRESH value literal takes no retain — its sole rc=1 moves into
	// the map — and must still read back correctly. Passes either side of the
	// fix, which is its job.
	{"fresh-value-control", `import "core/map";
function main(): i32 {
    let m: Map[i32, i32[]] = Map {};
    m = m.insert(1, [3, 4]);
    let junk: i32[] = [7, 9];
    match (m.get(1)) {
        Some(v) => {
            if (v[0] != 3) { return 2; }
            if (v[1] != 4) { return 3; }
        },
        None => { return 1; }
    }
    if (junk[0] != 7) { return 4; }
    return 0;
}
`},
}

// TestSelfHostMapValueAliasRetainIRX86_64 runs each case through the self-hosted
// CLI for x86-64.
func TestSelfHostMapValueAliasRetainIRX86_64(t *testing.T) {
	cli := newStrictCLI(t)
	for _, tc := range mapValueAliasRetainIRCases {
		t.Run(tc.name, func(t *testing.T) {
			if code, _ := cli.runX86(t, cli.emit(t, "x86-64-linux", tc.src)); code != 0 {
				t.Errorf("%s exited %d, want %d — the map's value column read back wrong", tc.name, code, 0)
			}
		})
	}
}

// TestSelfHostMapValueAliasRetainIRArm64 is the arm64 leg: same programs, the
// arm64 map_set emission, run under qemu.
func TestSelfHostMapValueAliasRetainIRArm64(t *testing.T) {
	gcc, qemu := arm64Tooling(t)
	cli := newStrictCLI(t)
	for _, tc := range mapValueAliasRetainIRCases {
		t.Run(tc.name, func(t *testing.T) {
			if code, _ := runArm64(t, gcc, qemu, cli.emit(t, "arm64-linux", tc.src)); code != 0 {
				t.Errorf("%s exited %d, want %d — the map's value column read back wrong", tc.name, code, 0)
			}
		})
	}
}

// TestSelfHostMapValueAliasRetainIRWasm is the parity leg: $__fern_map_set has
// always retained a `vis` value, so these pass either side of the fix and pin
// that the register backends now agree with it.
func TestSelfHostMapValueAliasRetainIRWasm(t *testing.T) {
	cli := newStrictCLI(t)
	for _, tc := range mapValueAliasRetainIRCases {
		t.Run(tc.name, func(t *testing.T) {
			if code, _ := runWasm(t, cli.emit(t, "wasm32-wasi", tc.src)); code != 0 {
				t.Errorf("%s exited %d, want %d — the map's value column read back wrong", tc.name, code, 0)
			}
		})
	}
}
