package e2eselfhost

import "testing"

// `let o = None;` names no payload, so the typed lowering settles the binding
// at a void payload, as it does a bare None payload (#10697). That Option is
// only ever None, so it reads as the None of any Option a destination names:
// a matched local, an argument, an annotated binding and a return.
const bareNoneBindingSrc = `function arg(p: Option[i64]): i32 { match (p) { Some(_) => { return 100; }, None => { return 1; } } }
function ret(): Option[string] { let o = None; return o; }
function round(i: i32): i32 {
    let o = None;
    let t: i32 = match (o) { Some(_) => 100, None => 1 };
    t = t + arg(o);
    let p: Option[string] = o;
    t = t + match (p) { Some(_) => 100, None => 1 };
    t = t + match (ret()) { Some(_) => 100, None => 1 };
    return t + i % 2;
}
function main(): i32 { let t: i32 = 0; let i: i32 = 0; while (i < 10) { t = t + round(i); i = i + 1; } return t; }
`

func TestSelfHostBareNoneBinding(t *testing.T) {
	if want := interpExit(t, buildLangBinForInterp(t), bareNoneBindingSrc); want != 45 {
		t.Fatalf("interpreter exit %d, want 45", want)
	}
	cli := buildSelfHostCLI(t)
	for _, target := range []string{"x86-64-linux", "arm64-linux", "wasm32-wasi"} {
		t.Run(target, func(t *testing.T) {
			stderr, exit := cli.exitOf(t, bareNoneBindingSrc, target, "FERN_LEAKCHECK=1")
			if exit != 45 {
				t.Fatalf("exit = %d, want 45\n%s", exit, stderr)
			}
			assertBalancedCensus(t, stderr)
		})
	}
}
