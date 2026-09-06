package ir

import (
	"testing"

	"github.com/jakechampion/lang/internal/checker"
	"github.com/jakechampion/lang/internal/parser"
)

// fieldPlaceMutationCopies decides, per function, which field-receiver appends
// have to give up the rc==1 in-place grow (#6665). It is the whole soundness
// argument for that shape and it has to stay tight in both directions: a
// missed site is the silent interp/native divergence the issue reported, and a
// spurious one turns the self-host emitters' accumulator threading quadratic.
// Each function below carries exactly one field-receiver append, so the count
// of marked calls in its body is a 0/1 verdict on that shape.
func TestFieldPlaceAppendCopies(t *testing.T) {
	src := `struct I { tag: i32, data: i32[] }
struct A { b: i32[] }
struct S { xs: i32[], ys: i32[], inner: I, deep: A, n: i32 }

// Marked: the alias sits one nesting level down in the replacing literal and
// is evaluated against the pre-append container.
function nested_alias(o: S, i: i32): S {
    o = S { xs: o.xs.append(i), ys: o.ys, inner: I { tag: i, data: o.xs }, deep: o.deep, n: i };
    return o;
}
// Marked: a flat sibling read of the same place.
function flat_alias(o: S, i: i32): S {
    o = S { xs: o.xs.append(i), ys: o.xs, inner: o.inner, deep: o.deep, n: i };
    return o;
}
// Marked: the whole container reaches the field.
function whole_container(o: S, i: i32): S {
    o = S { xs: o.xs.append(i), ys: o.ys, inner: o.inner, deep: o.deep, n: mark(o) };
    return o;
}
// Marked: a two-hop place aliased at its own depth.
function deep_alias(o: S, i: i32): S {
    o = S { xs: o.xs, ys: o.deep.b, inner: o.inner, deep: A { b: o.deep.b.append(i) }, n: i };
    return o;
}
// Marked: the container is not rebound, so a later statement still reads the
// place through it.
function no_rebind(o: S, i: i32): i32 {
    var q: S = S { xs: o.xs.append(i), ys: o.ys, inner: o.inner, deep: o.deep, n: i };
    return q.xs.len() + o.xs.len();
}
// Marked: the rebinding forwards the NAME, but this alias holds the container.
function aliased_container(o: S, i: i32): i32 {
    var old: S = o;
    o = S { xs: o.xs.append(i), ys: o.ys, inner: o.inner, deep: o.deep, n: i };
    return o.xs.len() + old.xs.len();
}

// Unmarked: sibling reads of DISJOINT fields — the functional-update threading
// shape, which must keep the in-place grow.
function disjoint(o: S, i: i32): S {
    o = S { xs: o.xs.append(i), ys: o.ys, inner: o.inner, deep: o.deep, n: i };
    return o;
}
// Unmarked: the struct-update spread copies every field EXCEPT the one it
// overrides, so it never names the grown buffer.
function spread(o: S, i: i32): S {
    o = S { ...o, xs: o.xs.append(i) };
    return o;
}
// Unmarked: return position. The function exits before anything here reads
// again; what the caller sees is the #4873 bracket's job.
function retpos(o: S, i: i32): S {
    if (o.xs.len() > 100) { return o; }
    return S { xs: o.xs.append(i), ys: o.ys, inner: o.inner, deep: o.deep, n: i };
}
// Unmarked: no read of the place anywhere else at all.
function only_read(o: S, i: i32): i32[] {
    var ys: i32[] = o.xs.append(i);
    return ys;
}
// Unmarked: the container reaches a call, but the call gives back a SCALAR, so
// no name that outlives the read can hold it. The x86 assembler's shape.
function scalar_binding(o: S, i: i32): S {
    var n: i32 = mark(o);
    o = S { ...o, xs: o.xs.append(i + n) };
    return o;
}
// Marked: the same read, bound to something that CAN hold the container.
function container_binding(o: S, i: i32): S {
    var keep: S = pass(o);
    o = S { ...o, xs: o.xs.append(i) };
    return S { ...o, ys: keep.xs };
}
// Marked: a Map binding is not pointer-shaped by IsPointerType, but the handle
// carries the container all the same — bindingHoldsContainer is a whitelist of
// the scalars for exactly this reason.
function map_binding(o: S, i: i32): S {
    var m: Map[i32, S] = stash(o);
    o = S { ...o, xs: o.xs.append(i + m.len()) };
    return o;
}

function mark(s: S): i32 { return s.xs.len(); }
function pass(s: S): S { return s; }
function stash(s: S): Map[i32, S] { var m: Map[i32, S] = map_new(4); return m.insert(0, s); }
function main(): i32 { return 0; }`

	prog, err := parser.Parse(src)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if _, err := checker.Check(prog); err != nil {
		t.Fatalf("check: %v", err)
	}
	want := map[string]int{
		"nested_alias":      1,
		"flat_alias":        1,
		"whole_container":   1,
		"deep_alias":        1,
		"no_rebind":         1,
		"aliased_container": 1,
		"disjoint":          0,
		"spread":            0,
		"retpos":            0,
		"only_read":         0,
		"scalar_binding":    0,
		"container_binding": 1,
		"map_binding":       1,
	}
	seen := map[string]bool{}
	for _, fn := range prog.Funcs {
		n, tracked := want[fn.Name]
		if !tracked || fn.Body == nil {
			continue
		}
		seen[fn.Name] = true
		if got := len(fieldPlaceMutationCopies(fn.Body, nil)); got != n {
			t.Errorf("%s: %d appends forced to copy, want %d", fn.Name, got, n)
		}
	}
	for name := range want {
		if !seen[name] {
			t.Errorf("%s was never checked — the source no longer declares it", name)
		}
	}
}

