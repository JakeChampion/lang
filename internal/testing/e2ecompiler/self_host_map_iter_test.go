package e2ecompiler

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

var mapIterCases = []struct {
	name string
	src  string
	exit int
}{
	{"key-len-plus-value", "import \"core/map\"; import \"core/iter\"; function main(): i32 { let m: Map[string,i32] = map_new(4); m = m.insert(\"a\", 10); m = m.insert(\"bb\", 20); let it: iter.ArrayIter[(string,i32)] = m.iter(); let sum: i32 = 0; for (__mapkey_it, __mapvalue_it) in it { sum = sum + __mapkey_it.len() + __mapvalue_it;  } return sum; }", 33},
	{"empty", "import \"core/map\"; import \"core/iter\"; function main(): i32 { let m: Map[string,i32] = map_new(4); let it: iter.ArrayIter[(string,i32)] = m.iter(); let n: i32 = 0; for (__mapkey_it, __mapvalue_it) in it { n = n + 1;  } return n + 50; }", 50},
	{"string-values", "import \"core/map\"; import \"core/iter\"; function main(): i32 { let m: Map[string,string] = map_new(4); m = m.insert(\"k1\", \"abc\"); m = m.insert(\"k2\", \"de\"); let it: iter.ArrayIter[(string,string)] = m.iter(); let t: i32 = 0; for (__mapkey_it, __mapvalue_it) in it { t = t + __mapvalue_it.len();  } return t; }", 5},
	{"count", "import \"core/map\"; import \"core/iter\"; function main(): i32 { let m: Map[string,i32] = map_new(8); m = m.insert(\"x\", 1); m = m.insert(\"y\", 2); m = m.insert(\"z\", 3); let it: iter.ArrayIter[(string,i32)] = m.iter(); let c: i32 = 0; for (__mapkey_it, __mapvalue_it) in it { c = c + 1;  } return c; }", 3},
}

func TestSelfHostMapIterX86_64(t *testing.T) {
	gcc, runner := x86_64Tooling(t)
	l := newStdlibLoader(t)
	dir := t.TempDir()

	for _, tc := range mapIterCases {
		t.Run(tc.name, func(t *testing.T) {
			asm := []byte(l.emit(t, tc.src))
			if len(asm) == 0 {
				t.Fatal("self-host compiler emitted 0 bytes")
			}
			progBin := buildBin(t, gcc, dir, tc.name, string(asm))
			var cmd *exec.Cmd
			if len(runner) == 0 {
				cmd = exec.Command(progBin)
			} else {
				cmd = exec.Command(runner[0], append(runner[1:], progBin)...)
			}
			_ = cmd.Run()
			if code := cmd.ProcessState.ExitCode(); code != tc.exit {
				t.Errorf("%s exited %d, want %d", tc.name, code, tc.exit)
			}
		})
	}
}

func TestSelfHostMapIterArm64(t *testing.T) {
	cli := buildSelfHostCLI(t)
	for _, tc := range mapIterCases {
		t.Run(tc.name, func(t *testing.T) {
			src := tc.src
			for _, target := range []string{"arm64-linux"} {
				if stderr, code := cli.exitOf(t, src, target); code != tc.exit {
					t.Errorf("%s on %s exited %d, want %d\n%s", tc.name, target, code, tc.exit, stderr)
				}
			}
		})
	}
}

func TestSelfHostMapIteratorProtocol(t *testing.T) {
	cli := buildSelfHostCLI(t)
	for _, name := range []string{"map_iterator_protocol", "iterator_nested_tuple_adapter"} {
		source, err := os.ReadFile(filepath.Join("..", "..", "..", "conformance", "cases", name, "main.fern"))
		if err != nil {
			t.Fatal(err)
		}
		runMapIterCensus(t, cli, name, name, string(source), 0)
	}
}
