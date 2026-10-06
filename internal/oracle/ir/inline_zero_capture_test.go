package ir

import "testing"

// InlineZeroCaptureClosures rewrites OpMakeClosure(I32=0) to
// OpConstFunc, keeping the target name. Other op kinds and
// OpMakeClosure with >=1 captures stay untouched.
func TestInlineZeroCaptureClosuresRewrite(t *testing.T) {
	prog := &Program{
		Funcs: []*Func{
			{
				Name: "user",
				Ops: []Op{
					{Kind: OpMakeClosure, Str: "__closure_zero", I32: 0},
					{Kind: OpMakeClosure, Str: "__closure_one", I32: 1},
					{Kind: OpConstI32, I32: 42},
				},
			},
		},
	}
	InlineZeroCaptureClosures(prog)
	got := prog.Funcs[0].Ops
	if got[0].Kind != OpConstFunc {
		t.Errorf("zero-capture closure should rewrite to OpConstFunc, got %s", got[0].Kind)
	}
	if got[0].Str != "__closure_zero" {
		t.Errorf("rewrite should preserve target name, got %q", got[0].Str)
	}
	if got[1].Kind != OpMakeClosure {
		t.Errorf("one-capture closure should NOT be rewritten, got %s", got[1].Kind)
	}
	if got[1].Str != "__closure_one" {
		t.Errorf("non-zero-capture target name should be preserved, got %q", got[1].Str)
	}
	if got[2].Kind != OpConstI32 || got[2].I32 != 42 {
		t.Errorf("unrelated ops should pass through unchanged, got %s I32=%d", got[2].Kind, got[2].I32)
	}
}

// OpMakeEnv (the post-ElideClosurePair shape for n=0) is NOT
// rewritten — it doesn't allocate a heap pair in the first
// place (just pushes the env_ptr / 0 sentinel for the
// OpCallClosureDirect call sites), and rewriting it would
// strip the env-aware call convention from those consumers.
func TestInlineZeroCaptureClosuresLeavesOpMakeEnvAlone(t *testing.T) {
	prog := &Program{
		Funcs: []*Func{
			{
				Name: "user",
				Ops: []Op{
					{Kind: OpMakeEnv, Str: "__closure_zero", I32: 0},
				},
			},
		},
	}
	InlineZeroCaptureClosures(prog)
	if got := prog.Funcs[0].Ops[0].Kind; got != OpMakeEnv {
		t.Errorf("OpMakeEnv should pass through unchanged, got %s", got)
	}
}
