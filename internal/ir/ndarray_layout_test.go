package ir_test

import (
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/ir"
)

// What a handle's layout is at a site (#9734). The pass rewrites nothing, so
// what these lock is what it PROVES — and, as much as that, that it proves
// the two apart: the program below makes the same call twice, on receivers
// the IR cannot otherwise tell apart, and one of them is free.

const layoutSrc = `import "std/ndarray";
function run(): i32 {
  var a: ndarray.NdArray[i32] = ndarray.from_flat([1, 2, 3, 4, 5, 6], [2, 3]);
  var t: ndarray.NdArray[i32] = a.transpose();
  var f1: i32[] = a.to_flat();
  var f2: i32[] = t.to_flat();
  return f1[0] + f2[0];
}
function main(): i32 { return run(); }`

func layoutSites(t *testing.T, p *ir.Program, fn string) []ir.NdarrayLayoutSite {
	t.Helper()
	var out []ir.NdarrayLayoutSite
	for _, s := range ir.RecognizeNdarrayLayouts(p) {
		if s.Func == fn {
			out = append(out, s)
		}
	}
	return out
}

// The finding this pass exists for: two calls to to_flat that are the same
// op with the same callee, one free and one O(n), told apart by where their
// receivers came from.
func TestToFlatOfAPackedAndAStridedHandleAreToldApart(t *testing.T) {
	p := lowerPipelineSrc(t, layoutSrc)
	got := layoutSites(t, p, "run")
	if len(got) != 2 {
		t.Fatalf("found %d storage-sensitive sites in run, want 2: %+v", len(got), got)
	}
	if got[0].Callee != got[1].Callee {
		t.Fatalf("the two sites call different functions (%q, %q), so nothing here is discriminating",
			got[0].Callee, got[1].Callee)
	}
	if got[0].Receiver != ir.NdarrayLayoutPacked || got[0].Verdict != ir.NdarrayCopyMetadata {
		t.Errorf("to_flat of from_flat's result = %s / %s, want packed / metadata",
			got[0].Receiver.Tag(), got[0].Verdict.Tag())
	}
	if got[1].Receiver != ir.NdarrayLayoutStrided || got[1].Verdict != ir.NdarrayCopyNotProvenPacked {
		t.Errorf("to_flat of a transpose = %s / %s, want strided / not-proven-packed",
			got[1].Receiver.Tag(), got[1].Verdict.Tag())
	}
}

// A parameter's handle came from a caller this pass does not follow, so it is
// `unknown` and not `strided`: the two both claim nothing and are separate
// rows because they fail for different reasons, which is what makes the tally
// a coverage checklist.
func TestAHandleParameterIsUnknownRatherThanStrided(t *testing.T) {
	p := lowerPipelineSrc(t, `import "std/ndarray";
function run(a: ndarray.NdArray[i32]): i32 { return a.to_flat()[0]; }
function main(): i32 { return run(ndarray.from_flat([1, 2], [2])); }`)
	got := layoutSites(t, p, "run")
	if len(got) != 1 {
		t.Fatalf("found %d sites in run, want 1: %+v", len(got), got)
	}
	if got[0].Receiver != ir.NdarrayLayoutUnknown {
		t.Errorf("a parameter's layout = %s, want unknown", got[0].Receiver.Tag())
	}
	if got[0].Verdict != ir.NdarrayCopyNotProvenPacked {
		t.Errorf("verdict = %s, want not-proven-packed", got[0].Verdict.Tag())
	}
}

// `packed()` is packed from anything — it returns the receiver when it is
// packed and a from_flat copy otherwise — so the to_flat after it is free
// even though the handle it was built from is not.
func TestPackedMakesTheNextToFlatFree(t *testing.T) {
	p := lowerPipelineSrc(t, `import "std/ndarray";
function run(a: ndarray.NdArray[i32]): i32 {
  var q: ndarray.NdArray[i32] = a.transpose().packed();
  return q.to_flat()[0];
}
function main(): i32 { return run(ndarray.from_flat([1, 2, 3, 4], [2, 2])); }`)
	got := layoutSites(t, p, "run")
	if len(got) != 2 {
		t.Fatalf("found %d sites in run, want 2 (packed, to_flat): %+v", len(got), got)
	}
	if got[0].Verb != "packed" || got[0].Receiver != ir.NdarrayLayoutStrided {
		t.Errorf("site 0 = %s on %s, want packed on strided", got[0].Verb, got[0].Receiver.Tag())
	}
	if got[1].Verb != "to_flat" || got[1].Receiver != ir.NdarrayLayoutPacked {
		t.Errorf("site 1 = %s on %s, want to_flat on packed", got[1].Verb, got[1].Receiver.Tag())
	}
	if got[1].Verdict != ir.NdarrayCopyMetadata {
		t.Errorf("to_flat after packed() = %s, want metadata", got[1].Verdict.Tag())
	}
}

