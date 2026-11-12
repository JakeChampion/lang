package e2ecompiler

import (
	"testing"

	"github.com/jakechampion/lang/internal/testing/e2eharness"
)

func TestSelfHostNestedFieldTransfersNative(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/checker_codes_run.fern")
	bin := buildSelfHostBin(t, gcc, dir, "drivers/checker_codes_run.fern", "nested_field_transfers")
	resultLoanCases(t, bin, runner, dir, "nested_field_transfer_cases.json")
}

func TestSelfHostNestedFieldTransfersWasm(t *testing.T) {
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/checker_codes_run.fern")
	bin := buildSelfHostBinFor(t, dir, "drivers/checker_codes_run.fern", "nested_field_transfers.wasm", e2eharness.TargetWasm32Wasi)
	resultLoanCases(t, bin, []string{e2eharness.Wasmtime(t), "run"}, dir, "nested_field_transfer_cases.json")
}

func TestSelfHostNestedOwnedFieldTransferRun(t *testing.T) {
	ownedResultRuntimeCase(t, "nested_owned_field_transfer")
}
