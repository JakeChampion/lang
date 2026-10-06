package e2ecompiler

import "testing"

// `a = Acc { ...a, buf: a.buf + piece }` grows the field's buffer in place
// when the record and the buffer are each sole-held, so 2000 two-byte appends
// allocate at size-class steps rather than once per append (#8785's shape).
// The gate is the RECORD's count: `shared` gets its record back from a
// counted-return call, so the box reads rc 2 while only that box holds the
// buffer, and growing on the buffer's count alone would rewrite the string the
// caller still reads. `held` is the converse, a unique box over a shared
// buffer, where the runtime's own test on the buffer declines. `al` is a
// second name the plan sees and refuses statically, `d` reads the field again
// on the right, and `lent` appends through a record it was lent, which no
// caller brackets, so its caller's string must stay whole. The 19-byte
// strings grow by one inside their size class on every target.
const stringFieldAppendSrc = `struct B { buf: string, n: i32 }

function heap(s: string): string { return s + ""; }

function grow(n: i32, piece: string): B {
    let a: B = B { buf: "", n: 0 };
    let i: i32 = 0;
    while (i < n) {
        a = B { ...a, buf: a.buf + piece, n: a.n + 1 };
        i = i + 1;
    }
    return a;
}

function lent(a: B): B { return B { ...a, buf: a.buf + "Q" }; }

function mk(p: B): B { return p; }

function shared(p: B): i32 {
    let a: B = mk(p);
    a = B { ...a, buf: a.buf + "S", n: a.n + 1 };
    if (p.buf != "0123456789abcdefghi") { return 11; }
    if (a.buf != "0123456789abcdefghiS") { return 12; }
    return 0;
}

function main(): i32 {
    let g: B = grow(2000, "ab");
    if (g.buf.len() != 4000 || g.n != 2000) { return 1; }
    if (g.buf[3999] as i32 != 98) { return 2; }

    let b: B = B { buf: heap("0123456789abcdefghi"), n: 1 };
    let al: B = b;
    b = B { ...b, buf: b.buf + "X", n: b.n + 1 };
    if (al.buf != "0123456789abcdefghi") { return 3; }
    al = B { ...al, buf: al.buf + "Y", n: al.n + 7 };
    if (al.buf != "0123456789abcdefghiY") { return 4; }
    if (b.buf != "0123456789abcdefghiX") { return 5; }

    let c: B = B { buf: heap("ihgfedcba9876543210"), n: 0 };
    let held: string = c.buf;
    c = B { ...c, buf: c.buf + "Z" + "W" };
    if (held != "ihgfedcba9876543210") { return 6; }
    if (c.buf != "ihgfedcba9876543210ZW") { return 7; }

    let d: B = B { buf: heap("ab"), n: 0 };
    d = B { ...d, buf: d.buf + d.buf };
    if (d.buf != "abab") { return 8; }

    let e: B = B { buf: heap("0123456789abcdefghi"), n: 0 };
    let f: B = lent(e);
    if (e.buf != "0123456789abcdefghi") { return 9; }
    if (f.buf != "0123456789abcdefghiQ") { return 10; }

    let h: B = B { buf: heap("0123456789abcdefghi"), n: 0 };
    let r: i32 = shared(h);
    if (r != 0) { return r; }
    return 42;
}`

func TestSelfHostStringFieldAppendInPlace(t *testing.T) {
	cli := buildSelfHostCLI(t)
	for _, target := range []string{"x86-64-linux", "arm64-linux", "wasm32-wasi"} {
		t.Run(target, func(t *testing.T) {
			if stderr, code := cli.exitOf(t, stringFieldAppendSrc, target); code != 42 {
				t.Errorf("exited %d, want 42 (3..12 name the alias whose string changed)\n%s", code, stderr)
			}
		})
	}
}

// The same program under the leak census: the grows must not cost a block
// per append, and nulling the grown field must leave nothing released twice
// or kept.
func TestSelfHostStringFieldAppendCensus(t *testing.T) {
	cli := buildSelfHostCLI(t)
	for _, target := range []string{"x86-64-linux", "arm64-linux"} {
		t.Run(target, func(t *testing.T) {
			stderr, code := cli.exitOf(t, stringFieldAppendSrc, target, "FERN_LEAKCHECK=1")
			if code != 42 {
				t.Fatalf("exited %d, want 42\n%s", code, stderr)
			}
			allocs, frees, live := parseLeakcheck(t, target, stderr)
			if allocs > 1000 {
				t.Errorf("allocs=%d for 2000 field appends, want at most 1000: the field append concatenates instead of growing", allocs)
			}
			if allocs != frees || live != 0 {
				t.Errorf("allocs=%d frees=%d live_bytes=%d: the field append left the census unbalanced", allocs, frees, live)
			}
		})
	}
}
