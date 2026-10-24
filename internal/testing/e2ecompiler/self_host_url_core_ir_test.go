package e2ecompiler

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

var urlCoreIRCases = []struct {
	name string
	main string
}{
	{"encode", "import \"std/url\";\nfunction main(): i32 { return url.url_encode(\"a b&c\").len(); }\n"},
	{"decode", "import \"std/url\";\nfunction main(): i32 { return url.url_decode(\"a%20b\").len(); }\n"},
	{"roundtrip", "import \"std/url\";\nfunction main(): i32 { return url.url_decode(url.url_encode(\"hi there\")).len(); }\n"},
	{"host-len", "import \"std/url\";\nfunction main(): i32 { match (url.url_parse(\"http://example.com/p\")) { Some(u) => { return u.host.len(); }, None => { return 0; }, } }\n"},
	{"port", "import \"std/url\";\nfunction main(): i32 { match (url.url_parse(\"http://x.com:42/p\")) { Some(u) => { return u.port; }, None => { return 0; }, } }\n"},
	{"query", "import \"std/url\";\nfunction main(): i32 { let m = url.query_parse(\"a=1&b=2\"); return m.len(); }\n"},
}

func TestSelfHostUrlCoreIRX86_64(t *testing.T) {
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

	for _, tc := range urlCoreIRCases {
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
