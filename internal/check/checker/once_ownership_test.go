package checker

import "testing"

func wantCode(t *testing.T, name, code, src string) {
	t.Helper()
	err := checkSource(t, src)
	if err == nil || !hasCode(err, code) {
		t.Errorf("%s: expected an %s error, got: %v", name, code, err)
	}
}

const onceStepPrelude = `enum Step[T] { Done(T), Wait(i32, once (i32) => Step[T]) }
struct Box[T] { xs: T[] }
function count[T](b: Box[T]): i32 { return b.xs.len(); }
function eat[T](own b: Box[T]): i32 { return b.xs.len(); }
function last[T](own b: Box[T], at: i32): Step[T] { return Done(b.xs[at]); }
`

// A `once` lambda moves its captures in, so it holds no loan on them.
func TestOnceLambdaHoldsNoLoanOnItsCaptures(t *testing.T) {
	wantOK(t, "borrow-before-once-lambda", onceStepPrelude+`
function suspend[T](own b: Box[T]): Step[T] {
    return Wait(count(b), once (at: i32): Step[T] => last(b, at));
}`)
	wantE050(t, "move-before-once-lambda", onceStepPrelude+`
function suspend[T](own b: Box[T]): Step[T] {
    return Wait(eat(b), once (at: i32): Step[T] => last(b, at));
}`)
}

// A match on an owned payload that can hold a handle binds owned payloads.
func TestOncePayloadOfAnOwnedPayloadIsOwned(t *testing.T) {
	const slot = onceStepPrelude + `enum Slot[T] { Full(i32, Step[T]), Empty }
`
	wantOK(t, "nested-owned-match", slot+`
function resume[T](own s: Slot[T]): i32 {
    match (s) {
        Full(at, f) => {
            match (f) {
                Wait(n, k) => { match (k(at + n)) { _ => {} } return 1; },
                _ => {}
            }
        },
        Empty => {}
    }
    return 0;
}`)
	wantCode(t, "nested-borrowed-match", "E051", slot+`
function resume[T](s: Slot[T]): i32 {
    match (s) {
        Full(at, f) => {
            match (f) {
                Wait(n, k) => { match (k(at + n)) { _ => {} } return 1; },
                _ => {}
            }
        },
        Empty => {}
    }
    return 0;
}`)
}

// A variant whose payloads are scalars holds nothing to lend, so a local that
// a loop sets from a continuation call stays fresh.
func TestOnceContinuationResumedFromAFreshLocal(t *testing.T) {
	const prelude = `enum Wake { Woke(i32), TimedOut }
enum Fut { Done(i32), Wait(i32, once (Wake) => Fut) }
function start(): Fut { return Done(1); }
`
	wantOK(t, "loop-resume", prelude+`
function drive(): i32 {
    let f = start();
    while (true) {
        match (f) {
            Done(v) => { return v; },
            Wait(n, k) => { f = k(Wake.TimedOut); }
        }
    }
    match (f) { _ => {} }
    return 0;
}`)
	wantOK(t, "loop-resume-scalar-payload", prelude+`
function drive(): i32 {
    let f = start();
    while (true) {
        match (f) {
            Done(v) => { return v; },
            Wait(n, k) => { f = k(Wake.Woke(n)); }
        }
    }
    match (f) { _ => {} }
    return 0;
}`)
	wantCode(t, "loop-resume-from-borrowed", "E051", prelude+`
function drive(f: Fut): i32 {
    match (f) {
        Done(v) => { return v; },
        Wait(n, k) => { match (k(Wake.TimedOut)) { _ => {} } }
    }
    return 0;
}`)
}
