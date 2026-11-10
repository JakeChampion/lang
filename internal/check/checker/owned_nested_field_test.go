package checker

import "testing"

func TestOwnedNestedFieldCalls(t *testing.T) {
	const prelude = `
struct Handle { fd: i32 }
function (own h: Handle) close(): i32 { return h.fd; }
struct Box { value: Handle }
struct Pair { a: Box, b: Box }
function inspect(p: Pair): i32 { return p.a.value.fd; }
`
	for name, body := range map[string]string{
		"owned root":    `function f(own p: Pair): i32 { return p.a.value.close() + p.b.value.close(); }`,
		"generic owner": `struct Generic[T] { value: T } function f(own p: Generic[Box]): i32 { return p.value.value.close(); }`,
	} {
		t.Run(name, func(t *testing.T) { wantOK(t, name, prelude+body) })
	}
	for name, body := range map[string]string{
		"reassigned owner": `function f(own p: Pair, h: Handle): i32 { p = Pair { a: Box { value: h }, b: Box { value: Handle { fd: 8 } } }; return p.a.value.close(); }`,
		"field alias":      `function f(own p: Pair): i32 { let q = p.a; let n = p.a.value.close(); return n + q.value.fd; }`,
		"borrowed root":    `function f(p: Pair): i32 { return p.a.value.close(); }`,
		"borrowed local":   `function f(h: Handle): i32 { let p = Pair { a: Box { value: h }, b: Box { value: Handle { fd: 8 } } }; return p.a.value.close(); }`,
		"second consume":   `function f(own p: Pair): i32 { let n = p.a.value.close(); return n + p.a.value.close(); }`,
		"field read later": `function f(own p: Pair): i32 { let n = p.a.value.close(); return n + p.a.value.fd; }`,
		"whole read later": `function f(own p: Pair): i32 { let n = p.a.value.close(); return n + inspect(p); }`,
		"loop":             `function f(own p: Pair, n: i32): i32 { let total = 0; for i in 0..n { total += p.a.value.close(); } return total; }`,
		"capture":          `function f(own p: Pair): i32 { let read = (): i32 => p.a.value.fd; let n = p.a.value.close(); return n + read(); }`,
		"defer":            `function f(own p: Pair): i32 { defer p.a.value.close(); return p.a.value.close(); }`,
	} {
		t.Run(name, func(t *testing.T) { wantE051(t, name, prelude+body) })
	}
	wantE050(t, "whole alias", prelude+`function f(own p: Pair): i32 { let q = p; let n = p.a.value.close(); return n + q.a.value.fd; }`)
}