// `reshape` is row-major whichever branch it takes, so a reshape of a
// reshape is metadata however the first one went. Its result is packed only
// from a packed receiver, which the to_flat below holds it to.
func TestReshapeIsRowMajorFromAnythingAndPackedOnlyFromPacked(t *testing.T) {
	p := lowerPipelineSrc(t, `import "std/ndarray";
function run(a: ndarray.NdArray[i32]): i32 {
  var r: ndarray.NdArray[i32] = a.transpose().reshape([4]);
  var s: ndarray.NdArray[i32] = r.reshape([2, 2]);
  return s.to_flat()[0];
}
function main(): i32 { return run(ndarray.from_flat([1, 2, 3, 4], [2, 2])); }`)
	got := layoutSites(t, p, "run")
	if len(got) != 3 {
		t.Fatalf("found %d sites in run, want 3: %+v", len(got), got)
	}
	if got[0].Verdict != ir.NdarrayCopyNotProvenRowMajor {
		t.Errorf("reshape of a transpose = %s, want not-proven-row-major", got[0].Verdict.Tag())
	}
	if got[1].Receiver != ir.NdarrayLayoutRowMajor || got[1].Verdict != ir.NdarrayCopyMetadata {
		t.Errorf("reshape of a reshape = %s / %s, want row-major / metadata",
			got[1].Receiver.Tag(), got[1].Verdict.Tag())
	}
	if got[2].Receiver != ir.NdarrayLayoutRowMajor || got[2].Verdict != ir.NdarrayCopyNotProvenPacked {
		t.Errorf("to_flat of a row-major handle = %s / %s, want row-major / not-proven-packed",
			got[2].Receiver.Tag(), got[2].Verdict.Tag())
	}
}

// One slot holding two handles is the meet of both, whichever store the
// branch took: the analysis is flow-insensitive, and reading the packed store
// alone would be wrong.
func TestASlotAssignedTwiceTakesTheWeakerLayout(t *testing.T) {
	p := lowerPipelineSrc(t, `import "std/ndarray";
function run(flip: boolean): i32 {
  var a: ndarray.NdArray[i32] = ndarray.from_flat([1, 2, 3, 4], [2, 2]);
  if (flip) { a = a.transpose(); }
  return a.to_flat()[0];
}
function main(): i32 { return run(true); }`)
	got := layoutSites(t, p, "run")
	if len(got) != 1 {
		t.Fatalf("found %d sites in run, want 1: %+v", len(got), got)
	}
	if got[0].Receiver != ir.NdarrayLayoutStrided {
		t.Errorf("a slot holding a packed handle on one arm and a strided one on the other = %s, want strided",
			got[0].Receiver.Tag())
	}
}

// A user's own NdArray with its own to_flat mangles differently, so this pass
// has no opinion about it — the boundary docs/ARRAY-SHAPES.md §6 sets.
func TestUserDeclaredNdArrayToFlatIsNotThisModules(t *testing.T) {
	p := lowerPipelineSrc(t, `struct NdArray[T] { data: T[] }
function (a: NdArray[T]) to_flat[T](): T[] { return a.data; }
function run(a: NdArray[i32]): i32 { return a.to_flat()[0]; }
function main(): i32 { return run(NdArray[i32] { data: [7] }); }`)
	if got := ir.RecognizeNdarrayLayouts(p); len(got) != 0 {
		t.Errorf("claimed %d sites over a user-declared NdArray, want 0: %+v", len(got), got)
	}
}

// std/ndarray's own bodies are not reported: `packed` calls `from_flat`, and
// `reshape` calls both, so without the skip the module would report itself.
func TestNdarrayLayoutsReportOnlyCallers(t *testing.T) {
	p := lowerPipelineSrc(t, layoutSrc)
	for _, s := range ir.RecognizeNdarrayLayouts(p) {
		if strings.HasPrefix(s.Func, "ndarray__") || strings.HasPrefix(s.Func, "__method_ndarray__") {
			t.Errorf("reported a site inside std/ndarray: %+v", s)
		}
	}
}

func TestNdarrayLayoutAnalysisDoesNotMutate(t *testing.T) {
	p := lowerPipelineSrc(t, layoutSrc)
	before := 0
	for _, fn := range p.Funcs {
		before += len(fn.Ops)
	}
	_ = ir.RecognizeNdarrayLayouts(p)
	_ = ir.FormatNdarrayLayouts(p)
	_ = ir.FormatNdarrayLayoutHistogram(p)
	after := 0
	for _, fn := range p.Funcs {
		after += len(fn.Ops)
	}
	if before != after {
		t.Errorf("the layout analysis changed the op count: %d -> %d", before, after)
	}
}

