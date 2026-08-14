package ir

import (
	"testing"

	"github.com/jakechampion/lang/internal/checker"
	"github.com/jakechampion/lang/internal/parser"
)

// fieldPlaceAppendCopies decides, per function, which field-receiver appends
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

function mark(s: S): i32 { return s.xs.len(); }
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
	}
	seen := map[string]bool{}
	for _, fn := range prog.Funcs {
		n, tracked := want[fn.Name]
		if !tracked || fn.Body == nil {
			continue
		}
		seen[fn.Name] = true
		if got := len(fieldPlaceAppendCopies(fn.Body)); got != n {
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
