package e2eselfhost

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// jsonCoreIRCases are real `import "std/json"` programs (qualified
// json.json_parse / json.json_get_* calls, the way modload routes
// stdlib access) that exercise the JSON read path: parse + the typed
// extractors over objects, strings, booleans, arrays, nesting, and
// null. std/json builds its DOM on core/map (`map_new` / `.insert`),
// so until core/map lowered fully through the IR path these programs
// bailed to the legacy AST emitter. With core/map routing IR (the
// __alloc / __ptr_width / __memset / __free / __fern_arr_dec stages),
// the whole read path — json_parse, __json_p_*, and the json_get_*
// extractors — is IR-eligible. The encode path (json_encode → the
// f64/f32 transcendental formatters that need libm) still bails, but
// it is unreachable from a parse-only program, so treeshake prunes it
// and the program reaches `module: IR` through the bundling driver.
//
// Each case asserts the modload -ir-probe verdict is `module: IR` AND
// that the compiled binary matches the interpreter oracle. x86-64 only
// (the loader driver takes argv file paths, like the other modload
// tests). Results are <= 126.
var jsonCoreIRCases = []struct {
	name string
	main string
}{
	// parse object + typed i32 extractor. get_i32("a") = 7.
	{"get-i32", "import \"std/json\";\nfunction main(): i32 { match (json.json_parse(\"{\\\"a\\\": 7}\")) { Some(v) => { match (json.json_get_i32(v, \"a\")) { Some(n) => { return n; }, None => { return 1; }, } }, None => { return 2; }, } }\n"},
	// parse object + string extractor, length of value. len(\"hello\") = 5.
	{"get-string", "import \"std/json\";\nfunction main(): i32 { match (json.json_parse(\"{\\\"k\\\": \\\"hello\\\"}\")) { Some(v) => { match (json.json_get_string(v, \"k\")) { Some(s) => { return s.len(); }, None => { return 1; }, } }, None => { return 2; }, } }\n"},
	// parse object + bool extractor. true => 9.
	{"get-bool", "import \"std/json\";\nfunction main(): i32 { match (json.json_parse(\"{\\\"b\\\": true}\")) { Some(v) => { match (json.json_get_bool(v, \"b\")) { Some(x) => { if (x) { return 9; } return 8; }, None => { return 1; }, } }, None => { return 2; }, } }\n"},
	// parse object holding an array + array extractor. len([1,2,3]) = 3.
	{"array", "import \"std/json\";\nfunction main(): i32 { match (json.json_parse(\"{\\\"xs\\\": [1,2,3]}\")) { Some(v) => { match (json.json_get_array(v, \"xs\")) { Some(a) => { return a.len(); }, None => { return 1; }, } }, None => { return 2; }, } }\n"},
	// nested object: parse, descend, extract. inner.n = 42.
	{"nested", "import \"std/json\";\nfunction main(): i32 { match (json.json_parse(\"{\\\"o\\\": {\\\"n\\\": 42}}\")) { Some(v) => { match (json.json_get_object(v, \"o\")) { Some(o) => { match (json.json_get_i32(o, \"n\")) { Some(n) => { return n; }, None => { return 3; }, } }, None => { return 1; }, } }, None => { return 2; }, } }\n"},
	// null literal + json_is_null predicate. is_null => 5.
	{"is-null", "import \"std/json\";\nfunction main(): i32 { match (json.json_parse(\"null\")) { Some(v) => { if (json.json_is_null(v)) { return 5; } return 4; }, None => { return 2; }, } }\n"},
	// f64 extractor: json_get_f64 routes through std/string's
	// parse_float receiver method — the cross-module method-reference
	// shape of #5420 — and must still reach `module: IR`. 2.5e1 = 25.
	{"get-f64", "import \"std/json\";\nfunction main(): i32 { match (json.json_parse(\"{\\\"x\\\": 2.5e1}\")) { Some(v) => { match (json.json_get_f64(v, \"x\")) { Some(x) => { if (x > 24.999 && x < 25.001) { return 25; } return 1; }, None => { return 2; }, } }, None => { return 3; }, } }\n"},
}

// TestSelfHostJsonCoreIRX86_64 compiles real `import "std/json"` parse
// programs through the multi-module bundling loader and asserts the
// whole program routes the IR path (`module: IR`) — the downstream win
// of lowering core/map, which std/json is built on. Each binary is
// oracle-checked against the interpreter.
func TestSelfHostJsonCoreIRX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	if len(runner) != 0 {
		t.Skip("file-loading driver test runs only natively (argv paths)")
	}
	interpBin := buildLangBinForInterp(t)
	dir := writeSelfHostAsmProject(t)
	copySelfHostDriver(t, dir, "asm_load_run.fern")
	mmc := buildSelfHostBin(t, gcc, dir, "asm_load_run.fern", "mmc")
	stdlibRoot, err := filepath.Abs("../../internal/stdlib")
	if err != nil {
		t.Fatalf("abs stdlib root: %v", err)
	}

	for _, tc := range jsonCoreIRCases {
		t.Run(tc.name, func(t *testing.T) {
			want := interpExit(t, interpBin, tc.main)
			proj := t.TempDir()
			mainPath := filepath.Join(proj, "main.fern")
			if err := os.WriteFile(mainPath, []byte(tc.main), 0o644); err != nil {
				t.Fatalf("write main.fern: %v", err)
			}
			// Routing assertion: the whole program must reach the IR path.
			probe, err := exec.Command(mmc, mainPath, stdlibRoot, "-ir-probe").Output()
			if err != nil {
				t.Fatalf("ir-probe: %v", err)
			}
			if !bytes.Contains(probe, []byte("module: IR")) {
				t.Fatalf("%s did not route module: IR\n%s", tc.name, probe)
			}
			// Compile + run, oracle-checked.
			asm, err := exec.Command(mmc, mainPath, stdlibRoot).Output()
			if err != nil || len(asm) == 0 {
				t.Fatalf("loader compile: %v", err)
			}
			progBin := buildBin(t, gcc, dir, tc.name, string(asm))
			cmd := exec.Command(progBin)
			_ = cmd.Run()
			if code := cmd.ProcessState.ExitCode(); code != want {
				t.Errorf("%s exited %d, want %d (interp oracle)", tc.name, code, want)
			}
		})
	}
}
