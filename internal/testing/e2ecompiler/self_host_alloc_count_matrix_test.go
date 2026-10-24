// CI-DARK: FERN_ALLOC_COUNT_DUMP — This flag regenerates the reference data.
package e2ecompiler

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/envflag"
)

type allocCell struct {
	name   string
	src    string
	rounds int64
}

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

func TestSelfHostAllocCountMatrixX86_64(t *testing.T) {
	dump := envflag.On("FERN_ALLOC_COUNT_DUMP")
	var known map[string]int64
	if !dump {
		known = loadAllocMatrix(t)
	}

	gcc, runner := x86_64Tooling(t)
	cli := buildLangBinForInterp(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/asm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "drivers/asm_ir_run.fern", "driver")

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
