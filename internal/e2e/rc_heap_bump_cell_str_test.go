package e2e

import (
	"testing"

	"github.com/jakechampion/lang/internal/ast"
)

// Cell[string] reclamation (#6885). A cell is a one-element array box, and
// its element is a heap string it CO-OWNS: `cell_new` moves or retains the
// value in, `get` retains what it hands out, `set` pre-drops the old buffer,
// and the cell's own drop releases the slot. Three of those four were short.
//
//   - The cell drop and the `set` pre-drop released a native single-word
//     string with __fern_drop_arr_ptr / __fern_rc_dec, which decrement and
//     never free — so on x86-64 a cell whose element was never read, and
//     every overwrite, stranded a buffer.
//   - `get` retains unconditionally, and only a BINDING balanced that. Every
//     borrowing consumer — `.len()`, `==`, a concat operand, a call argument
//     — dropped the retained reference on the floor, on all three backends.
//
// The probes are those six spellings measured apart, because that is what
// separates the two causes: pre-fix x86-64 leaked on all six and arm64 / wasm
// on the four `get`-as-a-temp ones. Each is a callee, so the reclaim under
// test is the function-exit sweep rather than the loop-body reinit.
//
// The program returns the 1-based index of the FIRST shape that is not flat
// (0 when all are), so a failure names the spelling instead of a byte count.
const cellStrChurnSrc = `import "std/i32";
function wide(k: i32): string { return "a-value-well-past-the-inline-threshold-" + k.to_string(); }
function eat(s: string): i32 { return s.len(); }

function c_len(k: i32): i32 {
    var c: Cell[string] = cell_new(wide(k));
    return c.get().len();
}
function c_eq(k: i32): i32 {
    var c: Cell[string] = cell_new(wide(k));
    if (c.get() == "zz") { return 2; }
    return 1;
}
function c_concat(k: i32): i32 {
    var c: Cell[string] = cell_new(wide(k));
    var s: string = c.get() + "y";
    return s.len();
}
function c_arg(k: i32): i32 {
    var c: Cell[string] = cell_new(wide(k));
    return eat(c.get());
}
function c_set(k: i32): i32 {
    var c: Cell[string] = cell_new(wide(k));
    c.set(wide(k + 1));
    var s: string = c.get();
    return s.len();
}
function c_unread(k: i32): i32 {
    var c: Cell[string] = cell_new(wide(k));
    return k - k + 1;
}

function churn(n: i32, which: i32): i32 {
    var t: i32 = 0;
    var i: i32 = 0;
    while (i < n) {
        if (which == 1) { t = t + c_len(i); }
        if (which == 2) { t = t + c_eq(i); }
        if (which == 3) { t = t + c_concat(i); }
        if (which == 4) { t = t + c_arg(i); }
        if (which == 5) { t = t + c_set(i); }
        if (which == 6) { t = t + c_unread(i); }
        i = i + 1;
    }
    return t;
}

function perRound(which: i32): i32 {
    var warm: i32 = churn(100, which);
    if (warm <= 0) { return 99; }
    var before: i64 = __heap_bump_bytes();
    var again: i32 = churn(200, which);
    if (again <= 0) { return 99; }
    return ((__heap_bump_bytes() - before) as i32) / 200;
}

function main(): i32 {
    var which: i32 = 1;
    while (which <= 6) {
        if (perRound(which) != 0) { return which; }
        which = which + 1;
    }
    return __rc_underflow_count();
}`

// cellStrShapes names cellStrChurnSrc's probes by the index it returns.
var cellStrShapes = []string{"", "c.get().len()", "c.get() == …", "c.get() + …", "eat(c.get())", "c.set(…) overwrite", "cell never read"}

func cellStrVerdict(code int) string {
	if code > 0 && code < len(cellStrShapes) {
		return cellStrShapes[code]
	}
	return "unknown shape"
}

func TestX86_64CellStringReclaimed(t *testing.T) {
	if _, code := compileAndRunX86_64FreeOn(t, cellStrChurnSrc); code != 0 {
		t.Errorf("Cell[string] churn is not flat on x86-64: shape %d (%s)", code, cellStrVerdict(code))
	}
}

func TestArm64CellStringReclaimed(t *testing.T) {
	if _, code := compileAndRunArm64FreeOn(t, cellStrChurnSrc); code != 0 {
		t.Errorf("Cell[string] churn is not flat on arm64: shape %d (%s)", code, cellStrVerdict(code))
	}
}

func TestWASMCellStringReclaimed(t *testing.T) {
	prev := ast.RcFreeEnabled
	ast.RcFreeEnabled = true
	defer func() { ast.RcFreeEnabled = prev }()
	if code := runWasm(t, cellStrChurnSrc); code != 0 {
		t.Errorf("Cell[string] churn is not flat on wasm: shape %d (%s)", code, cellStrVerdict(code))
	}
}
