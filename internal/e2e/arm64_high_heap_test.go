// High-heap arm64 gate.
//
// arm64-darwin runs with every heap pointer above 4 GiB: macOS ignores the
// arena's mmap address hint and relocates the mapping, while Linux honours
// the hint and puts the arena at 256 MiB. A load, store or compare that
// handles a heap pointer 32 bits wide is therefore correct on every lane the
// project can run cheaply and wrong only on Apple hardware — which is how a
// 4-byte deref of the Map handle in the IR's wide keys()/values() builders
// reached main and crashed the `map_keys_values_header_churn_free` rc-corpus
// case on the macos-15 runner.
//
// arm64codegen.Options.HighHeapProbe raises the hint to 8 GiB, and
// qemu-aarch64 honours it, so the same address regime is reachable here. The
// tests below re-run the map half of the rc corpus plus a set of shapes that
// round-trip a pointer through memory, under that hint. They are ordinary
// `TestArm64…` tests: the default `go test ./internal/e2e -run TestArm64`
// selection runs them.
package e2e

import (
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/checker"
	arm64codegen "github.com/jakechampion/lang/internal/codegen/arm64"
	"github.com/jakechampion/lang/internal/constfold"
	"github.com/jakechampion/lang/internal/e2eharness"
	"github.com/jakechampion/lang/internal/modload"
)

var compileAndRunArm64HighHeap = e2eharness.CompileAndRunArm64HighHeap

// TestArm64HighHeapMapCorpus re-runs every map-shaped rc-corpus case with the
// arena above 4 GiB. The Map runtime is the densest pointer-round-trip
// surface in the language — handle → buffer → entries → cell → value — so it
// is where a truncation shows first, and selecting the corpus by name means a
// map case added later joins this gate without anyone remembering to.
func TestArm64HighHeapMapCorpus(t *testing.T) {
	ran := 0
	for _, c := range rcCorpus {
		if !strings.Contains(c.name, "map") {
			continue
		}
		ran++
		t.Run(c.name, func(t *testing.T) {
			if _, code := compileAndRunArm64HighHeap(t, c.src); code != 0 {
				t.Errorf("%s at high heap: got exit %d, want 0 (pointer truncated above 4 GiB?)", c.name, code)
			}
		})
	}
	// A rename that empties the selection would leave a green test proving
	// nothing.
	if ran < 20 {
		t.Fatalf("high-heap map selection matched only %d corpus cases — the name filter has gone stale", ran)
	}
}

