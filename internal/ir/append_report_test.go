package ir

import (
	"testing"
)

// The append report (#6992) has to answer one question: why is this append
// loop quadratic? A copying append reallocates and copies the whole buffer,
// so a copying append inside a loop is O(n²) bytes — and nothing in the
// emitted code, the exit status, or any existing gate distinguishes it from
// the O(1) in-place form. #4838 was exactly that, found only as an OOM.
//
// What these assert is the DISTINCTION, not a constant: a report that said
// "copies" everywhere, or "in place" everywhere, would answer nothing while
// still looking like a working report.

// sitesOf returns fn's append sites keyed by receiver spelling and order of
// appearance, so a test can name one site without depending on line numbers.
func sitesOf(t *testing.T, p *Program, fn string) []AppendSite {
	t.Helper()
	f := findFunc(p, fn)
	if f == nil {
		t.Fatalf("no function %q in the lowered program", fn)
	}
	return f.AppendSites
}

func TestAppendReportSeparatesInPlaceFromCopying(t *testing.T) {
	// `grow` is the linear form: the only reference to the buffer is
	// overwritten by the assignment, so the grow can mutate in place.
	// `reused` reads its receiver again after the first append, so that
	// one must copy — and the second, at the receiver's last occurrence,
	// must not.
	src := `function grow(n: i32): i32 {
    var xs: i32[] = [];
    var i: i32 = 0;
    while (i < n) { xs = xs.append(i); i = i + 1; }
    return xs.len();
}
function reused(acc: i32[]): i32 {
    var a: i32 = acc.append(1).len();
    var b: i32 = acc.append(2).len();
    return a * 10 + b;
}
function main(): i32 { return grow(3) + reused([1, 2]); }`

	for _, ptrW := range []int{4, 8} {
		prog := lowerSourceWith(t, src, ptrW)

		growSites := sitesOf(t, prog, "grow")
		if len(growSites) != 1 {
			t.Fatalf("ptrW=%d: grow has %d append sites, want 1", ptrW, len(growSites))
		}
		if growSites[0].Copies {
			t.Errorf("ptrW=%d: grow's `xs = xs.append(i)` reported as copying (%s) — "+
				"a self-reassign append is the O(1) form",
				ptrW, growSites[0].Reason)
		}
		if got := growSites[0].Recv; got != "xs" {
			t.Errorf("ptrW=%d: grow's append receiver reported as %q, want \"xs\"", ptrW, got)
		}
		if growSites[0].Func != "grow" {
			t.Errorf("ptrW=%d: site attributed to %q, want \"grow\"", ptrW, growSites[0].Func)
		}
		if growSites[0].Line == 0 {
			t.Errorf("ptrW=%d: grow's append site has no source line", ptrW)
		}

		reusedSites := sitesOf(t, prog, "reused")
		if len(reusedSites) != 2 {
			t.Fatalf("ptrW=%d: reused has %d append sites, want 2", ptrW, len(reusedSites))
		}
		if !reusedSites[0].Copies {
			t.Errorf("ptrW=%d: reused's first append reported as in-place (%s) — "+
				"`acc` is read again after it, so the grow must not be observable",
				ptrW, reusedSites[0].Reason)
		}
		if reusedSites[1].Copies {
			t.Errorf("ptrW=%d: reused's second append reported as copying (%s) — "+
				"it is the receiver's last occurrence",
				ptrW, reusedSites[1].Reason)
		}
		for i, s := range reusedSites {
			if s.Reason == "" {
				t.Errorf("ptrW=%d: reused site %d has no reason", ptrW, i)
			}
		}
	}
}

// The #4849 exemptions are the two shapes whose whole point is that they
// look reused but are not. A report that called them copying would send a
// reader hunting a quadratic that is not there.
func TestAppendReportRecordsExemptShapesAsInPlace(t *testing.T) {
	src := `function retpos(acc: i32[], x: i32): i32[] {
    if (x > 0) { return acc.append(x); }
    return acc.append(0 - x);
}
function selfp(acc: i32[], n: i32): i32 {
    var i: i32 = 0;
    while (i < n) { acc = acc.append(i); i = i + 1; }
    return acc.len();
}
function main(): i32 { return retpos([1], 1).len() + selfp([1], 2); }`

	for _, ptrW := range []int{4, 8} {
		prog := lowerSourceWith(t, src, ptrW)
		for _, fn := range []string{"retpos", "selfp"} {
			sites := sitesOf(t, prog, fn)
			if len(sites) == 0 {
				t.Fatalf("ptrW=%d: %s reported no append sites", ptrW, fn)
			}
			for i, s := range sites {
				if s.Copies {
					t.Errorf("ptrW=%d: %s site %d (%s at %d:%d) reported as copying (%s) — "+
						"this is a #4849-exempt shape and must stay O(1)",
						ptrW, fn, i, s.Recv, s.Line, s.Col, s.Reason)
				}
			}
		}
	}
}

// A field receiver goes through fieldPlaceAppendCopies (#6665) rather than
// the ident path, and the report has to reach that branch too — a receiver
// spelling of "<expr>" there would make the site unmatchable to its source.
func TestAppendReportNamesFieldReceivers(t *testing.T) {
	src := `struct Bag { items: i32[] }
function add(b: Bag, v: i32): i32 {
    var c: Bag = Bag { items: b.items.append(v) };
    return c.items.len() + b.items.len();
}
function main(): i32 { return add(Bag { items: [1] }, 2); }`

	for _, ptrW := range []int{4, 8} {
		prog := lowerSourceWith(t, src, ptrW)
		sites := sitesOf(t, prog, "add")
		if len(sites) != 1 {
			t.Fatalf("ptrW=%d: add has %d append sites, want 1", ptrW, len(sites))
		}
		if got := sites[0].Recv; got != "b.items" {
			t.Errorf("ptrW=%d: field receiver reported as %q, want \"b.items\"", ptrW, got)
		}
	}
}

// The report is derived from the same branches emitArrayPush emits from, so
// a copying site must carry the rc-inc that marks the copy path and an
// in-place one must not. This is what keeps the report from drifting into a
// second, independently-wrong opinion about what the compiler did.
func TestAppendReportAgreesWithEmittedCode(t *testing.T) {
	src := `function reused(acc: i32[]): i32 {
    var a: i32 = acc.append(1).len();
    var b: i32 = acc.append(2).len();
    return a * 10 + b;
}
function selfp(acc: i32[], n: i32): i32 {
    var i: i32 = 0;
    while (i < n) { acc = acc.append(i); i = i + 1; }
    return acc.len();
}
function main(): i32 { return reused([1]) + selfp([1], 2); }`

	for _, ptrW := range []int{4, 8} {
		prog := lowerSourceWith(t, src, ptrW)
		for _, fn := range []string{"reused", "selfp"} {
			copying := 0
			for _, s := range sitesOf(t, prog, fn) {
				if s.Copies {
					copying++
				}
			}
			// The forced copy's signature is the bare rc-inc/rc-dec pair
			// bracketing the grow call; these functions have no other
			// rc-inc source (i32 elements need no element retain).
			if got := countRcIncs(prog, fn); got != copying {
				t.Errorf("ptrW=%d: %s reports %d copying append(s) but emitted %d rc-inc(s) — "+
					"the report disagrees with the code it describes",
					ptrW, fn, copying, got)
			}
		}
	}
}
