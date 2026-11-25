package checker

import "testing"

func TestOwnedValueCellConstruction(t *testing.T) {
	for name, src := range map[string]string{
		"scalar argument": `function take(own c: Cell[i32]): i32 { return c.get(); } function f(): i32 { return take(cell_new(7)); }`,
		"scalar result":   `function f(): own Cell[i32] { return cell_new(7); }`,
		"array payload":   `function f(xs: i32[]): own Cell[i32[]] { return cell_new(xs); }`,
		"record payload":  `struct Data { bytes: u8[] } function f(data: Data): own Cell[Data] { return cell_new(data); }`,
	} {
		wantOK(t, name, src)
	}
	for name, src := range map[string]string{
		"resource payload": `struct H { fd: i32 } function (own h: H) close(): void {} function f(h: H): own Cell[H] { return cell_new(h); }`,
		"generic payload":  `function f[T](x: T): own Cell[T] { return cell_new(x); }`,
		"mutable payload":  `function f(x: Cell[i32]): own Cell[Cell[i32]] { return cell_new(x); }`,
		"callback payload": `function f(x: () => i32): own Cell[() => i32] { return cell_new(x); }`,
		"local shadow":     `function take(own c: Cell[i32]): i32 { return c.get(); } function f(borrowed: Cell[i32]): i32 { let cell_new = (n: i32): Cell[i32] => borrowed; return take(cell_new(7)); }`,
		"parameter shadow": `function take(own c: Cell[i32]): i32 { return c.get(); } function f(cell_new: (i32) => Cell[i32]): i32 { return take(cell_new(7)); }`,
	} {
		wantE051(t, name, src)
	}
}

func TestOwnCellParamKeepsSetLoan(t *testing.T) {
	const prefix = `struct H { fd: i32 } function (own h: H) close(): i32 { return h.fd; } function eat(own c: Cell[H]): void {} `
	for name, src := range map[string]string{
		"owned payload":     `function f(own s: Cell[H], own h: H): void { s.set(h); eat(s); }`,
		"fresh payload":     `function f(own s: Cell[H]): void { s.set(H { fd: 2 }); eat(s); }`,
		"borrowed snapshot": `function f(own s: Cell[H], h: H): i32 { s.set(h); return s.get().fd; }`,
	} {
		wantOK(t, name, prefix+src)
	}
	for name, src := range map[string]string{
		"moved":       `function f(own s: Cell[H], h: H): void { s.set(h); eat(s); }`,
		"in a branch": `function f(own s: Cell[H], h: H): void { if (h.fd > 0) { s.set(h); } eat(s); }`,
		"own result":  `function f(own s: Cell[H], h: H): own Cell[H] { s.set(h); return s; }`,
	} {
		wantE051(t, name, prefix+src)
	}
}
