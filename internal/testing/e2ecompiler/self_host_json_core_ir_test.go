package e2ecompiler

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

var jsonCoreIRCases = []struct {
	name string
	main string
}{
	// parse object + typed i32 extractor. get_i32("a") = 7.
	{"get-i32", "import \"std/json\";\nfunction main(): i32 { match (json.parse(\"{\\\"a\\\": 7}\")) { Ok(v) => { match (json.json_get_i32(v, \"a\")) { Some(n) => { return n; }, None => { return 1; }, } }, Err(_) => { return 2; }, } }\n"},
	// parse object + string extractor, length of value. len(\"hello\") = 5.
	{"get-string", "import \"std/json\";\nfunction main(): i32 { match (json.parse(\"{\\\"k\\\": \\\"hello\\\"}\")) { Ok(v) => { match (json.json_get_string(v, \"k\")) { Some(s) => { return s.len(); }, None => { return 1; }, } }, Err(_) => { return 2; }, } }\n"},
	// parse object + bool extractor. true => 9.
	{"get-bool", "import \"std/json\";\nfunction main(): i32 { match (json.parse(\"{\\\"b\\\": true}\")) { Ok(v) => { match (json.json_get_bool(v, \"b\")) { Some(x) => { if (x) { return 9; } return 8; }, None => { return 1; }, } }, Err(_) => { return 2; }, } }\n"},
	// parse object holding an array + array extractor. len([1,2,3]) = 3.
	{"array", "import \"std/json\";\nfunction main(): i32 { match (json.parse(\"{\\\"xs\\\": [1,2,3]}\")) { Ok(v) => { match (json.json_get_array(v, \"xs\")) { Some(a) => { return a.len(); }, None => { return 1; }, } }, Err(_) => { return 2; }, } }\n"},
	// nested object: parse, descend, extract. inner.n = 42.
	{"nested", "import \"std/json\";\nfunction main(): i32 { match (json.parse(\"{\\\"o\\\": {\\\"n\\\": 42}}\")) { Ok(v) => { match (json.json_get_object(v, \"o\")) { Some(o) => { match (json.json_get_i32(o, \"n\")) { Some(n) => { return n; }, None => { return 3; }, } }, None => { return 1; }, } }, Err(_) => { return 2; }, } }\n"},
	// null literal + json_is_null predicate. is_null => 5.
	{"is-null", "import \"std/json\";\nfunction main(): i32 { match (json.parse(\"null\")) { Ok(v) => { if (json.json_is_null(v)) { return 5; } return 4; }, Err(_) => { return 2; }, } }\n"},
	// f64 extractor: json_get_f64 routes through std/string's
	// parse_float receiver method — the cross-module method-reference
	// shape of #5420 — and must still reach `module: IR`. 2.5e1 = 25.
	{"get-f64", "import \"std/json\";\nfunction main(): i32 { match (json.parse(\"{\\\"x\\\": 2.5e1}\")) { Ok(v) => { match (json.json_get_f64(v, \"x\")) { Some(x) => { if (x > 24.999 && x < 25.001) { return 25; } return 1; }, None => { return 2; }, } }, Err(_) => { return 3; }, } }\n"},
}

func TestSelfHostJsonCoreIRX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	if len(runner) != 0 {
		t.Skip("file-loading driver test runs only natively (argv paths)")
	}
	interpBin := buildLangBinForInterp(t)
	dir := writeSelfHostAsmProject(t)
	copySelfHostDriver(t, dir, "drivers/asm_load_run.fern")
	mmc := buildSelfHostBin(t, gcc, dir, "drivers/asm_load_run.fern", "mmc")
	stdlibRoot, err := filepath.Abs("../../stdlib")
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
			probe, err := exec.Command(mmc, mainPath, stdlibRoot, "-ir-probe").Output()
			if err != nil {
				t.Fatalf("ir-probe: %v", err)
			}
			if !bytes.Contains(probe, []byte("module: IR")) {
				t.Fatalf("%s did not route module: IR\n%s", tc.name, probe)
			}
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
