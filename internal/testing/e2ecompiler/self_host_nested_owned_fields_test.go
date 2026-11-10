package e2ecompiler

import (
	"os"
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/testing/e2eharness"
)

func TestSelfHostNestedOwnedFields(t *testing.T) {
	for _, target := range []string{e2eharness.TargetX86_64Linux, e2eharness.TargetWasm32Wasi} {
		t.Run(target, func(t *testing.T) {
			dir := t.TempDir()
			copySelfHostDriver(t, dir, "drivers/checker_codes_run.fern")
			bin := buildSelfHostBinFor(t, dir, "drivers/checker_codes_run.fern", "nested_owned_fields", target)
			for _, name := range []string{
				"handle_nested_fields_close_from_owned_root",
				"diag_e051_nested_field_borrowed_root",
				"diag_e051_nested_field_retained_alias",
				"diag_e051_nested_field_reassigned_owner",
				"diag_e051_nested_field_repeated_loop",
				"diag_e051_nested_field_captured_owner",

				"diag_e051_nested_field_borrowed_local",
				"diag_e051_nested_field_field_read_later",
				"diag_e051_nested_field_closed_twice",
				"diag_e051_nested_field_deferred_close",
				"diag_e051_nested_field_whole_read_later",
			} {
				t.Run(name, func(t *testing.T) {
					source, err := os.ReadFile(e2eharness.RepoPath("conformance", "cases", name, "main.fern"))
					if err != nil {
						t.Fatal(err)
					}
					cmd := mvsDriverCommand(t, target, bin)
					cmd.Stdin = strings.NewReader(string(source))
					for label, ds := range map[string][]driverDiag{
						"Go":   goCheckerDiags(t, dir, string(source)),
						"Fern": driverDiags(runCheckerDriver(t, cmd, name)),
					} {
						if strings.HasPrefix(name, "diag_") != (len(ds) > 0) {
							t.Fatalf("%s: unexpected diagnostics: %v", label, ds)
						}
						for _, d := range ds {
							if d.code != "E051" {
								t.Fatalf("%s: expected E051, got %v", label, ds)
							}
						}
					}
				})
			}
		})
	}
}
