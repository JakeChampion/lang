package e2ecompiler

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/testing/e2eharness"
)

func genericResultBorrowCases(t *testing.T, bin string, runner []string, dir string) {
	t.Helper()
	for _, name := range []string{
		"associated_factory_result_is_owned",
		"diag_e051_associated_factory_borrowed_argument",
		"diag_e051_associated_factory_shadowed_type",
		"diag_e051_generic_result_identity",
		"diag_e051_generic_result_index",
		"diag_e051_generic_result_for",
		"diag_e051_generic_callback_capture",
		"diag_e051_generic_callback_owned",
		"diag_e051_generic_callback_local",
		"diag_e051_generic_callback_forward",
		"diag_e051_generic_callback_capture_helper",
		"generic_result_owned_and_handle_free",
		"generic_callback_copies_and_shared_results",
	} {
		t.Run(name, func(t *testing.T) {
			path := e2eharness.RepoPath("conformance", "cases", name, "main.fern")
			source, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			check := func(label string, ds []driverDiag) {
				t.Helper()
				if !strings.HasPrefix(name, "diag_") {
					if len(ds) != 0 {
						t.Fatalf("%s: want no diagnostics, got %v", label, ds)
					}
					return
				}
				if len(ds) == 0 {
					t.Fatalf("%s: want E051, got no diagnostics", label)
				}
				for _, d := range ds {
					if d.code != "E051" {
						t.Fatalf("%s: want only E051, got %v", label, ds)
					}
				}
			}
			check("Go", goCheckerDiags(t, dir, string(source)))
			command := exec.Command(bin)
			if len(runner) > 0 {
				args := append(append([]string{}, runner[1:]...), bin)
				command = exec.Command(runner[0], args...)
			}
			command.Stdin = strings.NewReader(string(source))
			check("self-host", driverDiags(runCheckerDriver(t, command, filepath.Base(name))))
		})
	}
}

func TestSelfHostGenericResultBorrowX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/checker_codes_run.fern")
	bin := buildSelfHostBin(t, gcc, dir, "drivers/checker_codes_run.fern", "generic_result_checker")
	genericResultBorrowCases(t, bin, runner, dir)
}

func TestSelfHostGenericResultBorrowWasm(t *testing.T) {
	wasmtime, err := exec.LookPath("wasmtime")
	if err != nil {
		t.Skip("wasmtime not on PATH")
	}
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/checker_codes_run.fern")
	bin := buildSelfHostBinFor(t, dir, "drivers/checker_codes_run.fern", "generic_result_checker.wasm", e2eharness.TargetWasm32Wasi)
	genericResultBorrowCases(t, bin, []string{wasmtime, "run"}, dir)
}
