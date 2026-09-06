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

// A fresh box returned by a user function and passed straight on under a
// pointer-typed result is released after the call, behind the identity
// guard (#8755): `id_w(o).put(s)` stashes id_w's result and drops it unless
// put handed it back, in which case only the return transfer's count goes.
func TestBoxTempUnderPointerResultIsReleased(t *testing.T) {
	src := `struct W { buf: string, err: Option[i32] }
function (w: W) put(s: string): W { return W { ...w, buf: w.buf + s }; }
function id_w(w: W): W { return w; }
function main(): i32 {
    var o: W = W { buf: "", err: None };
    o = id_w(o).put("a");
    return o.buf.len();
}`
	prog := lowerSourceWith(t, src, 8)
	for _, f := range prog.Funcs {
		if f.Name != "main" {
			continue
		}
		guardedDrops, flatDecs := 0, 0
		for i, op := range f.Ops {
			if op.Kind == OpNe && i+1 < len(f.Ops) && f.Ops[i+1].Kind == OpIf {
				guardedDrops++
			}
			if op.Kind == OpRcDec && op.Str == "__fern_rc_dec" {
				flatDecs++
			}
		}
		if guardedDrops == 0 {
			t.Errorf("main has no identity-guarded release of id_w's result: the temp handed to put is never released")
		}
		if flatDecs == 0 {
			t.Errorf("main has no flat dec for the identity case: a callee handing the temp back leaves the return transfer's count unreleased")
		}
	}
}
