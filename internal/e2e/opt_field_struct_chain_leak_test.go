package e2e

import "testing"

// A struct carrying an Option field, reassigned from a chained call result,
// leaked its old value on every iteration and copied its buffer each time
// (#8755). Two decisions were behind it: typeSelfDropSafe had no entry for
// the builtin generic enums, so the overwrite fell to the flat dec that never
// frees a box; and a fresh box handed straight on as an argument under a
// pointer-typed result was never released at all. Each shape below leaked
// hundreds of megabytes over 20 000 iterations; all must end at the
// allocation baseline.
func TestX86_64OptFieldStructChainLeakFree(t *testing.T) {
	cases := []struct {
		name string
		src  string
	}{
		// The chained receiver: `o = id_w(o).put(..)`.
		{"chained-receiver", `struct W { buf: string, err: Option[i32] }
function (w: W) put(s: string): W { return W { ...w, buf: w.buf + s }; }
function id_w(w: W): W { return w; }
function main(): i32 {
    var o: W = W { buf: "", err: None };
    var i: i32 = 0;
    while (i < 2000) { o = id_w(o).put("abcdefghij"); i = i + 1; }
    if (o.buf.len() != 20000) { return 1; }
    return 0;
}`},
		// The borrowed field through a helper that appends more than once,
		// where put may hand its receiver back (as BufWriter.write_string
		// does on its error path): the local it initialises is tainted,
		// and its overwrite rests on the struct being self-drop-safe.
		{"borrowed-field-helper", `struct W { buf: string, err: Option[string] }
struct S { o: W, n: i32 }
function (w: W) put(s: string): W {
    if (s.len() == 0) { return w; }
    return W { ...w, buf: w.buf + s };
}
function rep(o: W): W {
    var out: W = o.put("abcde");
    out = out.put("fghij");
    return out;
}
function main(): i32 {
    var st: S = S { o: W { buf: "", err: None }, n: 0 };
    var i: i32 = 0;
    while (i < 2000) {
        var r: W = rep(st.o);
        st = S { ...st, o: r };
        i = i + 1;
    }
    if (st.o.buf.len() != 20000) { return 1; }
    return 0;
}`},
		// Recursive threading that returns its parameter at the base case,
		// chained: the identity case owes the return transfer's count.
		{"recursive-threading", `struct W { buf: string, err: Option[i32] }
function (w: W) put(s: string): W { return W { ...w, buf: w.buf + s }; }
function digits(w: W, k: i32): W {
    if (k == 0) { return w; }
    return digits(w.put("x"), k - 1);
}
function main(): i32 {
    var o: W = W { buf: "", err: None };
    var i: i32 = 0;
    while (i < 2000) { o = digits(o, 5).put(" "); i = i + 1; }
    if (o.buf.len() != 12000) { return 1; }
    return 0;
}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, stderr, exit := runLeakCheckX86_64(t, tc.src)
			if exit != 0 {
				t.Fatalf("exit %d, want 0", exit)
			}
			allocs, frees, live := parseLeakCheckLine(t, stderr)
			// The last box and its buffer are live at exit; anything more is
			// a per-iteration leak.
			if allocs-frees > 2 || live > 64 {
				t.Errorf("allocs=%d frees=%d live_bytes=%d — the superseded box or its buffer is not released (#8755)", allocs, frees, live)
			}
		})
	}
}