// The forced copy is the same rc-inc/rc-dec bracket around the grow that the
// bare-ident shape uses, so it has to reach the emitted ops rather than stop at
// the analysis. The two functions differ in exactly one character — `ys: o.xs`
// against `ys: o.ys` — and both spellings are a construction alias inc, so the
// difference in whole-function rc-inc count IS the bracket. A delta of 0 means
// the aliased shape lost its copy (the #6665 wrong answer); a delta above 1
// means the threading shape gained one (the accumulator goes quadratic).
func TestFieldAppendForcedCopyEmitted(t *testing.T) {
	src := `struct S { xs: i32[], ys: i32[], n: i32 }
function alias(o: S, i: i32): i32 {
    o = S { xs: o.xs.append(i), ys: o.xs, n: i };
    return o.xs.len() + o.ys.len();
}
function threaded(o: S, i: i32): i32 {
    o = S { xs: o.xs.append(i), ys: o.ys, n: i };
    return o.xs.len();
}
function main(): i32 { return 0; }`

	for _, ptrW := range []int{4, 8} {
		prog := lowerSourceWith(t, src, ptrW)
		aliased, threaded := countRcIncs(prog, "alias"), countRcIncs(prog, "threaded")
		if aliased-threaded != 1 {
			t.Errorf("ptrW=%d: alias emitted %d rc-incs and threaded %d; want exactly one more "+
				"(the #6665 copy-forcing bracket, and only on the aliased shape)", ptrW, aliased, threaded)
		}
	}
}

