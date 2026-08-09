package ir

import (
	"testing"

	"github.com/jakechampion/lang/internal/ast"
)

// A string parameter passed on to a callee that is ITSELF counted-retain in
// that position retains nothing new — whatever the callee does with it is
// already known to be a counted store or a pure read. structParamProjectionsSafe
// has always credited that argument position; stringParamCounted did not, so a
// single FORWARDING frame disqualified the whole chain.
//
// The cost was a leak, not a missed optimisation: with the param uncredited,
// computeFreeEligible kept tainting the CALLER's binding of the argument, so a
// freshly built string handed to a dispatcher was never reclaimed. A validator
// suite that rewrote one byte and re-checked it leaked 752 B a round while the
// inline spelling was flat.
func TestStringParamForwardedToACountedCalleeIsCounted(t *testing.T) {
	src := `function leaf(s: string): i32 { return s.len(); }
function forward(kind: string, s: string): i32 {
    if (kind == "a") { return leaf(s); }
    return leaf(s);
}
function main(): i32 { return 0; }`
	got := paramCountedFor(t, src, "forward")
	if len(got) != 2 {
		t.Fatalf("paramCountedRetain[forward] = %v, want two entries", got)
	}
	if !got[1] {
		t.Errorf("paramCountedRetain[forward][1] = false — `s` is only ever passed to "+
			"`leaf`, whose own parameter is counted-retain, so forwarding it retains "+
			"nothing; got %v", got)
	}
	// The leaf itself was already credited; if it ever stops being, the rule
	// above has nothing to stand on and this test would pass vacuously.
	if leaf := paramCountedFor(t, src, "leaf"); len(leaf) != 1 || !leaf[0] {
		t.Errorf("paramCountedRetain[leaf] = %v, want [true]", leaf)
	}
}

// Transitively: a chain of forwarders is credited, because the summary is a
// least fixpoint that keeps adding credits until it settles.
func TestStringParamForwardingChainIsCounted(t *testing.T) {
	src := `function leaf(s: string): i32 { return s.len(); }
function mid(s: string): i32 { return leaf(s); }
function top(s: string): i32 { return mid(s); }
function main(): i32 { return 0; }`
	for _, fn := range []string{"leaf", "mid", "top"} {
		got := paramCountedFor(t, src, fn)
		if len(got) != 1 || !got[0] {
			t.Errorf("paramCountedRetain[%s] = %v, want [true]", fn, got)
		}
	}
}

// The direction that would be a use-after-free rather than a leak: forwarding
// to a callee that RETAINS the string must stay uncredited, so the caller keeps
// the conservative taint and does not free a buffer the callee stored.
func TestStringParamForwardedToARetainingCalleeStaysUncredited(t *testing.T) {
	cases := []struct {
		name string
		src  string
	}{
		{"callee returns it", `function keep(s: string): string { return s; }
function forward(s: string): string { return keep(s); }`},
		{"callee stores it in an array it returns", `function keep(s: string): string[] {
    var out: string[] = [];
    out = out.append(s);
    return out;
}
function forward(s: string): string[] { return keep(s); }`},
		{"callee binds it to a local", `function keep(s: string): i32 {
    var t: string = s;
    return t.len();
}
function forward(s: string): i32 { return keep(s); }`},
	}
	for _, c := range cases {
		got := paramCountedFor(t, c.src+"\nfunction main(): i32 { return 0; }", "forward")
		if len(got) == 1 && got[0] {
			t.Errorf("%s: paramCountedRetain[forward] = [true], but it forwards to a "+
				"callee that retains the string — crediting it lets the caller free a "+
				"live buffer", c.name)
		}
	}
}

// A cycle with no grounding stays uncredited: the fixpoint starts all-false and
// only adds credits on positive evidence, so mutual recursion cannot bootstrap
// itself into a credit it has not earned.
func TestStringParamMutualRecursionStaysUncredited(t *testing.T) {
	src := `function ping(s: string): string { return pong(s); }
function pong(s: string): string { return ping(s); }
function main(): i32 { return 0; }`
	for _, fn := range []string{"ping", "pong"} {
		if got := paramCountedFor(t, src, fn); len(got) == 1 && got[0] {
			t.Errorf("paramCountedRetain[%s] = [true] from an ungrounded cycle", fn)
		}
	}
}

// Free-off lowering is unaffected: the credit only removes a taint that gates
// reclamation, and reclamation is compiled out here.
func TestStringParamForwardingCreditIsInertWithFreeOff(t *testing.T) {
	defer func(prev bool) { ast.RcFreeEnabled = prev }(ast.RcFreeEnabled)
	ast.RcFreeEnabled = false
	src := `function leaf(s: string): i32 { return s.len(); }
function forward(s: string): i32 { return leaf(s); }
function caller(): i32 {
    var b: string = "a-string-past-the-inline-threshold";
    return forward(b);
}
function main(): i32 { return 0; }`
	for _, ptrW := range []int{4, 8} {
		p := lowerSourceWith(t, src, ptrW)
		fn := findFunc(p, "caller")
		if n := countCallDirect(fn.Ops, "__fern_str_dec"); n != 0 {
			t.Errorf("ptrW=%d: caller emitted %d string frees with reclamation off; ops:\n%s",
				ptrW, n, p)
		}
	}
}
