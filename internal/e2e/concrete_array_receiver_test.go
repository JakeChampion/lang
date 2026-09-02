package e2e

import "testing"

// A receiver method pinned to one element type — `function (xs: i32[]) avg2()`
// — is a declaration any program can write. std/array's concrete-element verbs
// reached dispatch through the `__method_Array_<name>` naming convention, a
// route the checker recognised and no user could spell; the receiver form is
// the same dispatch without a compiler-known name behind it, and it mangles to
// the same `__method_Array_<name>` symbol, so the backends see nothing new.
//
// Two element types in one program, because the "Array" method namespace is
// keyed on the NAME: each method claims its name for every array, and it is the
// signature that decides whether a given receiver reaches it. A program mixing
// `i32[]` and `string[]` methods proves the two do not collide.
const concreteArrayReceiverSrc = `function (xs: i32[]) avg2(): i32 {
    if (xs.len() == 0) { return 0; }
    var t: i32 = 0;
    var i: i32 = 0;
    while (i < xs.len()) { t = t + xs[i]; i = i + 1; }
    return t / xs.len();
}
function (xs: string[]) total_len(): i32 {
    var t: i32 = 0;
    var i: i32 = 0;
    while (i < xs.len()) { t = t + xs[i].len(); i = i + 1; }
    return t;
}
function main(): i32 {
    var a: i32[] = [2, 4, 6];
    var s: string[] = ["ab", "cde"];
    return a.avg2() + s.total_len();
}
`

func TestConcreteElementArrayReceiver(t *testing.T) {
	const want = 9 // avg([2,4,6]) = 4, len("ab") + len("cde") = 5

	interpBin := buildLangBinForInterp(t)
	if got := interpExit(t, interpBin, concreteArrayReceiverSrc); got != want {
		t.Fatalf("interpreter oracle exits %d, want %d", got, want)
	}

	t.Run("x86_64", func(t *testing.T) {
		if _, code := compileAndRunX86_64(t, concreteArrayReceiverSrc); code != want {
			t.Errorf("exit %d, want %d", code, want)
		}
	})
}
