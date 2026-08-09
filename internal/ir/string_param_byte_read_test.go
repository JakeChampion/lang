package ir

import (
	"testing"

	"github.com/jakechampion/lang/internal/ast"
	"github.com/jakechampion/lang/internal/checker"
	"github.com/jakechampion/lang/internal/parser"
)

// A string parameter that is only READ — `p.len()`, `p[i]`, `p[a:b]` — retains
// nothing, so inferParamCountedRetain must credit it and computeFreeEligible
// must stop tainting the caller's binding of that function's result.
//
// The byte-index and slice reads were missing from stringParamCounted while its
// struct sibling (structParamProjectionsSafe) already credited exactly the same
// reads one field deep. The consequence was not a missing optimisation but a
// leak: with the param uncredited, the caller's `var f = table(p)` stayed
// taint-ineligible, so the exit sweep emitted the dec-only __fern_rc_dec rather
// than the freeing __fern_arr_dec and the array was never reclaimed. A
// KMP-shaped search leaked its failure table once per call, and a
// Boyer-Moore-shaped one its whole 256-entry bad-character table — measured at
// 32 B and 1 552 B per call, both unbounded.
func paramCountedFor(t *testing.T, src, fn string) []bool {
	t.Helper()
	prog, err := parser.Parse(src)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	info, err := checker.Check(prog)
	if err != nil {
		t.Fatalf("check: %v", err)
	}
	return inferParamCountedRetain(prog, info)[fn]
}

func TestStringParamByteReadIsCounted(t *testing.T) {
	cases := []struct {
		name string
		body string
	}{
		{"byte index", `var n: i32 = 0; if (p[0] == p[1]) { n = 1; } return n;`},
		{"index in a loop", `var n: i32 = 0; var i: i32 = 0;
             while (i < p.len()) { if (p[i] == 97) { n = n + 1; } i = i + 1; }
             return n;`},
		{"slice source", `return p[0:2].len();`},
		{"len only, the case that already worked", `return p.len();`},
	}
	for _, c := range cases {
		src := "function reads(p: string): i32 { " + c.body + " }\nfunction main(): i32 { return 0; }"
		got := paramCountedFor(t, src, "reads")
		if len(got) != 1 || !got[0] {
			t.Errorf("%s: paramCountedRetain[reads] = %v, want [true] — a read that "+
				"yields a scalar (or copies bytes out) retains nothing", c.name, got)
		}
	}
}

// The direction that would be a use-after-free rather than a leak: a parameter
// the callee actually RETAINS must stay uncredited, so the caller keeps the
// conservative taint and does not free a buffer the result still points at.
func TestStringParamThatIsRetainedStaysUncredited(t *testing.T) {
	cases := []struct {
		name string
		src  string
	}{
		{"returned bare", `function keep(p: string): string { return p; }`},
		{"stored in an array and returned", `function keep(p: string): string[] {
            var out: string[] = [];
            out = out.append(p);
            return out;
        }`},
		{"bound to a local", `function keep(p: string): i32 {
            var s: string = p;
            return s.len();
        }`},
	}
	for _, c := range cases {
		src := c.src + "\nfunction main(): i32 { return 0; }"
		got := paramCountedFor(t, src, "keep")
		if len(got) == 1 && got[0] {
			t.Errorf("%s: paramCountedRetain[keep] = [true], but the callee retains "+
				"its parameter — crediting it lets the caller free a live buffer", c.name)
		}
	}
}

// The end-to-end consequence, at the op level: the caller's binding of an array
// returned by a byte-reading callee must be released by the FREEING helper. The
// distinction matters because __fern_rc_dec only decrements — it never returns
// the buffer — so the dec-only form is exactly what an unbounded leak looks
// like in the IR.
func TestCallerFreesArrayFromAByteReadingCallee(t *testing.T) {
	src := `function table(p: string): i32[] {
    var f: i32[] = [];
    f = f.append(0);
    var k: i32 = 0;
    var i: i32 = 1;
    while (i < p.len()) {
        if (p[i] != p[0]) { k = f[k]; }
        f = f.append(k);
        i = i + 1;
    }
    return f;
}
function search(p: string): i32 {
    var f: i32[] = table(p);
    var s: i32 = 0;
    var j: i32 = 0;
    while (j < f.len()) { s = s + f[j]; j = j + 1; }
    return s;
}
function main(): i32 { return 0; }`
	for _, ptrW := range []int{4, 8} {
		p := lowerSourceWith(t, src, ptrW)
		fn := findFunc(p, "search")
		if n := countCallDirect(fn.Ops, "__fern_arr_dec"); n == 0 {
			t.Errorf("ptrW=%d: search never calls __fern_arr_dec — the table it bound is "+
				"dec'd but never freed, one array leaked per call; ops:\n%s", ptrW, p)
		}
	}
}

// Free-off lowering is unaffected: the credit only ever removes a taint that
// gates reclamation, and reclamation is compiled out here.
func TestStringParamByteReadCreditIsInertWithFreeOff(t *testing.T) {
	defer func(prev bool) { ast.RcFreeEnabled = prev }(ast.RcFreeEnabled)
	ast.RcFreeEnabled = false
	src := `function table(p: string): i32[] {
    var f: i32[] = [];
    var i: i32 = 0;
    while (i < p.len()) { f = f.append(p[i] as i32); i = i + 1; }
    return f;
}
function search(p: string): i32 { var f: i32[] = table(p); return f.len(); }
function main(): i32 { return 0; }`
	for _, ptrW := range []int{4, 8} {
		p := lowerSourceWith(t, src, ptrW)
		fn := findFunc(p, "search")
		if n := countCallDirect(fn.Ops, "__fern_arr_dec"); n != 0 {
			t.Errorf("ptrW=%d: search emitted %d frees with reclamation off; ops:\n%s", ptrW, n, p)
		}
	}
}
