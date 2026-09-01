package e2eselfhost

import (
	"bufio"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// --- The allocation-count matrix (#7351) -------------------------------------
//
// The companion the leak matrix deliberately declines to be. That gate
// classifies each cell CLEAN or LEAK on both compilers, which is layout-free
// and so cannot see a compiler allocating twice as many blocks as the other for
// the same program: 200 allocs against 200 frees at live_bytes 0 is `clean` no
// matter what the other compiler did with the same source. #7351 was found by
// hand for exactly that reason — every self-host heap string cost a box block
// AND a data block where native's cost one — and it survived every reclaim fix
// in flight because nothing measured volume.
//
// So this gate measures the one thing that is NOT layout: the COUNT of blocks
// allocated per round. One block per array, one per struct box, one per heap
// string is a property of the value graph, not of any box's size, so it stays
// meaningful across capacity schedules and header changes. Both numbers are
// pinned per cell in testdata/selfhost-alloc-count-matrix.txt, which makes the
// file a standing statement of where the two compilers still disagree and why —
// today that is the SSO gap alone (native x86-64 keeps a string of 7 bytes or
// fewer inline in the value; the self-host heap-allocates every string).
//
// `TestX86_64AllocScaling` bounds a RATIO inside one compiler and so is blind
// to a constant factor between two; this is the cross-compiler half.
//
// x86-64 only, like the leak matrix: the comparison is between compilers, not
// targets. Native compiles through the fern CLI as a child process, because
// FERN_LEAKCHECK is read at init by internal/ast — and that is also the
// pipeline that CONST-FOLDS, so every cell embeds the loop variable in what it
// builds. A cell whose payload folds measures nothing on the native leg.

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
function round(i: i32): i32 { var v: N = N { n: i }; return v.n; }
function main(): i32 {
    var t: i32 = 0;
    var r: i32 = 0;
    while (r < 100) { t = t + round(r); r = r + 1; }
    if (__rc_underflow_count() != 0) { return 99; }
    return t % 97;
}
`},
		{name: "bare_arr", rounds: rounds, src: `function round(i: i32): i32 { var xs: i32[] = [i, i + 1]; return xs.len() + xs[0]; }
function main(): i32 {
    var t: i32 = 0;
    var r: i32 = 0;
    while (r < 100) { t = t + round(r); r = r + 1; }
    if (__rc_underflow_count() != 0) { return 99; }
    return t % 97;
}
`},
		{name: "arr_field", rounds: rounds, src: `struct A { xs: i32[] }
function round(i: i32): i32 { var v: A = A { xs: [i, i + 1] }; return v.xs.len(); }
function main(): i32 {
    var t: i32 = 0;
    var r: i32 = 0;
    while (r < 100) { t = t + round(r); r = r + 1; }
    if (__rc_underflow_count() != 0) { return 99; }
    return t % 97;
}
`},
		// The two SSO cells: a result short enough to live inline in native's
		// single-word x86-64 string value. Not const-folded — `pick` makes the
		// argument genuinely runtime-varying, which is how #7351 established
		// that native's zero is the inline encoding and not the folder.
		{name: "bare_str_sso", rounds: rounds, src: `function w(a: string): string { return a + "!"; }
function pick(i: i32): string { if (i % 2 == 0) { return "p"; } return "q"; }
function round(i: i32): i32 { var s: string = w(pick(i)); return s.len() + i; }
function main(): i32 {
    var t: i32 = 0;
    var r: i32 = 0;
    while (r < 100) { t = t + round(r); r = r + 1; }
    if (__rc_underflow_count() != 0) { return 99; }
    return t % 97;
}
`},
		{name: "str_field_sso", rounds: rounds, src: `struct S { s: string }
function w(a: string): string { return a + "!"; }
function pick(i: i32): string { if (i % 2 == 0) { return "p"; } return "q"; }
function round(i: i32): i32 { var v: S = S { s: w(pick(i)) }; return v.s.len() + i; }
function main(): i32 {
    var t: i32 = 0;
    var r: i32 = 0;
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
function round(i: i32): i32 { var s: string = w(pick(i)); return s.len() + i; }
function main(): i32 {
    var t: i32 = 0;
    var r: i32 = 0;
    while (r < 100) { t = t + round(r); r = r + 1; }
    if (__rc_underflow_count() != 0) { return 99; }
    return t % 97;
}
`},
		{name: "str_field_heap", rounds: rounds, src: `struct S { s: string }
function w(a: string): string { return a + "!"; }
function pick(i: i32): string { if (i % 2 == 0) { return "abcdefghijklmnopqrstu"; } return "vwxyzabcdefghijklmnop"; }
function round(i: i32): i32 { var v: S = S { s: w(pick(i)) }; return v.s.len() + i; }
function main(): i32 {
    var t: i32 = 0;
    var r: i32 = 0;
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
function round(i: i32): i32 { var v: P = P { xs: [i, i + 1], s: w(pick(i)) }; return v.xs.len() + v.s.len(); }
function main(): i32 {
    var t: i32 = 0;
    var r: i32 = 0;
    while (r < 100) { t = t + round(r); r = r + 1; }
    if (__rc_underflow_count() != 0) { return 99; }
    return t % 97;
}
`},
	}
}

// allocPin is one row of testdata/selfhost-alloc-count-matrix.txt: the blocks
// each compiler allocates per round.
type allocPin struct {
	native   int64
	selfHost int64
}

func loadAllocMatrix(t *testing.T) map[string]allocPin {
	t.Helper()
	path := filepath.Join("testdata", "selfhost-alloc-count-matrix.txt")
	f, err := os.Open(path)
	if err != nil {
		t.Fatalf("open %s: %v", path, err)
	}
	defer f.Close()
	out := map[string]allocPin{}
	sc := bufio.NewScanner(f)
	for ln := 1; sc.Scan(); ln++ {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 3 {
			t.Fatalf("%s:%d: want `<cell> <native> <selfhost> <note>`, got %q", path, ln, line)
		}
		nat, err := strconv.ParseInt(fields[1], 10, 64)
		if err != nil {
			t.Fatalf("%s:%d: native count %q: %v", path, ln, fields[1], err)
		}
		sh, err := strconv.ParseInt(fields[2], 10, 64)
		if err != nil {
			t.Fatalf("%s:%d: self-host count %q: %v", path, ln, fields[2], err)
		}
		out[fields[0]] = allocPin{native: nat, selfHost: sh}
	}
	if err := sc.Err(); err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return out
}

// nativeAllocCount compiles src with the fern CLI (a child process, so
// FERN_LEAKCHECK instruments the emitted program) and returns its allocation
// count and exit code.
func nativeAllocCount(t *testing.T, cli, dir, name, src string) (int64, int) {
	t.Helper()
	srcPath := filepath.Join(dir, name+".fern")
	binPath := filepath.Join(dir, name+".nat")
	if err := os.WriteFile(srcPath, []byte(src), 0o644); err != nil {
		t.Fatalf("write %s: %v", srcPath, err)
	}
	compile := exec.Command(cli, "-target", "x86-64-linux", "-o", binPath, srcPath)
	compile.Env = childEnv("FERN_LEAKCHECK=1")
	if out, err := compile.CombinedOutput(); err != nil {
		t.Fatalf("%s: native compile failed:\n%s", name, out)
	}
	cmd := exec.Command(binPath)
	var errBuf strings.Builder
	cmd.Stderr = &errBuf
	_ = cmd.Run()
	return allocsFromLeakcheck(t, name+" (native)", errBuf.String()), cmd.ProcessState.ExitCode()
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
	var known map[string]allocPin
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
			natAllocs, natExit := nativeAllocCount(t, cli, dir, cell.name, cell.src)
			shAllocs, shExit := selfHostAllocCount(t, gcc, runner, driverBin, dir, cell.name, cell.src)

			if natExit == 99 || shExit == 99 {
				t.Fatalf("underflow guard tripped (native=%d self-host=%d): an over-release, "+
					"which no matrix row may pin", natExit, shExit)
			}
			if natExit != shExit {
				t.Fatalf("exit codes disagree: native=%d self-host=%d — a wrong-answer "+
					"divergence, not an allocation-count update:\n%s", natExit, shExit, cell.src)
			}
			if natAllocs%cell.rounds != 0 || shAllocs%cell.rounds != 0 {
				t.Fatalf("counts are not whole rounds (native=%d self-host=%d over %d rounds) — "+
					"something allocates outside the loop, so a per-round number would be a lie",
					natAllocs, shAllocs, cell.rounds)
			}
			natPer, shPer := natAllocs/cell.rounds, shAllocs/cell.rounds

			if dump {
				fmt.Printf("%-20s %-3d %-3d\n", cell.name, natPer, shPer)
				return
			}

			rec, listed := known[cell.name]
			if !listed {
				t.Errorf("cell not in testdata/selfhost-alloc-count-matrix.txt (measured "+
					"native=%d self-host=%d per round). Rerun with FERN_ALLOC_COUNT_DUMP=1 "+
					"and add the row with a note saying why the two numbers differ, or that "+
					"they agree", natPer, shPer)
				return
			}
			if rec.native != natPer || rec.selfHost != shPer {
				t.Errorf("blocks per round moved: recorded native=%d self-host=%d, measured "+
					"native=%d self-host=%d. A self-host number falling TO the native one is "+
					"progress — update the row and its note in the change that caused it. A "+
					"number rising is a volume regression: the same values are costing more "+
					"blocks than they did, which no runtime detector reports",
					rec.native, rec.selfHost, natPer, shPer)
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
