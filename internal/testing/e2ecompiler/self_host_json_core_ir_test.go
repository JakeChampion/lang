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
	{"get-i32", "import \"std/json\";\nfunction main(): i32 { match (json.json_parse(\"{\\\"a\\\": 7}\")) { Some(v) => { match (json.json_get_i32(v, \"a\")) { Some(n) => { return n; }, None => { return 1; }, } }, None => { return 2; }, } }\n"},
	{"get-string", "import \"std/json\";\nfunction main(): i32 { match (json.json_parse(\"{\\\"k\\\": \\\"hello\\\"}\")) { Some(v) => { match (json.json_get_string(v, \"k\")) { Some(s) => { return s.len(); }, None => { return 1; }, } }, None => { return 2; }, } }\n"},
	{"get-bool", "import \"std/json\";\nfunction main(): i32 { match (json.json_parse(\"{\\\"b\\\": true}\")) { Some(v) => { match (json.json_get_bool(v, \"b\")) { Some(x) => { if (x) { return 9; } return 8; }, None => { return 1; }, } }, None => { return 2; }, } }\n"},
	{"array", "import \"std/json\";\nfunction main(): i32 { match (json.json_parse(\"{\\\"xs\\\": [1,2,3]}\")) { Some(v) => { match (json.json_get_array(v, \"xs\")) { Some(a) => { return a.len(); }, None => { return 1; }, } }, None => { return 2; }, } }\n"},
	{"nested", "import \"std/json\";\nfunction main(): i32 { match (json.json_parse(\"{\\\"o\\\": {\\\"n\\\": 42}}\")) { Some(v) => { match (json.json_get_object(v, \"o\")) { Some(o) => { match (json.json_get_i32(o, \"n\")) { Some(n) => { return n; }, None => { return 3; }, } }, None => { return 1; }, } }, None => { return 2; }, } }\n"},
	{"is-null", "import \"std/json\";\nfunction main(): i32 { match (json.json_parse(\"null\")) { Some(v) => { if (json.json_is_null(v)) { return 5; } return 4; }, None => { return 2; }, } }\n"},
	{"get-f64", "import \"std/json\";\nfunction main(): i32 { match (json.json_parse(\"{\\\"x\\\": 2.5e1}\")) { Some(v) => { match (json.json_get_f64(v, \"x\")) { Some(x) => { if (x > 24.999 && x < 25.001) { return 25; } return 1; }, None => { return 2; }, } }, None => { return 3; }, } }\n"},
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
