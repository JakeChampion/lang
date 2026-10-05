package e2eselfhost

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// --- The allocation-count matrix (#7351) -------------------------------------
//
// The companion the leak matrix deliberately declines to be. That gate
// classifies each cell CLEAN or LEAK, which is layout-free and so cannot see a
// compiler allocating twice as many blocks as it needs for the same program:
// 200 allocs against 200 frees at live_bytes 0 is `clean` whatever the count.
// #7351, every self-host heap string costing a box block AND a data block, was
// found by hand for exactly that reason.
//
// So this gate measures the one thing that is NOT layout: the COUNT of blocks
// allocated per round. One block per array, one per struct box, one per heap
// string is a property of the value graph, not of any box's size, so it stays
// meaningful across capacity schedules and header changes. Each cell's count is
// pinned in testdata/selfhost-alloc-count-matrix.txt, so a number rising is a
// volume regression no runtime detector reports. The interpreter's exit code is
// the oracle that the cell computed the right answer.
//
// `TestX86_64AllocScaling` bounds a RATIO inside one compiler and so is blind
// to a constant factor; this pins the factor. x86-64 only, like the leak
// matrix. Every cell embeds the loop variable in what it builds, so the folder
// cannot turn it into a constant.

type allocCell struct {
	name string
	src  string
	// rounds is what the count is divided by, so a pinned number reads as
	// "blocks per round" rather than a total that moves with the loop bound.
	rounds int64
}

