package e2ecompiler

import (
	"testing"

	"github.com/jakechampion/lang/internal/testing/e2eharness"
)

func TestSelfHostOwnedCompositeCallbackTypesNative(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/checker_codes_run.fern")
	bin := buildSelfHostBin(t, gcc, dir, "drivers/checker_codes_run.fern", "owned_composite_callback_types")
	resultLoanCases(t, bin, runner, dir, "owned_composite_callback_cases.json")
}

func TestSelfHostOwnedCompositeCallbackTypesWasm(t *testing.T) {
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/checker_codes_run.fern")
	bin := buildSelfHostBinFor(t, dir, "drivers/checker_codes_run.fern", "owned_composite_callback_types.wasm", e2eharness.TargetWasm32Wasi)
	resultLoanCases(t, bin, []string{e2eharness.Wasmtime(t), "run"}, dir, "owned_composite_callback_cases.json")
}

func TestSelfHostOwnedCompositeCallbackTypesRun(t *testing.T) {
	ownedResultRuntimeCase(t, "owned_callback_result_composition")
}
