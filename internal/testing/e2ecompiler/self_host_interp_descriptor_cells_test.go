package e2ecompiler

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jakechampion/lang/internal/testing/e2eharness"
)

const descriptorCellIO = `function main(): i32 {
  let Ok(writer) = open_writer("native-cell.txt") else { return 1; };
  let writers = cell_new(writer);
  let first = writers.get();
  writers.set(writer);
  let Ok(_) = first.write("cell descriptors") else { return 2; };
  let fd = (): i32 => writers.get().fd;
  assert(fd() == writer.fd);
  let Ok(_) = writer.close() else { return 3; };
  let Ok(reader) = open_reader("native-cell.txt") else { return 4; };
  let readers = cell_new(reader);
  let Ok(text) = readers.get().read_chunk(64) else { return 5; };
  assert(text == "cell descriptors");
  let Ok(_) = reader.close() else { return 6; };
  return 0;
}
`

const descriptorCellDir = `function main(): i32 {
  let Ok(directory) = open_dir(".") else { return 1; };
  let directories = cell_new(directory);
  let first = directories.get();
  directories.set(directory);
  let Ok(reader) = first.open_reader("native-cell.txt") else { return 2; };
  let readers = cell_new(reader);
  let Ok(text) = readers.get().read_chunk(64) else { return 3; };
  assert(text == "cell descriptors");
  let Ok(_) = reader.close() else { return 4; };
  let Ok(_) = directory.close() else { return 5; };
  return 0;
}
`

func TestSelfHostInterpDescriptorCells(t *testing.T) {
	host := e2eharness.HostSelfHostTarget()
	if host == "" {
		t.Skip("requires a native self-host target")
	}
	cli := e2eharness.SelfHostCLI(t)
	project := t.TempDir()
	copySelfHostDriver(t, project, "drivers/interp_run.fern")
	entry := filepath.Join(project, "drivers/interp_run.fern")
	const driver = `import "./rundriver";
import "../interp";
function main(): i32 {
  let value = interp.eval_module(rundriver.parse_stdin("descriptor-cells"));
  if let interp.VErr(error) = value {
    print(error.msg);
    return 125;
  }
  let (is_status, status) = interp.exit_status(value);
  if (is_status) { return status; }
  return 124;
}
`
	if err := os.WriteFile(entry, []byte(driver), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, target := range []string{host, e2eharness.TargetWasm32Wasi} {
		t.Run(target, func(t *testing.T) {
			binary := filepath.Join(t.TempDir(), "interp")
			args := []string{"-target", target, "-sanitize"}
			wasmtime := ""
			if target == e2eharness.TargetWasm32Wasi {
				wasmtime = e2eharness.Wasmtime(t)
				args = append(args, "-emit", "core-module")
			}
			args = append(args, "-o", binary, entry, e2eharness.SelfHostStdlibRoot(t))
			compile := exec.Command(cli, args...)
			compile.Env = childEnv("FERN_IR_VERIFY=1", "FERN_STRICT_IR=1")
			if out, err := compile.CombinedOutput(); err != nil {
				t.Fatalf("compile interpreter: %v\n%s", err, out)
			}
			cases := []struct {
				name, source string
				code         int
			}{
				{"file-io", descriptorCellIO, 0},
				{"function-refused", "function main(): i32 { let c = cell_new(() => 1); return 0; }", 125},
				{"nested-cell-refused", "function main(): i32 { let c = cell_new(cell_new(1)); return 0; }", 125},
				{"nested-function-refused", "function main(): i32 { let c = cell_new([() => 1]); return 0; }", 125},
				{"set-function-refused", "function main(): i32 { let c = cell_new(1); c.set(() => 1); return 0; }", 125},
			}
			if target == host {
				cases = append(cases, struct {
					name, source string
					code         int
				}{"directory-io", descriptorCellDir, 0})
			}
			for _, tc := range cases {
				t.Run(tc.name, func(t *testing.T) {
					root := t.TempDir()
					if err := os.WriteFile(filepath.Join(root, "native-cell.txt"), []byte("cell descriptors"), 0o644); err != nil {
						t.Fatal(err)
					}
					run := exec.Command(binary)
					if wasmtime != "" {
						run = exec.Command(wasmtime, "run", "--dir=.", "--env", "FERN_LEAKCHECK=1", binary)
					}
					run.Dir = root
					run.Env = childEnv("FERN_LEAKCHECK=1")
					run.Stdin = bytes.NewBufferString(tc.source)
					out, err := run.CombinedOutput()
					if run.ProcessState == nil || run.ProcessState.ExitCode() != tc.code {
						t.Fatalf("interpreter: %v, want exit %d\n%s", err, tc.code, out)
					}
					if tc.code == 125 && !strings.Contains(string(out), "Cell") {
						t.Fatalf("want Cell refusal, got %s", out)
					}
					assertBalancedCensus(t, string(out))
				})
			}
		})
	}
}