// allocMatrixCells is the corpus: the field-isolating probes #7351 was
// characterised with, plus the composite that surfaced it. Every one runs 100
// rounds and embeds `i` in what it builds.
func allocMatrixCells() []allocCell {
	const rounds = 100
	return []allocCell{
		{name: "scalar_only", rounds: rounds, src: `struct N { n: i32 }
function round(i: i32): i32 { let v: N = N { n: i }; return v.n; }
function main(): i32 {
    let t: i32 = 0;
    let r: i32 = 0;
    while (r < 100) { t = t + round(r); r = r + 1; }
    if (__rc_underflow_count() != 0) { return 99; }
    return t % 97;
}
`},
		{name: "bare_arr", rounds: rounds, src: `function round(i: i32): i32 { let xs: i32[] = [i, i + 1]; return xs.len() + xs[0]; }
function main(): i32 {
    let t: i32 = 0;
    let r: i32 = 0;
    while (r < 100) { t = t + round(r); r = r + 1; }
    if (__rc_underflow_count() != 0) { return 99; }
    return t % 97;
}
`},
		{name: "arr_field", rounds: rounds, src: `struct A { xs: i32[] }
function round(i: i32): i32 { let v: A = A { xs: [i, i + 1] }; return v.xs.len(); }
function main(): i32 {
    let t: i32 = 0;
    let r: i32 = 0;
    while (r < 100) { t = t + round(r); r = r + 1; }
    if (__rc_underflow_count() != 0) { return 99; }
    return t % 97;
}
`},
		// The two SSO cells: a result short enough for a small-string form,
		// which the self-host does not have (#7351). `pick` makes the argument
		// runtime-varying, so the folder cannot remove the allocation.
		{name: "bare_str_sso", rounds: rounds, src: `function w(a: string): string { return a + "!"; }
function pick(i: i32): string { if (i % 2 == 0) { return "p"; } return "q"; }
function round(i: i32): i32 { let s: string = w(pick(i)); return s.len() + i; }
function main(): i32 {
    let t: i32 = 0;
    let r: i32 = 0;
    while (r < 100) { t = t + round(r); r = r + 1; }
    if (__rc_underflow_count() != 0) { return 99; }
    return t % 97;
}
`},
		{name: "str_field_sso", rounds: rounds, src: `struct S { s: string }
function w(a: string): string { return a + "!"; }
function pick(i: i32): string { if (i % 2 == 0) { return "p"; } return "q"; }
function round(i: i32): i32 { let v: S = S { s: w(pick(i)) }; return v.s.len() + i; }
function main(): i32 {
    let t: i32 = 0;
    let r: i32 = 0;
    while (r < 100) { t = t + round(r); r = r + 1; }
    if (__rc_underflow_count() != 0) { return 99; }
    return t % 97;
}
`},
		// Above the inline threshold on both compilers, so the only thing left
		// to disagree about is how many blocks ONE heap string costs. This is
		// the cell that read 100 against 200 before #7351 was fixed.
		{name: "bare_str_heap", rounds: rounds, src: `function w(a: string): string { return a + "!"; }
function pick(i: i32): string { if (i % 2 == 0) { return "abcdefghijklmnopqrstu"; } return "vwxyzabcdefghijklmnop"; }
function round(i: i32): i32 { let s: string = w(pick(i)); return s.len() + i; }
function main(): i32 {
    let t: i32 = 0;
    let r: i32 = 0;
    while (r < 100) { t = t + round(r); r = r + 1; }
    if (__rc_underflow_count() != 0) { return 99; }
    return t % 97;
}
`},
		{name: "str_field_heap", rounds: rounds, src: `struct S { s: string }
function w(a: string): string { return a + "!"; }
function pick(i: i32): string { if (i % 2 == 0) { return "abcdefghijklmnopqrstu"; } return "vwxyzabcdefghijklmnop"; }
function round(i: i32): i32 { let v: S = S { s: w(pick(i)) }; return v.s.len() + i; }
function main(): i32 {
    let t: i32 = 0;
    let r: i32 = 0;
    while (r < 100) { t = t + round(r); r = r + 1; }
    if (__rc_underflow_count() != 0) { return 99; }
    return t % 97;
}
`},
		// A CAPTURE-FREE function value (#9839), folded to a static cell, so
		// the only block it costs is the struct box that holds it. The value is
		// re-made every round rather than hoisted, so the cell measures the
		// construction and not a loop-invariant.
		{name: "capture_free_fn_value", rounds: rounds, src: `struct H { f: (i32) => i32 }
function dbl(x: i32): i32 { return x * 2; }
function round(i: i32): i32 { let h: H = H { f: dbl }; return h.f(i); }
function main(): i32 {
    let t: i32 = 0;
    let r: i32 = 0;
    while (r < 100) { t = t + round(r); r = r + 1; }
    if (__rc_underflow_count() != 0) { return 99; }
    return t % 97;
}
`},
		// A self-append chain, the shape std/http's serialiser is built from
		// (`hdr_block = hdr_block + name + ": " + value + "\r\n"`). A uniquely
		// held accumulator grows in place through __fern_str_grow (#10960).
		{name: "str_self_append_chain", rounds: rounds, src: `function piece(i: i32): string { if (i % 2 == 0) { return "abcdefghij"; } return "klmnopqrst"; }
function round(i: i32): i32 {
    let s: string = piece(i);
    s = s + piece(i + 1) + ": " + piece(i + 2) + "\r\n";
    s = s + piece(i + 3) + ": " + piece(i + 4) + "\r\n";
    return s.len() + i;
}
function main(): i32 {
    let t: i32 = 0;
    let r: i32 = 0;
    while (r < 100) { t = t + round(r); r = r + 1; }
    if (__rc_underflow_count() != 0) { return 99; }
    return t % 97;
}
`},
		// The composite #7351 was scoped from: one array field and one string
		// field in the same box.
		{name: "arr_and_str_heap", rounds: rounds, src: `struct P { xs: i32[], s: string }
function w(a: string): string { return a + "!"; }
function pick(i: i32): string { if (i % 2 == 0) { return "abcdefghijklmnopqrstu"; } return "vwxyzabcdefghijklmnop"; }
function round(i: i32): i32 { let v: P = P { xs: [i, i + 1], s: w(pick(i)) }; return v.xs.len() + v.s.len(); }
function main(): i32 {
    let t: i32 = 0;
    let r: i32 = 0;
    while (r < 100) { t = t + round(r); r = r + 1; }
    if (__rc_underflow_count() != 0) { return 99; }
    return t % 97;
}
`},
	}
}

// loadAllocMatrix reads testdata/selfhost-alloc-count-matrix.txt: the blocks
// each cell allocates per round.
func loadAllocMatrix(t *testing.T) map[string]int64 {
	t.Helper()
	path := filepath.Join("testdata", "selfhost-alloc-count-matrix.txt")
	f, err := os.Open(path)
	if err != nil {
		t.Fatalf("open %s: %v", path, err)
	}
	defer f.Close()
	out := map[string]int64{}
	sc := bufio.NewScanner(f)
	for ln := 1; sc.Scan(); ln++ {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 2 {
			t.Fatalf("%s:%d: want `<cell> <blocks> <note>`, got %q", path, ln, line)
		}
		n, err := strconv.ParseInt(fields[1], 10, 64)
		if err != nil {
			t.Fatalf("%s:%d: count %q: %v", path, ln, fields[1], err)
		}
		out[fields[0]] = n
	}
	if err := sc.Err(); err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return out
}