// highHeapRoundTrips are pointer-round-trip shapes outside the map runtime:
// each stores a heap pointer into memory and reads it back, so a narrow
// slot / load / store loses the high half and the program either faults or
// returns the wrong value. Every case returns 0 on success, on either heap —
// TestArm64HighHeapRoundTripControl runs the same set at the default hint so
// a high-heap failure is attributable to the address regime.
//
// Strings are built by concatenation with a long literal prefix: a short
// string stays inline (no buffer), and a literal alone lives in .rodata,
// which the arena hint does not move.
var highHeapRoundTrips = []struct {
	name string
	src  string
}{
	{
		// Wide-key and wide-value column snapshots — the shape that
		// crashed. keys()/values() on Map[i64, i64] dereference the map
		// handle to reach the entries table; the value column follows a
		// cell pointer on top of that.
		name: "map_wide_column_snapshot",
		src: `
import "core/int";
import "core/map";
function main(): i32 {
    var m: Map[i64, i64] = map_new(8);
    m = m.insert(5, 50);
    m = m.insert(6, 60);
    m = m.insert(7, 70);
    var ks = m.keys();
    var vs = m.values();
    if (ks.len() != 3) { return 1; }
    if (vs.len() != 3) { return 2; }
    var ksum: i64 = ks[0] + ks[1] + ks[2];
    var vsum: i64 = vs[0] + vs[1] + vs[2];
    if (ksum != 18) { return 3; }
    if (vsum != 180) { return 4; }
    return __rc_underflow_count();
}`,
	},
	{
		// f64 travels the same wide-column path as i64 — a truncated
		// cell pointer is a wild read either way.
		name: "map_f64_column_snapshot",
		src: `
import "core/int";
import "core/map";
function main(): i32 {
    var m: Map[i64, f64] = map_new(8);
    m = m.insert(1, 1.5);
    m = m.insert(2, 2.5);
    var vs = m.values();
    if (vs.len() != 2) { return 1; }
    if (vs[0] + vs[1] != 4.0) { return 2; }
    var ks = m.keys();
    if (ks[0] + ks[1] != 3) { return 3; }
    return __rc_underflow_count();
}`,
	},
	{
		// Heap strings into and out of a map: keys and values are built at
		// runtime and long enough to need a buffer, so nothing the map
		// holds lives in .rodata below 4 GiB.
		name: "map_heap_string_kv",
		src: `
import "core/int";
import "core/map";
function main(): i32 {
    var sfx: string[] = ["a", "b", "c", "d", "e", "f", "g", "h"];
    var m: Map[string, string] = map_new(8);
    var i: i32 = 0;
    while (i < 8) {
        m = m.insert("key-prefix-padpadpadpad-" + sfx[i], "value-payload-padpadpad-" + sfx[i]);
        i = i + 1;
    }
    if (m.len() != 8) { return 1; }
    if (m.get_or("key-prefix-padpadpadpad-" + "d", "") != "value-payload-padpadpad-d") { return 2; }
    var ks = m.keys();
    var vs = m.values();
    if (ks.len() != 8 || vs.len() != 8) { return 3; }
    var total: i32 = 0;
    for (k, v) in m { total = total + k.len() + v.len(); }
    if (total != 8 * 50) { return 4; }
    return __rc_underflow_count();
}`,
	},
	{
		// Array of heap strings, grown well past its initial capacity so
		// the buffer is reallocated and every element pointer is copied.
		name: "string_array_grow_copy",
		src: `
import "core/int";
function main(): i32 {
    var sfx: string[] = ["a", "b", "c", "d", "e", "f", "g", "h"];
    var a: string[] = [];
    var r: i32 = 0;
    while (r < 8) {
        var j: i32 = 0;
        while (j < 8) { a = a.append("elem-payload-padpadpadpad-" + sfx[j]); j = j + 1; }
        r = r + 1;
    }
    if (a.len() != 64) { return 1; }
    if (a[0] != "elem-payload-padpadpadpad-a") { return 2; }
    if (a[63] != "elem-payload-padpadpadpad-h") { return 3; }
    var total: i32 = 0;
    var k: i32 = 0;
    while (k < 64) { total = total + a[k].len(); k = k + 1; }
    if (total != 64 * 27) { return 4; }
    return __rc_underflow_count();
}`,
	},
	{
		// Struct fields and enum payloads hold pointers in slots the
		// layout sizes by type; a nested read walks three of them.
		name: "struct_enum_pointer_slots",
		src: `
import "core/int";
struct Inner { tag: string, xs: i32[] }
struct Outer { name: string, inner: Inner }
enum Box { Empty, Full(Outer) }
function main(): i32 {
    var inner: Inner = Inner { tag: "inner-tag-padpadpadpad" + "-7", xs: [1, 2, 3] };
    var o: Outer = Outer { name: "outer-name-padpadpadpad" + "-9", inner: inner };
    var b: Box = Full(o);
    match (b) {
        Empty => { return 1; },
        Full(got) => {
            if (got.name != "outer-name-padpadpadpad-9") { return 2; }
            if (got.inner.tag != "inner-tag-padpadpadpad-7") { return 3; }
            if (got.inner.xs.len() != 3) { return 4; }
            if (got.inner.xs[2] != 3) { return 5; }
        }
    }
    return __rc_underflow_count();
}`,
	},
	{
		// A closure environment is a heap box of captured pointers, read
		// back through the env pointer on every call.
		name: "closure_env_pointer_capture",
		src: `
import "core/int";
function mk(prefix: string, xs: i32[]): (i32) => i32 {
    return function(i: i32): i32 { return prefix.len() + xs[i]; };
}
function main(): i32 {
    var f: (i32) => i32 = mk("captured-prefix-padpadpad" + "!", [10, 20, 30]);
    if (f(0) != 36) { return 1; }
    if (f(2) != 56) { return 2; }
    return __rc_underflow_count();
}`,
	},
	{
		// Nested arrays: the outer buffer holds pointers to inner buffers,
		// which hold pointers to heap strings.
		name: "nested_array_pointer_columns",
		src: `
import "core/int";
function main(): i32 {
    var sfx: string[] = ["a", "b", "c", "d"];
    var rows: string[][] = [];
    var i: i32 = 0;
    while (i < 12) {
        var row: string[] = [];
        var j: i32 = 0;
        while (j < 4) { row = row.append("cell-payload-padpadpad-" + sfx[j]); j = j + 1; }
        rows = rows.append(row);
        i = i + 1;
    }
    if (rows.len() != 12) { return 1; }
    if (rows[11][3] != "cell-payload-padpadpad-d") { return 2; }
    var total: i32 = 0;
    var k: i32 = 0;
    while (k < 12) { total = total + rows[k].len(); k = k + 1; }
    if (total != 48) { return 3; }
    return __rc_underflow_count();
}`,
	},
	{
		// Sustained churn: hundreds of maps built and dropped, so the
		// bump allocator hands out many distinct addresses and the
		// CoW / overwrite identity tests (pointer compares) see live
		// pointers that are close in the low half and differ higher up.
		name: "map_churn_across_wide_span",
		src: `
import "core/int";
import "core/map";
function build(): Map[i32, string] {
    var sfx: string[] = ["a", "b", "c", "d", "e", "f", "g", "h"];
    var m: Map[i32, string] = map_new(16);
    var i: i32 = 0;
    while (i < 8) { m = m.insert(i, "payload-padpadpadpadpad-" + sfx[i]); i = i + 1; }
    return m;
}
function main(): i32 {
    var total: i32 = 0;
    var r: i32 = 0;
    while (r < 200) {
        var m: Map[i32, string] = build();
        total = total + m.len();
        r = r + 1;
    }
    if (total != 1600) { return 1; }
    return __rc_underflow_count();
}`,
	},
}

