package e2ecompiler

import (
	"testing"

	"github.com/jakechampion/lang/internal/testing/e2eharness"
)

func TestSelfHostBorrowedLocalLoansNative(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/checker_codes_run.fern")
	bin := buildSelfHostBin(t, gcc, dir, "drivers/checker_codes_run.fern", "borrowed_local_loans")
	resultLoanCases(t, bin, runner, dir, "borrowed_local_loans_cases.json")
}

func TestSelfHostBorrowedLocalLoansWasm(t *testing.T) {
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/checker_codes_run.fern")
	bin := buildSelfHostBinFor(t, dir, "drivers/checker_codes_run.fern", "borrowed_local_loans.wasm", e2eharness.TargetWasm32Wasi)
	resultLoanCases(t, bin, []string{e2eharness.Wasmtime(t), "run"}, dir, "borrowed_local_loans_cases.json")
}
