package e2eselfhost

import (
	"bytes"
	"os/exec"
	"path/filepath"
	"reflect"
	"testing"
)

// TestSelfHostCheckWarnsOfTodoStubsX86_64 is the differential for #11351:
// `-check` warns of every `todo` stub left in the entry module in native's
// words, the entry named, and only once the program has passed the gates
// native passes first. A stub in an imported module is not reported, and
// `todo` as an ordinary name is not a stub.
func TestSelfHostCheckWarnsOfTodoStubsX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	dir := t.TempDir()
	copySelfHostDriver(t, dir, "fern.fern")
	driver := buildSelfHostBin(t, gcc, dir, "fern.fern", "fern")
	native := buildLangBinForInterp(t)
	cases := []struct {
		name  string
		files map[string]string
		want  int // warnings native reports, so the case cannot pass vacuously
	}{
		{"one", map[string]string{"main.fern": "function f(): i32 {\n  todo;\n}\nfunction main(): i32 {\n  return 0;\n}\n"}, 1},
		{"two-with-message", map[string]string{"main.fern": "function f(): i32 {\n  todo(\"later\");\n}\nfunction g(): void {\n    todo;\n}\nfunction main(): i32 {\n  return 0;\n}\n"}, 2},
		{"in-an-import", map[string]string{
			"main.fern": "import \"./lib\";\nfunction main(): i32 {\n  return lib.f();\n}\n",
			"lib.fern":  "pub function f(): i32 {\n  todo;\n}\n",
		}, 0},
		{"beside-a-type-error", map[string]string{"main.fern": "function f(): i32 {\n  todo;\n}\nfunction main(): i32 {\n  return y;\n}\n"}, 0},
		{"an-ordinary-name", map[string]string{"main.fern": "function main(): i32 {\n  let todo: i32 = 0;\n  return todo;\n}\n"}, 0},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			d := t.TempDir()
			writeTree(t, d, c.files)
			p := filepath.Join(d, "main.fern")
			ncmd := exec.Command(native, "-check", p)
			nout, _ := ncmd.CombinedOutput()
			want := warnings(nout)
			if len(want) != c.want {
				t.Fatalf("native reported %d warnings, want %d — the case no longer exercises what it describes:\n%s", len(want), c.want, nout)
			}
			var errb bytes.Buffer
			scmd := runX86_64Bin(runner, driver, "-check", p)
			scmd.Stderr = &errb
			_ = scmd.Run()
			if got := warnings(errb.Bytes()); !reflect.DeepEqual(got, want) {
				t.Errorf("self-host warned %q, native %q\nself-host:\n%s\nnative:\n%s", got, want, errb.String(), nout)
			}
			if sc, nc := scmd.ProcessState.ExitCode(), ncmd.ProcessState.ExitCode(); sc != nc {
				t.Errorf("self-host exited %d, native %d", sc, nc)
			}
		})
	}
}

// warnings is each warning line in out.
func warnings(out []byte) []string {
	ws := []string{}
	for _, line := range bytes.Split(out, []byte("\n")) {
		if bytes.Contains(line, []byte(": warning: ")) {
			ws = append(ws, string(line))
		}
	}
	return ws
}
