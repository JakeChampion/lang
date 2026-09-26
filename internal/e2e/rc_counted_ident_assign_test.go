package e2e

import (
	"strconv"
	"strings"
	"testing"
)

// #9948 — `chunk = kept`, where `kept` is a borrowed alias whose inc was
// cancelled, left `chunk` inheriting kept's borrow taint. But the assignment
// retains kept, so chunk owns a count of its own: the taint only cost chunk
// its drops, including its own `[]` initialiser on the rounds that never
// reach the alias at all (50 rounds: allocs=250 frees=200 live_bytes=800).
//
// Each balanced case is held to the interpreter's exit code, a silent
// sanitizer and a balanced census. The escaped case is the direction the fix
// must not open: kept's struct is a map key, which the map holds uncounted,
// so chunk must stay tainted — freeing it through chunk is a use-after-free
// once the map is read back in the caller. That case leaks by design and is
// held to "no over-release" only.
const countedIdentAssignPrelude = `import "core/int";
import "std/i32";

function mk(i: i32): Option[u8[]] {
    var b: u8[] = [];
    var j: i32 = 0;
    while (j < 8) { b = b.append(((j + i) % 251) as u8); j = j + 1; }
    if (i % 5 == 0) { return None; }
    return Some(b);
}
`

var countedIdentAssignCases = []struct {
	name string
	src  string
}{
	{"cancelled_alias_copy", countedIdentAssignPrelude + `
function round(i: i32): i32 {
    var chunk: u8[] = [];
    var missing: boolean = false;
    var r: Option[u8[]] = mk(i);
    match (r) {
        Some(c) => { var kept: u8[] = c; chunk = kept; },
        None => { missing = true; },
    }
    if (missing) { return 0; }
    return chunk.len();
}
function main(): i32 {
    var t: i32 = 0;
    var i: i32 = 0;
    while (i < 50) { t = t + round(i); i = i + 1; }
    return t % 100;
}`},
	{"copy_handed_out", countedIdentAssignPrelude + `
function handed_out(i: i32): u8[] {
    var chunk: u8[] = [];
    var r: Option[u8[]] = mk(i);
    match (r) {
        Some(c) => { var kept: u8[] = c; chunk = kept; },
        None => {},
    }
    return chunk;
}
function main(): i32 {
    var t: i32 = 0;
    var i: i32 = 0;
    while (i < 50) {
        var b: u8[] = handed_out(i);
        for x in b { t = t + x as i32; }
        i = i + 1;
    }
    return t % 100;
}`},
	{"copy_pushed_past_the_loop", countedIdentAssignPrelude + `
function main(): i32 {
    var all: u8[][] = [];
    var i: i32 = 0;
    while (i < 20) {
        var chunk: u8[] = [];
        var r: Option[u8[]] = mk(i);
        match (r) {
            Some(c) => { var kept: u8[] = c; chunk = kept; },
            None => {},
        }
        all = all.append(chunk);
        i = i + 1;
    }
    var t: i32 = 0;
    for a in all { t = t + a.len(); }
    return t % 100;
}`},
	{"copy_then_reassigned", countedIdentAssignPrelude + `
function round(i: i32): i32 {
    var chunk: u8[] = [];
    var r: Option[u8[]] = mk(i);
    var t: i32 = 0;
    match (r) {
        Some(c) => {
            var kept: u8[] = c;
            chunk = kept;
            t = chunk.len();
            chunk = [1, 2, 3];
            t = t + kept.len();
        },
        None => {},
    }
    return t + chunk.len();
}
function main(): i32 {
    var t: i32 = 0;
    var i: i32 = 0;
    while (i < 50) { t = t + round(i); i = i + 1; }
    return t % 100;
}`},
	{"copy_of_a_borrowed_parameter", countedIdentAssignPrelude + `
@noinline
function from_param(p: u8[]): i32 {
    var kept: u8[] = p;
    var chunk: u8[] = [];
    chunk = kept;
    return chunk.len() + kept.len();
}
function main(): i32 {
    var t: i32 = 0;
    var i: i32 = 0;
    while (i < 50) {
        var p: u8[] = [1, 2, 3, 4];
        t = t + from_param(p) + p.len() + p[3] as i32;
        i = i + 1;
    }
    return t % 100;
}`},
}

