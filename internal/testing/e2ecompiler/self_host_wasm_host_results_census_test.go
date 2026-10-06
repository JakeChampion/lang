package e2ecompiler

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestSelfHostWasmHostResultsBalanceTheCensus runs the wasm runtime bodies
// that take a result from the host, as a core module and as a component, and
// requires every allocation to be freed. A component's host writes each list
// and string it returns into a cabi_realloc block, and the body writes the
// result's header into a return area of its own; both are the body's to give
// back once the value is copied out. The directory holds a name of every
// length from 1 to 33, so a name freed at the wrong size shows at each
// multiple of 8.
func TestSelfHostWasmHostResultsBalanceTheCensus(t *testing.T) {
	cli := buildSelfHostCLI(t)
	wasmtime, err := exec.LookPath("wasmtime")
	if err != nil {
		t.Skip("wasmtime not on PATH")
	}
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, "d"), 0o755); err != nil {
		t.Fatal(err)
	}
	for n := 1; n <= 33; n++ {
		if err := os.WriteFile(filepath.Join(dir, "d", strings.Repeat("f", n)), nil, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	loop := func(body string) string {
		return "  let i: i32 = 0;\n  while (i < 3) {\n" + body + "\n    i = i + 1;\n  }\n  return 0;\n}\n"
	}
	for _, tc := range []struct {
		name, src string
		core      bool
	}{
		{"read_dir", "function main(): i32 {\n" + loop(`    match (read_dir("d")) { Ok(names) => { if (names.len() != 33) { return 1; } }, Err(_) => { return 2; } }`), true},
		{"read_dir_all", "function main(): i32 {\n" + loop(`    match (read_dir_all("d")) { Ok(names) => { if (names.len() < 33) { return 1; } }, Err(_) => { return 2; } }`), true},
		{"read_dir_ino", "function main(): i32 {\n" + loop(`    match (read_dir_ino("d")) { Ok(es) => { if (es.len() != 33 || es[0].name.len() == 0) { return 1; } }, Err(_) => { return 2; } }`), true},
		{"read_dir_missing", "function main(): i32 {\n" + loop(`    match (read_dir("absent")) { Ok(_) => { return 1; }, Err(_) => {} }`), true},
		{"read_dir_ino_missing", "function main(): i32 {\n" + loop(`    match (read_dir_ino("absent")) { Ok(_) => { return 1; }, Err(_) => {} }`), true},
		{"env", "function main(): i32 {\n" + loop(`    match (env("FERN_CENSUS_VALUE")) { Some(v) => { if (v != "a value longer than a word") { return 1; } }, None => { return 2; } }
    match (env("FERN_CENSUS_ABSENT")) { Some(_) => { return 3; }, None => {} }
    if (now_ns() <= 0 as i64 || now_unix_ms() <= 0 as i64) { return 4; }`), true},
		{"args", "function main(): i32 {\n" + loop(`    if (args().len() < 1) { return 1; }`), true},
		{"environ", "function main(): i32 {\n" + loop(`    if (environ().len() < 1) { return 1; }`), true},
		{"import_list", "@import(\"wasi:random/random@0.2.0\", \"get-random-bytes\")\nfunction random_text(n: u64): string;\n@import(\"wasi:random/random@0.2.0\", \"get-random-bytes\")\nfunction random_list(n: u64): u8[];\nfunction main(): i32 {\n" + loop(`    if (random_text(16 as u64).len() != 16 || random_list(16 as u64).len() != 16) { return 1; }`), false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			src := filepath.Join(t.TempDir(), "main.fern")
			if err := os.WriteFile(src, []byte(tc.src), 0o644); err != nil {
				t.Fatal(err)
			}
			run := func(t *testing.T, module string) {
				cmd := exec.Command(wasmtime, "run", "--dir=.", "--env", "FERN_CENSUS_VALUE=a value longer than a word", module, "an-argument")
				cmd.Dir = dir
				var stderr strings.Builder
				cmd.Stderr = &stderr
				if err := cmd.Run(); err != nil {
					t.Fatalf("run: %v\n%s", err, stderr.String())
				}
				assertBalancedCensus(t, stderr.String())
			}
			if tc.core {
				t.Run("core", func(t *testing.T) {
					run(t, cli.emit(t, src, "wasm32-wasi", "FERN_LEAKCHECK=1"))
				})
			}
			t.Run("component", func(t *testing.T) {
				run(t, cli.wasmComponent(t, src, "FERN_LEAKCHECK=1"))
			})
		})
	}
}
