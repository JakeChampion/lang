package e2eselfhost

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/jakechampion/lang/internal/e2eharness"
)

func TestSelfHostFileBytes(t *testing.T) {
	cli, stdlib := witSelfHostCLI(t)
	bootstrap := buildLangBinForInterp(t)
	src := filepath.Join(t.TempDir(), "files.fern")
	if err := os.WriteFile(src, []byte(e2eharness.FileBytesProgram), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Run("bootstrap/interpreter", func(t *testing.T) {
		e2eharness.CheckFileBytes(t, exec.Command(bootstrap, "-interp", src))
	})
	_, targets, _ := hostTargets()
	if _, err := exec.LookPath("wasmtime"); err == nil {
		targets = append(targets, ssaBackendTarget{target: "wasm32-wasi", runner: []string{"wasmtime", "run", "--dir=."}})
	}
	for _, target := range targets {
		forms := []string{""}
		if target.target == "wasm32-wasi" {
			forms = append(forms, "core")
		}
		for _, mode := range []string{"checked", "plain"} {
			for _, form := range forms {
				t.Run("primary/"+target.target+"/"+mode+"/"+form, func(t *testing.T) {
					bin := filepath.Join(t.TempDir(), "files")
					args := []string{"-target", target.target, "-o", bin}
					if form == "core" {
						args = append(args, "-emit", "core-module")
					}
					compile := exec.Command(cli, append(args, src, stdlib)...)
					if mode == "checked" {
						compile.Env = append(os.Environ(), "FERN_STRICT_IR=1", "FERN_SANITIZE=1", "FERN_LEAKCHECK=1")
					}
					if out, err := compile.CombinedOutput(); err != nil {
						t.Fatalf("compile: %v\n%s", err, out)
					}
					diagnostic := e2eharness.CheckFileBytes(t, runX86_64Bin(target.runner, bin))
					if mode == "checked" && (target.target != "wasm32-wasi" || form == "core") {
						assertBalancedCensus(t, diagnostic)
					}
				})
			}
		}
	}
}

func TestSelfHostFileBytesFullDevice(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("requires Linux /dev/full")
	}
	cli, stdlib := witSelfHostCLI(t)
	program := `function main(): i32 {
  let bytes: u8[] = [255 as u8, 0 as u8, 128 as u8];
  let i: i32 = 0;
  while (i < 128) {
    match (write_file_bytes("/dev/full", bytes)) {
      Ok(_) => { return 1; },
      Err(e) => { match (e) {
        Other(p, text) => { if (p != "/dev/full" || text != "No space left on device") { return 2; } },
        _ => { return 3; }
      } }
    }
    i = i + 1;
  }
  match (write_file_bytes("after.bin", bytes)) { Err(_) => { return 4; }, Ok(_) => {} }
  return 0;
}`
	src := filepath.Join(t.TempDir(), "full.fern")
	if err := os.WriteFile(src, []byte(program), 0o644); err != nil {
		t.Fatal(err)
	}
	_, targets, _ := hostTargets()
	for _, target := range targets {
		t.Run("primary/"+target.target+"/checked", func(t *testing.T) {
			bin := filepath.Join(t.TempDir(), "full")
			compile := exec.Command(cli, "-target", target.target, "-o", bin, src, stdlib)
			compile.Env = append(os.Environ(), "FERN_STRICT_IR=1", "FERN_SANITIZE=1", "FERN_LEAKCHECK=1")
			if out, err := compile.CombinedOutput(); err != nil {
				t.Fatalf("compile: %v\n%s", err, out)
			}
			argv := append([]string{"-c", `ulimit -n 64; exec "$@"`, "file-bytes"}, target.runner...)
			argv = append(argv, bin)
			run := exec.Command("bash", argv...)
			run.Dir = t.TempDir()
			out, err := run.CombinedOutput()
			if err != nil {
				t.Fatalf("128 failed writes under a 64-descriptor limit: %v\n%s", err, out)
			}
			assertBalancedCensus(t, string(out))
			got, err := os.ReadFile(filepath.Join(run.Dir, "after.bin"))
			if err != nil || string(got) != string([]byte{255, 0, 128}) {
				t.Fatalf("write after failures: %v, %v", got, err)
			}
		})
	}
}
