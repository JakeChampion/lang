package e2eselfhost

import "testing"

// A `[u8]` view inside a composite type: a struct field, a tuple component
// and an array element (#10926). The parser used to erase `[T]` to `T[]` and
// keep the view only as a flag on a `var` or parameter, so a view spelled
// inside another type resolved to an owned array (E043 at the struct literal,
// E034 at the array literal) and `[u8][]` did not parse at all. The view now
// stays in the spelling, which the checker resolves and the lowering folds.
const viewCompositeSrc = `import "std/i32";
struct View { v: [u8], n: i32 }
function first(v: View): i32 { return v.v[0] as i32; }
function total(xs: [u8][]): i32 {
    var t: i32 = 0;
    for x in xs { t = t + x.len(); }
    return t;
}
function pair_len(p: ([u8], i32)): i32 { return p.0.len() + p.1; }
function main(): i32 {
    var s: string = "hello world";
    var b: [u8] = s.as_bytes();
    var w: View = View { v: b, n: 1 };
    var arr: [u8][] = [b, s.as_bytes()];
    var pair: ([u8], i32) = (b, 7);
    var c: [u8] = arr[1];
    var acc: i32 = 0;
    var i: i32 = 0;
    while (i < 100) {
        var w2: View = View { v: s.as_bytes(), n: i };
        var arr2: [u8][] = [b, s.as_bytes()];
        var p2: ([u8], i32) = (arr2[0], i);
        acc = acc + first(w2) + w2.n + total(arr2) + pair_len(p2) + arr2[1].len();
        i = i + 1;
    }
    print(first(w).to_string() + " " + total(arr).to_string() + " " + pair_len(pair).to_string() + " " + c.len().to_string() + " " + acc.to_string());
    return 0;
}
`

func viewCompositeWant(t *testing.T) string {
	t.Helper()
	want, code := runInterp(t, viewCompositeSrc)
	if code != 0 || want != "104 22 18 11 24700\n" {
		t.Fatalf("interpreter: exit %d, stdout %q", code, want)
	}
	return want
}

func TestSelfHostViewCompositeX86_64(t *testing.T) {
	cli := newStrictCLI(t)
	want := viewCompositeWant(t)
	if code, out := cli.runX86(t, cli.emit(t, "x86-64-linux", viewCompositeSrc)); code != 0 || out != want {
		t.Fatalf("exit %d, stdout %q; want 0, %q", code, out, want)
	}
}

func TestSelfHostViewCompositeArm64(t *testing.T) {
	gcc, qemu := arm64Tooling(t)
	cli := newStrictCLI(t)
	want := viewCompositeWant(t)
	if code, out := runArm64(t, gcc, qemu, cli.emit(t, "arm64-linux", viewCompositeSrc)); code != 0 || out != want {
		t.Fatalf("arm64: exit %d, stdout %q; want 0, %q", code, out, want)
	}
}

func TestSelfHostViewCompositeWasm(t *testing.T) {
	cli := newStrictCLI(t)
	want := viewCompositeWant(t)
	if code, out := runWasm(t, cli.emit(t, "wasm32-wasi", viewCompositeSrc)); code != 0 || out != want {
		t.Fatalf("wasm: exit %d, stdout %q; want 0, %q", code, out, want)
	}
}
