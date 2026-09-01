package ir

import (
	"testing"

	"github.com/jakechampion/lang/internal/checker"
	"github.com/jakechampion/lang/internal/parser"
)

// Line-coverage instrumentation (#5548). What these pin is the property a
// coverage report is worth nothing without: the counter table is the
// DENOMINATOR — every executable line has exactly one row, an unexecuted
// line's row still exists, and two files' line 12 are two rows. A table
// that merged them, or dropped the lines nothing reached, would still
// produce a report; it would just be a wrong one.

// lowerCover lowers src with the coverage pass on.
func lowerCover(t *testing.T, src string) *Program {
	t.Helper()
	prog, err := parser.Parse(src)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	info, err := checker.Check(prog)
	if err != nil {
		t.Fatalf("check: %v", err)
	}
	p, err := LowerWith(prog, info, 8, CoverPoints())
	if err != nil {
		t.Fatalf("lower: %v", err)
	}
	return p
}

// coverPointIndices returns the counter indices fn's ops bump, in order.
func coverPointIndices(t *testing.T, p *Program, fn string) []int32 {
	t.Helper()
	f := findFunc(p, fn)
	if f == nil {
		t.Fatalf("no function %q in the lowered program", fn)
	}
	var out []int32
	for _, op := range f.Ops {
		if op.Kind == OpCoverPoint {
			out = append(out, op.I32)
		}
	}
	return out
}

// An ordinary build must not carry a single coverage op or table row —
// that is the whole "zero cost when off" claim, and it is also what keeps
// the self-host's byte-identical fixpoint from noticing this feature.
func TestCoverPointsAbsentWithoutTheOption(t *testing.T) {
	src := `function main(): i32 {
    var x: i32 = 1;
    return x;
}`
	p := lowerSourceWith(t, src, 8)
	if len(p.CoverSites) != 0 {
		t.Errorf("default lowering produced %d cover sites, want 0", len(p.CoverSites))
	}
	for _, f := range p.Funcs {
		for _, op := range f.Ops {
			if op.Kind == OpCoverPoint {
				t.Fatalf("default lowering emitted an OpCoverPoint in %s", f.Name)
			}
		}
	}
}

// Every op's index has to address a real row, and every row has to name a
// real line — the two halves are read in lockstep by the emitted report
// loop, so a table shorter than the largest index would walk off the end
// of the counter array at exit.
func TestCoverSitesIndexTheTable(t *testing.T) {
	src := `function pick(n: i32): i32 {
    if (n > 10) {
        return 1;
    }
    return 2;
}
function main(): i32 { return pick(1); }`
	p := lowerCover(t, src)
	if len(p.CoverSites) == 0 {
		t.Fatal("cover lowering produced no sites")
	}
	for _, f := range p.Funcs {
		for _, op := range f.Ops {
			if op.Kind != OpCoverPoint {
				continue
			}
			if op.I32 < 0 || int(op.I32) >= len(p.CoverSites) {
				t.Fatalf("%s: cover point index %d out of range for %d sites", f.Name, op.I32, len(p.CoverSites))
			}
			if got := p.CoverSites[op.I32].Line; got != op.Pos.Line {
				t.Errorf("%s: cover point at line %d indexes site %d, whose line is %d",
					f.Name, op.Pos.Line, op.I32, got)
			}
		}
	}
	for i, s := range p.CoverSites {
		if s.Line <= 0 {
			t.Errorf("site %d has line %d, want a real source line", i, s.Line)
		}
	}
}

