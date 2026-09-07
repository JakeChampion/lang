package ir

import (
	"testing"

	"github.com/jakechampion/lang/internal/ast"
	"github.com/jakechampion/lang/internal/checker"
	"github.com/jakechampion/lang/internal/parser"
)

// A string parameter the body REASSIGNS is consumed-threaded, and the entry
// retain that promotion takes is what makes the callee self-balancing: it
// never spends the caller's reference. inferParamCountedRetain has to say so,
// because computeFreeEligible's native single-word string-arg taint reads it —
// and while it said no, the CALLER's local was stranded. `bump(base, "XYZ")`
// on `bump(a, s) { a = a + s; return a.len(); }` leaked base's whole buffer,
// once per local, with no dependence on how often it was called (#8785).
//
// The two occurrence kinds the tier was missing:
//
//   - the parameter as an assignment DESTINATION. A write names the slot, not
//     the buffer; the reference it discards is the frame's own.
//   - a bare `return p` — but ONLY where p is consumed-threaded, since then
//     the entry retain is the count move-on-return transfers out. `handout`
//     below is the control: the same `return a` on a borrowed parameter hands
//     out the CALLER's reference and must keep its refusal.
const consumedStrParamSrc = `
function bump(a: string, s: string): i32 {
    a = a + s;
    a = a + s;
    return a.len();
}
function pick(a: string, s: string, keep: boolean): string {
    a = s + s;
    if (keep) { return a; }
    return "fixed";
}
function handout(a: string, s: string): string {
    return a;
}
function main(): i32 {
    var base: string = "0123456789abcdef";
    return bump(base, "X") + pick(base, "Y", true).len() + handout(base, "Z").len();
}`

func TestReassignedStringParamIsCountedRetain(t *testing.T) {
	prog, err := parser.Parse(consumedStrParamSrc)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	info, err := checker.Check(prog)
	if err != nil {
		t.Fatalf("check: %v", err)
	}
	got := inferParamCountedRetain(prog, info, nil)
	for _, tc := range []struct {
		fn   string
		want bool
		why  string
	}{
		{"bump", true, "every occurrence is a write of its own slot, a concat operand or a pure read"},
		{"pick", true, "the write plus a `return a` whose reference is the entry retain's"},
		{"handout", false, "a bare `return a` on a BORROWED param hands out the caller's reference"},
	} {
		flags := got[tc.fn]
		if len(flags) == 0 {
			t.Fatalf("%s: no summary entry", tc.fn)
		}
		if flags[0] != tc.want {
			t.Errorf("%s param 0: counted-retain=%v, want %v — %s", tc.fn, flags[0], tc.want, tc.why)
		}
	}
}

// consumedStringParams is a PROJECTION of computeConsumedParams, not a second
// implementation of it, and this pins the two together the way
// TestConsumedArrayPositionsMatchTheLoweringVerdict pins the array one.
func TestConsumedStringParamsMatchTheLoweringVerdict(t *testing.T) {
	src := consumedStrParamSrc + `
function reassigned_then_read(a: string, s: string): i32 { a = s; return a.len(); }
function borrowed_only(a: string): i32 { return a.len(); }`
	prog, err := parser.Parse(src)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	info, err := checker.Check(prog)
	if err != nil {
		t.Fatalf("check: %v", err)
	}
	ip := lowerSourceWith(t, src, 8)
	for _, fn := range ip.Funcs {
		decl := declByName(prog, fn.Name)
		if decl == nil {
			continue
		}
		want := consumedStringParams(decl, info, map[string]bool{})
		for i, p := range decl.Params {
			if _, isStr := p.Type.(ast.StringType); !isStr || p.Own {
				continue
			}
			if i >= len(fn.ParamConsumed) {
				continue
			}
			if fn.ParamConsumed[i] != want[p.Name] {
				t.Errorf("%s param %d (%s): lowering says consumed=%v, the "+
					"whole-program projection says %v — the two have drifted",
					fn.Name, i, p.Name, fn.ParamConsumed[i], want[p.Name])
			}
		}
	}
}