// The report carries the layouts as their own section, and the histogram
// prints a row per tag even at zero, so both sets can be tallied.
func TestArrayReportCarriesTheLayouts(t *testing.T) {
	p := lowerPipelineSrc(t, layoutSrc)
	got := ir.FormatArrayPipelines(p)
	for _, want := range []string{
		"std/ndarray storage-sensitive operations, and what their receiver is proved to be:",
		"to_flat  over packed     -> metadata",
		"to_flat  over strided    -> not-proven-packed",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("the report is missing %q:\n%s", want, got)
		}
	}
	hist := ir.FormatArrayPipelineHistogram(p)
	for _, want := range []string{
		"std/ndarray storage-sensitive operations (#9734): 2, 1 proved to move no elements",
		"  metadata                   1\n",
		"  not-proven-packed          1\n",
		"  not-proven-row-major       0\n",
		"their receivers' layouts:",
		"  row-major                  0\n",
	} {
		if !strings.Contains(hist, want) {
			t.Errorf("the histogram is missing %q:\n%s", want, hist)
		}
	}
}

// Every tag is distinct and non-empty, which is what a tally of a closed set
// needs to be readable at all.
func TestNdarrayLayoutTagsAreDistinct(t *testing.T) {
	seen := map[string]bool{}
	for _, l := range ir.AllNdarrayLayouts {
		if l.Tag() == "" || seen[l.Tag()] {
			t.Errorf("layout tag %q is empty or repeated", l.Tag())
		}
		seen[l.Tag()] = true
	}
	seen = map[string]bool{}
	for _, v := range ir.AllNdarrayCopyVerdicts {
		if v.Tag() == "" || seen[v.Tag()] {
			t.Errorf("verdict tag %q is empty or repeated", v.Tag())
		}
		seen[v.Tag()] = true
	}
}

// The algebra's own sites carry the layout too, and it discriminates there:
// docs/ARRAY-SHAPES.md §6 says a reduction along the last axis walks
// contiguous storage, which is true of a packed receiver and false of the
// same reduction over a transpose of it.
func TestTheAlgebraSitesCarryTheirReceiversLayout(t *testing.T) {
	p := lowerPipelineSrc(t, `import "std/ndarray";
function add(x: i64, y: i64): i64 { return x + y; }
function run(a: ndarray.NdArray[i64]): i64 {
  var packed: ndarray.NdArray[i64] = a.packed();
  var r1: ndarray.NdArray[i64] = packed.reduce_axis(1, 0 as i64, add);
  var r2: ndarray.NdArray[i64] = packed.transpose().reduce_axis(1, 0 as i64, add);
  return r1.get([0]) + r2.get([0]);
}
function main(): i32 {
  return run(ndarray.from_flat([1 as i64, 2 as i64, 3 as i64, 4 as i64], [2, 2])) as i32;
}`)
	got := ir.FormatArrayPipelines(p)
	for _, want := range []string{
		"reduce_axis(axis 1)  over packed     add [i64 add]  -> kernel-candidate",
		"reduce_axis(axis 1)  over strided    add [i64 add]  -> kernel-candidate",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("the algebra section is missing %q:\n%s", want, got)
		}
	}
}

// §7's operations each return `from_flat` of a buffer they just filled, so
// their results are packed and the to_flat after one is free. The program's
// own assertion is the cross-check: `examples/tests/ndarray_test.fern` asks
// `m.is_packed()` at run time where `m` is the result of a map, and this is
// the same fact proved before it runs.
func TestAnAlgebraResultIsPacked(t *testing.T) {
	p := lowerPipelineSrc(t, `import "std/ndarray";
function dbl(x: i64): i64 { return x * (2 as i64); }
function run(a: ndarray.NdArray[i64]): i64 {
  var m: ndarray.NdArray[i64] = a.transpose().map(dbl);
  return m.to_flat()[0];
}
function main(): i32 {
  return run(ndarray.from_flat([1 as i64, 2 as i64, 3 as i64, 4 as i64], [2, 2])) as i32;
}`)
	got := layoutSites(t, p, "run")
	if len(got) != 1 {
		t.Fatalf("found %d sites in run, want 1: %+v", len(got), got)
	}
	if got[0].Receiver != ir.NdarrayLayoutPacked || got[0].Verdict != ir.NdarrayCopyMetadata {
		t.Errorf("to_flat of a map over a STRIDED receiver = %s / %s, want packed / metadata: the map allocates its own packed result",
			got[0].Receiver.Tag(), got[0].Verdict.Tag())
	}
}

