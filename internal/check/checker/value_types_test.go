package checker

import "testing"

func TestCheckedValueTypes(t *testing.T) {
	for name, body := range map[string]string{
		"nested immutable":    `struct Data { items: Option[u8[]] } @value struct Key { data: Data } function (own k: Key) erase(): void {} function f(k: Key): void { k.erase(); }`,
		"recursive immutable": `@value struct Tree { children: Tree[], data: u8[] } function (own t: Tree) erase(): void {} function f(t: Tree): void { t.erase(); }`,
	} {
		t.Run(name, func(t *testing.T) { wantOK(t, name, body) })
	}
	for name, body := range map[string]string{
		"descriptor":          `@value struct Bad { socket: Socket }`,
		"nested descriptor":   `struct Part { socket: Socket } @value struct Bad { parts: Part[] }`,
		"cell":                `@value struct Bad { data: Cell[i32] }`,
		"callback":            `@value struct Bad { data: () => i32 }`,
		"view":                `@value struct Bad { data: [u8] }`,
		"unbounded parameter": `@value struct Bad[T] { data: T }`,
		"inferred handle":     `struct Handle { fd: i32 } function (own h: Handle) close(): void {} @value struct Bad { handle: Handle }`,
		"obligation":          `@must_consume struct Part { bytes: u8[] } @value struct Bad { part: Part }`,
		"enum obligation":     `@must_consume enum Part { SomeData(u8[]), Empty } @value struct Bad { part: Part }`,
		"union cell":          `struct A { data: Cell[i32] } struct B { n: i32 } type Choice = A | B; @value struct Bad { data: Choice }`,
	} {
		t.Run(name, func(t *testing.T) { wantE051(t, name, body) })
	}
}