// The two arms #8523 brought over from the self-host port: the no-overlap arm
// is SEQUENCED (a read that precedes the site in length or index position
// cannot observe it), and a field-place `.with` is a candidate on the same
// terms as the append beside it. One mutation per function, so the count of
// marked calls is again a 0/1 verdict.
func TestFieldPlaceMutationSequencedReads(t *testing.T) {
	src := `struct S { xs: i32[], ys: i32[], n: i32 }

// Unmarked: both overlapping reads yield a scalar computed before the grow.
function pre_len_index(o: S, i: i32): S {
    var k: i32 = o.xs.len();
    var e: i32 = o.xs[0];
    var zs: i32[] = o.xs.append(i + k + e);
    return S { xs: zs, ys: o.ys, n: k };
}
// Marked: the length read comes AFTER the append and sees the grown buffer.
function post_len(o: S, i: i32): S {
    var zs: i32[] = o.xs.append(i);
    var k: i32 = o.xs.len();
    return S { xs: zs, ys: o.ys, n: k };
}
// Marked: one statement orders nothing within itself.
function same_stmt(o: S, i: i32): S {
    var zs: i32[] = o.xs.append(o.xs.len() + i);
    return S { xs: zs, ys: o.ys, n: i };
}
// Marked: a preceding read that BINDS the buffer rather than a scalar off it.
function pre_bind(o: S, i: i32): S {
    var keep: i32[] = o.xs;
    var zs: i32[] = o.xs.append(i);
    return S { xs: zs, ys: keep, n: i };
}
// Marked: the lambda reads the root when it is CALLED, so a read that precedes
// the site textually does not precede it in time.
function pre_len_lambda(o: S, i: i32): i32 {
    var f: () => i32 = (): i32 => { return o.xs.len(); };
    var zs: i32[] = o.xs.append(i);
    return zs.len() + f();
}
// Unmarked: the hash-index shape — the bucket and the chain link are both read
// before the store that replaces the bucket.
function with_pre_reads(o: S, i: i32): S {
    var bk: i32 = o.xs.len() - 1;
    var e: i32 = o.xs[bk];
    var zs: i32[] = o.xs.with(bk, i + e);
    return S { xs: zs, ys: o.ys, n: bk };
}
// Marked: an admitted .with moves the field out, so a body-scope host inside
// a loop would read the moved-out field on the next pass.
function with_in_loop(o: S, i: i32): i32 {
    var acc: i32 = 0;
    var j: i32 = 0;
    while (j < i) {
        var zs: i32[] = o.xs.with(0, j);
        acc = acc + zs[0];
        j = j + 1;
    }
    return acc;
}
// Unmarked: the same .with under a rebind of its own root, which replaces
// the container the next pass reads.
function with_in_loop_rebind(o: S, i: i32): i32 {
    var j: i32 = 0;
    while (j < i) {
        o = S { ...o, xs: o.xs.with(0, j) };
        j = j + 1;
    }
    return o.xs[0];
}
function main(): i32 { return 0; }`

	prog, err := parser.Parse(src)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if _, err := checker.Check(prog); err != nil {
		t.Fatalf("check: %v", err)
	}
	want := map[string]int{
		"pre_len_index":       0,
		"post_len":            1,
		"same_stmt":           1,
		"pre_bind":            1,
		"pre_len_lambda":      1,
		"with_pre_reads":      0,
		"with_in_loop":        1,
		"with_in_loop_rebind": 0,
	}
	seen := map[string]bool{}
	for _, fn := range prog.Funcs {
		n, tracked := want[fn.Name]
		if !tracked || fn.Body == nil {
			continue
		}
		seen[fn.Name] = true
		if got := len(fieldPlaceMutationCopies(fn.Body, nil)); got != n {
			t.Errorf("%s: %d mutations forced to copy, want %d", fn.Name, got, n)
		}
	}
	for name := range want {
		if !seen[name] {
			t.Errorf("%s was never checked — the source no longer declares it", name)
		}
	}
}

// The admitted field-place .with stores into the field's own buffer, and an
// ANSWER cannot tell that apart from the clone that computes the same thing —
// so the decision is read off the emitted ops. emitCowInplaceFieldMove is the
// only thing in these functions that tests a refcount at run time, so the
// OpRcIsUnique count is the 1/0 witness; a refused site takes the
// arraySetInc path instead, which incs unconditionally and never asks.
func TestFieldPlaceWithInPlaceShape(t *testing.T) {
	src := `struct Inner { xs: i32[] }
struct Outer { inner: Inner, n: i32 }
struct RefSet { names: i32[], head: i32[] }

// Admitted: every read of head precedes the store that replaces one slot.
function refset_add(rs: RefSet, name: i32): RefSet {
    var bk: i32 = name % rs.head.len();
    var names: i32[] = rs.names.append(name);
    var head: i32[] = rs.head.with(bk, names.len() - 1);
    return RefSet { names: names, head: head };
}
// Admitted: a struct this frame builds, whose field outlives the box.
function build(n: i32): i32[] {
    var b: Inner = Inner { xs: [0, 0, 0] };
    var ys: i32[] = b.xs.with(0, n);
    return ys;
}
// Refused: the root is a local naming another container's box.
function alias_root(v: i32): i32[] {
    var o: Outer = Outer { inner: Inner { xs: [1, 2, 3] }, n: 0 };
    var t: Inner = o.inner;
    return t.xs.with(0, v + o.inner.xs[0]);
}
// Refused: a body-scope host inside a loop runs again against the same root.
function loop_host(k: i32): i32 {
    var b: Inner = Inner { xs: [1, 2, 3] };
    var acc: i32 = 0;
    var j: i32 = 0;
    while (j < k) {
        var zs: i32[] = b.xs.with(0, j);
        acc = acc + zs[0];
        j = j + 1;
    }
    return acc;
}
function main(): i32 { return 0; }`

	for _, ptrW := range []int{4, 8} {
		prog := lowerSourceWith(t, src, ptrW)
		want := map[string]int{"refset_add": 1, "build": 1, "alias_root": 0, "loop_host": 0}
		for name, n := range want {
			fn := findFunc(prog, name)
			if fn == nil {
				t.Fatalf("ptrW=%d: %s is gone from the source", ptrW, name)
			}
			if got := countOps(fn, OpRcIsUnique); got != n {
				t.Errorf("ptrW=%d: %s has %d runtime uniqueness tests, want %d (the field .with admission)", ptrW, name, got, n)
			}
		}
	}
}
