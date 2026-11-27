package e2ecompiler

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// A generic array method on a value that a builtin method returned. The
// monomorphizer has to know the result type of the builtin method, or the
// call stays generic and the typed lowering refuses it.
var builtinMethodTypeCases = []struct {
	name string
	body string
}{
	{"free-function-form", `let start = r.read_chunk_bytes(512);
  r.close();
  let Ok(file) = start else { return 9; };
  let b = array.slice(file, 2, file.len());`},
	{"method-form", `let start = r.read_chunk_bytes(512);
  r.close();
  let Ok(file) = start else { return 9; };
  let b = file.slice(2, file.len());`},
	{"match-binding", `let b: u8[] = match (r.read_chunk_bytes(512)) { Ok(file) => file.take_last(4), Err(_) => { return 9; } };
  r.close();`},
}

func TestSelfHostBuiltinMethodType(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := copySelfHostTree(t)
	driver := buildSelfHostBin(t, gcc, dir, "drivers/asm_load_run.fern", "alr")
	root, err := filepath.Abs("../../stdlib")
	if err != nil {
		t.Fatalf("abs stdlib root: %v", err)
	}
	data := filepath.Join(dir, "builtin_method_type.txt")
	if err := os.WriteFile(data, []byte("abcdef"), 0o644); err != nil {
		t.Fatalf("write data: %v", err)
	}

	runDriver := func(args ...string) (string, int) {
		argv := append([]string{driver}, args...)
		var cmd *exec.Cmd
		if len(runner) == 0 {
			cmd = exec.Command(argv[0], argv[1:]...)
		} else {
			cmd = exec.Command(runner[0], append(runner[1:], argv...)...)
		}
		out, _ := cmd.Output()
		return string(out), cmd.ProcessState.ExitCode()
	}

	for _, tc := range builtinMethodTypeCases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			src := "import \"std/array\";\n\nfunction main(): i32 {\n" +
				"  let Ok(r) = open_reader(\"" + data + "\") else { return 8; };\n  " +
				tc.body + "\n  return b.len() * 10 + (b[0] as i32 - 97);\n}\n"
			entry := filepath.Join(dir, "bmt_"+tc.name+".fern")
			if err := os.WriteFile(entry, []byte(src), 0o644); err != nil {
				t.Fatalf("write entry: %v", err)
			}
			_, want := runFixtureInterp(t, entry, "")
			if want != 42 {
				t.Fatalf("%s oracle = %d, want 42", tc.name, want)
			}
			if out, _ := runDriver(entry, root, "-decide"); strings.TrimSpace(out) != "ir" {
				t.Errorf("%s decide = %q, want \"ir\"", tc.name, strings.TrimSpace(out))
			}
			asm, _ := runDriver(entry, root)
			if len(asm) == 0 {
				t.Fatalf("%s: driver emitted 0 bytes", tc.name)
			}
			bin := buildBin(t, gcc, dir, "bmt_"+tc.name+"_bin", asm)
			var cmd *exec.Cmd
			if len(runner) == 0 {
				cmd = exec.Command(bin)
			} else {
				cmd = exec.Command(runner[0], append(runner[1:], bin)...)
			}
			_ = cmd.Run()
			if code := cmd.ProcessState.ExitCode(); code != want {
				t.Errorf("%s self-host run = %d, want %d (oracle)", tc.name, code, want)
			}
		})
	}
}
