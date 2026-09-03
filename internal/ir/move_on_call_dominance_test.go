package ir_test

import (
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/ir"
)

// Move-on-call claims an `own` param WHOLE-FUNCTION from its textually-last
// occurrence, and the claim silences the exit sweep on every path. That is only
// sound where the transfer DOMINATES every exit, which nothing checked: on
//
//	if (…) { s = S { …s, code: f(s.code) }; return s; }
//	return g(s);                 // last occurrence — but not on the path above
//
// the first path hands nothing away and still loses its sweep, so it leaks one
// box per call. The leak is silent and the damage is elsewhere: the stale box
// keeps a reference to every rc-tracked field, so the next append to that field
// sees the buffer at rc 2 and copies the whole thing. That is what put the x86
// assembler's 16 MB `.text` buffer through a 32 MB copy every other instruction
// and exhausted the 16 GiB arena (#8146).
//
// Both halves are pinned. A guard that claimed nothing would pass the first
// case forever, so the dominating transfer must still be claimed.
func TestMoveOnCallNeedsDominatingTransfer(t *testing.T) {
	dumps := map[string]string{}
	ir.RcPlanHook = func(fn, dump string) { dumps[fn] = dump }
	defer func() { ir.RcPlanHook = nil }()

	lowerForTest(t, `struct S { code: i32[], n: i32 }
function push(buf: i32[], v: i32): i32[] { return buf.append(v); }
function bump(own s: S, v: i32): S { return S { ...s, n: s.n + v }; }
function branchy(own s: S, v: i32): S {
	if (v > 0) {
		s = S { ...s, code: push(s.code, v) };
		return s;
	}
	return bump(s, v);
}
function straight(own s: S, v: i32): S {
	var t: S = bump(s, v);
	return S { ...t, n: t.n + 1 };
}
function main(): i32 {
	var a: S = branchy(S { code: [], n: 0 }, 1);
	var b: S = straight(S { code: [], n: 0 }, 1);
	return a.n + b.n;
}`)

	if got := dumps["branchy"]; strings.Contains(got, "movedLocals: s") {
		t.Errorf("branchy claims `s` moved whole-function, but the transfer at `bump(s, v)` "+
			"is not on the `return s` path — that path then leaks its box and every field "+
			"reference with it; dump:\n%s", got)
	}
	// Anti-vacuity: the transfer here runs before any return, so it does
	// dominate and must keep the claim (dropping it costs ownArgNeedsRetain's
	// compensating retain on a hot accumulator — the #6125 regression).
	if got := dumps["straight"]; !strings.Contains(got, "movedLocals: s") {
		t.Errorf("straight does NOT claim `s` moved, but `bump(s, v)` runs on every path to "+
			"every exit; the claim is what keeps the transfer from paying a retain; dump:\n%s", got)
	}
	if len(dumps) == 0 {
		t.Fatal("RcPlanHook never fired")
	}
}
