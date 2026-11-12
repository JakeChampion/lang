package e2ecompiler

import (
	"encoding/json"
	"os"
	"os/exec"
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/testing/e2eharness"
)

func callbackResultLoanCases(t *testing.T, bin string, runner []string, dir string) {
	t.Helper()
	data, err := os.ReadFile(langSrcAbs(t, "internal/check/checker/testdata/callback_result_loans_cases.json"))
	if err != nil {
		t.Fatal(err)
	}
	var cases []struct{ Name, Source, Want string }
	if err := json.Unmarshal(data, &cases); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{
		"diag_e050_factory_callback_uses_consumed_capture",
		"diag_e050_factory_callback_uses_consumed_local",
		"diag_e050_dynamic_factory_uses_consumed_capture",
	} {
		source, err := os.ReadFile(e2eharness.RepoPath("conformance", "cases", name, "main.fern"))
		if err != nil {
			t.Fatal(err)
		}
		cases = append(cases, struct{ Name, Source, Want string }{name, string(source), "E050"})
	}
	for _, tc := range cases {
		t.Run(tc.Name, func(t *testing.T) {
			cmd := exec.Command(bin)
			if len(runner) > 0 {
				cmd = exec.Command(runner[0], append(append([]string{}, runner[1:]...), bin)...)
			}
			cmd.Stdin = strings.NewReader(tc.Source)
			for label, ds := range map[string][]driverDiag{
				"Go":   goCheckerDiags(t, dir, tc.Source),
				"Fern": driverDiags(runCheckerDriver(t, cmd, tc.Name)),
			} {
				if tc.Want == "" && len(ds) == 0 {
					continue
				}
				found := false
				for _, d := range ds {
					found = found || tc.Want != "" && d.code == tc.Want
				}
				if !found {
					t.Errorf("%s: want %q, got %v", label, tc.Want, ds)
				}
			}
		})
	}
}

func TestSelfHostCallbackResultLoansNative(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/checker_codes_run.fern")
	bin := buildSelfHostBin(t, gcc, dir, "drivers/checker_codes_run.fern", "callback_result_loans")
	callbackResultLoanCases(t, bin, runner, dir)
}

func TestSelfHostCallbackResultLoansWasm(t *testing.T) {
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/checker_codes_run.fern")
	bin := buildSelfHostBinFor(t, dir, "drivers/checker_codes_run.fern", "callback_result_loans.wasm", "wasm32-wasi")
	callbackResultLoanCases(t, bin, []string{e2eharness.Wasmtime(t), "run"}, dir)
}

func TestSelfHostAggregateCallbackEnvironmentRun(t *testing.T) {
	ownedResultRuntimeCase(t, "callback_aggregate_environment_loans")
}
