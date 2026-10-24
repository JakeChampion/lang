package e2ecompiler

import (
	"reflect"
	"testing"
)

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
