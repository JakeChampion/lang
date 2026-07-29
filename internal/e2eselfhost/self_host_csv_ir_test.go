package e2eselfhost

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// TestSelfHostCsvParseLineIR covers std/csv's RFC-4180 single-line parser
// through the self-hosted x86-64 IR path (a "self-host pending" audit gap).
// csv_parse_line is self-contained — it uses only builtins (string byte index
// `s[i]`, slice `s[i:j]`, concat, and a `string[]` builder), no std/string
// methods — so it lowers through the IR path directly. (csv_escape / csv_join
// pull in `index_of` / `replace`, deferred.) The program self-checks the parse
// of a quoted-field-with-comma line and returns 42 on success.
func TestSelfHostCsvParseLineIR(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	for _, name := range []string{"util.fern", "astwalk.fern", "asmcore.fern", "lexer.fern", "parser.fern", "ir.fern", "irlower.fern", "asm_ir.fern", "asm.fern", "asm_arm64.fern", "asm_arm64_ir.fern", "asm_ir_run.fern"} {
		src, err := os.ReadFile(filepath.Join("../../examples/self_host", name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		if err := os.WriteFile(filepath.Join(dir, name), src, 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_ir_run.fern", "driver")

	emitAndRunIR := func(t *testing.T, src string) int {
		t.Helper()
		var cmd *exec.Cmd
		if len(runner) == 0 {
			cmd = exec.Command(driverBin, "-ir")
		} else {
			cmd = exec.Command(runner[0], append(append(append([]string{}, runner[1:]...), driverBin), "-ir")...)
		}
		cmd.Stdin = bytes.NewReader([]byte(src))
		emitted, err := cmd.Output()
		if err != nil || len(emitted) == 0 {
			t.Fatalf("driver failed for %q: %v", src, err)
		}
		innerAsm := filepath.Join(dir, "ir_inner.s")
		innerBin := filepath.Join(dir, "ir_inner")
		if err := os.WriteFile(innerAsm, emitted, 0o644); err != nil {
			t.Fatalf("write inner asm: %v", err)
		}
		if out, err := exec.Command(gcc, "-static", "-nostdlib", "-no-pie", innerAsm, "-o", innerBin).CombinedOutput(); err != nil {
			t.Fatalf("inner gcc: %v\n%s", err, out)
		}
		var inner *exec.Cmd
		if len(runner) == 0 {
			inner = exec.Command(innerBin)
		} else {
			inner = exec.Command(runner[0], append(append([]string{}, runner[1:]...), innerBin)...)
		}
		_ = inner.Run()
		if inner.ProcessState == nil || !inner.ProcessState.Exited() {
			t.Fatalf("inner did not exit normally for %q", src)
		}
		return inner.ProcessState.ExitCode()
	}

	// std/csv's csv_parse_line, verbatim.
	const parser = `
function csv_parse_line(s: string): string[] {
    var out: string[] = [];
    var n: i32 = s.len();
    var i: i32 = 0;
    var field: string = "";
    var in_quotes: boolean = false;
    while (i < n) {
        var c: i32 = s[i] as i32;
        if (in_quotes) {
            if (c == 34) {
                if (i + 1 < n && s[i + 1] == 34) { field = field + "\""; i = i + 2; }
                else { in_quotes = false; i = i + 1; }
            } else { field = field + s[i:i + 1]; i = i + 1; }
        } else {
            if (c == 44) { out = out.append(field); field = ""; i = i + 1; }
            else if (c == 34 && field.len() == 0) { in_quotes = true; i = i + 1; }
            else { field = field + s[i:i + 1]; i = i + 1; }
        }
    }
    return out.append(field);
}
`
	cases := []struct {
		name string
		src  string
	}{
		// 4 fields, a quoted field with an embedded comma ("c,d"), and tail field.
		{"quoted-comma", parser + `function main(): i32 {
    var f: string[] = csv_parse_line("a,bb,\"c,d\",e");
    if (f.len() != 4) { return 100; }
    if (f[0].len() != 1 || f[1].len() != 2 || f[2].len() != 3 || f[3].len() != 1) { return 101; }
    if (f[2][0] != 99 || f[2][1] != 44 || f[2][2] != 100) { return 102; }
    return 42;
}`},
		// Doubled quote inside a quoted field decodes to one quote: "a""b" -> a"b.
		{"doubled-quote", parser + `function main(): i32 {
    var f: string[] = csv_parse_line("\"a\"\"b\",x");
    if (f.len() != 2) { return 100; }
    if (f[0].len() != 3) { return 101; }
    if (f[0][0] != 97 || f[0][1] != 34 || f[0][2] != 98) { return 102; }
    return 42;
}`},
		// Plain unquoted line.
		{"plain", parser + `function main(): i32 {
    var f: string[] = csv_parse_line("x,y,z");
    if (f.len() != 3) { return 100; }
    return 42;
}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := emitAndRunIR(t, tc.src); got != 42 {
				t.Errorf("self-host IR csv_parse_line %q: check = %d, want 42", tc.name, got)
			}
		})
	}
}