// A line the program can execute gets exactly one counter, however many
// functions or statements sit on it — the report is per LINE, so two
// counters for one line would double its denominator and halve the
// percentage.
func TestCoverSitesAreOnePerLine(t *testing.T) {
	src := `function main(): i32 {
    var a: i32 = 1;
    var b: i32 = 2;
    return a + b;
}`
	p := lowerCover(t, src)
	seen := map[CoverSite]bool{}
	for _, s := range p.CoverSites {
		if seen[s] {
			t.Errorf("site %+v appears twice in the table", s)
		}
		seen[s] = true
	}
	// The three body lines are the executable ones; the `}` is not.
	for _, want := range []int{2, 3, 4} {
		if !seen[CoverSite{Line: want}] {
			t.Errorf("line %d has no counter; table is %+v", want, p.CoverSites)
		}
	}
}

// A branch arm gets its own counter bump even when the whole `if` is on
// one line: the two are separate basic blocks, and a report that counted
// only the `if` could not tell a taken arm from an untaken one.
func TestCoverPointsSurviveASingleLineBranch(t *testing.T) {
	src := `function pick(n: i32): i32 {
    if (n > 10) { return 1; } else { return 2; }
}
function main(): i32 { return pick(1); }`
	p := lowerCover(t, src)
	idx := coverPointIndices(t, p, "pick")
	if len(idx) < 2 {
		t.Fatalf("pick bumps %d counters, want at least 2 (the `if` and its arms are separate blocks); ops:\n%s", len(idx), p)
	}
	// All on line 2, so they must all address the SAME counter — the
	// table is keyed by line, not by block.
	for _, i := range idx {
		if p.CoverSites[i].Line != 2 {
			t.Errorf("cover point indexes site %d at line %d, want line 2", i, p.CoverSites[i].Line)
		}
	}
}

// Statements sharing a line inside one straight-line run bump the counter
// once. Without the dedupe a hit count would report how many statements
// the author crammed onto a line rather than how often it ran.
func TestCoverPointsDedupeWithinABasicBlock(t *testing.T) {
	src := `function main(): i32 {
    var a: i32 = 1; var b: i32 = 2; var c: i32 = 3;
    return a + b + c;
}`
	p := lowerCover(t, src)
	idx := coverPointIndices(t, p, "main")
	onLine2 := 0
	for _, i := range idx {
		if p.CoverSites[i].Line == 2 {
			onLine2++
		}
	}
	if onLine2 != 1 {
		t.Errorf("line 2's counter is bumped %d times in one straight-line run, want 1", onLine2)
	}
}

// Two files' line 12 are two different lines. ast.Position carries only a
// line number, so the table is keyed by (file, line) off FuncDecl's file
// stamp — merge them and a module's coverage silently lands on another's.
func TestCoverSitesAreKeyedByFile(t *testing.T) {
	c := newCoverTable()
	a := c.id("a.fern", 12)
	b := c.id("b.fern", 12)
	if a == b {
		t.Fatalf("a.fern:12 and b.fern:12 share counter %d", a)
	}
	if again := c.id("a.fern", 12); again != a {
		t.Errorf("a.fern:12 allocated a second counter %d, want %d", again, a)
	}
	if len(c.sites) != 2 {
		t.Errorf("table holds %d sites, want 2", len(c.sites))
	}
}

// --- Branch coverage, slice 3 (#5548) ----------------------------------
//
// A branch counter pair exists to state what a line counter cannot: that
// a line RAN but one of its edges never did. The properties below are the
// ones a wrong implementation would break while still producing a
// plausible-looking report — that both edges of a conditional are
// accounted for, that two conditionals on one line stay apart, and that
// the compiler's own conditionals are not counted as the author's.

// Each instrumented conditional gets exactly two counters, and they are
// the eval/true pair — the false edge is a subtraction, so a lone counter
// or three would mean the report cannot derive it.
func TestCoverBranchSitesComeInPairs(t *testing.T) {
	src := `function pick(n: i32): i32 {
    if (n > 10) {
        return 1;
    }
    return 2;
}
function main(): i32 { return pick(1); }`
	p := lowerCover(t, src)
	kinds := map[branchPos][]CoverKind{}
	for _, s := range p.CoverSites {
		if s.Kind == CoverLine {
			continue
		}
		k := branchPos{s.File, s.Line, s.Col}
		kinds[k] = append(kinds[k], s.Kind)
	}
	if len(kinds) != 1 {
		t.Fatalf("got %d branch sites, want 1 (the `if`); table is %+v", len(kinds), p.CoverSites)
	}
	for k, got := range kinds {
		if len(got) != 2 {
			t.Fatalf("branch at %+v has %d counters, want 2", k, got)
		}
		if got[0] != CoverBranchEval || got[1] != CoverBranchTrue {
			t.Errorf("branch at %+v has kinds %v, want [eval true]", k, got)
		}
	}
}

