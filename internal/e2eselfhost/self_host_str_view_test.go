package e2eselfhost

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// strViewSelfHostProgram uses the `str` borrowed-string view type (#4813) in
// every position the self-host parse-boundary erasure must cover: param,
// var annotation, array-of-views (`str[]`), and return type.
const strViewSelfHostProgram = `function view_len(v: str): i32 {
    return v.len();
}

function pick(vs: str[]): str {
    return vs[0];
}

function main(): i32 {
    var s: string = "hello";
    var v: str = s;
    var vs: str[] = ["ab", "cde"];
    var p: str = pick(vs);
    var h: str = s[1:4];
    return view_len(s) + view_len(v) + p.len() + h.len();
}
`

// strViewSelfHostStringSpelled is the same program with every `str` spelled
// `string` — the erasure oracle.
const strViewSelfHostStringSpelled = `function view_len(v: string): i32 {
    return v.len();
}

function pick(vs: string[]): string {
    return vs[0];
}

function main(): i32 {
    var s: string = "hello";
    var v: string = s;
    var vs: string[] = ["ab", "cde"];
    var p: string = pick(vs);
    var h: string = s[1:4];
    return view_len(s) + view_len(v) + p.len() + h.len();
}
`

// TestSelfHostStrViewErasure pins the self-host acceptance of the `str` view
// type (#4813, the self-host leg of the native ir/erase_str.go erasure): the
// self-host parser normalizes `str` → "string" at the parse boundary
// (parser.fern parse_type_name), so a str-spelled program must emit
// BYTE-IDENTICAL output to its string-spelled twin through the whole
// self-host wasm-IR pipeline. View-discipline enforcement (E065, the borrow
// rules) is the native checker's job; the self-host's contract is faithful
// compilation, which byte-equality against the string oracle proves
// completely — every string-program behavior suite then covers `str` too.
func TestSelfHostStrViewErasure(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	for _, name := range []string{
		"util.fern", "astwalk.fern", "asmcore.fern", "lexer.fern", "parser.fern",
		"ir.fern", "irlower.fern", "asm_ir.fern", "wasm_ir.fern", "wasm_ir_run.fern",
	} {
		src, err := os.ReadFile(filepath.Join("../../examples/self_host", name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		if err := os.WriteFile(filepath.Join(dir, name), src, 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	driverBin := buildSelfHostBin(t, gcc, dir, "wasm_ir_run.fern", "driver")

	emit := func(t *testing.T, src string) []byte {
		t.Helper()
		var cmd *exec.Cmd
		if len(runner) == 0 {
			cmd = exec.Command(driverBin, "-ir")
		} else {
			cmd = exec.Command(runner[0], append(append(append([]string{}, runner[1:]...), driverBin), "-ir")...)
		}
		cmd.Stdin = bytes.NewReader([]byte(src + "\n"))
		wat, err := cmd.Output()
		if err != nil || len(wat) == 0 {
			t.Fatalf("driver failed: %v (got %d bytes)", err, len(wat))
		}
		return wat
	}

	strWat := emit(t, strViewSelfHostProgram)
	oracleWat := emit(t, strViewSelfHostStringSpelled)
	if !bytes.Equal(strWat, oracleWat) {
		t.Fatalf("str-spelled program emitted different wat than its string-spelled twin (%d vs %d bytes) — the parse-boundary erasure is leaking", len(strWat), len(oracleWat))
	}
}
