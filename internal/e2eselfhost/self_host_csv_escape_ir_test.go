package e2eselfhost

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// csvEscapeIRCases exercise std/csv's `csv_escape` + `csv_join` through the
// self-host IR path on x86-64 + wasm — the remaining "self-host pending" piece
// of std/csv after `csv_parse_line` (TestSelfHostCsvParseLineIR). Unlike the
// parser (builtins only), escape/join pull in the string `.index_of()` /
// `.replace()` methods, which the self-host IR lowers as builtins
// (`op_str_index_of` / `op_str_replace`); this pins that they route through the
// IR path and behave identically to the interpreter oracle.
//
// `import "std/string"` is present so the native interpreter oracle can resolve
// `.index_of()` / `.replace()` (std/string receiver methods, not native
// builtins). The self-host single-program driver does NOT load imports — it
// parses one module and treats those methods as builtins — so the program still
// routes through the IR path. Each case returns a value kept <= 126 and is
// oracle-checked against the interpreter (cf. the hardcoded-expectation gap in
// #2908). FEATURE-AUDIT std/csv row.
const csvEscapeIRPrelude = `import "std/string";
function csv_escape(s: string): string {
    if (s.index_of(",") < 0 && s.index_of("\"") < 0 &&
        s.index_of("\n") < 0 && s.index_of("\r") < 0) {
        return s;
    }
    return "\"" + s.replace("\"", "\"\"") + "\"";
}
function csv_join(arr: string[]): string {
    var n: i32 = arr.len();
    var out: string = "";
    var i: i32 = 0;
    while (i < n) {
        if (i > 0) { out = out + ","; }
        out = out + csv_escape(arr[i]);
        i = i + 1;
    }
    return out;
}
`

var csvEscapeIRCases = []struct {
	name string
	main string
}{
	// Plain field, no special chars: passes through unchanged ("ab" -> "ab", 2).
	{"escape-plain", `return csv_escape("ab").len();`},
	// Embedded comma -> wrapped in quotes ("a,b" -> "\"a,b\"", 5).
	{"escape-comma", `return csv_escape("a,b").len();`},
	// Embedded quote -> interior quote doubled + wrapped ("a\"b" -> "\"a\"\"b\"", 6).
	{"escape-quote", `return csv_escape("a\"b").len();`},
	// Embedded newline -> wrapped ("a\nb" -> "\"a\nb\"", 5).
	{"escape-newline", `return csv_escape("a\nb").len();`},
	// First byte of a quote-escaped field is the wrapping quote (34 == '"').
	{"escape-firstbyte", `var e: string = csv_escape("a,b"); return e[0] as i32;`},
	// Join of plain fields ("a,b,c", 5).
	{"join-plain", `return csv_join(["a", "b", "c"]).len();`},
	// Join where one field needs escaping ("\"a,b\",c", 7).
	{"join-escaped", `return csv_join(["a,b", "c"]).len();`},
}

func csvEscapeIRSrc(mainBody string) string {
	return csvEscapeIRPrelude + "\nfunction main(): i32 { " + mainBody + " }\n"
}

// TestSelfHostCsvEscapeIRX86_64 routes each case through the self-hosted x86-64
// IR driver, oracle-checked, with the routing pinned to the "ir" path.
func TestSelfHostCsvEscapeIRX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	interpBin := buildLangBinForInterp(t)
	dir := writeSelfHostAsmProject(t)
	copySelfHostDriver(t, dir, "asm_run.fern", "asm_pathprobe_run.fern")
	driverBin := buildSelfHostBin(t, gcc, dir, "asm_run.fern", "driver")
	probeBin := buildSelfHostBin(t, gcc, dir, "asm_pathprobe_run.fern", "pathprobe")

	for _, tc := range csvEscapeIRCases {
		t.Run(tc.name, func(t *testing.T) {
			src := []byte(csvEscapeIRSrc(tc.main))
			want := interpExit(t, interpBin, string(src))
			path := strings.TrimSpace(string(runCapture(t, gcc, runner, probeBin, src)))
			if path != "ir" {
				t.Fatalf("%s routed through %q path, want \"ir\"", tc.name, path)
			}
			asm := runCapture(t, gcc, runner, driverBin, src)
			if len(asm) == 0 {
				t.Fatal("self-host compiler emitted 0 bytes")
			}
			progBin := buildBin(t, gcc, dir, tc.name, string(asm))
			var cmd *exec.Cmd
			if len(runner) == 0 {
				cmd = exec.Command(progBin)
			} else {
				cmd = exec.Command(runner[0], append(runner[1:], progBin)...)
			}
			_ = cmd.Run()
			if code := cmd.ProcessState.ExitCode(); code != want {
				t.Errorf("%s exited %d, want %d (interp oracle)", tc.name, code, want)
			}
		})
	}
}

// TestSelfHostCsvEscapeIRWasm runs the same cases through the wasm IR backend.
func TestSelfHostCsvEscapeIRWasm(t *testing.T) {
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not on PATH; skipping self-host csv-escape wasm IR e2e")
	}
	gcc, runner := x86_64Tooling(t)
	interpBin := buildLangBinForInterp(t)
	dir := t.TempDir()
	for _, name := range []string{
		"util.fern", "astwalk.fern", "asmcore.fern", "lexer.fern", "parser.fern",
		"ir.fern", "irlower.fern", "irverify.fern", "irverifystack.fern", "irverifygate.fern", "asm_ir.fern", "wasm_ir.fern", "wasm_ir_run.fern",
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

	for _, tc := range csvEscapeIRCases {
		t.Run(tc.name, func(t *testing.T) {
			src := []byte(csvEscapeIRSrc(tc.main))
			want := interpExit(t, interpBin, string(src))
			var cmd *exec.Cmd
			if len(runner) == 0 {
				cmd = exec.Command(driverBin, "-ir")
			} else {
				cmd = exec.Command(runner[0], append(append(append([]string{}, runner[1:]...), driverBin), "-ir")...)
			}
			cmd.Stdin = bytes.NewReader(src)
			wat, err := cmd.Output()
			if err != nil || len(wat) == 0 {
				t.Fatalf("driver failed for %q: %v", tc.name, err)
			}
			watFile := filepath.Join(dir, "csvesc_prog.wat")
			if err := os.WriteFile(watFile, wat, 0o644); err != nil {
				t.Fatalf("write wat: %v", err)
			}
			run := exec.Command("wasmtime", "run", watFile)
			_ = run.Run()
			if run.ProcessState == nil || !run.ProcessState.Exited() {
				t.Fatalf("wasmtime did not exit normally for %q:\n%s", tc.name, wat)
			}
			if code := run.ProcessState.ExitCode(); code != want {
				t.Errorf("csv-escape wasm IR %q = %d, want %d (interp oracle)", tc.name, code, want)
			}
		})
	}
}
