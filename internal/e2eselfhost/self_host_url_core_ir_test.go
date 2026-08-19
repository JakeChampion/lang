package e2eselfhost

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// urlCoreIRCases are real `import "std/url"` programs (qualified
// url.url_* calls, the way modload routes stdlib access) covering the
// whole std/url surface: percent encode/decode, url_parse + the parsed
// Url fields (scheme / host / port), and query_parse (which returns a
// Map[string, string[]]). std/url's query_parse builds its result on
// core/map, so until core/map lowered fully through the IR path these
// programs bailed to the legacy AST emitter. With core/map routing
// IR (the __alloc / __ptr_width / __memset / __free / __fern_arr_dec
// stages), the entire std/url module is IR-eligible — it has zero
// AST-only functions — so every one of these programs reaches
// `module: IR` through the bundling loader.
//
// Each case asserts the modload -ir-probe verdict is `module: IR` AND
// that the compiled binary matches the interpreter oracle. x86-64 only
// (the loader driver takes argv file paths, like the other modload
// tests). Results are <= 126.
var urlCoreIRCases = []struct {
	name string
	main string
}{
	// percent-encode "a b&c" -> "a%20b%26c" (len 9).
	{"encode", "import \"std/url\";\nfunction main(): i32 { return url.url_encode(\"a b&c\").len(); }\n"},
	// percent-decode "a%20b" -> "a b" (len 3).
	{"decode", "import \"std/url\";\nfunction main(): i32 { return url.url_decode(\"a%20b\").len(); }\n"},
	// encode then decode round-trips "hi there" (len 8).
	{"roundtrip", "import \"std/url\";\nfunction main(): i32 { return url.url_decode(url.url_encode(\"hi there\")).len(); }\n"},
	// parse + host field. "example.com" len 11.
	{"host-len", "import \"std/url\";\nfunction main(): i32 { match (url.url_parse(\"http://example.com/p\")) { Some(u) => { return u.host.len(); }, None => { return 0; }, } }\n"},
	// parse + port field. :42 -> 42.
	{"port", "import \"std/url\";\nfunction main(): i32 { match (url.url_parse(\"http://x.com:42/p\")) { Some(u) => { return u.port; }, None => { return 0; }, } }\n"},
	// query_parse (Map-backed). "a=1&b=2" -> 2 keys.
	{"query", "import \"std/url\";\nfunction main(): i32 { var m = url.query_parse(\"a=1&b=2\"); return m.len(); }\n"},
}

// TestSelfHostUrlCoreIRX86_64 compiles real `import "std/url"` programs
// through the multi-module bundling loader and asserts the whole program
// routes the IR path (`module: IR`) — another downstream win of lowering
// core/map, which std/url's query_parse is built on. Each binary is
// oracle-checked against the interpreter.
func TestSelfHostUrlCoreIRX86_64(t *testing.T) {
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

	for _, tc := range urlCoreIRCases {
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
