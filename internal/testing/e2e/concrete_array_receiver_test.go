package e2e

import "testing"

const concreteArrayReceiverSrc = `function (xs: i32[]) avg2(): i32 {
    if (xs.len() == 0) { return 0; }
    let t: i32 = 0;
    let i: i32 = 0;
    while (i < xs.len()) { t = t + xs[i]; i = i + 1; }
    return t / xs.len();
}
function (xs: string[]) total_len(): i32 {
    let t: i32 = 0;
    let i: i32 = 0;
    while (i < xs.len()) { t = t + xs[i].len(); i = i + 1; }
    return t;
}
function main(): i32 {
    let a: i32[] = [2, 4, 6];
    let s: string[] = ["ab", "cde"];
    return a.avg2() + s.total_len();
}
`

func TestConcreteElementArrayReceiver(t *testing.T) {
	const want = 9

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
