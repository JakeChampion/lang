package e2ecompiler

import (
	"testing"

	"github.com/jakechampion/lang/internal/testing/e2eharness"
)

func TestSelfHostDeclaredMethodResultLoansNative(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/checker_codes_run.fern")
	bin := buildSelfHostBin(t, gcc, dir, "drivers/checker_codes_run.fern", "declared_method_result_loans")
	resultLoanCases(t, bin, runner, dir, "declared_method_result_loans_cases.json")
}

func TestSelfHostDeclaredMethodResultLoansWasm(t *testing.T) {
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/checker_codes_run.fern")
	bin := buildSelfHostBinFor(t, dir, "drivers/checker_codes_run.fern", "declared_method_result_loans.wasm", e2eharness.TargetWasm32Wasi)
	resultLoanCases(t, bin, []string{e2eharness.Wasmtime(t), "run"}, dir, "declared_method_result_loans_cases.json")
}

func TestSelfHostDeclaredMethodOwnedResultsRun(t *testing.T) {
	ownedResultRuntimeCase(t, "declared_method_owned_results")
}
