package e2eselfhost

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// The wasm half of TestSelfHostSharedVariantPayloadIRX86_64, and it fails
// differently: wasm has true 32/64-bit stack types, so storing an f64 payload
// through the i32.store the WRONG decl selected does not miscompile — the
// module does not validate at all ("type mismatch: expected i32, found f64").
// The register backends store every field as 8 bytes and so never saw it.
//
// The construction side is what wasm rejects: op_struct_make named its shape
// and the backend re-derived each field's store width from that name, which two
// enums declaring one variant name share. The op now carries the resolved decl
// index (ir.Op.decl) and the backend reads widths off it.
//
// Kept separate from the x86 file rather than folded in as a third target: the
// failure mode, the driver (wasm_ir_run) and the runner all differ, and this one
// skips without wasmtime.
func TestSelfHostSharedVariantPayloadWasmIR(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping self-host shared-variant-payload wasm IR e2e")
	}
	gcc, runner := x86_64Tooling(t)
	interpBin := buildLangBinForInterp(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "wasm_ir_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "wasm_ir_run.fern", "driver")

	for _, tc := range sharedVariantPayloadCases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			src := tc.src + "\n"
			want := interpExit(t, interpBin, src)
			var cmd *exec.Cmd
			if len(runner) == 0 {
				cmd = exec.Command(driverBin, "-ir")
			} else {
				cmd = exec.Command(runner[0], append(append(append([]string{}, runner[1:]...), driverBin), "-ir")...)
			}
			cmd.Stdin = bytes.NewReader([]byte(src))
			cmd.Env = append(os.Environ(), "FERN_STRICT_IR=1")
			wat, err := cmd.Output()
			if err != nil || len(wat) == 0 {
				t.Fatalf("driver failed for %s: %v", tc.name, err)
			}
			watFile := filepath.Join(dir, tc.name+".wat")
			if err := os.WriteFile(watFile, wat, 0o644); err != nil {
				t.Fatalf("write wat: %v", err)
			}
			rcmd := exec.Command("wasmtime", "run", watFile)
			var stderr bytes.Buffer
			rcmd.Stderr = &stderr
			_ = rcmd.Run()
			if rcmd.ProcessState == nil || !rcmd.ProcessState.Exited() {
				t.Fatalf("%s did not exit normally\n%s", tc.name, stderr.String())
			}
			if code := rcmd.ProcessState.ExitCode(); code != want {
				t.Errorf("%s exited %d, want %d (interp oracle)\n%s", tc.name, code, want, stderr.String())
			}
		})
	}
}
