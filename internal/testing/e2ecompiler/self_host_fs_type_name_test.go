package e2ecompiler

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/jakechampion/lang/internal/testing/e2eharness"
)

func TestSelfHostFsTypeName(t *testing.T) {
	dir := t.TempDir()
	first := hostFsTypeName(t, dir)
	other := hostFsTypeName(t, "/dev")
	source := fmt.Sprintf(`function name(path: string): string {
  match (statfs(path)) {
    Ok(fs) => { return fs.fs_type_name; },
    Err(_) => { return "statfs failed"; }
  }
}
function exercise(): void {
  let saved: string = name(%[1]q);
  assert(saved == %[2]q);
  for i in 0..32 {
    match (statfs("/dev")) {
      Ok(fs) => {
        let alias: FsStat = fs;
        let values: FsStat[] = [fs, alias];
        assert(values[0].fs_type_name == %[3]q);
        assert(values[1].fs_type_name == %[3]q);
      },
      Err(_) => { assert(false); }
    }
    assert(saved == %[2]q);
  }
  match (statfs(%[4]q)) {
    Ok(_) => { assert(false); },
    Err(_) => {}
  }
  assert(saved == %[2]q);
}
function main(): i32 { exercise(); return 0; }
`, dir, first, other, filepath.Join(dir, "missing", "child"))
	path := filepath.Join(dir, "fs-type-name.fern")
	if err := os.WriteFile(path, []byte(source), 0o644); err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS == "darwin" && runtime.GOARCH == "arm64" {
		t.Run("arm64-darwin", func(t *testing.T) {
			project := t.TempDir()
			copySelfHostDriver(t, project, "fern.fern")
			cli := buildSelfHostBinArm64Darwin(t, project, "fern.fern", "fern")
			bin := filepath.Join(dir, "program")
			cmd := exec.Command(cli, "-target", "arm64-darwin", "-o", bin, path, e2eharness.SelfHostStdlibRoot(t))
			cmd.Env = e2eharness.SelfHostChildEnv("FERN_SANITIZE=1", "FERN_LEAKCHECK=1")
			if out, err := cmd.CombinedOutput(); err != nil {
				t.Fatalf("compile: %v\n%s", err, out)
			}
			out, err := exec.Command(bin).CombinedOutput()
			if err != nil {
				t.Fatalf("run: %v\n%s", err, out)
			}
			assertBalancedCensus(t, string(out))
		})
	} else if runtime.GOOS == "linux" {
		cli := buildSelfHostCLI(t)
		for _, target := range []string{"x86-64-linux", "arm64-linux"} {
			t.Run(target, func(t *testing.T) {
				stderr, code := cli.exitOfFile(t, path, target, nil, "FERN_SANITIZE=1", "FERN_LEAKCHECK=1")
				if code != 0 {
					t.Fatalf("exit %d: %s", code, stderr)
				}
				assertBalancedCensus(t, stderr)
			})
		}
	} else {
		t.Skip("requires a supported native filesystem target")
	}
	t.Run("reference-interpreter", func(t *testing.T) {
		if out, err := exec.Command(buildLangBinForInterp(t), "-interp", path).CombinedOutput(); err != nil {
			t.Fatalf("interpreter: %v\n%s", err, out)
		}
	})
}
