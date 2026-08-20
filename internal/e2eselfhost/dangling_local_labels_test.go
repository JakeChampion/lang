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
