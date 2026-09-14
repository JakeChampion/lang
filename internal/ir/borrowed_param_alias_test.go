package ir

import "testing"

// A local aliasing a BORROWED string parameter takes no reference of its
// own: the caller holds one for the whole call and nothing in this frame
// releases the parameter, so the transfer inc the alias would otherwise
// take has no matching dec anywhere — the exit sweep's string arm touches
// a local only when it is freeEligible, and an alias never is. One
// reference leaked per call (#9244), and for a read loop handing each
// block to a helper that aliases its parameter that is the whole buffer:
// 64 MiB live after a 64 MiB copy under `FERN_LEAKCHECK`, with the next
// read faulting a fresh mapping in rather than reusing the block
// (`ru_minflt` 17,629 against 252).
//
// The cancellation is the pair, not a dec: inc elided, sweep already
// silent.
func TestBorrowedParamAliasTakesNoInc(t *testing.T) {
	for _, c := range []struct {
		name string
		body string
		// incs is how many alias transfer incs the function should emit.
		incs int
	}{
		// `return piece.len()` hands out a scalar, so the mention inside
		// the return is not the alias escaping — aliasReturnsConfined is
		// what says so where a bare `returned[y]` test would refuse.
		{"read through the alias", `var piece: string = data;
    return piece.len();`, 0},
		// A borrowing call argument is a read too: write_some is a
		// copying builtin, so handing it the alias keeps the credit.
		{"alias handed to a copying builtin", `var piece: string = data;
    var w: Writer = stdout();
    match (w.write_some(piece)) { Ok(_) => {}, Err(_) => { return 1; } }
    return piece.len();`, 0},
		// Returning the alias WHOLE is the escape the cancellation must
		// refuse: the caller receives it, so the reference has to be real.
		{"alias returned whole", `var piece: string = data;
    if (piece.len() == 0) { return ""; }
    return piece;`, 1},
	} {
		t.Run(c.name, func(t *testing.T) {
			ret := "i32"
			if c.name == "alias returned whole" {
				ret = "string"
			}
			// main must CALL f: with no call site the parameter's verdict
			// is decided differently, and the shape under test is the one
			// a caller that owns the string produces.
			use := "total = total + f(d).len();"
			if ret == "i32" {
				use = "total = total + f(d);"
			}
			src := "function f(data: string): " + ret + " {\n    " + c.body + "\n}\n" +
				`function main(): i32 {
    var r: Reader = stdin();
    var total: i32 = 0;
    match (r.read_chunk(8)) {
        Ok(d) => { ` + use + ` },
        Err(_) => { return 1; }
    }
    return total;
}`
			p := lowerSourceWith(t, src, 8)
			fn := findFunc(p, "f")
			if n := countKind(fn.Ops, OpRcInc); n != c.incs {
				t.Errorf("f emits %d alias incs, want %d — an inc with no dec is one leaked reference per call; ops:\n%s", n, c.incs, p)
			}
		})
	}
}
