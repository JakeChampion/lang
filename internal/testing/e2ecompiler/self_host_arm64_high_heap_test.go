package e2ecompiler

import (
	"os"
	"strings"
	"testing"
)

// The self-host half of the arm64 high-heap gate (internal/testing/e2e/arm64_high_heap_test.go).
//
// Linux honours the arena's mmap address hint, so every self-host arm64 lane
// runs with heap pointers below 4 GiB; XNU ignores the hint and maps the arena
// above the 4 GiB __PAGEZERO, so on arm64-darwin every heap pointer has a
// non-zero high half from the first allocation. A pointer the emitted code
// handles 32 bits wide — a `w` load of a handle, a 32-bit compare of two
// addresses, an offset computed then sign-extended — is therefore green on
// every Linux lane and wrong only on Apple hardware. FERN_HIGH_HEAP=1 makes
// the self-host arm64 emitter raise the hint to 8 GiB, and qemu-aarch64
// honours the raised hint, so the regime is reachable here.
//
// The shapes are the e2e gate's minus the two Map[i64, _] cases, which the
// self-host refuses to lower (#10005: a map key wider than 4 bytes has no
// column). Each stores heap pointers into memory and reads them back, and
// returns 0 on success on either heap.
var selfHostHighHeapRoundTrips = []struct {
	name string
	src  string
}{
	{
		name: "map_heap_string_kv",
		src: `
import "core/int";
import "core/map";
function main(): i32 {
    let sfx: string[] = ["a", "b", "c", "d", "e", "f", "g", "h"];
    let m: Map[string, string] = map_new(8);
    let i: i32 = 0;
    while (i < 8) {
        m = m.insert("key-prefix-padpadpadpad-" + sfx[i], "value-payload-padpadpad-" + sfx[i]);
        i = i + 1;
    }
    if (m.len() != 8) { return 1; }
    if (m.get_or("key-prefix-padpadpadpad-" + "d", "") != "value-payload-padpadpad-d") { return 2; }
    let ks = m.keys();
    let vs = m.values();
    if (ks.len() != 8 || vs.len() != 8) { return 3; }
    let total: i32 = 0;
    for (k, v) in m { total = total + k.len() + v.len(); }
    if (total != 8 * 50) { return 4; }
    return __rc_underflow_count();
}`,
	},
	{
		name: "string_array_grow_copy",
		src: `
import "core/int";
function main(): i32 {
    let sfx: string[] = ["a", "b", "c", "d", "e", "f", "g", "h"];
    let a: string[] = [];
    let r: i32 = 0;
    while (r < 8) {
        let j: i32 = 0;
        while (j < 8) { a = a.append("elem-payload-padpadpadpad-" + sfx[j]); j = j + 1; }
        r = r + 1;
    }
    if (a.len() != 64) { return 1; }
    if (a[0] != "elem-payload-padpadpadpad-a") { return 2; }
    if (a[63] != "elem-payload-padpadpadpad-h") { return 3; }
    let total: i32 = 0;
    let k: i32 = 0;
    while (k < 64) { total = total + a[k].len(); k = k + 1; }
    if (total != 64 * 27) { return 4; }
    return __rc_underflow_count();
}`,
	},
	{
		name: "struct_enum_pointer_slots",
		src: `
import "core/int";
struct Inner { tag: string, xs: i32[] }
struct Outer { name: string, inner: Inner }
enum Box { Empty, Full(Outer) }
function main(): i32 {
    let inner: Inner = Inner { tag: "inner-tag-padpadpadpad" + "-7", xs: [1, 2, 3] };
    let o: Outer = Outer { name: "outer-name-padpadpadpad" + "-9", inner: inner };
    let b: Box = Full(o);
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
		name: "closure_env_pointer_capture",
		src: `
import "core/int";
function mk(prefix: string, xs: i32[]): (i32) => i32 {
    return (i: i32): i32 => { return prefix.len() + xs[i]; };
}
function main(): i32 {
    let f: (i32) => i32 = mk("captured-prefix-padpadpad" + "!", [10, 20, 30]);
    if (f(0) != 36) { return 1; }
    if (f(2) != 56) { return 2; }
    return __rc_underflow_count();
}`,
	},
	{
		name: "nested_array_pointer_columns",
		src: `
import "core/int";
function main(): i32 {
    let sfx: string[] = ["a", "b", "c", "d"];
    let rows: string[][] = [];
    let i: i32 = 0;
    while (i < 12) {
        let row: string[] = [];
        let j: i32 = 0;
        while (j < 4) { row = row.append("cell-payload-padpadpad-" + sfx[j]); j = j + 1; }
        rows = rows.append(row);
        i = i + 1;
    }
    if (rows.len() != 12) { return 1; }
    if (rows[11][3] != "cell-payload-padpadpad-d") { return 2; }
    let total: i32 = 0;
    let k: i32 = 0;
    while (k < 12) { total = total + rows[k].len(); k = k + 1; }
    if (total != 48) { return 3; }
    return __rc_underflow_count();
}`,
	},
	{
		name: "map_churn_across_wide_span",
		src: `
import "core/int";
import "core/map";
function build(): Map[i32, string] {
    let sfx: string[] = ["a", "b", "c", "d", "e", "f", "g", "h"];
    let m: Map[i32, string] = map_new(16);
    let i: i32 = 0;
    while (i < 8) { m = m.insert(i, "payload-padpadpadpadpad-" + sfx[i]); i = i + 1; }
    return m;
}
function main(): i32 {
    let total: i32 = 0;
    let r: i32 = 0;
    while (r < 200) {
        let m: Map[i32, string] = build();
        total = total + m.len();
        r = r + 1;
    }
    if (total != 1600) { return 1; }
    return __rc_underflow_count();
}`,
	},
}

// TestSelfHostArm64HighHeapPointerRoundTrip runs the shapes with the arena
// above 4 GiB. A slot, load, store or compare that is 32 bits wide either
// faults or returns a wrong answer here while staying green on the 256 MiB heap.
func TestSelfHostArm64HighHeapPointerRoundTrip(t *testing.T) {
	arm64Tooling(t)
	cli := buildSelfHostCLI(t)
	for _, c := range selfHostHighHeapRoundTrips {
		t.Run(c.name, func(t *testing.T) {
			if stderr, code := cli.exitOf(t, c.src, "arm64-linux", "FERN_HIGH_HEAP=1"); code != 0 {
				t.Errorf("%s at high heap: got exit %d, want 0 (pointer truncated above 4 GiB?)\n%s", c.name, code, stderr)
			}
		})
	}
}

// TestSelfHostArm64HighHeapRoundTripControl is the same set at the DEFAULT
// hint: green here and red above means the address regime, not the program.
func TestSelfHostArm64HighHeapRoundTripControl(t *testing.T) {
	arm64Tooling(t)
	cli := buildSelfHostCLI(t)
	for _, c := range selfHostHighHeapRoundTrips {
		t.Run(c.name, func(t *testing.T) {
			if stderr, code := cli.exitOf(t, c.src, "arm64-linux"); code != 0 {
				t.Errorf("%s at normal heap: got exit %d, want 0\n%s", c.name, code, stderr)
			}
		})
	}
}

// TestSelfHostArm64HighHeapProbeRaisesTheHint pins the knob itself: without
// it the two runs above would be the same run twice and the gate vacuous.
func TestSelfHostArm64HighHeapProbeRaisesTheHint(t *testing.T) {
	cli := buildSelfHostCLI(t)
	const shift = "    lsl x0, x0, #28\n"
	read := func(t *testing.T, src string, env ...string) string {
		t.Helper()
		asm, err := os.ReadFile(cli.emit(t, src, "arm64-linux", env...))
		if err != nil {
			t.Fatal(err)
		}
		return string(asm)
	}
	for _, c := range selfHostHighHeapRoundTrips {
		t.Run(c.name, func(t *testing.T) {
			src := writeTempFern(t, t.TempDir(), "main.fern", c.src)
			low := read(t, src)
			high := read(t, src, "FERN_HIGH_HEAP=1")
			if !strings.Contains(low, "    mov x0, #1\n"+shift) {
				t.Errorf("the default emit does not carry the 256 MiB arena hint (mov x0, #1; lsl #28)")
			}
			if strings.Contains(low, "    mov x0, #32\n"+shift) {
				t.Errorf("the default emit carries the 8 GiB arena hint: FERN_HIGH_HEAP leaked into the shipped value")
			}
			if !strings.Contains(high, "    mov x0, #32\n"+shift) {
				t.Errorf("FERN_HIGH_HEAP=1 emit does not carry the 8 GiB arena hint (mov x0, #32; lsl #28)")
			}
			if strings.Contains(high, "    mov x0, #1\n"+shift) {
				t.Errorf("FERN_HIGH_HEAP=1 emit still carries the 256 MiB hint")
			}
		})
	}
	// The original probe is a static aggregate and does not need an arena.
	t.Run("constant-array-needs-no-heap", func(t *testing.T) {
		src := writeTempFern(t, t.TempDir(), "main.fern", `function main(): i32 { let a: i32[] = [1, 2, 3]; return a[0] - 1; }`)
		low := read(t, src)
		high := read(t, src, "FERN_HIGH_HEAP=1")
		if low != high {
			t.Error("heap-free output changed with FERN_HIGH_HEAP")
		}
		if strings.Contains(low, "__fern_heap_ptr") || strings.Contains(low, shift) {
			t.Error("constant array unexpectedly includes the heap arena")
		}
	})
}
