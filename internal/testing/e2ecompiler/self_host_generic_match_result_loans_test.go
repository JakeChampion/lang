package e2ecompiler

import (
	"testing"

	"github.com/jakechampion/lang/internal/testing/e2eharness"
)

func TestSelfHostGenericMatchResultLoansNative(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/checker_codes_run.fern")
	bin := buildSelfHostBin(t, gcc, dir, "drivers/checker_codes_run.fern", "generic_match_result_loans")
	resultLoanCases(t, bin, runner, dir, "generic_match_result_loans_cases.json")
}

func TestSelfHostGenericMatchResultLoansWasm(t *testing.T) {
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/checker_codes_run.fern")
	bin := buildSelfHostBinFor(t, dir, "drivers/checker_codes_run.fern", "generic_match_result_loans.wasm", e2eharness.TargetWasm32Wasi)
	resultLoanCases(t, bin, []string{e2eharness.Wasmtime(t), "run"}, dir, "generic_match_result_loans_cases.json")
}

func TestSelfHostGenericMatchOwnedPayloadsRun(t *testing.T) {
	ownedResultRuntimeCase(t, "generic_match_owned_payloads")
}
