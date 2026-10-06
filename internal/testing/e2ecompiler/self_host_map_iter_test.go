package e2ecompiler

import (
	"os/exec"
	"testing"
)

// mapIterCases exercise the self-host Map iteration API: m.iter() →
// MapIter[K,V], then has_next() / key() / value() / advance() over the
// parallel keys[]/values[] arrays. Needed by std/json's json_encode
// (which walks a JObject's Map). Exit codes cross-checked vs the Go
// backend.
var mapIterCases = []struct {
	name string
	src  string
	exit int
}{
	{"key-len-plus-value", "import \"core/map\"; function main(): i32 { let m: Map[string,i32] = map_new(4); m = m.insert(\"a\", 10); m = m.insert(\"bb\", 20); let it: MapIter[string,i32] = m.iter(); let sum: i32 = 0; while (it.has_next()) { sum = sum + it.key().len() + it.value(); it.advance(); } return sum; }", 33},
	{"empty", "import \"core/map\"; function main(): i32 { let m: Map[string,i32] = map_new(4); let it: MapIter[string,i32] = m.iter(); let n: i32 = 0; while (it.has_next()) { n = n + 1; it.advance(); } return n + 50; }", 50},
	{"string-values", "import \"core/map\"; function main(): i32 { let m: Map[string,string] = map_new(4); m = m.insert(\"k1\", \"abc\"); m = m.insert(\"k2\", \"de\"); let it: MapIter[string,string] = m.iter(); let t: i32 = 0; while (it.has_next()) { t = t + it.value().len(); it.advance(); } return t; }", 5},
	{"count", "import \"core/map\"; function main(): i32 { let m: Map[string,i32] = map_new(8); m = m.insert(\"x\", 1); m = m.insert(\"y\", 2); m = m.insert(\"z\", 3); let it: MapIter[string,i32] = m.iter(); let c: i32 = 0; while (it.has_next()) { c = c + 1; it.advance(); } return c; }", 3},
}

// TestSelfHostMapIterX86_64 compiles Map-iteration programs with the
// self-hosted x86-64 compiler and checks exit codes.
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

// TestSelfHostMapIterArm64 — CI-gated arm64 counterpart.
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
