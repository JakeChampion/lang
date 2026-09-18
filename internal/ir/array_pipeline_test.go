package ir_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/ast"
	"github.com/jakechampion/lang/internal/checker"
	"github.com/jakechampion/lang/internal/constfold"
	"github.com/jakechampion/lang/internal/ir"
	"github.com/jakechampion/lang/internal/modload"
	"github.com/jakechampion/lang/internal/monomorph"
)

// Recognition of std/array pipelines (#9730). The pass rewrites nothing, so
// what these lock is what it SEES: the stage list, the cardinality of each
// stage, and — as much as the positives — the shapes it declines to chain.
//
// A recogniser is only as good as its refusals. One that chains a value used
// twice would hand #9731 a pipeline that is not a single traversal, and the
// fusion built on it would be wrong rather than slow. So the negatives here
// carry the same weight as the positives and are written first.
//
// These go through modload because the string-source helpers in this package
// do not resolve `import "std/array"`, and recognising std/array's mangled
// names is the entire subject.

func lowerPipelineSrc(t *testing.T, src string) *ir.Program {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "main.fern")
	if err := os.WriteFile(path, []byte(src), 0o644); err != nil {
		t.Fatalf("write source: %v", err)
	}
	prog, _, err := modload.Load(path)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	constfold.Fold(prog, nil)
	info, err := checker.Check(prog)
	if err != nil {
		t.Fatalf("check: %v", err)
	}
	monomorph.Run(prog, info)
	prev := ast.RcFreeEnabled
	ast.RcFreeEnabled = true
	defer func() { ast.RcFreeEnabled = prev }()
	ip, err := ir.LowerWith(prog, info, 8)
	if err != nil {
		t.Fatalf("lower: %v", err)
	}
	return ip
}

// shapesIn returns the recognized pipelines in `fn`, as their rendered
// shapes. Keyed on the function so a test says nothing about std/array's own
// bodies, which the pass skips.
//
// A negative test asserts on the STAGE COUNT, never on the rendered shape:
// the cardinality is spelled `n->n`, so a chained-ness check written as
// `strings.Contains(shape, "->")` passes on every single-stage pipeline. It
// was written that way first and reported two correct refusals as failures.
func shapesIn(t *testing.T, p *ir.Program, fn string) []string {
	t.Helper()
	var out []string
	for _, pl := range ir.RecognizeArrayPipelines(p) {
		if pl.Func == fn {
			out = append(out, pl.Shape())
		}
	}
	return out
}

func TestRecognizeElementwiseReductionChain(t *testing.T) {
	p := lowerPipelineSrc(t, `import "std/array";
function run(xs: i64[]): i64 {
  var out: Option[i64] = xs
    .map((x: i64): i64 => x + (1 as i64))
    .map((x: i64): i64 => x * (2 as i64))
    .reduce((a: i64, b: i64): i64 => a + b);
  match (out) { Some(v) => { return v; }, None => { return 0 as i64; } }
}
function main(): i32 { return run([1 as i64, 2 as i64]) as i32; }`)

	got := shapesIn(t, p, "run")
	want := []string{"map(n->n) -> map(n->n) -> reduce(n->1)"}
	if len(got) != 1 || got[0] != want[0] {
		t.Errorf("recognized %v, want %v:\n%s", got, want, ir.FormatArrayPipelines(p))
	}
}

// Variable cardinality is the fact #9728 measured separately and the one a
// fusion scheme assuming a fixed trip count breaks on, so the pass has to
// report it rather than treat filter as another elementwise stage.
func TestRecognizeSelectionCardinality(t *testing.T) {
	p := lowerPipelineSrc(t, `import "std/array";
function run(xs: i64[]): i64 {
  var out: Option[i64] = xs
    .filter((x: i64): boolean => x > (0 as i64))
    .map((x: i64): i64 => x + (1 as i64))
    .reduce((a: i64, b: i64): i64 => a + b);
  match (out) { Some(v) => { return v; }, None => { return 0 as i64; } }
}
function main(): i32 { return run([1 as i64, 2 as i64]) as i32; }`)

	got := shapesIn(t, p, "run")
	want := "filter(n->k) -> map(n->n) -> reduce(n->1)"
	if len(got) != 1 || got[0] != want {
		t.Errorf("recognized %v, want [%s]:\n%s", got, want, ir.FormatArrayPipelines(p))
	}
}

