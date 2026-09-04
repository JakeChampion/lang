package ir

import (
	"sort"
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/checker"
	"github.com/jakechampion/lang/internal/parser"
)

// The #4873 containment bracket retains a struct argument's array-field buffer
// across a call whenever the caller's binding survives it, which puts the
// callee's append on the copy path. When every surviving use of that binding
// is a call that provably cannot reach the field, the bracket protects nothing
// and costs one full-buffer copy per call — the shape that dominates the
// self-host append-cliff baseline (`irlower.LowerState.emit`, reached through
// `lower_view_borrowed_parked`'s `s`, whose only later uses are predicates
// that read no `ops`).
//
// Both directions are pinned: an argument whose later uses cannot read `ops`
// loses the bracket, and one whose later use can — directly, or through a
// callee that does — keeps it.
func TestGrowBracketSkipsUnobservedParamField(t *testing.T) {
	src := `struct St { ops: i32[], names: string[], ctrl: i32 }
function (s: St) emit(v: i32): St {
    return St { ...s, ops: s.ops.append(v), ctrl: s.ctrl + 1 };
}
function depth(s: St): i32 { return s.ctrl; }
function opcount(s: St): i32 { return s.ops.len(); }
function depth2(s: St): i32 { return depth(s); }
function opcount2(s: St): i32 { return opcount(s); }

// Every later use of 's' reads a scalar field only, so nothing in this frame
// can observe the buffer emit grows.
function unobserved(s: St, v: i32): i32 {
    var p: St = s.emit(v);
    return p.ctrl + depth(s);
}
// Same, one call deeper — the summary has to close over calls.
function unobserved_indirect(s: St, v: i32): i32 {
    var p: St = s.emit(v);
    return p.ctrl + depth2(s);
}
// The later use reads ops through the binding: the bracket has to stay.
function observed(s: St, v: i32): i32 {
    var p: St = s.emit(v);
    return p.ctrl + opcount(s);
}
// The later use reaches ops one call deeper.
function observed_indirect(s: St, v: i32): i32 {
    var p: St = s.emit(v);
    return p.ctrl + opcount2(s);
}
// The later use names the field itself.
function observed_direct(s: St, v: i32): i32 {
    var p: St = s.emit(v);
    return p.ctrl + s.ops.len();
}
// An unmodelled use — the binding is aliased, and the alias can read anything.
function aliased(s: St, v: i32): i32 {
    var p: St = s.emit(v);
    var q: St = s;
    return p.ctrl + q.ops.len();
}
function main(): i32 { return 0; }`

	for _, ptrW := range []int{4, 8} {
		prog := lowerSourceWith(t, src, ptrW)
		for _, tc := range []struct {
			fn   string
			want bool
		}{
			{"unobserved", false},
			{"unobserved_indirect", false},
			{"observed", true},
			{"observed_indirect", true},
			{"observed_direct", true},
			{"aliased", true},
		} {
			// The bracket is an rc-inc/rc-dec PAIR around the call, and it
			// is the only rc-dec any of these bodies emits; the lone inc
			// they all carry is the result binding's alias retain.
			got := countRcDecs(prog, tc.fn) > 0
			if got != tc.want {
				t.Errorf("ptrW=%d: %s bracketed=%v, want %v", ptrW, tc.fn, got, tc.want)
			}
		}
	}
}

// countRcDecs counts the OpRcDec ops in a function — the closing half of the
// #4873 containment bracket.
func countRcDecs(p *Program, fnName string) int {
	n := 0
	for _, fn := range p.Funcs {
		if fn.Name != fnName {
			continue
		}
		for _, op := range fn.Ops {
			if op.Kind == OpRcDec {
				n++
			}
		}
	}
	return n
}

// The other two deaths #4873's bracket gained with the field-granular one: a
// read that is last on every path REACHING it (the self-host lowering is a
// chain of `if (…) { … return …; }` branches, so its threading reads always
// have textually later company they can never run with), and a local unpacked
// from a call result's field, which is the only name that field's buffer has.
//
// Both directions again: the divergent branch's read dies, the same read in a
// branch that falls through does not, and an unpack whose container is read
// again at the SAME field does not.
func TestCallArgDeathsPathLastAndUnpack(t *testing.T) {
	src := `struct St { ops: i32[], ctrl: i32 }
struct Pair { state: St, slot: i32 }
function mk(): St { return St { ops: [], ctrl: 0 }; }
function (s: St) emit(v: i32): St {
    return St { ...s, ops: s.ops.append(v), ctrl: s.ctrl + 1 };
}
function park(s: St, v: i32): Pair { return Pair { state: s.emit(v), slot: v }; }

// The read is in a branch that returns, and the textually later reads are on
// paths it cannot reach.
function branch_returns(s: St, k: i32): i32 {
    if (k > 0) {
        var a: St = s.emit(k);
        return a.ctrl;
    }
    var b: St = s.emit(0);
    return b.ctrl;
}
// The same first read, but the branch falls through to a later read of s.
function branch_falls_through(s: St, k: i32): i32 {
    var t: i32 = 0;
    if (k > 0) {
        var a: St = s.emit(k);
        t = a.ctrl;
    }
    var b: St = s.emit(0);
    return b.ctrl + t;
}
// 'sl' is the only name for the buffer in p.state, and p is read only at its
// other field afterwards.
function unpack(s: St, k: i32): i32 {
    var p: Pair = park(s, k);
    var sl: St = p.state;
    var r: St = sl.emit(k + 1);
    return r.ctrl + p.slot;
}
// p.state is read again, so sl is not the only name for it.
function unpack_reread(s: St, k: i32): i32 {
    var p: Pair = park(s, k);
    var sl: St = p.state;
    var r: St = sl.emit(k + 1);
    return r.ctrl + p.state.ops.len();
}
function main(): i32 { return branch_returns(mk(), 1) + branch_falls_through(mk(), 1) +
    unpack(mk(), 1) + unpack_reread(mk(), 1); }`

	prog, err := parser.Parse(src)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	info, err := checker.Check(prog)
	if err != nil {
		t.Fatalf("check: %v", err)
	}
	obs := computeParamFieldObs(prog, nil)
	want := map[string]string{
		// Both reads of `s` die: the first is last on its own (returning)
		// path, the second is textually last. `s.ops` rides along on the
		// second, where the first read is excluded as unreachable-with-it.
		// `k` is the scalar argument the existing shapes already claim.
		"branch_returns": "k,s,s,s.ops",
		// Only the second read: the first has a reachable later one, so
		// neither the whole-name nor the field death is available to it.
		"branch_falls_through": "k,s",
		// `sl` is the unpack, at its last use.
		"unpack": "s,s.ops,sl",
		// p.state is read again, so `sl` keeps its bracket.
		"unpack_reread": "s,s.ops",
	}
	seen := map[string]bool{}
	for _, fn := range prog.Funcs {
		exp, tracked := want[fn.Name]
		if !tracked || fn.Body == nil {
			continue
		}
		seen[fn.Name] = true
		var got []string
		for _, names := range callArgDeaths(fn, info, obs) {
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
