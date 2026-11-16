package checker

import "testing"

const armLoanDecls = `struct H { fd: i32 } function (own h: H) close(): i32 { return h.fd; } function take(own xs: H[]): i32 { return xs.len(); } function take1(own h: H): i32 { return h.close(); } enum Node { Leaf(i32, H), Empty } function into(n: Node, own acc: H[]): H[] { match (n) { Leaf(k, v) => { acc = acc.append(v); return acc; }, Empty => { return acc; } } } function make(): Node { return Leaf(1, H { fd: 3 }); } function wrap(n: Node): Node { return n; } `

// A payload that a match arm binds from a borrowed value is borrowed, so a
// result that holds it is not an owned value.
func TestMatchArmPayloadOfABorrowedValueIsALoan(t *testing.T) {
	for name, src := range map[string]string{
		"arm-payload-appended-to-an-own-accumulator": `function bad(n: Node): i32 { let a: H[] = []; return take(into(n, a)); }`,
		"arm-payload-in-a-variant-result":            `function get(n: Node): Option[H] { match (n) { Leaf(k, v) => { return Some(v); }, Empty => { return None; } } } function bad(n: Node): i32 { match (get(n)) { Some(h) => { return take1(h); }, None => { return 0; } } }`,
		"arm-payload-appended-to-a-local":            `function one(n: Node): H[] { let acc: H[] = []; match (n) { Leaf(k, v) => { acc = acc.append(v); }, Empty => {} } return acc; } function bad(n: Node): i32 { return take(one(n)); }`,
		"lending-argument-is-a-local":                `function bad(): i32 { let n = make(); let a: H[] = []; let r = take(into(n, a)); return r + into(n, []).len(); }`,
		"lending-argument-is-a-call-that-borrows":    `function bad(n: Node): i32 { let a: H[] = []; return take(into(wrap(n), a)); }`,
	} {
		wantE051(t, name, armLoanDecls+src+` function main(): i32 { return 0; }`)
	}
}

// The result of a call is owned when each argument that it can borrow from
// is a fresh construction.
func TestCallResultIsOwnedWhenEachLendingArgumentIsFresh(t *testing.T) {
	for name, src := range map[string]string{
		"lending-argument-is-a-fresh-variant": `function ok(): i32 { let a: H[] = []; return take(into(Leaf(1, H { fd: 3 }), a)); }`,
		"lending-argument-is-a-fresh-call":    `function ok(): i32 { let a: H[] = []; return take(into(make(), a)); }`,
	} {
		wantOK(t, name, armLoanDecls+src+` function main(): i32 { return 0; }`)
	}
}
