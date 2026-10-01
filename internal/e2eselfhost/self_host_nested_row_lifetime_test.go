package e2eselfhost

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// The source container dies inside collect. Keep allocations alive after it
// returns so a missing retain overwrites the borrowed row before its read.
const nestedRowLifetimeSource = `struct Rows { rows: ELEMENT[][] }
function collect(): Rows {
  var out: Rows = Rows { rows: [] };
  var source: Rows = Rows { rows: [[FIRST, SECOND]] };
  return Rows { rows: out.rows.append(source.rows[0]) };
}
function main(): i32 {
  var out: Rows = collect();
  var churn: ELEMENT[][] = [];
  var i: i32 = 0;
  while (i < 32) { churn = churn.append([CHURN, CHURN]); i = i + 1; }
  if (churn[31][1] != CHURN) { return 1; }
  if (out.rows[0].len() != 2) { return 2; }
  if (out.rows[0][0] != FIRST || out.rows[0][1] != SECOND) { return 3; }
  return 0;
}
`

func nestedRowLifetimeCases() map[string]string {
	out := map[string]string{}
	for _, tc := range []struct{ element, first, second, churn string }{
		{"i32", "7", "9", "0"},
		{"u8", "7 as u8", "9 as u8", "0 as u8"},
		{"boolean", "true", "false", "true"},
		{"i64", "7i64", "9i64", "0i64"},
		{"f64", "7.0", "9.0", "0.0"},
	} {
		out[tc.element] = strings.NewReplacer("ELEMENT", tc.element, "FIRST", tc.first,
			"SECOND", tc.second, "CHURN", tc.churn).Replace(nestedRowLifetimeSource)
	}
	return out
}

func TestSelfHostNestedRowLifetime(t *testing.T) {
	cli := buildSelfHostCLI(t)
	for name, source := range nestedRowLifetimeCases() {
		t.Run(name, func(t *testing.T) {
			src := filepath.Join(t.TempDir(), "row.fern")
			if err := os.WriteFile(src, []byte(source), 0o644); err != nil {
				t.Fatal(err)
			}
			for _, lowering := range []string{"", "1"} {
				for _, target := range []string{"x86-64-linux", "arm64-linux", "wasm32-wasi"} {
					t.Run(target+"/semantic="+lowering, func(t *testing.T) {
						env := []string{"FERN_SEM_IR=" + lowering, "FERN_STRICT_IR=1"}
						var cmd *exec.Cmd
						switch target {
						case "x86-64-linux":
							cmd = runX86_64Bin(cli.runner, cli.x86Binary(t, src, env...))
						case "arm64-linux":
							gcc, qemu := arm64Tooling(t)
							asm, err := os.ReadFile(cli.emit(t, src, target, env...))
							if err != nil {
								t.Fatal(err)
							}
							cmd = runArm64Bin(qemu, buildBinArm64(t, gcc, t.TempDir(), "row", string(asm)))
						case "wasm32-wasi":
							cmd = exec.Command("wasmtime", "run", cli.emit(t, src, target, env...))
						}
						if out, err := cmd.CombinedOutput(); err != nil {
							t.Fatalf("borrowed row did not survive source disposal: %v\n%s", err, out)
						}
					})
				}
			}
		})
	}
}

func TestSelfHostArm64DarwinNestedRowLifetime(t *testing.T) {
	if runtime.GOOS != "darwin" || runtime.GOARCH != "arm64" {
		t.Skip("requires Apple Silicon")
	}
	dir := writeSelfHostAsmProject(t)
	copySelfHostDriver(t, dir, "fern.fern")
	cli := buildSelfHostBinArm64Darwin(t, dir, "fern.fern", "fern")
	for name, source := range nestedRowLifetimeCases() {
		t.Run(name, func(t *testing.T) {
			src := filepath.Join(dir, "row.fern")
			if err := os.WriteFile(src, []byte(source), 0o644); err != nil {
				t.Fatal(err)
			}
			for _, lowering := range []string{"", "1"} {
				bin := filepath.Join(t.TempDir(), "row")
				cmd := exec.Command(cli, "-target", "arm64-darwin", src, "-o", bin)
				cmd.Env = append(os.Environ(), "FERN_SEM_IR="+lowering, "FERN_STRICT_IR=1")
				if out, err := cmd.CombinedOutput(); err != nil {
					t.Fatalf("compile semantic=%q: %v\n%s", lowering, err, out)
				}
				if out, err := exec.Command(bin).CombinedOutput(); err != nil {
					t.Fatalf("borrowed row did not survive source disposal: %v\n%s", err, out)
				}
			}
		})
	}
}
