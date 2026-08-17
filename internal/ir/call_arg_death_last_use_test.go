package ir

import (
	"sort"
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/checker"
	"github.com/jakechampion/lang/internal/parser"
)

// callArgDeaths decides which call arguments skip #4873's containment bracket,
// and the bracket is what forces the callee's grow onto its copy path. So a
// missed death is one full-buffer copy per call — quadratic over a threading
// chain — and a spurious one lets a callee mutate in place a buffer this frame
// still reads through, which is an interp/native divergence.
//
// Each function below is checked by the exact set of (argument name) deaths it
// yields, so both directions are pinned: the last-occurrence shape has to fire
// on the chain links and stay off every binding whose value outlives the call.
func TestCallArgDeathsLastOccurrence(t *testing.T) {
	src := `struct St { ops: i32[], names: string[], ctrl: i32, who: string }
function mk(): St { return St { ops: [], names: [], ctrl: 0, who: "x" }; }
function (s: St) emit(v: i32): St {
    return St { ...s, ops: s.ops.append(v), ctrl: s.ctrl + 1 };
}
function pair(a: St, b: St): i32 { return a.ctrl + b.ctrl; }

// Every link is at its last occurrence, and each intermediate is bound from a
// direct call — the threading chain the shape exists for.
function chain(s: St, k: i32): St {
    var a: St = s.emit(k);
    var b: St = a.emit(k + 1);
    var c: St = b.emit(k + 2);
    return c;
}
// A param whose last read is the call, with an earlier read that puts it out
// of the sole-occurrence shape's reach.
function param_last(p: St): i32 {
    var n: i32 = p.ctrl;
    var r: St = p.emit(1);
    return r.ctrl + n;
}
// NOT the last occurrence: the first call's argument is read again after it.
function read_after(s: St): i32 {
    var a: St = s.emit(1);
    var x: St = a.emit(2);
    var y: St = a.emit(3);
    return x.ctrl + y.ctrl;
}
// An ALIAS initialiser. ` + "`t`" + ` dies at the call, but binding a struct incs
// the box and not the field buffers inside it, so an in-place grow of t.ops is
// still observable through h.
function alias_init(h: St): i32 {
    var t: St = h;
    var r: St = t.emit(1);
    return r.ctrl + h.ops.len();
}
// Inside a loop, one textual read is many dynamic ones: the next iteration
// would observe the previous one's in-place growth.
function in_loop(s: St, n: i32): St {
    var i: i32 = 0;
    while (i < n) {
        var a: St = s.emit(i);
        s = a.emit(i + 1);
        i = i + 1;
    }
    return s;
}
// Twice in the SAME call: the second read would see the first's growth.
function twice_in_call(s: St): i32 {
    var a: St = s.emit(1);
    return pair(a, a);
}
// Read inside a lambda, which runs when the closure is called rather than
// where it is written.
function lambda_capture(s: St): i32 {
    var a: St = s.emit(1);
    var f: () => i32 = () => a.ops.len();
    var r: St = a.emit(2);
    return r.ctrl + f();
}
function main(): i32 { return chain(mk(), 1).ctrl + param_last(mk()) + read_after(mk()) +
    alias_init(mk()) + in_loop(mk(), 2).ctrl + twice_in_call(mk()) + lambda_capture(mk()); }`

	prog, err := parser.Parse(src)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if _, err := checker.Check(prog); err != nil {
		t.Fatalf("check: %v", err)
	}
	// Per function, the multiset of names marked dead across all its calls.
	want := map[string]string{
		// `c` is returned rather than passed on, so no call carries it.
		"chain":      "a,b,s",
		"param_last": "p",
		// The `a.emit(3)` link is a's last occurrence and still qualifies; the
		// `a.emit(2)` one before it does not. `s` is sole-occurrence as before.
		"read_after": "a,s",
		// `h` survives to the len() read; `t` is excluded by its alias init.
		"alias_init":     "",
		"in_loop":        "",
		"twice_in_call":  "s",
		"lambda_capture": "s",
	}
	seen := map[string]bool{}
	for _, fn := range prog.Funcs {
		exp, tracked := want[fn.Name]
		if !tracked || fn.Body == nil {
			continue
		}
		seen[fn.Name] = true
		var got []string
		for _, names := range callArgDeaths(fn) {
			for n := range names {
				got = append(got, n)
			}
		}
		sort.Strings(got)
		if strings.Join(got, ",") != exp {
			t.Errorf("%s: deaths %q, want %q", fn.Name, strings.Join(got, ","), exp)
		}
	}
	for name := range want {
		if !seen[name] {
			t.Errorf("%s was never checked — the source no longer declares it", name)
		}
	}
}
