package e2ecompiler

import (
	"encoding/json"
	"os"
	"os/exec"
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/testing/e2eharness"
)

func genericFunctionValueCases(t *testing.T, bin string, runner []string) {
	t.Helper()
	data, err := os.ReadFile(e2eharness.RepoPath("internal/check/checker/testdata/generic_function_value_cases.json"))
	if err != nil {
		t.Fatal(err)
	}
	var cases []struct{ Name, Source, Want string }
	if err := json.Unmarshal(data, &cases); err != nil {
		t.Fatal(err)
	}
	for _, tc := range cases {
		t.Run(tc.Name, func(t *testing.T) {
			cmd := exec.Command(bin)
			if len(runner) > 0 {
				cmd = exec.Command(runner[0], append(append([]string{}, runner[1:]...), bin)...)
			}
			cmd.Stdin = strings.NewReader(tc.Source)
			ds := driverDiags(runCheckerDriver(t, cmd, tc.Name))
			if tc.Want == "" && len(ds) == 0 {
				return
			}
			if len(ds) != 1 || ds[0].code != tc.Want {
				t.Fatalf("want one %s diagnostic, got %v", tc.Want, ds)
			}
		})
	}
}

func TestSelfHostGenericFunctionValueContractsNative(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/checker_codes_run.fern")
	bin := buildSelfHostBin(t, gcc, dir, "drivers/checker_codes_run.fern", "generic_function_values")
	genericFunctionValueCases(t, bin, runner)
}

func TestSelfHostGenericFunctionValueContractsWasm(t *testing.T) {
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/checker_codes_run.fern")
	bin := buildSelfHostBinFor(t, dir, "drivers/checker_codes_run.fern", "generic_function_values.wasm", "wasm32-wasi")
	genericFunctionValueCases(t, bin, []string{e2eharness.Wasmtime(t), "run"})
}

func TestSelfHostGenericFunctionValueInstancesRun(t *testing.T) {
	ownedResultRuntimeCase(t, "generic_array_callbacks_keep_their_types")
}
