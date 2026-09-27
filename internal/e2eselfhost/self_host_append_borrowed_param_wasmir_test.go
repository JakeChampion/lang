package e2eselfhost

import (
	"testing"
)

// TestSelfHostAppendBorrowedParamWasmIR — the #4873 caller-side may-grow
// containment on the self-host WASM-IR backend (#5325, re-landing the
// reverted #5138). Three pieces:
//
//   - the rc-uniqueness gate in $__fern_arr_push (arr_push_helper): in-place
//     append only for a sole-owner (rc==1) or immortal (bit-31) receiver;
//     a shared receiver takes the copy path (un-share copies keep the SAME
//     cap — the #3425 arena lesson);
//   - the caller-side share bracket wired to the register-backend pair
//     (share_inc → $__fern_rc_inc, share_dec → the freeing $__fern_arr_dec)
//     instead of the historical no-op (whose "arrays are headerless" premise
//     was stale — wasm-IR arrays are rc-headered via $__fern_arr_box);
//   - the root-cause fix that blocked #5138: $__fern_arr_push_owned frees
//     the superseded old buffer ONLY when it was the sole owner (rc==1),
//     mirroring asm_ir's defensive "not sole owner — leave" gate. The old
//     unconditional delegation to the DECREMENTING $__fern_arr_dec cancelled the
//     bracket's +1 on a bracketed shared receiver, so the bracket's own dec
//     freed the caller's still-referenced buffer — the WIT-codec SIGABRT.
//
// Cases: selfHostAppendBorrowedCases, shared verbatim with the register leg
// (self_host_append_borrowed_param_test) so neither backend can drift from the
// other's containment — including the two whose oracle is __rc_underflow_count(),
// which asserts the rc accounting balances exactly rather than merely that the
// heap survived.
func TestSelfHostAppendBorrowedParamWasmIR(t *testing.T) {
	cli := buildSelfHostCLI(t)
	for _, tc := range selfHostAppendBorrowedCases {
		t.Run(tc.name, func(t *testing.T) {
			src := tc.src + "\n"
			for _, target := range []string{"wasm32-wasi"} {
				if stderr, code := cli.exitOf(t, src, target); code != tc.exit {
					t.Errorf("%s on %s exited %d, want %d\n%s", tc.name, target, code, tc.exit, stderr)
				}
			}
		})
	}
}