// A handle from a helper reads what the helper returns, so the spelling a
// program happens to use stops mattering: `examples/tests/ndarray_test.fern`
// builds every handle in a `grid()` whose body is one `from_flat`, and its
// sites now read `packed` exactly as the inline spelling does.
func TestAHandleFromAHelperTakesTheHelpersLayout(t *testing.T) {
	p := lowerPipelineSrc(t, `import "std/ndarray";
function grid(): ndarray.NdArray[i32] { return ndarray.from_flat([1, 2, 3, 4], [2, 2]); }
function run(): i32 { return grid().to_flat()[0]; }
function inlined(): i32 { return ndarray.from_flat([1, 2, 3, 4], [2, 2]).to_flat()[0]; }
function main(): i32 { return run() + inlined(); }`)
	viaCall := layoutSites(t, p, "run")
	inline := layoutSites(t, p, "inlined")
	if len(viaCall) != 1 || len(inline) != 1 {
		t.Fatalf("found %d and %d sites, want 1 each: %+v %+v", len(viaCall), len(inline), viaCall, inline)
	}
	if viaCall[0].Receiver != ir.NdarrayLayoutPacked || viaCall[0].Verdict != ir.NdarrayCopyMetadata {
		t.Errorf("a handle returned by a helper = %s / %s, want packed / metadata",
			viaCall[0].Receiver.Tag(), viaCall[0].Verdict.Tag())
	}
	if inline[0].Receiver != viaCall[0].Receiver {
		t.Errorf("the helper reads %s where the same expression inline reads %s; the two should agree",
			viaCall[0].Receiver.Tag(), inline[0].Receiver.Tag())
	}
}

// A helper's layout is the MEET of its returns, so two returns that disagree
// claim nothing — the summary may not pick the arm a caller happens to want.
func TestAHelperWhoseReturnsDisagreeClaimsNothing(t *testing.T) {
	p := lowerPipelineSrc(t, `import "std/ndarray";
function pick(flip: boolean, a: ndarray.NdArray[i32]): ndarray.NdArray[i32] {
  if (flip) { return ndarray.from_flat([1, 2, 3, 4], [2, 2]); }
  return a.transpose();
}
function run(a: ndarray.NdArray[i32]): i32 { return pick(true, a).to_flat()[0]; }
function main(): i32 { return run(ndarray.from_flat([1, 2, 3, 4], [2, 2])); }`)
	got := layoutSites(t, p, "run")
	if len(got) != 1 {
		t.Fatalf("found %d sites in run, want 1: %+v", len(got), got)
	}
	// packed on one arm and strided on the other meet at strided: the
	// weaker of the two, never the arm the call site took.
	if got[0].Receiver != ir.NdarrayLayoutStrided {
		t.Errorf("a helper returning packed on one arm and strided on the other = %s, want strided",
			got[0].Receiver.Tag())
	}
	if got[0].Verdict != ir.NdarrayCopyNotProvenPacked {
		t.Errorf("verdict = %s, want not-proven-packed", got[0].Verdict.Tag())
	}
}

// A helper that returns a transpose is a strided producer, so the layout
// travels rather than only `packed` doing so.
func TestAHelperCanReturnAStridedHandle(t *testing.T) {
	p := lowerPipelineSrc(t, `import "std/ndarray";
function flip(a: ndarray.NdArray[i32]): ndarray.NdArray[i32] { return a.transpose(); }
function run(a: ndarray.NdArray[i32]): i32 { return flip(a).to_flat()[0]; }
function main(): i32 { return run(ndarray.from_flat([1, 2, 3, 4], [2, 2])); }`)
	got := layoutSites(t, p, "run")
	if len(got) != 1 {
		t.Fatalf("found %d sites in run, want 1: %+v", len(got), got)
	}
	if got[0].Receiver != ir.NdarrayLayoutStrided {
		t.Errorf("a helper returning a transpose = %s, want strided", got[0].Receiver.Tag())
	}
}

// A recursive helper claims nothing and does not hang: the cycle reads
// Unknown rather than recursing, which is what bounds the summary.
func TestARecursiveHelperClaimsNothing(t *testing.T) {
	p := lowerPipelineSrc(t, `import "std/ndarray";
function grow(n: i32, a: ndarray.NdArray[i32]): ndarray.NdArray[i32] {
  if (n <= 0) { return a; }
  return grow(n - 1, a.transpose());
}
function run(a: ndarray.NdArray[i32]): i32 { return grow(3, a).to_flat()[0]; }
function main(): i32 { return run(ndarray.from_flat([1, 2, 3, 4], [2, 2])); }`)
	got := layoutSites(t, p, "run")
	if len(got) != 1 {
		t.Fatalf("found %d sites in run, want 1: %+v", len(got), got)
	}
	if got[0].Receiver.ProvesPacked() {
		t.Errorf("a recursive helper claimed %s; it returns its own parameter on one arm and may be anything",
			got[0].Receiver.Tag())
	}
}