// `fold` carries its own seed, so it reduces like `reduce` but types
// differently; both belong to the reduction family and the pass should not
// care which one a program reached for.
func TestRecognizeFoldAsReduction(t *testing.T) {
	p := lowerPipelineSrc(t, `import "std/array";
function run(xs: i64[]): i64 {
  return xs
    .map((x: i64): i64 => x + (1 as i64))
    .fold(0 as i64, (a: i64, b: i64): i64 => a + b);
}
function main(): i32 { return run([1 as i64, 2 as i64]) as i32; }`)

	got := shapesIn(t, p, "run")
	want := "map(n->n) -> fold(n->1)"
	if len(got) != 1 || got[0] != want {
		t.Errorf("recognized %v, want [%s]:\n%s", got, want, ir.FormatArrayPipelines(p))
	}
}

// NEGATIVE: an intermediate read twice is not one traversal.
//
// This is the case that decides whether the pass is safe to build fusion on.
// `ys` feeds the reduction AND is measured afterwards, so a fused loop that
// never materialised it would have nothing to take the length of. The pass
// must break the chain here, not report `map -> reduce`.
func TestRefusesIntermediateUsedTwice(t *testing.T) {
	p := lowerPipelineSrc(t, `import "std/array";
function run(xs: i64[]): i64 {
  var ys: i64[] = xs.map((x: i64): i64 => x + (1 as i64));
  var out: Option[i64] = ys.reduce((a: i64, b: i64): i64 => a + b);
  var total: i64 = 0 as i64;
  match (out) { Some(v) => { total = v; }, None => { total = 0 as i64; } }
  return total + (ys.len() as i64);
}
function main(): i32 { return run([1 as i64, 2 as i64]) as i32; }`)

	var sawReason bool
	for _, pl := range ir.RecognizeArrayPipelines(p) {
		if pl.Func != "run" {
			continue
		}
		if len(pl.Stages) > 1 {
			t.Errorf("chained %q across an intermediate that is read again; a fused loop "+
				"would have no array left for `ys.len()` to measure:\n%s",
				pl.Shape(), ir.FormatArrayPipelines(p))
		}
		if strings.Contains(pl.Stop, "read again") {
			sawReason = true
		}
	}
	// Refusing is half the job; saying which rule refused is the other half
	// (docs/ARRAY-ALGEBRA.md §7). A report that stopped silently would leave
	// a reader unable to tell this case from an unrecognized combinator.
	if !sawReason {
		t.Errorf("refused the chain without naming the rule that decided:\n%s",
			ir.FormatArrayPipelines(p))
	}
}

// NEGATIVE: a combinator outside the recognized set does not join a chain.
//
// `windows` is a structural operator the set does not include, so a chain
// through it is two pipelines rather than one. Recognising it as a link would
// claim a cardinality relationship the pass has not been taught.
func TestRefusesCombinatorOutsideTheSet(t *testing.T) {
	p := lowerPipelineSrc(t, `import "std/array";
function run(xs: i64[]): i32 {
  var chunked: i64[][] = xs
    .map((x: i64): i64 => x + (1 as i64))
    .windows(2);
  return chunked.len();
}
function main(): i32 { return run([1 as i64, 2 as i64, 3 as i64]); }`)

	for _, pl := range ir.RecognizeArrayPipelines(p) {
		if pl.Func != "run" {
			continue
		}
		if strings.Contains(pl.Shape(), "windows") {
			t.Errorf("named an unrecognized combinator as a stage: %q", pl.Shape())
		}
		if len(pl.Stages) > 1 {
			t.Errorf("chained through a combinator outside the set: %q:\n%s",
				pl.Shape(), ir.FormatArrayPipelines(p))
		}
	}
}

