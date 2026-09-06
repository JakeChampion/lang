package ir

import (
	"testing"

	"github.com/jakechampion/lang/internal/ast"
	"github.com/jakechampion/lang/internal/checker"
	"github.com/jakechampion/lang/internal/parser"
)

// A field-receiver append gives up its in-place grow when the container is
// also read into a binding that could still name it afterwards
// (fieldPlaceMutationCopies, #6665). "Could" is the whole question: reading the
// container into a CALL ARGUMENT names it only if the callee lets it out, and
// inferParamEscapes already answers that per parameter.
//
// The three functions below are the same shape — read `o`, bind the result,
// then append to `o.xs` — and differ only in what the callee does with its
// parameter. Getting the summary wrong in either direction is expensive: too
// optimistic is a use-after-free, too pessimistic makes every self-host
// assembler's accumulator threading quadratic (#8190 was 0.9 GB of copying on
// one arm64 compile).
func TestFieldPlaceAppendCopiesConsultsEscapeSummary(t *testing.T) {
	src := `struct S { xs: i32[], n: i32 }

// Returns a container it built itself — nothing of ` + "`s`" + ` leaves.
function fresh(s: S): i32[] { var out: i32[] = [s.n]; return out; }
// Hands the parameter straight back.
function pass(s: S): S { return s; }
// Returns a PROJECTION of the parameter — a different object by the escape
// summary's reckoning (returnedCountedProjection), but the same buffer.
function peel(s: S): i32[] { return s.xs; }
// Stores the parameter in a Map it returns.
function stash(s: S): Map[i32, S] { var m: Map[i32, S] = map_new(4); return m.insert(0, s); }

// Unmarked: fresh() cannot leak o, so ` + "`q`" + ` names nothing of it.
function noescape_call(o: S, i: i32): S {
    var q: i32[] = fresh(o);
    o = S { ...o, xs: o.xs.append(i + q.len()) };
    return o;
}
// Marked: pass() returns the parameter, so ` + "`keep`" + ` is o.
function escaping_call(o: S, i: i32): S {
    var keep: S = pass(o);
    o = S { ...o, xs: o.xs.append(i) };
    return S { ...o, n: keep.n + o.xs.len() };
}
// Marked: peel() hands back o.xs itself. inferParamEscapes excuses a returned
// counted projection as "not a flow-out", so the escape summary alone says
// nothing of o leaves — but the result IS the buffer the append would grow.
function projection_call(o: S, i: i32): S {
    var xs: i32[] = peel(o);
    o = S { ...o, xs: o.xs.append(i + xs.len()) };
    return o;
}
// Marked: the Map handle carries o even though the binding is not
// pointer-shaped by IsPointerType.
function map_call(o: S, i: i32): S {
    var m: Map[i32, S] = stash(o);
    o = S { ...o, xs: o.xs.append(i + m.len()) };
    return o;
}
function main(): i32 { return 0; }`

	prog, err := parser.Parse(src)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	info, err := checker.Check(prog)
	if err != nil {
		t.Fatalf("check: %v", err)
	}
	// Both halves, as the builder's callArgNoEscape uses them: the summary
	// alone excuses a returned projection, so it cannot answer this on its own.
	esc := inferParamEscapes(prog, info, nil, nil)
	proj := findReturnsParamProjection(prog)
	noEsc := func(c *ast.Call, argIdx int) bool {
		id, ok := c.Callee.(*ast.Ident)
		if !ok {
			return false
		}
		if proj[id.Name] {
			return false
		}
		e, known := esc[id.Name]
		return known && argIdx < len(e) && !e[argIdx]
	}

	want := map[string]int{"noescape_call": 0, "escaping_call": 1, "projection_call": 1, "map_call": 1}
	seen := map[string]bool{}
	for _, fn := range prog.Funcs {
		n, tracked := want[fn.Name]
		if !tracked || fn.Body == nil {
			continue
		}
		seen[fn.Name] = true
		if got := len(fieldPlaceMutationCopies(fn.Body, noEsc)); got != n {
			t.Errorf("%s: %d appends forced to copy, want %d", fn.Name, got, n)
		}
		// Without the summary every one of them copies, which is what says
		// the difference above comes from the summary and not from the shape.
		if got := len(fieldPlaceMutationCopies(fn.Body, nil)); got != 1 {
			t.Errorf("%s: %d appends forced to copy with no summary, want 1", fn.Name, got)
		}
	}
	for name := range want {
		if !seen[name] {
			t.Errorf("%s was never checked — the source no longer declares it", name)
		}
	}
}
