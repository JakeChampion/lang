package e2e

import "testing"

func TestX86_64OptFieldStructChainLeakFree(t *testing.T) {
	cases := []struct {
		name string
		src  string
	}{
		{"chained-receiver", `struct W { buf: string, err: Option[i32] }
function (w: W) put(s: string): W { return W { ...w, buf: w.buf + s }; }
@noinline function id_w(w: W): W { return w; }
function main(): i32 {
    let o: W = W { buf: "", err: None };
    let i: i32 = 0;
    while (i < 2000) { o = id_w(o).put("abcdefghij"); i = i + 1; }
    if (o.buf.len() != 20000) { return 1; }
    return 0;
}`},
		{"borrowed-field-helper", `struct W { buf: string, err: Option[string] }
struct S { o: W, n: i32 }
function (w: W) put(s: string): W {
    if (s.len() == 0) { return w; }
    return W { ...w, buf: w.buf + s };
}
function rep(o: W): W {
    let out: W = o.put("abcde");
    out = out.put("fghij");
    return out;
}
function main(): i32 {
    let st: S = S { o: W { buf: "", err: None }, n: 0 };
    let i: i32 = 0;
    while (i < 2000) {
        let r: W = rep(st.o);
        st = S { ...st, o: r };
        i = i + 1;
    }
    if (st.o.buf.len() != 20000) { return 1; }
    return 0;
}`},
		{"recursive-threading", `struct W { buf: string, err: Option[i32] }
function (w: W) put(s: string): W { return W { ...w, buf: w.buf + s }; }
function digits(w: W, k: i32): W {
    if (k == 0) { return w; }
    return digits(w.put("x"), k - 1);
}
function main(): i32 {
    let o: W = W { buf: "", err: None };
    let i: i32 = 0;
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
			if allocs-frees > 2 || live > 64 {
				t.Errorf("allocs=%d frees=%d live_bytes=%d — the superseded box or its buffer is not released (#8755)", allocs, frees, live)
			}
		})
	}
}
