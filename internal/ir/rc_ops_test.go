package ir

import "testing"

// #4402 opt 2a — the dedicated rc op kinds. Every rc-inc / rc-dec /
// is-unique probe lowers as OpRcInc / OpRcDec / OpRcIsUnique rather
// than an OpCallDirect that passes only by Str match. Two contracts:
//
//  1. The kinds actually appear where rc traffic is emitted (alias
//     retain, exit-sweep release, is_unique-gated drop).
//  2. Every rc op still carries the OpCallDirect-compatible payload —
//     Str = the runtime helper symbol, I32 = 1 — because all three
//     backends lower them through the shared call path until opt 2b
//     inlines the fast bodies, and the wasm helper-reachability scan
//     keys off Str.
func TestRcOpsCarryCallPayload(t *testing.T) {
	p := lowerSource(t, `function work(k: i32): i32 {
    var x: i32[][] = [[k, k + 1]];
    var e: i32[] = x[0];
    var y: i32[][] = x;
    return e[0] + y[0][1] + x[0][0];
}`)
	wantStr := map[OpKind]string{
		OpRcInc:      "__fern_rc_inc",
		OpRcDec:      "__fern_rc_dec",
		OpRcIsUnique: "__fern_rc_is_unique",
	}
	seen := map[OpKind]int{}
	for _, f := range p.Funcs {
		for _, op := range f.Ops {
			want, isRc := wantStr[op.Kind]
			if !isRc {
				// The three helper symbols must never ride a plain
				// OpCallDirect anymore — that would silently dodge
				// every pass that now matches structurally.
				if op.Kind == OpCallDirect && (op.Str == "__fern_rc_inc" ||
					op.Str == "__fern_rc_dec" || op.Str == "__fern_rc_is_unique") {
					t.Errorf("%s: rc helper emitted as OpCallDirect %q — must use the dedicated kind", f.Name, op.Str)
				}
				continue
			}
			seen[op.Kind]++
			if op.Str != want {
				t.Errorf("%s: %s carries Str %q, want %q (backends key the call + reachability off it)", f.Name, op.Kind, op.Str, want)
			}
			if op.I32 != 1 {
				t.Errorf("%s: %s carries I32=%d, want 1 (shared call-lowering arg count)", f.Name, op.Kind, op.I32)
			}
		}
	}
	// The element alias (`e = x[0]`) retains via OpRcInc; x's releases
	// route through the deep-drop helpers, whose per-element walks and
	// uniqueness gates lower in the generated __drop_arr_* bodies.
	if seen[OpRcInc] == 0 {
		t.Errorf("expected at least one OpRcInc (element-alias retain), got ops:\n%s", p)
	}
	if seen[OpRcDec] == 0 && seen[OpRcIsUnique] == 0 {
		t.Errorf("expected rc release traffic (OpRcDec / OpRcIsUnique) somewhere in the program:\n%s", p)
	}
}
