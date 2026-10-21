package e2ecompiler

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// The shipped interpreter runs these conformance cases as the oracle and the
// compiled targets do (#12687): the runtime's own intrinsics, map order after
// a delete, Map.iter and Map.cleared, nested, struct and `@` destructuring, a
// tuple binder's scope, a variant resolved by its scrutinee's enum, the
// checked operators, the i64 / u32 method surface, and a recursion as deep as
// the one tco_sum makes.
func TestSelfHostInterpConformanceCases(t *testing.T) {
	cli := buildSelfHostCLI(t)
	for _, name := range []string{
		"rc_corpus_map_string_kv",
		"f64_sqrt",
		"tasks_agree_without_a_scheduler",
		"map_delete_and_clear_rebuild_the_map",
		"map_iter_unannotated",
		"map_cleared_alias",
		"map_struct_enum_keys",
		"tuple_nested_destructure",
		"destructure_tuple_and_struct_forms",
		"foreach_pattern_binding",
		"let_else_at_binding",
		"if_let_at_binding_variant",
		"tuple_binder_shadows_outer_var",
		"a_variant_pattern_names_the_scrutinees_enum",
		"int_checked_ops",
		"int_checked_shl",
		"int_checked_div",
		"int_byte_swap",
		"int_bit_accessors",
		"i64_bitops",
		"prim_recv_array_method",
		"tco_sum",
	} {
		t.Run(name, func(t *testing.T) {
			dir := filepath.Join("../../../conformance/cases", name)
			want, _ := os.ReadFile(filepath.Join(dir, "expected.stdout"))
			wantExit := 0
			if b, err := os.ReadFile(filepath.Join(dir, "expected.exit")); err == nil {
				wantExit, _ = strconv.Atoi(strings.TrimSpace(string(b)))
			}
			cmd := runX86_64Bin(cli.runner, cli.bin, "-interp", "main.fern", cli.stdlib)
			cmd.Dir = dir
			out, code := stdoutAndExit(t, cmd)
			if out != string(want) || code != wantExit {
				t.Errorf("exit %d, stdout:\n%s\nwant exit %d, stdout:\n%s", code, out, wantExit, want)
			}
		})
	}
}
