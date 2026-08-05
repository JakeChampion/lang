package e2eselfhost

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
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
//     unconditional delegation to the DECREMENTING $__fern_arr_dec ate the
//     bracket's +1 on a bracketed shared receiver, so the bracket's own dec
//     freed the caller's still-referenced buffer — the WIT-codec SIGABRT.
//
// Cases: selfHostAppendBorrowedCases, shared verbatim with the register leg
// (self_host_append_borrowed_param_test) so neither backend can drift from the
// other's containment — including the two whose oracle is __rc_underflow(),
// which asserts the rc accounting balances exactly rather than merely that the
// heap survived.
func TestSelfHostAppendBorrowedParamWasmIR(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping wasm-IR borrowed-param e2e")
	}
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	for _, name := range []string{
		"util.fern", "astwalk.fern", "asmcore.fern", "lexer.fern", "parser.fern",
		"ir.fern", "irlower.fern", "asm_ir.fern", "wasm_ir.fern", "wasm_ir_run.fern",
	} {
		src, err := os.ReadFile(filepath.Join("../../examples/self_host", name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		if err := os.WriteFile(filepath.Join(dir, name), src, 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	driverBin := buildSelfHostBin(t, gcc, dir, "wasm_ir_run.fern", "driver")

	for _, tc := range selfHostAppendBorrowedCases {
		t.Run(tc.name, func(t *testing.T) {
			var cmd *exec.Cmd
			if len(runner) == 0 {
				cmd = exec.Command(driverBin, "-ir")
			} else {
				cmd = exec.Command(runner[0], append(append(append([]string{}, runner[1:]...), driverBin), "-ir")...)
			}
			cmd.Stdin = bytes.NewReader([]byte(tc.src + "\n"))
			wat, err := cmd.Output()
			if err != nil || len(wat) == 0 {
				t.Fatalf("driver failed for %q: %v", tc.name, err)
			}
			watFile := filepath.Join(dir, "bp_prog.wat")
			if err := os.WriteFile(watFile, wat, 0o644); err != nil {
				t.Fatalf("write wat: %v", err)
			}
			run := exec.Command("wasmtime", "run", watFile)
			_ = run.Run()
			if run.ProcessState == nil || !run.ProcessState.Exited() {
				t.Fatalf("wasmtime did not exit normally for %q", tc.name)
			}
			if code := run.ProcessState.ExitCode(); code != tc.exit {
				t.Errorf("%s exited %d, want %d", tc.name, code, tc.exit)
			}
		})
	}
}
