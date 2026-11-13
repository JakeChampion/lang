package e2ecompiler

import (
	"github.com/jakechampion/lang/internal/testing/e2eharness"
	"testing"
)

func TestSelfHostGenericNominalCallbackSignaturesRun(t *testing.T) {
	ownedResultRuntimeCase(t, "generic_nominal_callback_signatures")
}

func TestSelfHostGenericNominalCallbackSignaturesNative(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/checker_codes_run.fern")
	bin := buildSelfHostBin(t, gcc, dir, "drivers/checker_codes_run.fern", "nominal_callbacks")
	resultLoanCases(t, bin, runner, dir, "nominal_callback_cases.json")
}

func TestSelfHostGenericNominalCallbackSignaturesWasm(t *testing.T) {
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/checker_codes_run.fern")
	bin := buildSelfHostBinFor(t, dir, "drivers/checker_codes_run.fern", "nominal_callbacks.wasm", e2eharness.TargetWasm32Wasi)
	resultLoanCases(t, bin, []string{e2eharness.Wasmtime(t), "run"}, dir, "nominal_callback_cases.json")
}