// TestArm64HighHeapPointerRoundTrip runs the shapes above with the arena
// above 4 GiB. Each stores a heap pointer into memory and reads it back, so a
// slot / load / store / compare that is 32 bits wide either faults or returns
// a wrong answer here while staying green on the 256 MiB heap.
func TestArm64HighHeapPointerRoundTrip(t *testing.T) {
	for _, c := range highHeapRoundTrips {
		t.Run(c.name, func(t *testing.T) {
			if _, code := compileAndRunArm64HighHeap(t, c.src); code != 0 {
				t.Errorf("%s at high heap: got exit %d, want 0 (pointer truncated above 4 GiB?)", c.name, code)
			}
		})
	}
}

// TestArm64HighHeapRoundTripControl is the same set at the DEFAULT hint. It
// is what makes a failure above attributable: green here and red there means
// the address regime, not the program.
func TestArm64HighHeapRoundTripControl(t *testing.T) {
	for _, c := range highHeapRoundTrips {
		t.Run(c.name, func(t *testing.T) {
			if _, code := compileAndRunArm64(t, c.src); code != 0 {
				t.Errorf("%s at normal heap: got exit %d, want 0", c.name, code)
			}
		})
	}
}

// TestArm64HighHeapProbeRaisesTheHint pins the knob itself. Without it the
// two runs above would be the same run twice and the gate would be vacuous,
// with nothing to say so.
func TestArm64HighHeapProbeRaisesTheHint(t *testing.T) {
	const src = `function main(): i32 { var a: i32[] = [1, 2, 3]; return a[0] - 1; }`
	prog, _, err := modload.LoadSource(src)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if err := constfold.Fold(prog, nil); err != nil {
		t.Fatalf("constfold: %v", err)
	}
	info, err := checker.Check(prog)
	if err != nil {
		t.Fatalf("check: %v", err)
	}

	plain, err := arm64codegen.EmitWithOptions(prog, info, arm64codegen.Options{})
	if err != nil {
		t.Fatalf("emit (default): %v", err)
	}
	high, err := arm64codegen.EmitWithOptions(prog, info, arm64codegen.Options{HighHeapProbe: true})
	if err != nil {
		t.Fatalf("emit (probe): %v", err)
	}
	// 1 << 28 = 0x1000_0000 (256 MiB); 32 << 28 = 0x2_0000_0000 (8 GiB).
	if !strings.Contains(plain, "mov x13, #1\n") {
		t.Errorf("default emit does not carry the 0x10000000 arena hint")
	}
	if strings.Contains(plain, "mov x13, #32\n") {
		t.Errorf("default emit carries the high-heap hint")
	}
	if !strings.Contains(high, "mov x13, #32\n") {
		t.Errorf("HighHeapProbe emit does not carry the 8 GiB arena hint")
	}
	if strings.Contains(high, "lsl x0, x13, #28") == false {
		t.Errorf("HighHeapProbe emit lost the hint shift the base feeds")
	}
}
