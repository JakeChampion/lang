package ir

import (
	"testing"

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
//   - a bare `return p`. On a consumed-threaded p the entry retain is the
//     count move-on-return transfers out; on a borrowed one (`handout`) the
//     return-transfer inc is.
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
    let base: string = "0123456789abcdef";
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
	got := inferParamCountedRetain(prog, info)
	for _, tc := range []struct {
		fn   string
		want bool
		why  string
	}{
		{"bump", true, "every occurrence is a write of its own slot, a concat operand or a pure read"},
		{"pick", true, "the write plus a `return a` whose reference is the entry retain's"},
		{"handout", true, "a bare `return a` on a BORROWED param takes the return-transfer inc"},
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
