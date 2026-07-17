package e2e

import "testing"

// Differential regression for #4873: an in-place array growth inside a
// callee must never be observable through a CALLER's surviving binding.
//
// Function parameters are borrowed (no call-site inc), so a callee's
// rc==1 in-place fast paths (`__method_Array_push`'s grow, the
// #4838-exempted return-position / self-reassign appends, and the
// functional-update field append) saw a uniquely-referenced buffer that
// the caller in fact still owned — growing it in place silently mutated
// the caller's value (interp 22 vs compiled 23 on every shape below).
// The fix is the caller-side containment bracket (computeGrowParams +
// growBracketArgs): a surviving argument at a may-grow position is rc-
// inc'd across the call, so the callee's uniqueness gate takes the copy
// path. Dying args (`a = grow(a, x)` self-reassign, `return f(.., a, ..)`
// return-position) skip the bracket, keeping the #4838 O(n) accumulator
// chains on the in-place fast path.
//
// Each case returns before*10+after where `after` must equal `before`
// (the caller's binding untouched by the observing call). Exit codes are
// pinned to the interpreter's (the value-semantics oracle).

var appendBorrowedParamCases = []struct {
	name string
	src  string
	exit int
}{
	// Bare array param, observing caller (`var c = grow(a, 3)` keeps `a`).
	{"bare-param", `function grow(xs: i32[], x: i32): i32[] {
    return xs.append(x);
}
function main(): i32 {
    var a: i32[] = [];
    a = grow(a, 1);
    a = grow(a, 2);
    var before: i32 = a.len();
    var c: i32[] = grow(a, 3);
    var after: i32 = a.len();
    return before * 10 + after;
}`, 22},
	// Struct receiver method appending a field (the #4873 issue repro,
	// monomorphic form).
	{"struct-field-method", `struct Box { xs: i32[] }
function (b: Box) push(x: i32): Box {
    var ys: i32[] = b.xs.append(x);
    return Box { xs: ys };
}
function main(): i32 {
    var a: Box = Box { xs: [] };
    a = a.push(1);
    a = a.push(2);
    var before: i32 = a.xs.len();
    var c: Box = a.push(3);
    var after: i32 = a.xs.len();
    return before * 10 + after;
}`, 22},
	// NESTED field chain (`s.cur.insts.append` — the EmitState functional-
	// update shape) with a LIVE caller alias: the bracket walks the field
	// path to the buffer.
	{"nested-field-method", `struct Blk { insts: i32[] }
struct St { cur: Blk }
function (s: St) emit(x: i32): St {
    return St { cur: Blk { insts: s.cur.insts.append(x) } };
}
function main(): i32 {
    var s: St = St { cur: Blk { insts: [] } };
    s = s.emit(1);
    s = s.emit(2);
    var before: i32 = s.cur.insts.len();
    var t: St = s.emit(3);
    var after: i32 = s.cur.insts.len();
    return before * 10 + after;
}`, 22},
	// Transitive: the growing append is one call deeper; the middle fn
	// passes its param onward in a dying (return) position, so the summary
	// propagates and the OUTER call site brackets.
	{"transitive", `function grow(xs: i32[], x: i32): i32[] {
    return xs.append(x);
}
function via(xs: i32[], x: i32): i32[] {
    return grow(xs, x);
}
function main(): i32 {
    var a: i32[] = [];
    a = via(a, 1);
    a = via(a, 2);
    var before: i32 = a.len();
    var c: i32[] = via(a, 3);
    var after: i32 = a.len();
    return before * 10 + after;
}`, 22},
	// Return-position borrow: the callee's append result is bound while
	// the caller's binding stays live.
	{"return-borrow", `function tail(acc: i32[], x: i32): i32[] {
    return acc.append(x);
}
function caller(acc: i32[]): i32 {
    var r = tail(acc, 9);
    return r.len() * 10 + acc.len();
}
function main(): i32 {
    var a: i32[] = [];
    a = a.append(1);
    a = a.append(2);
    return caller(a) + 10;
}`, 42},
	// Recursive accumulator (`return walk(acc.append(n), …)`) — the dying
	// shapes stay on the in-place fast path and the result is correct.
	{"recursive-acc", `function walk(acc: i32[], n: i32): i32[] {
    if (n == 0) { return acc; }
    return walk(acc.append(n), n - 1);
}
function main(): i32 {
    var out = walk([], 40);
    return out.len() + 2;
}`, 42},
	// `.with` on a borrowed param: already contained by its own
	// receiver-live machinery (#2832) — pinned here as the sibling
	// regression guard.
	{"with-param", `function poke(xs: i32[], v: i32): i32[] {
    return xs.with(0, v);
}
function main(): i32 {
    var a: i32[] = [];
    a = a.append(5);
    a = a.append(6);
    var c: i32[] = poke(a, 9);
    return a[0] * 10 + c[0];
}`, 59},
}

func TestX86_64AppendBorrowedParamValueSemantics(t *testing.T) {
	for _, tc := range appendBorrowedParamCases {
		t.Run(tc.name, func(t *testing.T) {
			if got := runInterpExit(t, tc.src); got != tc.exit {
				t.Fatalf("interp oracle drifted: got %d, want %d", got, tc.exit)
			}
			if _, got := compileAndRunX86_64(t, tc.src); got != tc.exit {
				t.Errorf("x86-64 exited %d, want %d (callee in-place growth observed through the caller's binding, #4873)", got, tc.exit)
			}
		})
	}
}

func TestArm64AppendBorrowedParamValueSemantics(t *testing.T) {
	for _, tc := range appendBorrowedParamCases {
		t.Run(tc.name, func(t *testing.T) {
			if _, got := compileAndRunArm64(t, tc.src); got != tc.exit {
				t.Errorf("arm64 exited %d, want %d (callee in-place growth observed through the caller's binding, #4873)", got, tc.exit)
			}
		})
	}
}
