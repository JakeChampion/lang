package e2eselfhost

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// Error payloads own their path independently of the caller. A heap-backed
// path used again after the Result is dropped catches missing retains that a
// static literal cannot expose. Census also covers fresh, payloadless errors.
func TestSelfHostWasmIoErrorOwnsPath(t *testing.T) {
	cli := buildSelfHostCLI(t)
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "1"), []byte("file"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("contents", filepath.Join(dir, "1.link")); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ name, call string }{
		{"open", `open_reader(p)`},
		{"open with", `open_reader_with(p, 0)`},
		{"write open", `open_writer(p)`},
		{"write open with", `open_writer_with(p, 0)`},
		{"read text", `read_file(p)`},
		{"read bytes", `read_file_bytes(p)`},
		{"write", `write_file(p, "content")`},
		{"stat", `stat(p)`},
		{"lstat", `lstat(p)`},
		{"unlink", `remove_file(p)`},
		{"mkdir", `create_dir(p, 493)`},
		{"mkdir all", `create_dir_all(p)`},
		{"rmdir", `remove_dir(p)`},
		{"rename", `rename(p, p)`},
		{"read directory", `read_dir(p)`},
		{"read link", `read_link(p)`},
		{"read link success", `read_link(p)`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			suffix := "/child"
			matchBody := `Ok(_) => { return 1; },
      Err(e) => {
        match (e) {
          Other(path, _) => { if (path != p) { return 2; } },
          NotFound(path) => { if (path != p) { return 3; } },
          _ => { return 4; }
        }
      }`
			if tc.name == "read link success" {
				suffix = ".link"
				matchBody = `Ok(target) => { if (target != "contents") { return 6; } }, Err(_) => { return 7; }`
			}
			source := fmt.Sprintf(`import "std/string";
function main(): i32 {
  let p: string = args().len().to_string() + %q;
  let i: i32 = 0;
  while (i < 30) {
    match (%s) {
      %s
    }
    // Reuse freed blocks before reading the retained caller's path.
    let churn: string = i.to_string() + "abcdefg";
    if (churn.len() < 8 || p != %q) { return 5; }
    i = i + 1;
  }
  return 0;
}`, suffix, tc.call, matchBody, "1"+suffix)
			src := filepath.Join(t.TempDir(), "main.fern")
			if err := os.WriteFile(src, []byte(source), 0o644); err != nil {
				t.Fatal(err)
			}
			t.Run("core", func(t *testing.T) {
				wat := cli.emit(t, src, "wasm32-wasi", "FERN_STRICT_IR=1", "FERN_SANITIZE=1", "FERN_LEAKCHECK=1")
				cmd := exec.Command("wasmtime", "run", "--dir=.", wat)
				cmd.Dir = dir
				out, err := cmd.CombinedOutput()
				if err != nil {
					t.Fatalf("run: %v\n%s", err, out)
				}
				assertBalancedCensus(t, string(out))
			})
			if tc.name == "read text" || tc.name == "read bytes" || tc.name == "write" {
				t.Run("component", func(t *testing.T) {
					componentSource := source
					if tc.name == "write" {
						// The current wrapper supports read+write+args, but not
						// write-only+args. Read the fixture to use that framing.
						componentSource = strings.Replace(source, "function main(): i32 {", `function main(): i32 {
  match (read_file("1")) { Ok(s) => { if (s != "file") { return 9; } }, Err(_) => { return 10; } }`, 1)
					}
					componentSrc := filepath.Join(t.TempDir(), "main.fern")
					if err := os.WriteFile(componentSrc, []byte(componentSource), 0o644); err != nil {
						t.Fatal(err)
					}
					bin := filepath.Join(t.TempDir(), "main.wasm")
					compile := runX86_64Bin(cli.runner, cli.bin, "-target", "wasm32-wasi", "-o", bin, componentSrc, cli.stdlib)
					compile.Env = append(os.Environ(), "FERN_STRICT_IR=1", "FERN_SANITIZE=1", "FERN_LEAKCHECK=1")
					if out, err := compile.CombinedOutput(); err != nil {
						t.Fatalf("compile component: %v\n%s", err, out)
					}
					cmd := exec.Command("wasmtime", "run", "--dir=.", bin)
					cmd.Dir = dir
					out, err := cmd.CombinedOutput()
					if err != nil {
						t.Fatalf("run component: %v\n%s", err, out)
					}
					// Components return through wasi:cli/run and currently do
					// not emit the core module's exit-time allocation census.
				})
			}
		})
	}
}
