package ir

import "testing"

// A struct with an Option field is as droppable on self-reassignment as one
// without: typeSelfDropSafe judges the builtin generic enums by their type
// arguments rather than looking for a declaration that does not exist
// (#8755). Pinned on the lowered ops: the overwrite routes through the
// struct's drop function, not the flat dec.
func TestOptFieldStructOverwriteDeepDrops(t *testing.T) {
	// put may hand its receiver back (BufWriter.write_string does on its
	// error path), which taints the local it initialises: freeEligible is
	// withheld, so the overwrite of `out` rests on selfReassignOwnedLocal
	// alone — and that read the Option field as unsafe.
	src := `struct W { buf: string, err: Option[i32] }
function (w: W) put(s: string): W {
    if (s.len() == 0) { return w; }
    return W { ...w, buf: w.buf + s };
}
function rep(o: W): W {
    var out: W = o.put("a");
    out = out.put("b");
    return out;
}
function main(): i32 {
    var o: W = W { buf: "", err: None };
    return rep(o).buf.len();
}`
	prog := lowerSourceWith(t, src, 8)
	for _, f := range prog.Funcs {
		if f.Name != "rep" {
			continue
		}
		deep := 0
		for _, op := range f.Ops {
			if op.Kind == OpCallDirect && op.Str == "__drop_struct_W" {
				deep++
			}
		}
		if deep == 0 {
			t.Errorf("rep releases the overwritten `out` with a flat dec rather than __drop_struct_W — the Option field made typeSelfDropSafe refuse the struct")
		}
	}
}