// selfHostAllocCount compiles src with the self-host x86-64 driver, links it
// and returns the same pair. A refusal fails hard: every cell here is inside
// the compilable subset, so a driver refusal is a frontend regression, not a
// matrix update.
func selfHostAllocCount(t *testing.T, gcc string, runner []string, driverBin, dir, name, src string) (int64, int) {
	t.Helper()
	asm := hevCompile(t, runner, driverBin, src, []string{"FERN_LEAKCHECK=1"})
	bin := buildBin(t, gcc, dir, "alloccnt_"+name, asm)
	stderr, exit := hevRun(t, runner, bin)
	return allocsFromLeakcheck(t, name+" (self-host)", stderr), exit
}

func allocsFromLeakcheck(t *testing.T, label, stderr string) int64 {
	t.Helper()
	summary := leakSummaryLine(stderr)
	if summary == "" {
		t.Fatalf("%s: no leakcheck summary on stderr — FERN_LEAKCHECK did not take effect", label)
	}
	var allocs, frees, live int64
	if _, err := fmtSscan(summary, &allocs, &frees, &live); err != nil {
		t.Fatalf("%s: parse %q: %v", label, summary, err)
	}
	if live != 0 {
		t.Errorf("%s: %s — a leaking cell cannot state a per-round count; fix the leak "+
			"(or move the cell to the leak matrix), never pin it here", label, summary)
	}
	return allocs
}

// TestSelfHostAllocCountMatrixX86_64 is the gate. FERN_ALLOC_COUNT_DUMP=1
// prints every cell's measured row in matrix-file format instead of comparing,
// for regenerating the pin after a deliberate change.
func TestSelfHostAllocCountMatrixX86_64(t *testing.T) {
	// CI-DARK: FERN_ALLOC_COUNT_DUMP — a regeneration tool, not coverage. It
	// prints rows INSTEAD of comparing, so a lane setting it would disable this
	// gate. The compare path below is the CI behaviour.
	dump := os.Getenv("FERN_ALLOC_COUNT_DUMP") == "1"
	var known map[string]int64
	if !dump {
		known = loadAllocMatrix(t)
	}

	gcc, runner := x86_64Tooling(t)
	cli := buildLangBinForInterp(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_ir_run.fern", "driver")

	cells := allocMatrixCells()
	seen := map[string]bool{}
	for _, cell := range cells {
		seen[cell.name] = true
		t.Run(cell.name, func(t *testing.T) {
			want := interpExit(t, cli, cell.src)
			allocs, exit := selfHostAllocCount(t, gcc, runner, driverBin, dir, cell.name, cell.src)

			if exit == 99 {
				t.Fatalf("underflow guard tripped: an over-release, which no matrix row may pin")
			}
			if exit != want {
				t.Fatalf("exit %d, the interpreter gives %d — a wrong-answer divergence, not an "+
					"allocation-count update:\n%s", exit, want, cell.src)
			}
			if allocs%cell.rounds != 0 {
				t.Fatalf("%d blocks is not a whole number of %d rounds — something allocates "+
					"outside the loop, so a per-round number would be a lie", allocs, cell.rounds)
			}
			per := allocs / cell.rounds

			if dump {
				fmt.Printf("%-21s %-3d\n", cell.name, per)
				return
			}

			rec, listed := known[cell.name]
			if !listed {
				t.Errorf("cell not in testdata/selfhost-alloc-count-matrix.txt (measured %d per "+
					"round). Rerun with FERN_ALLOC_COUNT_DUMP=1 and add the row with a note "+
					"saying what the blocks are", per)
				return
			}
			if rec != per {
				t.Errorf("blocks per round moved: recorded %d, measured %d. A number falling is "+
					"progress — update the row and its note in the change that caused it. A "+
					"number rising is a volume regression: the same values are costing more "+
					"blocks than they did, which no runtime detector reports", rec, per)
			}
		})
	}

	if !dump {
		for name := range known {
			if !seen[name] {
				t.Errorf("testdata pins %q but the generator emits no such cell — "+
					"rename or remove the row", name)
			}
		}
	}
}
