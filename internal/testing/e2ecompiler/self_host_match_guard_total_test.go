package e2ecompiler

import (
	"os"
	"path/filepath"
	"testing"
)

// A value match whose plain variant arms cover the union is total however
// many guarded arms stand ahead of them: a guard decides nothing about
// coverage, since its false edge reaches the arms after it. The self-host
// lowering counted every arm against the variant count, so one guarded arm
// made the match "fall through" and the typed lowering refused it with
// `value block match falls through`, while the statement form and the
// native compiler both accepted it (found by fernsmith's guard production).
// Each case runs under the self-host CLI for every target this host can run
// and under the native build of the same program; the two must agree.
var matchGuardTotalCases = []struct {
	name string
	src  string
}{
	{"statement_none_closes_chain", `@noinline function choose(v: Option[i32]): i32 {
    match (v) {
        Some(g) when g > 5 => { return g * 2; },
        Some(x) => { return x; },
        None => { return 37; },
    }
}
function main(): i32 { return choose(None); }
`}, // 37
	{"guard_then_plain", `function main(): i32 {
    let v: Option[i32] = Some(5i32);
    let r: i32 = (match (v) { Some(g) when (g > 3i32) => g, Some(x) => x + 100i32, None => 0i32 });
    return r;
}`}, // 5
	{"guard_false_reaches_plain", `function main(): i32 {
    let v: Option[i32] = Some(5i32);
    let r: i32 = (match (v) { Some(g) when (g > 9i32) => 100i32, None when (true) => 200i32, Some(x) => x, None => 300i32 });
    return r;
}`}, // 5
	{"guard_with_at_binding", `function main(): i32 {
    let v: Option[i32] = Some(5i32);
    let r: i32 = (match (v) { Some(g) when (g > 3i32) => g, w @ Some(x) => (match (w) { Some(y) => y + x, None => 0i32 }), None => 0i32 });
    return r;
}`}, // 5
	{"result_guard_then_plain", `function main(): i32 {
    let v: Result[i32, i32] = Err(7i32);
    let r: i32 = (match (v) { Ok(g) when (g > 3i32) => g, Ok(x) => x + 100i32, Err(e) when (e > 10i32) => 50i32, Err(e) => e + 20i32 });
    return r;
}`}, // 27
	{"plain_arms_out_of_order", `function main(): i32 {
    let v: Option[i32] = Some(2i32);
    let r: i32 = (match (v) { Some(g) when (g > 3i32) => 100i32, None => 200i32, Some(x) => x + 10i32 });
    return r;
}`}, // 12
}

func TestSelfHostMatchGuardTotal(t *testing.T) {
	h := selfHostCLIForHost(t)
	for _, tc := range matchGuardTotalCases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			src := filepath.Join(dir, "p.fern")
			if err := os.WriteFile(src, []byte(tc.src), 0o644); err != nil {
				t.Fatal(err)
			}
			for _, tg := range h.targets {
				native := filepath.Join(dir, "native-"+tg.target)
				h.compileNative(t, tg, src, native)
				_, want := h.runProduced(t, tg, native)
				bin := filepath.Join(dir, "selfhost-"+tg.target)
				h.compileWith(t, tg, src, bin)
				if _, got := h.runProduced(t, tg, bin); got != want {
					t.Errorf("%s: self-host build exits %d, native build exits %d", tg.target, got, want)
				}
			}
		})
	}
}
