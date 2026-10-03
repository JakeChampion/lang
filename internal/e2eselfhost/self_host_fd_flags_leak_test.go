package e2eselfhost

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestSelfHostWasmFdFlagsReleasesScratch(t *testing.T) {
	cli := buildSelfHostCLI(t)
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "input"), []byte("content"), 0o644); err != nil {
		t.Fatal(err)
	}
	src := filepath.Join(dir, "main.fern")
	const program = `function main(): i32 {
  match (open_reader("input")) {
    Ok(r) => {
      let i: i32 = 0;
      while (i < 100) {
        match (r.flags()) { Ok(n) => { if (n != 1 as i64) { return 1; } }, Err(_) => { return 2; } }
        // Unsupported variants must release the fresh empty error path.
        match (r.syncfs()) { None => { return 5; }, Some(e) => { match (e) { Unsupported => {}, _ => { return 6; } } } }
        match (r.splice_to(stdout(), 1)) { Ok(_) => { return 7; }, Err(e) => { match (e) { Unsupported => {}, _ => { return 8; } } } }
        i = i + 1;
      }
      r.close();
      i = 0;
      while (i < 100) {
        match (r.flags()) { Ok(_) => { return 3; }, Err(_) => {} }
        i = i + 1;
      }
    },
    Err(_) => { return 4; }
  }
  return 0;
}`
	if err := os.WriteFile(src, []byte(program), 0o644); err != nil {
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
}
