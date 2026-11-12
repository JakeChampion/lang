package e2ecompiler

import (
	"testing"

	"github.com/jakechampion/lang/internal/testing/e2eharness"
)

func TestSelfHostMatchPayloadRebindLoansNative(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/checker_codes_run.fern")
	bin := buildSelfHostBin(t, gcc, dir, "drivers/checker_codes_run.fern", "match_payload_rebind")
	resultLoanCases(t, bin, runner, dir, "match_payload_rebind_cases.json")
}

func TestSelfHostMatchPayloadRebindLoansWasm(t *testing.T) {
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/checker_codes_run.fern")
	bin := buildSelfHostBinFor(t, dir, "drivers/checker_codes_run.fern", "match_payload_rebind.wasm", e2eharness.TargetWasm32Wasi)
	resultLoanCases(t, bin, []string{e2eharness.Wasmtime(t), "run"}, dir, "match_payload_rebind_cases.json")
}

func TestSelfHostMatchPayloadSelfUpdateRun(t *testing.T) {
	ownedResultRuntimeCase(t, "match_payload_self_update")
}
