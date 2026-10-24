package e2e

import "testing"

var appendBorrowedParamCases = []struct {
	name string
	src  string
	exit int
}{
	{"bare-param", `function grow(xs: i32[], x: i32): i32[] {
    return xs.append(x);
}
function main(): i32 {
    let a: i32[] = [];
    a = grow(a, 1);
    a = grow(a, 2);
    let before: i32 = a.len();
    let c: i32[] = grow(a, 3);
    let after: i32 = a.len();
    return before * 10 + after;
}`, 22},
	{"struct-field-method", `struct Box { xs: i32[] }
function (b: Box) push(x: i32): Box {
    let ys: i32[] = b.xs.append(x);
    return Box { xs: ys };
}
function main(): i32 {
    let a: Box = Box { xs: [] };
    a = a.push(1);
    a = a.push(2);
    let before: i32 = a.xs.len();
    let c: Box = a.push(3);
    let after: i32 = a.xs.len();
    return before * 10 + after;
}`, 22},
	{"nested-field-method", `struct Blk { insts: i32[] }
struct St { cur: Blk }
function (s: St) emit(x: i32): St {
    return St { cur: Blk { insts: s.cur.insts.append(x) } };
}
function main(): i32 {
    let s: St = St { cur: Blk { insts: [] } };
    s = s.emit(1);
    s = s.emit(2);
    let before: i32 = s.cur.insts.len();
    let t: St = s.emit(3);
    let after: i32 = s.cur.insts.len();
    return before * 10 + after;
}`, 22},
	{"transitive", `function grow(xs: i32[], x: i32): i32[] {
    return xs.append(x);
}
function via(xs: i32[], x: i32): i32[] {
    return grow(xs, x);
}
function main(): i32 {
    let a: i32[] = [];
    a = via(a, 1);
    a = via(a, 2);
    let before: i32 = a.len();
    let c: i32[] = via(a, 3);
    let after: i32 = a.len();
    return before * 10 + after;
}`, 22},
	{"return-borrow", `function tail(acc: i32[], x: i32): i32[] {
    return acc.append(x);
}
function caller(acc: i32[]): i32 {
    let r = tail(acc, 9);
    return r.len() * 10 + acc.len();
}
function main(): i32 {
    let a: i32[] = [];
    a = a.append(1);
    a = a.append(2);
    return caller(a) + 10;
}`, 42},
	{"recursive-acc", `function walk(acc: i32[], n: i32): i32[] {
    if (n == 0) { return acc; }
    return walk(acc.append(n), n - 1);
}
function main(): i32 {
    let out = walk([], 40);
    return out.len() + 2;
}`, 42},
	{"renamed-cursor", `struct Blk { insts: i32[] }
function emit(s: Blk, x: i32): Blk { return Blk { insts: s.insts.append(x) }; }
function thread(c0: Blk, x: i32): Blk {
    let c: Blk = c0;
    let open: Blk = emit(c, x);
    return emit(open, x + 1);
}
function main(): i32 {
    let s: Blk = Blk { insts: [] };
    s = thread(s, 1);
    let before: i32 = s.insts.len();
    let t: Blk = thread(s, 3);
    let after: i32 = s.insts.len();
    return before * 10 + after + (t.insts.len() - 4);
}`, 22},
	{"destructure-rebind", `struct Blk { insts: i32[] }
function emit(s: Blk, x: i32): (Blk, i32) { return (Blk { insts: s.insts.append(x) }, x); }
function thread(c0: Blk, x: i32): Blk {
    let (c2, p) = emit(c0, x);
    c0 = c2;
    return c0;
}
function main(): i32 {
    let s: Blk = Blk { insts: [] };
    s = thread(s, 1);
    s = thread(s, 2);
    let before: i32 = s.insts.len();
    let t: Blk = thread(s, 3);
    let after: i32 = s.insts.len();
    return before * 10 + after + (t.insts.len() - 3);
}`, 22},
	{"tuple-return-rebind", `struct Blk { insts: i32[] }
function emit(s: Blk, x: i32): Blk { return Blk { insts: s.insts.append(x) }; }
function step(s: Blk, x: i32): (Blk, boolean) { return (emit(s, x), true); }
function main(): i32 {
    let s: Blk = Blk { insts: [] };
    let r: (Blk, boolean) = step(s, 1);
    s = r.0;
    let before: i32 = s.insts.len();
    let t: (Blk, boolean) = step(s, 3);
    let after: i32 = s.insts.len();
    return before * 10 + after + (t.0.insts.len() - 2);
}`, 11},
	{"loop-rebind-two-statement", `struct Blk { insts: i32[] }
function emit(s: Blk, x: i32): Blk { return Blk { insts: s.insts.append(x) }; }
function in_loop(s: Blk, n: i32): Blk {
    let i: i32 = 0;
    while (i < n) {
        let a: Blk = emit(s, i);
        s = emit(a, i + 1);
        i = i + 1;
    }
    return s;
}
function main(): i32 {
    let s: Blk = Blk { insts: [] };
    s = in_loop(s, 1);
    let before: i32 = s.insts.len();
    let t: Blk = in_loop(s, 1);
    let after: i32 = s.insts.len();
    return before * 10 + after + (t.insts.len() - 4);
}`, 22},
	{"nested-call-rebind", `struct Blk { insts: i32[] }
function emit(s: Blk, x: i32): Blk { return Blk { insts: s.insts.append(x) }; }
function thread(s: Blk, n: i32): Blk {
    let i: i32 = 0;
    while (i < n) {
        s = emit(emit(s, i), i + 1);
        i = i + 1;
    }
    return s;
}
function main(): i32 {
    let s: Blk = Blk { insts: [] };
    s = thread(s, 1);
    let before: i32 = s.insts.len();
    let t: Blk = thread(s, 1);
    let after: i32 = s.insts.len();
    return before * 10 + after + (t.insts.len() - 4);
}`, 22},
	{"with-param", `function poke(xs: i32[], v: i32): i32[] {
    return xs.with(0, v);
}
function main(): i32 {
    let a: i32[] = [];
    a = a.append(5);
    a = a.append(6);
    let c: i32[] = poke(a, 9);
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
