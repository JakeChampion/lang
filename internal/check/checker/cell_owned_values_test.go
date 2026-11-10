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
