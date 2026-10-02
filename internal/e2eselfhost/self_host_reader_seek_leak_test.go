package e2eselfhost

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestSelfHostWasmReaderSeekReleasesScratch(t *testing.T) {
	cli := buildSelfHostCLI(t)
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "input"), []byte("content"), 0o644); err != nil {
		t.Fatal(err)
	}
	src := filepath.Join(dir, "main.fern")
	const program = `function main(): i32 {
  match (open_reader("input")) {
    Ok(r) => {
      var i: i32 = 0;
      while (i < 100) {
        match (r.seek(1 as i64, 0)) { Ok(n) => { if (n != 1 as i64) { return 1; } }, Err(_) => { return 2; } }
        match (r.seek(0 as i64, 1)) { Ok(n) => { if (n != 1 as i64) { return 3; } }, Err(_) => { return 4; } }
        match (r.seek(-1 as i64, 0)) { Ok(_) => { return 5; }, Err(_) => {} }
        match (r.seek(0 as i64, 3)) { Ok(_) => { return 6; }, Err(_) => {} }
        i = i + 1;
      }
      r.close();
    },
    Err(_) => { return 7; }
  }
  return 0;
}`
	if err := os.WriteFile(src, []byte(program), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, mode := range []string{"0", "1"} {
		t.Run("typed="+mode, func(t *testing.T) {
			wat := cli.emit(t, src, "wasm32-wasi", "FERN_SEM_IR="+mode, "FERN_SEM_IR_STRICT="+mode, "FERN_SEM_IR_ONLY=", "FERN_SEM_IR_SKIP=", "FERN_STRICT_IR=1", "FERN_SANITIZE=1", "FERN_LEAKCHECK=1")
			cmd := exec.Command("wasmtime", "run", "--dir=.", wat)
			cmd.Dir = dir
			out, err := cmd.CombinedOutput()
			if err != nil {
				t.Fatalf("run: %v\n%s", err, out)
			}
			assertBalancedCensus(t, string(out))
		})
	}
}
