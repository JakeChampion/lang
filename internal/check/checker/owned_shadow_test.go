package checker

import "testing"

const ownedShadowPrelude = `struct State { n: i32 }
function (own s: State) step(): State { return State { n: s.n + 1 }; }
function take(own s: State): State { return s; }
function pair(own s: State): (State, i32) { return (s, 1); }
function both(own a: State, own b: State): State { return State { n: a.n + b.n }; }
`

func TestOwnedShadowInitializer(t *testing.T) {
	for _, body := range []string{
		`match (Some(1)) { None => { return next; }, Some(value) => { let next = take(next); return next; } }`,
		`match (Some(1)) { None => { return next; }, Some(value) => { let next = next.step(); return next; } }`,
		`match (Some(1)) { None => { return next; }, Some(value) => { let (next, used) = pair(next); return next; } }`,
		`if (true) { let next = take(next); return next; } return next;`,
		`if (true) { let out = take(next); let next = State { n: 2 }; return next; } return next;`,
	} {
		wantOK(t, body, ownedShadowPrelude+`function work(): State { let next = State { n: 1 }; `+body+` } function main(): i32 { return work().n; }`)
	}
}

func TestOwnedShadowInitializerRetainsOuterLiveness(t *testing.T) {
	for _, body := range []string{
		`if (true) { let next = take(next); let n = next.n; } return next;`,
		`if (true) { let next = both(next, next); return next; } return next;`,
		`for i in 0..2 { let next = take(next); break; } return next;`,
		`for i in 0..2 { let next = take(next); continue; } return State { n: 0 };`,
		`let view = () => next.n; if (true) { let next = take(next); return next; } return next;`,
	} {
		wantE051(t, body, ownedShadowPrelude+`function work(): State { let next = State { n: 1 }; `+body+` } function main(): i32 { return work().n; }`)
	}
	wantE051(t, "borrowed outer parameter", ownedShadowPrelude+`function work(next: State): State { if (true) { let next = take(next); return next; } return next; } function main(): i32 { return 0; }`)
}
