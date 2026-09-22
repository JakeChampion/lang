package e2eselfhost

import (
	"reflect"
	"testing"
)

// TestDanglingLocalLabels pins the matcher behind assertNoDanglingLocalLabels.
// It reported a false positive the moment parser.fern grew its first capturing
// lambda: a hoisted closure is named `<fn>$cloN`, and the label character class
// stopped at the `$`, so `jz .Lssa_f$clo0_11` was recorded as a reference to
// `.Lssa_f` while the definition line kept the whole name.
func TestDanglingLocalLabels(t *testing.T) {
	for _, tc := range []struct {
		name string
		asm  string
		want []string
	}{
		{"clean", "" +
			".Lssa_f_1:\n" +
			"    jz .Lssa_f_1\n", nil},
		{"real-dangling", "" +
			".Lssa_f_1:\n" +
			"    jz .Lssa_f_2\n", []string{".Lssa_f_2"}},
		{"closure-label-is-not-dangling", "" +
			".Lssa_parser__rl_expr_kids$clo0_11:\n" +
			"    jz .Lssa_parser__rl_expr_kids$clo0_11\n", nil},
		{"dangling-inside-a-closure", "" +
			".Lssa_parser__rl_expr_kids$clo0_11:\n" +
			"    jz .Lssa_parser__rl_expr_kids$clo0_18\n",
			[]string{".Lssa_parser__rl_expr_kids$clo0_18"}},
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
// merely REFERENCED twice is not, and every local label prefix counts alike.
func TestDuplicateLocalLabels(t *testing.T) {
	for _, tc := range []struct {
		name string
		asm  string
		want []string
	}{
		{"clean", "" +
			".Lssa_f_1:\n" +
			"    jz .Lssa_f_1\n" +
			".Lssa_f_2:\n", nil},
		{"referenced-twice-is-not-a-definition", "" +
			".Lssa_f_1:\n" +
			"    jz .Lssa_f_1\n" +
			"    jmp .Lssa_f_1\n", nil},
		{"defined-twice", "" +
			".Lssa_walk_4:\n" +
			"    ret\n" +
			".Lssa_walk_4:\n" +
			"    ret\n", []string{".Lssa_walk_4"}},
		{"defined-three-times-reported-once", "" +
			".Lssa_f_7:\n.Lssa_f_7:\n.Lssa_f_7:\n", []string{".Lssa_f_7"}},
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
