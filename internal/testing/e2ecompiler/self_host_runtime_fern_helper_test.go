package e2ecompiler

import (
	"bytes"
	"os/exec"
	"strings"
	"testing"
)

func TestSelfHostRuntimeHelpersAreFern(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "drivers/asm_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "drivers/asm_run.fern", "driver")

	cases := []struct {
		name string
		src  string
		sym  string
		gone []string
	}{
		{
			"str_cmp",
			`function main(): i32 { if ("abc" < "abd") { return 1; } return 0; }`,
			"__fn___fern_str_cmp",
			[]string{"\n__fern_str_cmp:", ".Lstrcmp_loop"},
		},
		{
			"str_eq",
			`function main(): i32 { let xs: string[] = args(); if (xs[0] == "ab") { return 1; } return 0; }`,
			"__fn___fern_str_eq",
			[]string{"\n__fern_str_eq:", ".Lstreq_loop"},
		},
		{
			"str_trim",
			`function main(): i32 { return "  hi ".trim().len(); }`,
			"__fn___fern_str_trim",
			[]string{"\n__fern_str_trim:", ".Ltrim_front"},
		},
		{
			"str_lines",
			`function main(): i32 { return "a\nb\n".lines().len(); }`,
			"__fn___fern_str_lines",
			[]string{".Llines_have_"},
		},
		{
			"str_bytes",
			`function main(): i32 { return "abc".bytes().len(); }`,
			"__fn___fern_str_bytes_u8",
			[]string{"\n__fern_str_bytes:", ".Lbytes_loop"},
		},
		{
			"str_concat",
			`function main(): i32 { let s: string = "ab" + "cd"; return s.len(); }`,
			"__fn___fern_str_concat",
			[]string{"\n__fern_str_concat:", ".Lstrconcat_a_loop"},
		},
		{
			"str_to_upper",
			`function main(): i32 { return "aB".to_ascii_upper()[0] as i32; }`,
			"__fn___fern_str_to_upper",
			[]string{"\n__fern_str_to_upper:", ".Lupper_loop"},
		},
		{
			"str_to_lower",
			`function main(): i32 { return "Ab".to_ascii_lower()[0] as i32; }`,
			"__fn___fern_str_to_lower",
			[]string{"\n__fern_str_to_lower:", ".Llower_loop"},
		},
		{
			"str_repeat",
			`function main(): i32 { return "ab".repeat(3).len(); }`,
			"__fn___fern_str_repeat",
			[]string{"\n__fern_str_repeat:", ".Lrep_outer"},
		},
		{
			"str_replace",
			`function main(): i32 { return "a.b".replace(".", "-").len(); }`,
			"__fn___fern_str_replace",
			[]string{"\n__fern_str_replace:", ".Lrepl_walk"},
		},
		{
			"string_from_bytes_unchecked",
			`function main(): i32 { let b: u8[] = [104 as u8, 105 as u8]; return string_from_bytes_unchecked(b).len(); }`,
			"__fn___fern_string_from_bytes_u8",
			[]string{"\n__fern_string_from_bytes:", ".Lsfb_loop"},
		},
		{
			"str_split",
			`function main(): i32 { return "a,b,c".split(",").len(); }`,
			"__fn___fern_str_split",
			[]string{"\n__fern_str_split:", ".Lsplit_cl"},
		},
		{
			"arr_str_join",
			`function main(): i32 { let xs: string[] = ["a", "b"]; return xs.join(",").len(); }`,
			"__fn___fern_arr_str_join",
			[]string{"\n__fern_arr_str_join:", ".Lasj_loop"},
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			var cmd *exec.Cmd
			if len(runner) == 0 {
				cmd = exec.Command(driverBin)
			} else {
				cmd = exec.Command(runner[0], append(runner[1:], driverBin)...)
			}
			cmd.Stdin = bytes.NewReader([]byte(tc.src))
			asm, err := cmd.Output()
			if err != nil {
				t.Fatalf("driver run: %v", err)
			}
			got := string(asm)
			if !strings.Contains(got, tc.sym+":") {
				t.Errorf("emitted asm missing definition %q — %s no longer compiled from Fern?", tc.sym+":", tc.name)
			}
			if !strings.Contains(got, "call "+tc.sym) {
				t.Errorf("emitted asm missing %q — call site not migrated to the Fern symbol", "call "+tc.sym)
			}
			for _, bad := range tc.gone {
				if strings.Contains(got, bad) {
					t.Errorf("emitted asm still contains hand-written form %q — %s migration regressed", bad, tc.name)
				}
			}
		})
	}
}
