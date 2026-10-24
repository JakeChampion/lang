package e2ecompiler

import "testing"

const shadowedParamTraitBoundSrc = `import "std/convert";

trait From[T] { function make(v: T): Self; }

struct P { x: i32 }

impl convert.From[string] for P { function from(v: string): P { return P { x: v.len() }; } }
impl From[i32] for P { function make(v: i32): P { return P { x: v }; } }

function pick[T: From[i32]](v: T): T { return T.make(3); }

function main(): i32 { return pick(P { x: 7 }).x; }
`

func TestSelfHostShadowedParamTraitBound(t *testing.T) {
	if got := interpExit(t, buildLangBinForInterp(t), shadowedParamTraitBoundSrc); got != 3 {
		t.Fatalf("interpreter exit %d, want 3", got)
	}
	cli := buildSelfHostCLI(t)
	for _, target := range []string{"x86-64-linux", "arm64-linux", "wasm32-wasi"} {
		t.Run(target, func(t *testing.T) {
			if stderr, exit := cli.exitOf(t, shadowedParamTraitBoundSrc, target); exit != 3 {
				t.Errorf("exit = %d, want 3\n%s", exit, stderr)
			}
		})
	}
}
