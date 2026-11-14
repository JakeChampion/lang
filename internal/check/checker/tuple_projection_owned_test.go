package checker

import "testing"

const tupleProjectionPrefix = `struct R { a: string[], n: i32, t: Option[Reader] }
function bump(own r: R): (boolean, R) { return (true, R { ...r, n: r.n + 1 }); }
function swap(own r: R): (R, i32) { return (R { ...r, n: r.n + 1 }, 7); }
function nest(own r: R): (i32, (boolean, R)) { return (1, (false, r)); }
function same[T](x: T): T { return x; }
function eat(own r: R): i32 { return r.n; }
function keep(own r: R): R { return r; }
struct W { r: R, k: i32 }
function opt(own r: R): (i32, Option[R]) { return (1, Some(r)); }
function eatw(own w: W): i32 { return w.k; }
`

func TestTupleProjectionOfFreshCallIsOwned(t *testing.T) {
	for _, tc := range []struct{ name, body string }{
		{"own parameter rebound", `function f(own run: R): i32 { run = bump(run).1; return eat(run); }`},
		{"local rebound", `function f(own run0: R): R { let run = R { ...run0, n: 2 }; run = bump(run).1; run = keep(run); return run; }`},
		{"argument", `function f(own run: R): i32 { return eat(bump(run).1); }`},
		{"element zero", `function f(own run0: R): R { let run = R { ...run0, n: 2 }; run = swap(run).0; run = keep(run); return run; }`},
		{"let", `function f(own run0: R): R { let run = R { ...run0, n: 2 }; let r1 = bump(run).1; run = keep(r1); return run; }`},
		{"nested", `function f(own run0: R): R { let run = R { ...run0, n: 2 }; run = nest(run).1.1; run = keep(run); return run; }`},
		{"loop", `function f(own run0: R): i32 { let run = R { ...run0, n: 0 }; for i in [0, 1] { run = bump(run).1; } return eat(run); }`},
		{"own result", `function f(own r: R): own R { return bump(r).1; }`},
		{"literal field", `function f(own r: R): i32 { return eatw(W { r: bump(r).1, k: 1 }); }`},
		{"match payload", `function f(own r: R): i32 { match (opt(r).1) { Some(x) => { return eat(x); }, None => { return 0; } } return 0; }`},
		{"let-else payload", `function f(own r: R): i32 { let Some(x) = opt(r).1 else { return 0; }; return eat(x); }`},
	} {
		wantOK(t, tc.name, tupleProjectionPrefix+tc.body)
	}
}

func TestTupleProjectionOfBorrowedTupleIsRefused(t *testing.T) {
	for _, tc := range []struct{ name, body string }{
		{"borrowed parameter", `function f(t: (boolean, R)): i32 { return eat(t.1); }`},
		{"lent call result", `function f(t: (boolean, R)): i32 { return eat(same(t).1); }`},
		{"lent call result in a local", `function f(t: (boolean, R)): i32 { let r = same(t).1; return eat(r); }`},
		{"nested lent call result", `function f(t: (i32, (boolean, R))): i32 { return eat(same(t).1.1); }`},
	} {
		if err := checkSource(t, tupleProjectionPrefix+tc.body); !hasCode(err, "E051") {
			t.Errorf("%s: want E051, got %v", tc.name, err)
		}
	}
}