type branchPos struct {
	file string
	line int
	col  int
}

// Two conditionals on one line are two branches. Keyed by line alone they
// would share counters and the report could not say which arm was missed
// — the `&&` inside an `if` is the everyday form of this.
func TestCoverBranchSitesSeparateByColumn(t *testing.T) {
	src := `function guard(a: i32, b: i32): i32 {
    if (a > 0 && b > 0) { return 1; }
    return 0;
}
function main(): i32 { return guard(0, 1); }`
	p := lowerCover(t, src)
	cols := map[int]bool{}
	for _, s := range p.CoverSites {
		if s.Kind == CoverBranchEval && s.Line == 2 {
			cols[s.Col] = true
		}
	}
	if len(cols) != 2 {
		t.Errorf("line 2 has %d branch sites (columns %v), want 2 — the `if` and the `&&`", len(cols), cols)
	}
}

// A `while` is a conditional: its condition runs once per iteration and
// its true edge is the entry into the body. This is the case line
// coverage reports as covered while the body never ran.
func TestCoverBranchInstrumentsWhile(t *testing.T) {
	src := `function main(): i32 {
    var i: i32 = 0;
    while (i > 99) {
        i = i + 1;
    }
    return i;
}`
	p := lowerCover(t, src)
	var found bool
	for _, s := range p.CoverSites {
		if s.Kind == CoverBranchEval && s.Line == 3 {
			found = true
		}
	}
	if !found {
		t.Errorf("the `while` on line 3 has no branch site; table is %+v", p.CoverSites)
	}
}

// The lowering opens far more conditionals than the program wrote — drop
// glue, bounds checks, rc tests. Counting those would report branches the
// author cannot see, let alone cover, and would make the denominator move
// with unrelated codegen changes.
func TestCoverBranchSkipsCompilerConditionals(t *testing.T) {
	// An array index emits a bounds check; a heap value emits drop glue.
	// Neither is a branch the author wrote, so neither may appear.
	src := `function main(): i32 {
    var xs: i32[] = [1, 2, 3];
    return xs[1];
}`
	p := lowerCover(t, src)
	for _, s := range p.CoverSites {
		if s.Kind != CoverLine {
			t.Errorf("a source with no conditionals produced branch site %+v", s)
		}
	}
}

// The report text is the wire format both natives bake into .rodata and
// the reader parses back. Pin all three shapes here, where they are
// defined, rather than in either backend.
func TestCoverSiteReportLine(t *testing.T) {
	for _, c := range []struct {
		site CoverSite
		want string
	}{
		{CoverSite{File: "a.fern", Line: 12}, "fern-cover: a.fern:12 "},
		{CoverSite{File: "a.fern", Line: 12, Col: 5, Kind: CoverBranchEval}, "fern-branch: a.fern:12:5 E "},
		{CoverSite{File: "a.fern", Line: 12, Col: 5, Kind: CoverBranchTrue}, "fern-branch: a.fern:12:5 T "},
		// A program built straight from the parser has no file stamp;
		// naming that keeps every row parseable by one reader.
		{CoverSite{Line: 3}, "fern-cover: <unknown>:3 "},
	} {
		if got := c.site.ReportLine(); got != c.want {
			t.Errorf("%+v.ReportLine() = %q, want %q", c.site, got, c.want)
		}
	}
}
