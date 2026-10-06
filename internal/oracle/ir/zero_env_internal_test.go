package ir

import "testing"

// The guard on the env fold, exercised directly.
//
// It cannot be reached through the compiler: a capturing closure is an
// OpMakeClosure with captures, which InlineZeroCaptureClosures leaves alone
// and ElideClosurePair usually rewrites before this pass runs, so no source
// program produces the shape this rejects. That makes hand-built IR the only
// way to prove the rejection happens — and the rejection is the half that
// carries correctness, since folding a real env to 0 would not fail to
// compile. It would hand every call a null env and read the captured values
// as garbage.
func TestFoldZeroCaptureEnvLoadsGuard(t *testing.T) {
	const envOff = 8
	callSite := func(slot int32) []Op {
		return []Op{
			{Kind: OpLoadLocal, I32: slot},
			{Kind: OpConstI32, I32: envOff},
			{Kind: OpAdd},
			{Kind: OpLoad, Width: WidthPtr},
			{Kind: OpCallClosureDirect, Str: "target", I32: 2},
		}
	}

	for _, tc := range []struct {
		name    string
		writers []Op
		want    int // env fetches remaining
	}{
		{
			name:    "plain function value folds",
			writers: []Op{{Kind: OpConstFunc, Str: "target"}, {Kind: OpStoreLocal, I32: 1}},
			want:    0,
		},
		{
			name: "a capturing writer anywhere blocks the fold",
			writers: []Op{
				{Kind: OpConstFunc, Str: "target"}, {Kind: OpStoreLocal, I32: 1},
				{Kind: OpMakeClosure, Str: "target", I32: 1}, {Kind: OpStoreLocal, I32: 1},
			},
			want: 1,
		},
		{
			name: "a writer that is not a function value at all blocks it",
			writers: []Op{
				{Kind: OpLoadLocal, I32: 0}, {Kind: OpStoreLocal, I32: 1},
			},
			want: 1,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fn := &Func{Name: "f", Ops: append(append([]Op{}, tc.writers...), callSite(1)...)}
			foldZeroCaptureEnvLoadsIn(fn, envOff)
			got := 0
			for i := 0; i+4 < len(fn.Ops); i++ {
				if fn.Ops[i].Kind == OpLoadLocal &&
					fn.Ops[i+1].Kind == OpConstI32 && fn.Ops[i+1].I32 == envOff &&
					fn.Ops[i+2].Kind == OpAdd && fn.Ops[i+3].Kind == OpLoad &&
					fn.Ops[i+4].Kind == OpCallClosureDirect {
					got++
				}
			}
			if got != tc.want {
				t.Errorf("%d env fetches left, want %d", got, tc.want)
			}
			// The call must survive either way; this folds an argument, not a call.
			calls := 0
			for _, op := range fn.Ops {
				if op.Kind == OpCallClosureDirect {
					calls++
				}
			}
			if calls != 1 {
				t.Errorf("%d calls left, want 1 — the fold replaces the env argument, not the call", calls)
			}
		})
	}
}

// A fetch whose offset is not the env offset is somebody else's load.
func TestFoldZeroCaptureEnvLoadsLeavesOtherOffsetsAlone(t *testing.T) {
	fn := &Func{Name: "f", Ops: []Op{
		{Kind: OpConstFunc, Str: "target"}, {Kind: OpStoreLocal, I32: 1},
		{Kind: OpLoadLocal, I32: 1},
		{Kind: OpConstI32, I32: 16},
		{Kind: OpAdd},
		{Kind: OpLoad, Width: WidthPtr},
		{Kind: OpCallClosureDirect, Str: "target", I32: 2},
	}}
	before := len(fn.Ops)
	foldZeroCaptureEnvLoadsIn(fn, 8)
	if len(fn.Ops) != before {
		t.Errorf("rewrote %d ops into %d for an offset that is not the env offset", before, len(fn.Ops))
	}
}
