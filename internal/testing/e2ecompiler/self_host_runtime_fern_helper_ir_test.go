package e2ecompiler

import (
	"strings"
	"testing"
)

func TestSelfHostRuntimeHelpersAreFernIR(t *testing.T) {
	cli := newStrictCLI(t)

	cases := []struct {
		name string
		prog string
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
			`import "std/string";
function main(): i32 { return "  hi ".trim().len(); }`,
			"__fn___fern_str_trim",
			[]string{"\n__fern_str_trim:", ".Lir_trim_front", ".Ltrim_front"},
		},
		{
			"str_lines",
			`import "std/string";
function main(): i32 { return "a\nb\n".lines().len(); }`,
			"__fn___fern_str_lines",
			[]string{"\n__fern_str_lines:", ".Lir_lines_box"},
		},
		{
			"str_bytes",
			`import "std/string";
function main(): i32 { return "abc".bytes().len(); }`,
			"__fn___fern_str_bytes_u8",
			[]string{"\n__fern_str_bytes:", ".Lir_bytes_loop"},
		},
		{
			"str_concat",
			`function main(): i32 { let xs: string[] = args(); let s: string = xs[0] + "cd"; return s.len(); }`,
			"__fn___fern_str_concat",
			[]string{"\n__fern_str_concat:", ".Lstrconcat_a_loop"},
		},
		{
			"str_to_upper",
			`import "std/string";
function main(): i32 { return "aB".to_ascii_upper()[0] as i32; }`,
			"__fn___fern_str_to_upper",
			[]string{"\n__fern_str_to_upper:", ".Lir_upper_loop"},
		},
		{
			"str_to_lower",
			`import "std/string";
function main(): i32 { return "Ab".to_ascii_lower()[0] as i32; }`,
			"__fn___fern_str_to_lower",
			[]string{"\n__fern_str_to_lower:", ".Lir_lower_loop"},
		},
		{
			"str_repeat",
			`import "std/string";
function main(): i32 { return "ab".repeat(3).len(); }`,
			"__fn___fern_str_repeat",
			[]string{"\n__fern_str_repeat:", ".Lir_rep_outer"},
		},
		{
			"str_replace",
			`import "std/string";
function main(): i32 { return "a.b".replace(".", "-").len(); }`,
			"__fn___fern_str_replace",
			[]string{"\n__fern_str_replace:", ".Lir_repl_walk"},
		},
		{
			"string_from_bytes_unchecked",
			`function main(): i32 { let b: u8[] = [104 as u8, 105 as u8]; return string_from_bytes_unchecked(b).len(); }`,
			"__fn___fern_string_from_bytes_u8",
			[]string{"\n__fern_string_from_bytes:", ".Lir_sfb_loop"},
		},
		{
			"str_split",
			`import "std/string";
function main(): i32 { return "a,b,c".split(",").len(); }`,
			"__fn___fern_str_split",
			[]string{"\n__fern_str_split:", ".Lir_split_cl"},
		},
		{
			"random_bytes",
			`function main(): i32 { let b: u8[] = random_bytes(8); return b.len(); }`,
			"__fn___fern_random_bytes_u8",
			[]string{"\n__fern_random_bytes:"},
		},
		{
			"monotonic_ns",
			`function main(): i32 { let a: i64 = monotonic_ns(); if (a > (0 as i64)) { return 1; } return 0; }`,
			"__fn___fern_monotonic_ns",
			[]string{"\n__fern_monotonic_ns:"},
		},
		{
			"now_unix_ms",
			`function main(): i32 { let a: i64 = now_unix_ms(); if (a > (0 as i64)) { return 1; } return 0; }`,
			"__fn___fern_now_unix_ms",
			[]string{"\n__fern_now_unix_ms:"},
		},
		{
			"now_ns",
			`function main(): i32 { let a: i64 = now_ns(); if (a > (0 as i64)) { return 1; } return 0; }`,
			"__fn___fern_now_ns",
			[]string{"\n__fern_now_ns:"},
		},
		{
			"stat",
			`function main(): i32 { match (stat("/tmp")) { Ok(s) => { return 1; }, Err(e) => { return 0; } } }`,
			"__fn___fern_stat",
			[]string{"\n__fern_stat:"},
		},
		{
			"read_file",
			`function main(): i32 { match (read_file("/nonexistent")) { Ok(s) => { return 1; }, Err(e) => { return 0; } } }`,
			"__fn___fern_read_file",
			[]string{"\n__fern_read_file:"},
		},
		{
			"remove_file",
			`function main(): i32 { match (remove_file("/nonexistent-xyz")) { Ok(_) => { return 0; }, Err(e) => { return 1; } } }`,
			"__fn___fern_remove_file",
			[]string{"\n__fern_remove_file:"},
		},
		{
			"write_file",
			`function main(): i32 { match (write_file("/tmp/fern-lockin-wf", "x")) { Ok(_) => { return 0; }, Err(e) => { return 1; } } }`,
			"__fn___fern_write_file",
			[]string{"\n__fern_write_file:"},
		},
		{
			"temp_dir",
			`function main(): i32 { match (temp_dir("ferntest")) { Ok(p) => { return 0; }, Err(e) => { return 1; } } }`,
			"__fn___fern_temp_dir",
			[]string{"\n__fern_temp_dir:"},
		},
		{
			"env",
			`function main(): i32 { match (env("PATH")) { Some(v) => { return 0; }, None => { return 1; } } }`,
			"__fn___fern_env",
			[]string{"\n__fern_env:"},
		},
		{
			"read_dir",
			`function main(): i32 { match (read_dir("/tmp")) { Ok(es) => { return 0; }, Err(e) => { return 1; } } }`,
			"__fn___fern_read_dir",
			[]string{"\n__fern_read_dir:", ".Lrd_g", ".Lrd_p2"},
		},
		{
			"remove_dir_all",
			`function main(): i32 { match (remove_dir_all("/nonexistent-fern-xyz")) { Ok(_) => { return 0; }, Err(e) => { return 1; } } }`,
			"__fn___fern_remove_dir_all",
			[]string{"\n__fern_remove_dir_all:", ".Lrda_copy", ".Lrda_it"},
		},
		{
			"print_str",
			`function main(): i32 { print("x"); return 0; }`,
			"__fn___fern_print_str",
			[]string{"\n__fern_print_str:"},
		},
		{
			"putchar",
			`function main(): i32 { putchar(65); return 0; }`,
			"__fn___fern_putchar",
			[]string{"\n__fern_putchar:"},
		},
		{
			"eprint_str",
			`function main(): i32 { eprint("x"); return 0; }`,
			"__fn___fern_eprint_str",
			[]string{"\n__fern_eprint_str:"},
		},
		{
			"reader_read_chunk",
			`function main(): i32 { let r: Reader = stdin(); return match (r.read_chunk(4096)) { Ok(c) => c.len(), Err(e) => 0 }; }`,
			"__fn___fern_reader_read_chunk",
			[]string{"\n__fern_reader_read_chunk:", ".Lirc_none", ".Lirc_ret"},
		},
		{
			"reader_close",
			`function main(): i32 { let r: Reader = stdin(); r.close(); return 7; }`,
			"__fn___fern_reader_close",
			[]string{"\n__fern_reader_close:"},
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			got := cli.emit(t, "x86-64-linux", tc.prog)
			if !strings.Contains(got, tc.sym+":") {
				t.Errorf("asm missing %s: definition — helper no longer compiled from Fern?", tc.sym)
			}
			if !strings.Contains(got, "call "+tc.sym) {
				t.Errorf("asm missing call %s — call site not resolving to the Fern helper", tc.sym)
			}
			for _, bad := range tc.gone {
				scope := got
				if !strings.HasPrefix(bad, "\n") && !strings.HasPrefix(bad, ".") {
					scope = extractFuncBody(got, tc.sym)
				}
				if strings.Contains(scope, bad) {
					t.Errorf("asm still contains hand-written form %q — migration regressed", bad)
				}
			}
		})
	}

	t.Run("io_error_shared", func(t *testing.T) {
		const prog = `function main(): i32 {
    let r: i32 = 0;
    match (stat("/nope")) { Ok(_) => {}, Err(_) => { r = r + 1; } }
    match (read_file("/nope")) { Ok(_) => {}, Err(_) => { r = r + 1; } }
    match (remove_file("/nope")) { Err(_) => { r = r + 1; }, Ok(_) => {} }
    match (write_file("/nope-dir/x", "y")) { Err(_) => { r = r + 1; }, Ok(_) => {} }
    match (temp_dir("/nope-parent/p")) { Ok(_) => {}, Err(_) => { r = r + 1; } }
    match (read_dir("/nope")) { Ok(_) => {}, Err(_) => { r = r + 1; } }
    match (remove_dir_all("/nope-parent/deep/tree")) { Err(_) => { r = r + 1; }, Ok(_) => {} }
    return r;
}`
		got := cli.emit(t, "x86-64-linux", prog)
		if n := strings.Count(got, "\n__fn___fern_io_error:"); n != 1 {
			t.Errorf("__fn___fern_io_error defined %d times, want exactly 1 (shared classifier)", n)
		}
		if !strings.Contains(got, "call __fn___fern_io_error") {
			t.Error("no `call __fn___fern_io_error` — the fs helpers are not calling the shared classifier")
		}
		for _, sym := range []string{"__fn___fern_stat", "__fn___fern_read_file", "__fn___fern_remove_file", "__fn___fern_write_file", "__fn___fern_temp_dir", "__fn___fern_read_dir", "__fn___fern_remove_dir_all"} {
			body := extractFuncBody(got, sym)
			if body == "" {
				t.Errorf("%s not defined in the fs bundle", sym)
				continue
			}
			if !strings.Contains(body, "call __fn___fern_io_error") {
				t.Errorf("%s does not call __fern_io_error — it is inlining the errno mapping instead of delegating", sym)
			}
		}
	})
}

func extractFuncBody(asm, sym string) string {
	start := strings.Index(asm, "\n"+sym+":")
	if start < 0 {
		return ""
	}
	body := asm[start+1:]
	for off := len(sym) + 2; ; {
		next := strings.Index(body[off:], "\n")
		if next < 0 {
			return body
		}
		lineStart := off + next + 1
		rest := body[lineStart:]
		if len(rest) > 0 && rest[0] != ' ' && rest[0] != '\t' && rest[0] != '.' && rest[0] != 'L' && rest[0] != '\n' && (rest[0] < '0' || rest[0] > '9') && !strings.HasPrefix(rest, sym+".r:") && !strings.HasPrefix(rest, sym+".k:") {
			if end := strings.Index(rest, "\n"); end < 0 || strings.Contains(rest[:end], ":") {
				return body[:lineStart]
			}
		}
		off = lineStart
	}
}
