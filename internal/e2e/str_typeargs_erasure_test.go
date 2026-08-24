package e2e

import "testing"

// strTypeArgsProgram pins the `str` erasure reaching ast.Call.TypeArgs —
// the third checker-stamped type slot on an expression, and the one the
// ArrayLit / Index / SliceExpr fix (#5695, the sibling
// str_array_erasure_test.go) did not cover.
//
// The checker rewrites the container builtins into calls that carry the
// container's element / key / value types on TypeArgs:
// `__method_Array_push` for `xs.append(v)`, the `__method_Map_*` family
// for the map methods. The lowering reads those as RUNTIME-SHAPE
// classifiers — which grow helper retains the copied elements, which
// width the tail store uses, whether a value needs boxing across the Map
// helper boundary — and every one of those tests is a `.(ast.StringType)`
// type assertion. An unerased `str` falls through all of them, so the
// element is stored without the retain an owned `string` element gets and
// the container is left holding pointers whose storage is freed
// underneath it.
//
// Same use-after-free as the literal case, reached through `append` and
// through `Map.insert` instead of through a literal. Both were segfaults
// on arm64 and quiet passes on x86-64, so this runs on every backend.
//
// Exits 0 on success, a distinct code per failed step.
const strTypeArgsProgram = `
import "core/map";

// A str[] built by append and returned across a function boundary.
function split2(s: string): str[] {
    var o: str[] = [];
    o = o.append(slice_unchecked(s, 0, 1));
    o = o.append(slice_unchecked(s, 1, 3));
    return o;
}

function main(): i32 {
    // The minimal shape: empty literal, one append, one element read.
    // The literal has no elements for the checker to settle a type from,
    // so the element type reaches the lowering only via TypeArgs.
    var o: str[] = [];
    o = o.append("a");
    if (o.len() != 1) { return 1; }
    if (o[0].len() != 1) { return 2; }

    // Appending onto a NON-empty literal too, so the literal path and
    // the append path have to agree about the element layout.
    var m: str[] = ["a"];
    m = m.append("bb");
    if (m.len() != 2) { return 3; }
    if (m[0].len() != 1) { return 4; }
    if (m[1].len() != 2) { return 5; }

    // Across a function boundary, appending slices rather than literals.
    var gs: str[] = split2("abc");
    if (gs.len() != 2) { return 6; }
    if (gs[0].len() != 1) { return 7; }
    if (gs[1].len() != 2) { return 8; }
    if (gs[0] + gs[1] != "abc") { return 9; }

    // Built in a loop, which grows the buffer — the grow helper is what
    // chooses whether the copied elements are retained.
    var src: string = "hello world";
    var many: str[] = [];
    var i: i32 = 0;
    while (i < 5) {
        many = many.append(slice_unchecked(src, i, i + 2));
        i = i + 1;
    }
    var total: i32 = 0;
    var j: i32 = 0;
    while (j < many.len()) {
        total = total + many[j].len();
        j = j + 1;
    }
    if (total != 10) { return 10; }

    // The owned spelling is the control that always passed. Kept so a
    // future change cannot "fix" str[] by breaking string[].
    var ctl: string[] = [];
    ctl = ctl.append("a");
    ctl = ctl.append("bb");
    if (ctl.len() != 2) { return 11; }
    if (ctl[0].len() != 1) { return 12; }
    if (ctl[1].len() != 2) { return 13; }

    // Map carries the same slots for its key and value types. A str
    // VALUE segfaulted on arm64 exactly like the array element did.
    var mp: Map[string, str] = map_new(8);
    mp = mp.insert("k", "vv");
    if (mp.get_or("k", "").len() != 2) { return 14; }
    var mctl: Map[string, string] = map_new(8);
    mctl = mctl.insert("k", "vv");
    if (mctl.get_or("k", "").len() != 2) { return 15; }

    // A str KEY as well as a str value.
    var mk: Map[str, i32] = map_new(8);
    mk = mk.insert("kk", 7);
    if (mk.get_or("kk", 0) != 7) { return 16; }

    // char[] must be unaffected here for the same reason it is in the
    // ElemType slots: char is classified at pointer width by these
    // sites, so erasing it to i32 would narrow this one stride and
    // break char[] exactly the way this fixes str[]. Only str is
    // rewritten, and this leg is what catches a "tidy-up" that widens
    // it to the whole surface set.
    var cs: char[] = [];
    cs = cs.append(65 as char);
    cs = cs.append(66 as char);
    if (cs.len() != 2) { return 17; }
    if (cs[0] as i32 != 65) { return 18; }
    if (cs[1] as i32 != 66) { return 19; }
    return 0;
}
`

func TestStrTypeArgsErasureInterp(t *testing.T) {
	if got := runInterpExit(t, strTypeArgsProgram); got != 0 {
		t.Fatalf("interp got %d, want 0", got)
	}
}

func TestStrTypeArgsErasureX86_64(t *testing.T) {
	if _, got := compileAndRunX86_64(t, strTypeArgsProgram); got != 0 {
		t.Fatalf("x86-64 got %d, want 0", got)
	}
}

func TestStrTypeArgsErasureWasm(t *testing.T) {
	if got := compileAndRunWasmbinMain(t, strTypeArgsProgram); got != 0 {
		t.Fatalf("wasm got %d, want 0", got)
	}
}

func TestStrTypeArgsErasureArm64(t *testing.T) {
	if _, got := compileAndRunArm64(t, strTypeArgsProgram); got != 0 {
		t.Fatalf("arm64 got %d, want 0", got)
	}
}