// NEGATIVE: a user's own `map` is not std/array's.
//
// The recognition boundary docs/ARRAY-ALGEBRA.md §6 settles is
// module-qualified stdlib identity. A free `map` in the entry module keeps its
// bare name, so nothing about it looks like `array__map`, and the pass must
// not claim it.
func TestRefusesUserDefinedMap(t *testing.T) {
	p := lowerPipelineSrc(t, `function map(xs: i64[], f: (i64) => i64): i64[] {
  var out: i64[] = [];
  var i: i32 = 0;
  while (i < xs.len()) { out = out.append(f(xs[i])); i = i + 1; }
  return out;
}
function run(xs: i64[]): i32 {
  return map(xs, (x: i64): i64 => x + (1 as i64)).len();
}
function main(): i32 { return run([1 as i64, 2 as i64]); }`)

	if got := shapesIn(t, p, "run"); len(got) != 0 {
		t.Errorf("recognized %v in a program whose `map` is the user's own:\n%s",
			got, ir.FormatArrayPipelines(p))
	}
}

// The element function is named where the lowering parked it in a slot, which
// is what §1 of docs/ARRAY-ALGEBRA.md needs to decide fusibility at all: a
// stage whose element function cannot be named is a stage that does not fuse.
func TestNamesTheElementFunction(t *testing.T) {
	p := lowerPipelineSrc(t, `import "std/array";
function run(xs: i64[]): i64 {
  return xs.map((x: i64): i64 => x + (1 as i64)).fold(0 as i64, (a: i64, b: i64): i64 => a + b);
}
function main(): i32 { return run([1 as i64, 2 as i64]) as i32; }`)

	var named int
	for _, pl := range ir.RecognizeArrayPipelines(p) {
		if pl.Func != "run" {
			continue
		}
		for _, s := range pl.Stages {
			if s.Element != "" {
				named++
			}
		}
	}
	if named != 2 {
		t.Errorf("named %d element functions, want 2:\n%s", named, ir.FormatArrayPipelines(p))
	}
}

// The three programs #9728 measured are the pass's acceptance bar: whatever
// else it recognises, it has to recognise these, and it has to agree with
// what the report of that experiment says they are.
func TestRecognizesTheBaselinePipelines(t *testing.T) {
	for _, tc := range []struct {
		file, fn, want string
	}{
		{"map_map_reduce", "run_pipeline", "map(n->n) -> map(n->n) -> reduce(n->1)"},
		{"filter_map_reduce", "run_pipeline", "filter(n->k) -> map(n->n) -> reduce(n->1)"},
		{"own_map_inplace", "via_map_own", "map(n->n)"},
	} {
		t.Run(tc.file, func(t *testing.T) {
			path := filepath.Join("..", "..", "examples", "array_pipeline", tc.file+".fern")
			src, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("read %s: %v", path, err)
			}
			p := lowerPipelineSrc(t, string(src))
			got := shapesIn(t, p, tc.fn)
			if len(got) != 1 || got[0] != tc.want {
				t.Errorf("%s/%s recognized %v, want [%s]", tc.file, tc.fn, got, tc.want)
			}
		})
	}
}

// The pass answers a question and rewrites nothing. #9730's acceptance says
// this lands with no allocation or performance delta, which is also what
// makes the delta from fusion (#9731) attributable — so "changes nothing" is
// a property to check rather than an intention to state.
//
// Checked by comparing the program's own rendering across a run: `Program`
// carries the op stream, so a pass that touched an op, a local or a name
// would show up here.
func TestRecognitionDoesNotMutate(t *testing.T) {
	p := lowerPipelineSrc(t, `import "std/array";
function run(xs: i64[]): i64 {
  return xs.map((x: i64): i64 => x + (1 as i64)).fold(0 as i64, (a: i64, b: i64): i64 => a + b);
}
function main(): i32 { return run([1 as i64, 2 as i64]) as i32; }`)

	before := p.String()
	if got := ir.RecognizeArrayPipelines(p); len(got) == 0 {
		t.Fatal("recognized nothing, so this proves nothing about mutation")
	}
	_ = ir.FormatArrayPipelines(p)
	if after := p.String(); after != before {
		t.Error("recognition changed the program; it is supposed only to read it")
	}
}
