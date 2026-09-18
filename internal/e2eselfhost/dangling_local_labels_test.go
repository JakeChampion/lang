package e2eselfhost

import (
	"reflect"
	"testing"
)

// TestDanglingLocalLabels pins the matcher behind assertNoDanglingLocalLabels.
// It reported a false positive the moment parser.fern grew its first capturing
// lambda: a hoisted closure is named `<fn>$cloN`, and the label character class
// stopped at the `$`, so `jz .Lir_f$clo0_11` was recorded as a reference to
// `.Lir_f` while the definition line kept the whole name.
func TestDanglingLocalLabels(t *testing.T) {
	for _, tc := range []struct {
		name string
		asm  string
		want []string
	}{
		{"clean", "" +
			".Lir_f_1:\n" +
			"    jz .Lir_f_1\n", nil},
		{"real-dangling", "" +
			".Lir_f_1:\n" +
			"    jz .Lir_f_2\n", []string{".Lir_f_2"}},
		{"closure-label-is-not-dangling", "" +
			".Lir_parser__rl_expr_kids$clo0_11:\n" +
			"    jz .Lir_parser__rl_expr_kids$clo0_11\n", nil},
		{"dangling-inside-a-closure", "" +
			".Lir_parser__rl_expr_kids$clo0_11:\n" +
			"    jz .Lir_parser__rl_expr_kids$clo0_18\n",
			[]string{".Lir_parser__rl_expr_kids$clo0_18"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := danglingLocalLabels([]byte(tc.asm)); !reflect.DeepEqual(got, tc.want) {
				t.Errorf("danglingLocalLabels() = %v, want %v", got, tc.want)
			}
		})
	}
}

// TestDuplicateLocalLabels pins the matcher behind
// assertNoDuplicateLocalLabels: a label defined twice is reported once, a label
// merely REFERENCED twice is not, and the `.Lssa_*` register-path labels count
// alongside the stack machine's `.Lir_*` / `.Lira_*`.
func TestDuplicateLocalLabels(t *testing.T) {
	for _, tc := range []struct {
		name string
		asm  string
		want []string
	}{
		{"clean", "" +
			".Lir_f_1:\n" +
			"    jz .Lir_f_1\n" +
			".Lir_f_2:\n", nil},
		{"referenced-twice-is-not-a-definition", "" +
			".Lir_f_1:\n" +
			"    jz .Lir_f_1\n" +
			"    jmp .Lir_f_1\n", nil},
		{"defined-twice", "" +
			".Lssa_walk_4:\n" +
			"    ret\n" +
			".Lssa_walk_4:\n" +
			"    ret\n", []string{".Lssa_walk_4"}},
		{"defined-three-times-reported-once", "" +
			".Lira_f_7:\n.Lira_f_7:\n.Lira_f_7:\n", []string{".Lira_f_7"}},
		{"distinct-functions-same-block-id", "" +
			".Lssa_f_2:\n" +
			".Lssa_g_2:\n", nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := duplicateLocalLabels([]byte(tc.asm)); !reflect.DeepEqual(got, tc.want) {
				t.Errorf("duplicateLocalLabels() = %v, want %v", got, tc.want)
			}
		})
	}
}
