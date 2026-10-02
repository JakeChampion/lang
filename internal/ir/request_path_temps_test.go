package ir

import (
	"testing"

	"github.com/jakechampion/lang/internal/checker"
	"github.com/jakechampion/lang/internal/parser"
)

// Three shapes the serve loop and the request parser are built from, each
// of which stranded one allocation per request on the Go compiler while the
// self-host build was flat.

// A fresh buffer handed to a pair-form callee whose payload is a struct
// built from a copy of the buffer: `parse(mk())`. The call-level gate
// refuses the callee (its payload can carry a pointer), and the per-argument
// counted admission used to refuse the whole pair-form family, so nothing
// released the temp. The credit is about the callee's body, and the drop it
// ends in leaves the (tag, payload) pair on the stack untouched.
func TestPairFormCalleeReleasesCountedArgTemp(t *testing.T) {
	p := lowerSourceWith(t, `struct S { data: u8[] }
struct R { body: S }
function copy2(buf: u8[]): u8[] { let out: u8[] = __alloc_u8(2); out = out.with(0, buf[0]); out = out.with(1, buf[1]); return out; }
function wrap(bs: u8[]): S { return S { data: bs }; }
function parse(buf: u8[]): Option[R] { if (buf.len() < 2) { return None; } return Some(R { body: wrap(copy2(buf)) }); }
function mk(): u8[] { let out: u8[] = __alloc_u8(3); return out; }
function main(): i32 {
    let t: i32 = 0;
    match (parse(mk())) { Some(r) => { t = t + r.body.data.len(); }, None => { t = t + 100; } }
    return t;
}`, 8)
	if !p.PairForm["parse"] {
		t.Fatal("parse is not pair-form; this test no longer covers the pair-form path")
	}
	if n := countCallDirect(findFunc(p, "main").Ops, "__fern_arr_dec"); n == 0 {
		t.Errorf("main never releases the fresh buffer it hands to parse — the callee borrows it and its payload holds a copy, so nobody does; ops:\n%s", p)
	}
}

// A local seeded from an element of an array reached through a field of a
// parameter, then reassigned: the serve loop's `let buf = c.bufs[at]; buf =
// buf.concat(…)`. The binding takes its inc and the array deep-drops its
// elements, so the seed is counted at both ends exactly as an element read
// out of an array local is; the conservative taint stranded every value the
// slot later held.
func TestElementReadThroughFieldChainIsOwned(t *testing.T) {
	dumps := map[string]string{}
	RcPlanHook = func(fn, dump string) { dumps[fn] = dump }
	defer func() { RcPlanHook = nil }()
	lowerSourceWith(t, `struct C { bufs: u8[][] }
struct W { c: C }
function read(c: C, at: i32): i32 {
    let buf: u8[] = c.bufs[at];
    buf = buf.append(9 as u8);
    return buf.len();
}
function deep(w: W, at: i32): i32 {
    let buf: u8[] = w.c.bufs[at];
    buf = buf.append(9 as u8);
    return buf.len();
}
function main(): i32 {
    let c: C = C { bufs: [[1 as u8, 2 as u8], [3 as u8]] };
    return read(c, 1) + deep(W { c: c }, 0);
}`, 8)
	for _, fn := range []string{"read", "deep"} {
		if !hasPlanName(dumps[fn], "freeEligible", "buf") {
			t.Errorf("%s: buf is not freeEligible — the element seed taints it, so the appended buffer the slot holds at exit is only flat-dec'd; plan:\n%s", fn, dumps[fn])
		}
	}
}

// A parameter whose bytes are copied out by a fresh-result builtin does not
// escape: `body_string` (`string_from_bytes_unchecked(r.body.data)`). The
// escape summary treated the builtin as unknown, so the arm calling it kept
// its payload rather than releasing it, and every request a handler read the
// body of was stranded whole.
func TestParamEscapesThroughFreshResultBuiltin(t *testing.T) {
	prog, err := parser.Parse(`struct S { data: u8[] }
struct R { body: S }
struct W { r: R }
function (r: R) body_string(): string { return string_from_bytes_unchecked(r.body.data); }
function (r: R) wrap(): W { return W { r: r }; }
function main(): i32 { return 0; }`)
	if err != nil {
		t.Fatal(err)
	}
	info, err := checker.Check(prog)
	if err != nil {
		t.Fatal(err)
	}
	esc := inferParamEscapes(prog, info, nil, nil)
	for name, want := range map[string]bool{
		"__method_R_body_string": false,
		"__method_R_wrap":        true,
	} {
		got := false
		if e := esc[name]; len(e) > 0 {
			got = e[0]
		}
		if got != want {
			t.Errorf("paramEscapes[%s][0] = %v, want %v", name, got, want)
		}
	}
}
