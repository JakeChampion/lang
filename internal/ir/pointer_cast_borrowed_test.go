package ir

import "testing"

// A cast from a raw address to a counted type views what the address points
// at and takes no reference: core/map's key and value columns read each cell
// as `cellPtr as string`. Counted as a fresh owner, the view was released
// once per cell, so every keys() call took a reference from the map that it
// never gave, and the native ssa backend crashed on tests/proposals/cow_snapshots.fern.
func TestPointerCastViewIsNotReleased(t *testing.T) {
	src := `function column(cells: usize[]): string[] {
    let out: string[] = [];
    let i: i32 = 0;
    while (i < cells.len()) {
        let cellPtr: usize = cells[i];
        let s: string = cellPtr as string;
        out = out.append(s);
        i = i + 1;
    }
    return out;
}
function main(): i32 { return 0; }`
	p := lowerSourceWith(t, src, 8)
	fn := findFunc(p, "column")
	if n := countCallDirect(fn.Ops, "__fern_str_dec"); n != 0 {
		t.Errorf("column releases the string view it cast from an address %d times; ops:\n%s", n, p)
	}
}