const countedIdentAssignEscapedSrc = `import "core/int";
import "core/cmp";
import "core/map";
import "std/i32";
import "std/string";

@derive(cmp.Eq, cmp.Hash)
struct Key { a: i32, s: string }

@noinline
function build(i: i32): Map[Key, i32] {
    var m: Map[Key, i32] = map_new(4);
    var kept: Key = Key { a: i, s: i.to_string() + " is a key past the inline threshold" };
    m = m.insert(kept, 1);
    var chunk: Key = Key { a: 0, s: "" };
    chunk = kept;
    var t: i32 = chunk.s.len();
    return m.insert(Key { a: -1, s: "" }, t);
}

function main(): i32 {
    var t: i32 = 0;
    var i: i32 = 0;
    while (i < 20) {
        var m: Map[Key, i32] = build(i);
        var probe: Key = Key { a: i, s: i.to_string() + " is a key past the inline threshold" };
        match (m.get(probe)) {
            Some(v) => { t = t + v; },
            None => { t = t + 100; },
        }
        for k in m.keys() { t = t + k.s.len(); }
        i = i + 1;
    }
    return t % 100;
}`

func checkCountedIdentAssign(t *testing.T, stdout, stderr string, code, want int) {
	t.Helper()
	if code != want {
		t.Fatalf("exit = %d, want %d\nstdout: %s\nstderr: %s", code, want, stdout, stderr)
	}
	if strings.Contains(stderr, "fern-sanitizer:") {
		t.Errorf("sanitizer report:\n%s", stderr)
	}
	allocs, frees, live := parseLeakCheckLine(t, stderr)
	if allocs == 0 || allocs != frees || live != 0 {
		t.Errorf("census: allocs=%d frees=%d live_bytes=%d, want balanced / 0", allocs, frees, live)
	}
}

// checkNoOverRelease allows the sanitizer's leak verdict and nothing else.
func checkNoOverRelease(t *testing.T, stdout, stderr string, code, want int) {
	t.Helper()
	if code != want {
		t.Fatalf("exit = %d, want %d\nstdout: %s\nstderr: %s", code, want, stdout, stderr)
	}
	for _, line := range strings.Split(stderr, "\n") {
		if strings.Contains(line, "fern-sanitizer:") && !strings.Contains(line, "fern-sanitizer: leak ") {
			t.Errorf("sanitizer report:\n%s", stderr)
		}
	}
}

func runCountedIdentAssign(t *testing.T, run func(*testing.T, string) (string, string, int)) {
	for _, tc := range countedIdentAssignCases {
		t.Run(tc.name, func(t *testing.T) {
			want := runInterpByte(t, tc.src)
			stdout, stderr, code := run(t, tc.src)
			checkCountedIdentAssign(t, stdout, stderr, code, want)
		})
	}
	t.Run("escaped_source_keeps_its_taint", func(t *testing.T) {
		want := runInterpByte(t, countedIdentAssignEscapedSrc)
		stdout, stderr, code := run(t, countedIdentAssignEscapedSrc)
		checkNoOverRelease(t, stdout, stderr, code, want)
	})
}

func TestX86_64CountedIdentAssignOwnsItsCount(t *testing.T) {
	runCountedIdentAssign(t, runSanitizeX86_64)
}

func TestArm64CountedIdentAssignOwnsItsCount(t *testing.T) {
	runCountedIdentAssign(t, runSanitizeArm64)
}

func TestWASMCountedIdentAssignOwnsItsCount(t *testing.T) {
	for _, tc := range countedIdentAssignCases {
		t.Run(tc.name, func(t *testing.T) {
			want := runInterpByte(t, tc.src)
			// The census build prints main's result rather than exiting with it.
			stdout, stderr, code := runLeakCheckWasm(t, tc.src, false)
			if code != 0 || strings.TrimSpace(stdout) != strconv.Itoa(want) {
				t.Fatalf("exit = %d, stdout = %q, want main's result %d\nstderr: %s", code, stdout, want, stderr)
			}
			m := wasmLeakCheckLineRe.FindStringSubmatch(stderr)
			if m == nil {
				t.Fatalf("no leakcheck report on stderr: %q", stderr)
			}
			if m[1] == "0" || m[1] != m[2] || m[3] != "0" {
				t.Errorf("census: allocs=%s frees=%s live_bytes=%s, want balanced / 0", m[1], m[2], m[3])
			}
		})
	}
}
